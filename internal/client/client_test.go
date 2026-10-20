package client

import (
	"bufio"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codeberg.org/samposa_1337/Nkit/internal/server"
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
			c := New(tt.cfg)
			err := c.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	t.Parallel()

	c := New(Config{})
	if c.cfg.ConnectTimeout <= 0 {
		t.Fatalf("ConnectTimeout = %v, want positive", c.cfg.ConnectTimeout)
	}
	if c.cfg.MinBackoff <= 0 {
		t.Fatalf("MinBackoff = %v, want positive", c.cfg.MinBackoff)
	}
	if c.cfg.MaxBackoff < c.cfg.MinBackoff {
		t.Fatalf("MaxBackoff = %v, want >= %v", c.cfg.MaxBackoff, c.cfg.MinBackoff)
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
		errCh <- New(clientCfg).Run(ctx, strings.NewReader("hello\n"), outputWriter)
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
