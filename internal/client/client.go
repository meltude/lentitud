package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"codeberg.org/Dehydration/lentitud/internal/protocol"

	"github.com/gorilla/websocket"
)

type Config struct {
	ServerURL        string
	Nick             string
	Insecure         bool
	ConnectTimeout   time.Duration
	WriteTimeout     time.Duration
	PongWait         time.Duration
	MinBackoff       time.Duration
	MaxBackoff       time.Duration
	StableConnection time.Duration
}

func DefaultConfig() Config {
	return Config{
		ServerURL:        "ws://localhost:8080/ws",
		ConnectTimeout:   10 * time.Second,
		WriteTimeout:     10 * time.Second,
		PongWait:         90 * time.Second,
		MinBackoff:       time.Second,
		MaxBackoff:       30 * time.Second,
		StableConnection: 30 * time.Second,
	}
}

type Client struct {
	Config     *Config
	Connection *websocket.Conn
}

type RejectedError struct {
	Reason string
}

func (e *RejectedError) Error() string {
	return "rejected by the server: " + e.Reason
}

func New(cfg *Config) *Client {
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	if cfg.PongWait <= 0 {
		cfg.PongWait = 90 * time.Second
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff < cfg.MinBackoff {
		cfg.MaxBackoff = cfg.MinBackoff
	}
	if cfg.StableConnection <= 0 {
		cfg.StableConnection = 30 * time.Second
	}
	return &Client{Config: cfg}
}

func (c *Client) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	if err := c.validate(); err != nil {
		return err
	}

	input := make(chan string)
	inputDone := make(chan struct{})
	go readInput(ctx, in, input, inputDone)

	b := newBackoff(c.Config)

	for {
		if ctx.Err() != nil {
			return nil
		}

		started := time.Now()
		err := c.session(ctx, input, out)
		if ctx.Err() != nil {
			return nil
		}
		select {
		case <-inputDone:
			return nil
		default:
		}

		b.resetIfStable(time.Since(started))

		if err != nil {
			if rejected, ok := errors.AsType[*RejectedError](err); ok {
				fmt.Fprintf(out, "! %s\n", rejected)
			} else {
				fmt.Fprintf(out, "connection error: %v\n", err)
			}
		}

		wait := b.next()
		fmt.Fprintf(out, "reconnecting in %s...\n", wait.Round(time.Millisecond))

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-inputDone:
			timer.Stop()
			return nil
		case <-timer.C:
		}

	}
}

func (c *Client) validate() error {
	if err := protocol.ValidateNick(c.Config.Nick); err != nil {
		return fmt.Errorf("invalid nickname: %w", err)
	}
	u, err := url.Parse(c.Config.ServerURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return fmt.Errorf("server URL must use ws:// or wss://")
	}
	if u.Host == "" {
		return fmt.Errorf("server URL is missing a host")
	}
	return nil
}

func (c *Client) session(ctx context.Context, input <-chan string, out io.Writer) error {
	dialCtx, cancelDial := context.WithTimeout(ctx, c.Config.ConnectTimeout)
	defer cancelDial()

	dialer := websocket.Dialer{HandshakeTimeout: c.Config.ConnectTimeout}
	if c.Config.Insecure {
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	conn, resp, err := dialer.DialContext(dialCtx, c.Config.ServerURL, nil)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return err
	}
	c.Connection = conn
	defer conn.Close()

	stopClose := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stopClose()

	join, err := json.Marshal(protocol.Envelope{Type: protocol.TypeJoin, Nick: c.Config.Nick})
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(c.Config.WriteTimeout))
	if err := conn.WriteMessage(websocket.TextMessage, join); err != nil {
		return err
	}

	conn.SetPingHandler(func(appData string) error {
		_ = conn.SetReadDeadline(time.Now().Add(c.Config.PongWait))
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(c.Config.WriteTimeout))
	})
	_ = conn.SetReadDeadline(time.Now().Add(c.Config.PongWait))

	sessionCtx, cancelSession := context.WithCancel(ctx)
	defer cancelSession()

	writeErr := make(chan error, 1)
	go func() {
		err := c.writeLoop(sessionCtx, conn, input)
		if err != nil {
			_ = conn.Close()
		}
		writeErr <- err
	}()

	readErr := c.readLoop(conn, out)

	cancelSession()
	_ = conn.Close()
	wErr := <-writeErr

	if readErr != nil {
		return readErr
	}
	return wErr
}

func (c *Client) writeLoop(ctx context.Context, conn *websocket.Conn, input <-chan string) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-input:
			if !ok {
				return c.close(conn)
			}
			data, err := json.Marshal(protocol.Envelope{Type: protocol.TypeMessage, Text: line})
			if err != nil {
				return err
			}
			_ = conn.SetWriteDeadline(time.Now().Add(c.Config.WriteTimeout))
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return err
			}
		}
	}
}

func (c *Client) readLoop(conn *websocket.Conn, out io.Writer) error {
	joined := false
	reason := ""

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if reason != "" {
				return &RejectedError{Reason: reason}
			}
			return err
		}

		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			fmt.Fprintln(out, "! received an invalid message from the server")
			continue
		}

		switch env.Type {
		case protocol.TypeWelcome:
			joined = true
			reason = ""
			fmt.Fprintf(out, "* connected as %s\n", env.Nick)
		case protocol.TypeMessage:
			fmt.Fprintf(out, "<%s> %s\n", env.Nick, env.Text)
		case protocol.TypeSystem:
			fmt.Fprintf(out, "* %s\n", env.Text)
		case protocol.TypeError:
			if joined {
				fmt.Fprintf(out, "! %s\n", env.Text)
				continue
			}
			reason = env.Text
		default:
			fmt.Fprintf(out, "! unknown message type %q\n", env.Type)
		}
	}
}

func (c *Client) close(conn *websocket.Conn) error {
	_ = conn.SetReadDeadline(time.Now().Add(c.Config.WriteTimeout))
	return conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(c.Config.WriteTimeout),
	)
}

func readInput(ctx context.Context, in io.Reader, input chan<- string, done chan<- struct{}) error {
	defer close(done)
	defer close(input)

	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		select {
		case input <- line:
		case <-ctx.Done():
			return nil
		}
	}
	if scanner.Err() != nil {
		return scanner.Err()
	}
	return nil
}

type backoff struct {
	min    time.Duration
	max    time.Duration
	stable time.Duration
	cur    time.Duration
}

func newBackoff(cfg *Config) *backoff {
	return &backoff{
		min:    cfg.MinBackoff,
		max:    cfg.MaxBackoff,
		stable: cfg.StableConnection,
		cur:    cfg.MinBackoff,
	}
}

func (b *backoff) resetIfStable(elapsed time.Duration) {
	if elapsed >= b.stable {
		b.cur = b.min
	}
}

func (b *backoff) next() time.Duration {
	wait := withJitter(b.cur)
	b.cur *= 2
	if b.cur > b.max {
		b.cur = b.max
	}
	return wait
}

func withJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	half := d / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

func (c *Client) HandleShutdown() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGINT)
	go func() {
		<-ch
		if c.Connection != nil {
			c.close(c.Connection)
		}
		os.Exit(0)
	}()
}
