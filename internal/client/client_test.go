package client

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codeberg.org/Dehydration/lentitud/internal/protocol"
	"codeberg.org/Dehydration/lentitud/internal/server"

	"github.com/gorilla/websocket"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{name: "valid ws", cfg: Config{ServerURL: "ws://localhost:8080/ws", Nick: "alice"}},
		{name: "valid wss", cfg: Config{ServerURL: "wss://chat.example/ws", Nick: "alice"}},
		{name: "missing nick", cfg: Config{ServerURL: "ws://localhost:8080/ws"}, wantErr: true},
		{name: "invalid nick", cfg: Config{ServerURL: "ws://localhost:8080/ws", Nick: "alice smith"}, wantErr: true},
		{name: "wrong scheme", cfg: Config{ServerURL: "http://localhost:8080/ws", Nick: "alice"}, wantErr: true},
		{name: "missing host", cfg: Config{ServerURL: "ws:///ws", Nick: "alice"}, wantErr: true},
		{name: "bad url", cfg: Config{ServerURL: "://bad", Nick: "alice"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := New(&tt.cfg)
			err := c.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	t.Parallel()

	c := New(&Config{})
	if c.Config.ConnectTimeout <= 0 {
		t.Fatalf("ConnectTimeout = %v, want positive", c.Config.ConnectTimeout)
	}
	if c.Config.MinBackoff <= 0 {
		t.Fatalf("MinBackoff = %v, want positive", c.Config.MinBackoff)
	}
	if c.Config.MaxBackoff < c.Config.MinBackoff {
		t.Fatalf("MaxBackoff = %v, want >= %v", c.Config.MaxBackoff, c.Config.MinBackoff)
	}
}

func TestWithJitter(t *testing.T) {
	t.Parallel()

	const d = 10 * time.Second
	for i := 0; i < 100; i++ {
		got := withJitter(d)
		if got < d/2 || got > d {
			t.Fatalf("withJitter(%v) = %v, want between %v and %v", d, got, d/2, d)
		}
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	t.Parallel()

	b := newBackoff(&Config{
		MinBackoff:       10 * time.Millisecond,
		MaxBackoff:       40 * time.Millisecond,
		StableConnection: time.Second,
	})

	for _, want := range []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 40 * time.Millisecond} {
		before := b.cur
		got := b.next()
		if got < before/2 || got > before {
			t.Fatalf("next() = %v, want within [%v, %v]", got, before/2, before)
		}
		if b.cur != want {
			t.Fatalf("current backoff = %v, want %v", b.cur, want)
		}
	}
}

func TestBackoffResetIfStable(t *testing.T) {
	t.Parallel()

	b := newBackoff(&Config{
		MinBackoff:       time.Second,
		MaxBackoff:       8 * time.Second,
		StableConnection: 5 * time.Second,
	})

	b.next()
	b.next()
	if b.cur != 4*time.Second {
		t.Fatalf("current backoff = %v, want 4s", b.cur)
	}

	b.resetIfStable(4 * time.Second)
	if b.cur != 4*time.Second {
		t.Fatalf("current backoff after unstable reset = %v, want 4s", b.cur)
	}

	b.resetIfStable(5 * time.Second)
	if b.cur != time.Second {
		t.Fatalf("current backoff after stable reset = %v, want 1s", b.cur)
	}
}

func TestRejectedJoinReportsServerReason(t *testing.T) {
	t.Parallel()

	serverCfg := server.DefaultConfig()
	serverCfg.JoinTimeout = 2 * time.Second
	serverCfg.WriteTimeout = 2 * time.Second
	ts := httptest.NewServer(server.New(serverCfg, nil).Handler())
	defer ts.Close()

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	occupier, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial occupier: %v", err)
	}
	defer occupier.Close()

	joinData, err := json.Marshal(protocol.Envelope{Type: protocol.TypeJoin, Nick: "alice"})
	if err != nil {
		t.Fatalf("marshal join: %v", err)
	}
	_ = occupier.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := occupier.WriteMessage(websocket.TextMessage, joinData); err != nil {
		t.Fatalf("write join: %v", err)
	}
	_ = occupier.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := occupier.ReadMessage(); err != nil {
		t.Fatalf("read welcome: %v", err)
	}

	cfg := DefaultConfig()
	cfg.ServerURL = url
	cfg.Nick = "alice"
	cfg.MinBackoff = 50 * time.Millisecond
	cfg.MaxBackoff = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lines := startClient(t, cfg, ctx)

	waitForLine(t, lines, "! rejected by the server: nickname already in use")

	cancel()
}

func TestReconnectsAfterServerRestart(t *testing.T) {
	t.Parallel()

	addr := freeAddr(t)

	serverCfg := server.DefaultConfig()
	serverCfg.Addr = addr
	serverCfg.ShutdownTimeout = 2 * time.Second
	serverCfg.WriteTimeout = 2 * time.Second
	serverCfg.PingInterval = time.Second
	serverCfg.PongWait = 3 * time.Second

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	runA := make(chan error, 1)
	go func() { runA <- server.New(serverCfg, nil).Run(ctxA) }()

	cfg := DefaultConfig()
	cfg.ServerURL = "ws://" + addr + "/ws"
	cfg.Nick = "alice"
	cfg.MinBackoff = 50 * time.Millisecond
	cfg.MaxBackoff = 100 * time.Millisecond
	cfg.StableConnection = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lines := startClient(t, cfg, ctx)
	waitForLine(t, lines, "* connected as alice")

	cancelA()
	select {
	case err := <-runA:
		if err != nil {
			t.Fatalf("first server Run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first server did not stop")
	}

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	runB := make(chan error, 1)
	go func() { runB <- server.New(serverCfg, nil).Run(ctxB) }()

	waitForLine(t, lines, "* connected as alice")
}

func startClient(t *testing.T, cfg Config, ctx context.Context) <-chan string {
	t.Helper()

	stdinReader, stdinWriter := io.Pipe()
	t.Cleanup(func() { _ = stdinWriter.Close() })

	outputReader, outputWriter := io.Pipe()
	t.Cleanup(func() { _ = outputReader.Close() })

	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(outputReader)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- New(&cfg).Run(ctx, stdinReader, outputWriter) }()
	t.Cleanup(func() {
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Error("client Run() did not return")
		}
	})

	return lines
}

func waitForLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("output closed while waiting for %q", want)
			}
			if line == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return addr
}

func TestRunSendsAndPrintsMessages(t *testing.T) {
	t.Parallel()

	cfg := server.DefaultConfig()
	cfg.JoinTimeout = 2 * time.Second
	cfg.WriteTimeout = 2 * time.Second
	cfg.PingInterval = time.Second
	cfg.PongWait = 3 * time.Second
	srv := server.New(cfg, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	clientCfg := DefaultConfig()
	clientCfg.ServerURL = "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	clientCfg.Nick = "alice"
	clientCfg.MinBackoff = 50 * time.Millisecond
	clientCfg.MaxBackoff = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	outputReader, outputWriter := io.Pipe()
	defer outputReader.Close()

	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(outputReader)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- New(&clientCfg).Run(ctx, strings.NewReader("hello\n"), outputWriter)
	}()

	gotWelcome := false
	gotEcho := false
	runDone := false
	var runErr error
	deadline := time.After(4 * time.Second)

	for !gotEcho {
		select {
		case line := <-lines:
			if line == "* connected as alice" {
				gotWelcome = true
			}
			if line == "<alice> hello" {
				gotEcho = true
			}
		case err := <-errCh:
			runErr = err
			runDone = true
			errCh = nil
		case <-deadline:
			cancel()
			t.Fatalf("timed out waiting for output; welcome=%v echo=%v runErr=%v", gotWelcome, gotEcho, runErr)
		}
	}

	if !runDone {
		select {
		case runErr = <-errCh:
		case <-time.After(4 * time.Second):
			cancel()
			t.Fatal("Run() did not exit after stdin EOF")
		}
	}
	if runErr != nil {
		t.Fatalf("Run() error = %v", runErr)
	}

	_ = outputWriter.Close()
}
