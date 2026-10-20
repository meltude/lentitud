package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codeberg.org/Dehydration/lentitud/internal/protocol"
)

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func postWebhook(t *testing.T, ts *httptest.Server, body []byte, header http.Header) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/webhook", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for key, values := range header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post webhook: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestWebhookDisabledWithoutSecret(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, nil)
	resp := postWebhook(t, ts, []byte(`{"text":"hello"}`), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestWebhookRejectsInvalidCredentials(t *testing.T) {
	t.Parallel()

	const secret = "sekret"
	ts := newTestServer(t, func(cfg *Config) { cfg.WebhookSecret = secret })
	body := []byte(`{"text":"hello"}`)

	tests := []struct {
		name   string
		header http.Header
	}{
		{name: "no credentials"},
		{name: "wrong signature", header: http.Header{"X-Hub-Signature-256": {"sha256=deadbeef"}}},
		{name: "wrong bearer", header: http.Header{"Authorization": {"Bearer nope"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := postWebhook(t, ts, body, tt.header)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
		})
	}
}

func TestWebhookBroadcastsPush(t *testing.T) {
	t.Parallel()

	const secret = "sekret"
	ts := newTestServer(t, func(cfg *Config) { cfg.WebhookSecret = secret })

	conn := dial(t, ts, "")
	join(t, conn, "alice")

	body := []byte(`{
		"ref": "refs/heads/head",
		"repository": {"full_name": "Dehydration/lentitud"},
		"pusher": {"login": "meltude"},
		"commits": [
			{"message": "fix the thing\n\nwith a body"},
			{"message": "second commit"}
		]
	}`)
	header := http.Header{
		"X-Forgejo-Event":     {"push"},
		"X-Hub-Signature-256": {signBody(secret, body)},
	}

	resp := postWebhook(t, ts, body, header)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	const want = "meltude pushed 2 commits to Dehydration/lentitud (head): fix the thing"
	env := readEnvelope(t, conn)
	if env.Type != protocol.TypeSystem || env.Text != want {
		t.Fatalf("broadcast = %#v, want system %q", env, want)
	}
}

func TestWebhookAcceptsBearerAndSanitizes(t *testing.T) {
	t.Parallel()

	const secret = "sekret"
	ts := newTestServer(t, func(cfg *Config) { cfg.WebhookSecret = secret })

	conn := dial(t, ts, "")
	join(t, conn, "alice")

	body := []byte("{\"text\":\"line one\\nline two \\u001b[31mred\"}")
	header := http.Header{
		"Authorization": {"Bearer " + secret},
		"X-Gitea-Event": {"push"},
	}

	resp := postWebhook(t, ts, body, header)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	const want = "line one line two [31mred"
	env := readEnvelope(t, conn)
	if env.Type != protocol.TypeSystem || env.Text != want {
		t.Fatalf("broadcast = %#v, want system %q", env, want)
	}
}

func TestWebhookAcceptsGiteaSignature(t *testing.T) {
	t.Parallel()

	const secret = "sekret"
	ts := newTestServer(t, func(cfg *Config) { cfg.WebhookSecret = secret })

	conn := dial(t, ts, "")
	join(t, conn, "alice")

	body := []byte(`{"repository":{"full_name":"Dehydration/lentitud"}}`)
	header := http.Header{
		"X-Gitea-Event":     {"issues"},
		"X-Gitea-Signature": {strings.TrimPrefix(signBody(secret, body), "sha256=")},
	}

	resp := postWebhook(t, ts, body, header)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	const want = "issues event from Dehydration/lentitud"
	env := readEnvelope(t, conn)
	if env.Text != want {
		t.Fatalf("broadcast = %q, want %q", env.Text, want)
	}
}

func TestWebhookRejectsOversizedBody(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, func(cfg *Config) { cfg.WebhookSecret = "sekret" })
	body := bytes.Repeat([]byte("a"), maxWebhookBody+1)

	resp := postWebhook(t, ts, body, nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
	}
}

func TestWebhookRequiresPost(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t, func(cfg *Config) { cfg.WebhookSecret = "sekret" })
	resp, err := http.Get(ts.URL + "/webhook")
	if err != nil {
		t.Fatalf("get webhook: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

func TestFormatWebhook(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event string
		body  string
		want  string
	}{
		{
			name: "explicit text",
			body: `{"text":"deploy finished"}`,
			want: "deploy finished",
		},
		{
			name: "single push",
			body: `{
				"ref":"refs/heads/main",
				"repository":{"full_name":"owner/repo"},
				"pusher":{"login":"alice"},
				"commits":[{"message":"one change"}]
			}`,
			want: "alice pushed 1 commit to owner/repo (main): one change",
		},
		{
			name: "push without pusher",
			body: `{
				"repository":{"full_name":"owner/repo"},
				"commits":[{"message":"one change"}]
			}`,
			want: "someone pushed 1 commit to owner/repo: one change",
		},
		{
			name:  "non push event",
			event: "pull_request",
			body:  `{"repository":{"full_name":"owner/repo"}}`,
			want:  "pull_request event from owner/repo",
		},
		{
			name: "invalid json",
			body: "{",
			want: "webhook received",
		},
		{
			name: "empty object",
			body: `{}`,
			want: "webhook received",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatWebhook(tt.event, []byte(tt.body)); got != tt.want {
				t.Fatalf("formatWebhook() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeWebhookText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "newlines and tabs", in: "a\nb\rc\td", want: "a b c d"},
		{name: "control and format chars", in: "safe\x1b[31m \u202eevil\u200b", want: "safe[31m evil"},
		{name: "trims space", in: "  hello  ", want: "hello"},
		{name: "truncates", in: strings.Repeat("x", maxWebhookTextBytes+10), want: strings.Repeat("x", maxWebhookTextBytes) + "..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeWebhookText(tt.in); got != tt.want {
				t.Fatalf("sanitizeWebhookText() = %q, want %q", got, tt.want)
			}
		})
	}
}
