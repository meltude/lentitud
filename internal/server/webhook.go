package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"
)

const (
	maxWebhookBody      = 1 << 20
	maxWebhookTextBytes = 512
)

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if s.cfg.WebhookSecret == "" {
		http.NotFound(w, r)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	if !validWebhookRequest(s.cfg.WebhookSecret, r.Header, body) {
		s.log.Warn("webhook rejected", "remote", r.RemoteAddr)
		http.Error(w, "invalid webhook credentials", http.StatusUnauthorized)
		return
	}

	event := webhookEvent(r.Header)
	text := sanitizeWebhookText(formatWebhook(event, body))
	if text == "" {
		text = "webhook received"
	}

	s.broadcastSystem(nil, text)
	s.log.Info("webhook received", "event", event)
	w.WriteHeader(http.StatusNoContent)
}

func validWebhookRequest(secret string, header http.Header, body []byte) bool {
	if auth := header.Get("Authorization"); auth != "" &&
		subtle.ConstantTimeCompare([]byte(auth), []byte("Bearer "+secret)) == 1 {
		return true
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))

	if got := strings.TrimPrefix(header.Get("X-Hub-Signature-256"), "sha256="); got != "" {
		return hmac.Equal([]byte(got), []byte(want))
	}
	if got := header.Get("X-Gitea-Signature"); got != "" {
		return hmac.Equal([]byte(got), []byte(want))
	}
	return false
}

func webhookEvent(header http.Header) string {
	for _, key := range []string{"X-Forgejo-Event", "X-Gitea-Event", "X-GitHub-Event"} {
		if value := header.Get(key); value != "" {
			return value
		}
	}
	return "webhook"
}

func formatWebhook(event string, body []byte) string {
	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return "webhook received"
	}
	if text := strings.TrimSpace(payload.Text); text != "" {
		return text
	}

	repo := payload.Repository.FullName
	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")
	target := repo
	if repo != "" && branch != "" {
		target = repo + " (" + branch + ")"
	}

	switch {
	case repo != "" && len(payload.Commits) > 0:
		author := payload.Pusher.Login
		if author == "" {
			author = "someone"
		}
		subject := strings.SplitN(payload.Commits[0].Message, "\n", 2)[0]
		if len(payload.Commits) == 1 {
			return fmt.Sprintf("%s pushed 1 commit to %s: %s", author, target, subject)
		}
		return fmt.Sprintf("%s pushed %d commits to %s: %s", author, len(payload.Commits), target, subject)
	case repo != "":
		return fmt.Sprintf("%s event from %s", event, repo)
	default:
		return "webhook received"
	}
}

type webhookPayload struct {
	Text       string `json:"text"`
	Ref        string `json:"ref"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Pusher struct {
		Login string `json:"login"`
	} `json:"pusher"`
	Commits []struct {
		Message string `json:"message"`
	} `json:"commits"`
}

func sanitizeWebhookText(text string) string {
	var b strings.Builder
	for _, r := range text {
		if b.Len() >= maxWebhookTextBytes {
			b.WriteString("...")
			break
		}
		switch {
		case r == '\t' || r == '\r' || r == '\n':
			b.WriteByte(' ')
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			continue
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
