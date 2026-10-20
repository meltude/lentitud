package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codeberg.org/samposa_1337/Nkit/internal/protocol"

	"github.com/gorilla/websocket"
)

func TestMessageBroadcast(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, nil)

	alice := dial(t, ts, "")
	join(t, alice, "alice")

	bob := dial(t, ts, "")
	join(t, bob, "bob")

	system := readEnvelope(t, alice)
	if system.Type != protocol.TypeSystem || system.Text != "bob joined" {
		t.Fatalf("join notification = %#v, want system message", system)
	}

	writeEnvelope(t, alice, protocol.Envelope{Type: protocol.TypeMessage, Text: "hello"})

	echo := readEnvelope(t, alice)
	if echo.Type != protocol.TypeMessage || echo.Nick != "alice" || echo.Text != "hello" {
		t.Fatalf("sender echo = %#v", echo)
	}

	received := readEnvelope(t, bob)
	if received.Type != protocol.TypeMessage || received.Nick != "alice" || received.Text != "hello" {
		t.Fatalf("receiver message = %#v", received)
	}
}

func TestJoinRequirements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  protocol.Envelope
		raw  string
	}{
		{name: "message before join", env: protocol.Envelope{Type: protocol.TypeMessage, Text: "hello"}},
		{name: "invalid nickname", env: protocol.Envelope{Type: protocol.TypeJoin, Nick: "bad nick"}},
		{name: "malformed json", raw: "{"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := newTestServer(t, nil)
			conn := dial(t, ts, "")

			if tt.raw != "" {
				writeRaw(t, conn, tt.raw)
			} else {
				writeEnvelope(t, conn, tt.env)
			}

			env := readEnvelope(t, conn)
			if env.Type != protocol.TypeError {
				t.Fatalf("message = %#v, want error", env)
			}
		})
	}
}

func TestDuplicateNickname(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, nil)

	first := dial(t, ts, "")
	join(t, first, "Alice")

	second := dial(t, ts, "")
	writeEnvelope(t, second, protocol.Envelope{Type: protocol.TypeJoin, Nick: "alice"})

	env := readEnvelope(t, second)
	if env.Type != protocol.TypeError || !strings.Contains(env.Text, "in use") {
		t.Fatalf("duplicate join response = %#v", env)
	}
}

func TestNicknameIsAvailableAfterDisconnect(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, nil)

	first := dial(t, ts, "")
	join(t, first, "alice")
	_ = first.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		second := dial(t, ts, "")
		writeEnvelope(t, second, protocol.Envelope{Type: protocol.TypeJoin, Nick: "alice"})
		env := readEnvelope(t, second)
		if env.Type == protocol.TypeWelcome {
			return
		}
		_ = second.Close()
		if time.Now().After(deadline) {
			t.Fatalf("nickname was not released: %#v", env)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMessageValidation(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, func(cfg *Config) {
		cfg.MaxMessageBytes = 8
	})
	conn := dial(t, ts, "")
	join(t, conn, "alice")

	writeEnvelope(t, conn, protocol.Envelope{Type: protocol.TypeMessage, Text: ""})
	if env := readEnvelope(t, conn); env.Type != protocol.TypeError {
		t.Fatalf("empty message response = %#v, want error", env)
	}

	writeEnvelope(t, conn, protocol.Envelope{Type: protocol.TypeMessage, Text: "too long!"})
	if env := readEnvelope(t, conn); env.Type != protocol.TypeError {
		t.Fatalf("long message response = %#v, want error", env)
	}

	writeEnvelope(t, conn, protocol.Envelope{Type: protocol.TypeMessage, Text: "ok"})
	if env := readEnvelope(t, conn); env.Type != protocol.TypeMessage || env.Text != "ok" {
		t.Fatalf("valid message response = %#v", env)
	}
}

func TestRateLimit(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, func(cfg *Config) {
		cfg.RateLimitPerSecond = 1
	})
	conn := dial(t, ts, "")
	join(t, conn, "alice")

	writeEnvelope(t, conn, protocol.Envelope{Type: protocol.TypeMessage, Text: "one"})
	if env := readEnvelope(t, conn); env.Type != protocol.TypeMessage || env.Text != "one" {
		t.Fatalf("first message response = %#v", env)
	}

	writeEnvelope(t, conn, protocol.Envelope{Type: protocol.TypeMessage, Text: "two"})
	env := readEnvelope(t, conn)
	if env.Type != protocol.TypeError || !strings.Contains(env.Text, "rate limit") {
		t.Fatalf("rate limit response = %#v", env)
	}
}

func TestBinaryAndUnknownMessages(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, nil)
	conn := dial(t, ts, "")
	join(t, conn, "alice")

	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("binary")); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if env := readEnvelope(t, conn); env.Type != protocol.TypeError {
		t.Fatalf("binary response = %#v, want error", env)
	}

	writeEnvelope(t, conn, protocol.Envelope{Type: protocol.TypeWelcome})
	if env := readEnvelope(t, conn); env.Type != protocol.TypeError {
		t.Fatalf("unknown type response = %#v, want error", env)
	}
}

func TestOriginValidation(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, nil)
	u := wsURL(ts)

	conn, resp, err := websocket.DefaultDialer.Dial(u, http.Header{
		"Origin": []string{"http://evil.example"},
	})
	if err == nil {
		conn.Close()
		t.Fatal("cross-origin dial unexpectedly succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin response = %#v, want 403", resp)
	}

	allowed := newTestServer(t, func(cfg *Config) {
		cfg.AllowedOrigins = []string{"http://allowed.example"}
	})
	conn = dialWithOrigin(t, allowed, "http://allowed.example")
	join(t, conn, "alice")
}

func TestHealthEndpoint(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, nil)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "ok\n" {
		t.Fatalf("body = %q, want %q", body, "ok\n")
	}
}

func newTestServer(t *testing.T, mutate func(*Config)) *httptest.Server {
	t.Helper()

	cfg := DefaultConfig()
	cfg.JoinTimeout = 2 * time.Second
	cfg.WriteTimeout = 2 * time.Second
	cfg.PingInterval = time.Second
	cfg.PongWait = 3 * time.Second
	cfg.ShutdownTimeout = time.Second
	if mutate != nil {
		mutate(&cfg)
	}

	s := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func wsURL(ts *httptest.Server) string {
	return "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
}

func dial(t *testing.T, ts *httptest.Server, origin string) *websocket.Conn {
	t.Helper()
	return dialWithOrigin(t, ts, origin)
}

func dialWithOrigin(t *testing.T, ts *httptest.Server, origin string) *websocket.Conn {
	t.Helper()

	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(ts), header)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial: %v (status %d)", err, resp.StatusCode)
		}
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func join(t *testing.T, conn *websocket.Conn, nick string) {
	t.Helper()

	writeEnvelope(t, conn, protocol.Envelope{Type: protocol.TypeJoin, Nick: nick})
	env := readEnvelope(t, conn)
	if env.Type != protocol.TypeWelcome || env.Nick != nick {
		t.Fatalf("welcome = %#v, want welcome for %q", env, nick)
	}
}

func writeEnvelope(t *testing.T, conn *websocket.Conn, env protocol.Envelope) {
	t.Helper()

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	writeRaw(t, conn, string(data))
}

func writeRaw(t *testing.T, conn *websocket.Conn, raw string) {
	t.Helper()

	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, []byte(raw)); err != nil {
		t.Fatalf("write message: %v", err)
	}
}

func readEnvelope(t *testing.T, conn *websocket.Conn) protocol.Envelope {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read message: %v", err)
	}

	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal envelope %q: %v", data, err)
	}
	return env
}
