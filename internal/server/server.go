package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"codeberg.org/Dehydration/lentitud/internal/hub"
	"codeberg.org/Dehydration/lentitud/internal/protocol"

	"github.com/gorilla/websocket"
)

type Config struct {
	Addr               string
	AllowedOrigins     map[string]struct{}
	MaxMessageBytes    int
	SendBuffer         int
	JoinTimeout        time.Duration
	WriteTimeout       time.Duration
	PingInterval       time.Duration
	PongWait           time.Duration
	RateLimitPerSecond int
	TLSCert            string
	TLSKey             string
	WebhookSecret      string
	ShutdownTimeout    time.Duration
}

func DefaultConfig() Config {
	return Config{
		Addr:               ":8080",
		MaxMessageBytes:    protocol.MaxMessageBytes,
		SendBuffer:         256,
		JoinTimeout:        10 * time.Second,
		WriteTimeout:       10 * time.Second,
		PingInterval:       30 * time.Second,
		PongWait:           60 * time.Second,
		RateLimitPerSecond: 10,
		ShutdownTimeout:    5 * time.Second,
	}
}

type Server struct {
	cfg      Config
	hub      *hub.Hub
	log      *slog.Logger
	allowed  map[string]struct{}
	upgrader websocket.Upgrader

	mu      sync.Mutex
	closing bool
	conns   map[*websocket.Conn]struct{}
	wg      sync.WaitGroup
}

func New(cfg Config, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	s := &Server{
		cfg:     cfg,
		hub:     hub.New(),
		log:     logger,
		allowed: cfg.AllowedOrigins,
		conns:   make(map[*websocket.Conn]struct{}),
	}
	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     s.checkOrigin,
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("POST /webhook", s.handleWebhook)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	return mux
}

func (s *Server) Run(ctx context.Context) error {
	if (s.cfg.TLSCert == "") != (s.cfg.TLSKey == "") {
		return errors.New("both TLS certificate and key must be set")
	}

	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("server listening", "addr", s.cfg.Addr, "tls", s.cfg.TLSCert != "")
		if s.cfg.TLSCert != "" {
			errCh <- srv.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey)
			return
		}
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		s.log.Info("server shutting down")
		s.beginShutdown()
		s.broadcastSystem("server shutting down")
		s.hub.Close()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()

		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) beginShutdown() {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
}

func (s *Server) track() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.wg.Add(1)
	return true
}

func (s *Server) register(conn *websocket.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.conns[conn] = struct{}{}
	return true
}

func (s *Server) unregister(conn *websocket.Conn) {
	s.mu.Lock()
	delete(s.conns, conn)
	s.mu.Unlock()
}

func (s *Server) interruptConnections() {
	s.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.mu.Unlock()

	for _, conn := range conns {
		_ = conn.SetReadDeadline(time.Now())
	}
}

func (s *Server) waitForHandlers(ctx context.Context, clients []*hub.Client) {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-ctx.Done():
	}

	for _, c := range clients {
		c.Close()
	}
	s.hub.Close()
	select {
	case <-done:
	case <-time.After(s.cfg.WriteTimeout):
	}
}

func (s *Server) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if _, ok := s.allowed[origin]; ok {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.track() {
		http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	}
	defer s.wg.Done()

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warn("websocket upgrade failed", "error", err, "remote", r.RemoteAddr)
		return
	}
	defer conn.Close()

	if !s.register(conn) {
		return
	}
	defer s.unregister(conn)

	s.serve(conn, r.RemoteAddr)
}

const (
	maxWebhookBody      = 1 << 20
	maxWebhookTextBytes = 512
)

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

	s.broadcastSystem(text)
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

func (s *Server) serve(conn *websocket.Conn, remote string) {
	c := hub.NewClient(conn, s.cfg.SendBuffer)
	defer c.Close()

	conn.SetReadLimit(int64(s.cfg.MaxMessageBytes) + 4096)
	conn.SetReadDeadline(time.Now().Add(s.cfg.JoinTimeout))

	mt, data, err := conn.ReadMessage()
	if err != nil {
		s.log.Debug("join read failed", "error", err, "remote", remote)
		return
	}
	if mt != websocket.TextMessage {
		s.fail(conn, "expected a text join message")
		return
	}

	var join protocol.Envelope
	if err := json.Unmarshal(data, &join); err != nil || join.Type != protocol.TypeJoin {
		s.fail(conn, "the first message must be a join message")
		return
	}
	if err := protocol.ValidateNick(join.Nick); err != nil {
		s.fail(conn, "invalid nickname: "+err.Error())
		return
	}
	if err := s.hub.Add(c, join.Nick); err != nil {
		s.fail(conn, "nickname already in use")
		return
	}

	writerDone := make(chan struct{})
	go func() {
		s.writePump(c)
		close(writerDone)
	}()

	defer func() {
		s.hub.Remove(c.Nick)
		c.Stop()
		select {
		case <-writerDone:
		case <-time.After(s.cfg.WriteTimeout):
			c.Close()
			<-writerDone
		}
		c.Close()
		s.broadcastSystem(fmt.Sprintf("%s left", join.Nick))
		s.log.Info("client disconnected", "nick", join.Nick, "remote", remote)
	}()

	s.send(c, protocol.New(protocol.TypeWelcome, join.Nick, ""))
	s.broadcastSystemExcept(c, fmt.Sprintf("%s joined", join.Nick))
	s.log.Info("client connected", "nick", join.Nick, "remote", remote)

	conn.SetReadDeadline(time.Now().Add(s.cfg.PongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(s.cfg.PongWait))
	})
	conn.SetCloseHandler(func(int, string) error {
		return nil
	})

	limiter := rateLimiter{}

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				s.log.Debug("client read failed", "nick", join.Nick, "error", err)
			}
			return
		}
		if mt != websocket.TextMessage {
			s.sendError(c, "expected a text message")
			continue
		}

		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			s.sendError(c, "invalid JSON")
			continue
		}

		switch env.Type {
		case protocol.TypeMessage:
			if !limiter.allow(time.Now(), s.cfg.RateLimitPerSecond) {
				s.sendError(c, "rate limit exceeded")
				continue
			}
			if err := protocol.ValidateText(env.Text, s.cfg.MaxMessageBytes); err != nil {
				s.sendError(c, err.Error())
				continue
			}
			out := protocol.New(protocol.TypeMessage, join.Nick, env.Text)
			data, err := json.Marshal(out)
			if err != nil {
				s.log.Error("failed to marshal message", "error", err)
				s.sendError(c, "failed to encode message")
				continue
			}
			s.sendData(c, data)
			s.hub.Broadcast(c, data)
		default:
			s.sendError(c, "unsupported message type")
		}
	}
}

func (s *Server) writePump(c *hub.Client) {
	ticker := time.NewTicker(s.cfg.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case msg := <-c.Send():
			if err := s.writeMessage(c, websocket.TextMessage, msg); err != nil {
				c.Close()
				return
			}
		case <-ticker.C:
			if err := s.writeMessage(c, websocket.PingMessage, nil); err != nil {
				c.Close()
				return
			}
		case <-c.Done():
			for {
				select {
				case msg := <-c.Send():
					if err := s.writeMessage(c, websocket.TextMessage, msg); err != nil {
						return
					}
				default:
					_ = s.writeMessage(c, websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
					return
				}
			}
		}
	}
}

func (s *Server) writeMessage(c *hub.Client, messageType int, msg []byte) error {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout)); err != nil {
		return err
	}
	return c.Conn.WriteMessage(messageType, msg)
}

func (s *Server) send(c *hub.Client, env protocol.Envelope) {
	data, err := json.Marshal(env)
	if err != nil {
		s.log.Error("failed to marshal message", "error", err)
		return
	}
	s.sendData(c, data)
}

func (s *Server) sendData(c *hub.Client, data []byte) {
	if !c.Enqueue(data) {
		c.Close()
	}
}

func (s *Server) sendError(c *hub.Client, text string) {
	s.send(c, protocol.New(protocol.TypeError, "", text))
}

func (s *Server) broadcastSystem(text string) {
	data, err := json.Marshal(protocol.New(protocol.TypeSystem, "", text))
	if err != nil {
		s.log.Error("failed to marshal system message", "error", err)
		return
	}
	s.hub.Broadcast(nil, data)
}

func (s *Server) broadcastSystemExcept(c *hub.Client, text string) {
	data, err := json.Marshal(protocol.New(protocol.TypeSystem, "", text))
	if err != nil {
		s.log.Error("failed to marshal system message", "error", err)
		return
	}
	s.hub.Broadcast(c, data)
}

func (s *Server) fail(conn *websocket.Conn, text string) {
	data, err := json.Marshal(protocol.New(protocol.TypeError, "", text))
	if err != nil {
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout))
	_ = conn.WriteMessage(websocket.TextMessage, data)
	_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, text))
}

type rateLimiter struct {
	windowStart time.Time
	count       int
}

func (l *rateLimiter) allow(now time.Time, limit int) bool {
	if l.windowStart.IsZero() || now.Sub(l.windowStart) >= time.Second {
		l.windowStart = now
		l.count = 0
	}
	l.count++
	return l.count <= limit
}
