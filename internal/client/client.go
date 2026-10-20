package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/url"
	"strings"
	"time"

	"codeberg.org/samposa_1337/Nkit/internal/protocol"

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
	cfg Config
}

func New(cfg Config) *Client {
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
	return &Client{cfg: cfg}
}

func (c *Client) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	if err := c.validate(); err != nil {
		return err
	}

	input := make(chan string)
	inputDone := make(chan struct{})
	go readInput(ctx, in, input, inputDone)

	backoff := c.cfg.MinBackoff

	for {
		if ctx.Err() != nil {
			return nil
		}

		started := time.Now()
		err := c.session(ctx, input, inputDone, out)
		if ctx.Err() != nil {
			return nil
		}
		select {
		case <-inputDone:
			return nil
		default:
		}

		if time.Since(started) >= c.cfg.StableConnection {
			backoff = c.cfg.MinBackoff
		}

		if err != nil {
			fmt.Fprintf(out, "connection error: %v\n", err)
		}

		wait := withJitter(backoff)
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

		backoff *= 2
		if backoff > c.cfg.MaxBackoff {
			backoff = c.cfg.MaxBackoff
		}
	}
}

func (c *Client) validate() error {
	if err := protocol.ValidateNick(c.cfg.Nick); err != nil {
		return fmt.Errorf("invalid nickname: %w", err)
	}
	u, err := url.Parse(c.cfg.ServerURL)
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

func (c *Client) session(ctx context.Context, input <-chan string, inputDone <-chan struct{}, out io.Writer) error {
	dialCtx, cancelDial := context.WithTimeout(ctx, c.cfg.ConnectTimeout)
	defer cancelDial()

	dialer := websocket.Dialer{HandshakeTimeout: c.cfg.ConnectTimeout}
	if c.cfg.Insecure {
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	conn, resp, err := dialer.DialContext(dialCtx, c.cfg.ServerURL, nil)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return err
	}
	defer conn.Close()

	stopClose := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stopClose()

	join, err := protocol.Envelope{Type: protocol.TypeJoin, Nick: c.cfg.Nick}.Marshal()
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(c.cfg.WriteTimeout))
	if err := conn.WriteMessage(websocket.TextMessage, join); err != nil {
		return err
	}

	conn.SetPingHandler(func(appData string) error {
		_ = conn.SetReadDeadline(time.Now().Add(c.cfg.PongWait))
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(c.cfg.WriteTimeout))
	})
	_ = conn.SetReadDeadline(time.Now().Add(c.cfg.PongWait))

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
			data, err := protocol.Envelope{Type: protocol.TypeMessage, Text: line}.Marshal()
			if err != nil {
				return err
			}
			_ = conn.SetWriteDeadline(time.Now().Add(c.cfg.WriteTimeout))
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return err
			}
		}
	}
}

func (c *Client) readLoop(conn *websocket.Conn, out io.Writer) error {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			fmt.Fprintln(out, "! received an invalid message from the server")
			continue
		}

		switch env.Type {
		case protocol.TypeWelcome:
			fmt.Fprintf(out, "* connected as %s\n", env.Nick)
		case protocol.TypeMessage:
			fmt.Fprintf(out, "<%s> %s\n", env.Nick, env.Text)
		case protocol.TypeSystem:
			fmt.Fprintf(out, "* %s\n", env.Text)
		case protocol.TypeError:
			fmt.Fprintf(out, "! %s\n", env.Text)
		default:
			fmt.Fprintf(out, "! unknown message type %q\n", env.Type)
		}
	}
}

func (c *Client) close(conn *websocket.Conn) error {
	_ = conn.SetReadDeadline(time.Now().Add(c.cfg.WriteTimeout))
	return conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(c.cfg.WriteTimeout),
	)
}

func readInput(ctx context.Context, in io.Reader, input chan<- string, done chan<- struct{}) {
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
			return
		}
	}
}

func withJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	half := d / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}
