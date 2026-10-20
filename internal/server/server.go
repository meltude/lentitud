package server

import (
	"context"
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

	"codeberg.org/Dehydration/lentitud/internal/hub"
	"codeberg.org/Dehydration/lentitud/internal/protocol"

	"github.com/gorilla/websocket"
)

type Config struct {
	Addr               string
	AllowedOrigins     []string
	MaxMessageBytes    int
	SendBuffer         int
	JoinTimeout        time.Duration
	WriteTimeout       time.Duration
	PingInterval       time.Duration
	PongWait           time.Duration
	RateLimitPerSecond int
	TLSCert            string
	TLSKey             string
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
	if cfg.MaxMessageBytes <= 0 {
		cfg.MaxMessageBytes = protocol.MaxMessageBytes
	}
	if cfg.SendBuffer <= 0 {
		cfg.SendBuffer = 256
	}
	if cfg.JoinTimeout <= 0 {
		cfg.JoinTimeout = 10 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = 30 * time.Second
	}
	if cfg.PongWait <= cfg.PingInterval {
		cfg.PongWait = 2 * cfg.PingInterval
	}
	if cfg.RateLimitPerSecond <= 0 {
		cfg.RateLimitPerSecond = 10
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 5 * time.Second
	}

	allowed := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, origin := range cfg.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowed[origin] = struct{}{}
		}
	}

	s := &Server{
		cfg:     cfg,
		hub:     hub.New(),
		log:     logger,
		allowed: allowed,
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
		clients := s.hub.CloseAll()
		s.interruptConnections()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		shutdownErr := srv.Shutdown(shutdownCtx)
		s.waitForHandlers(shutdownCtx, clients)
		if shutdownErr != nil {
			return shutdownErr
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-errCh:
		s.beginShutdown()
		s.hub.CloseAll()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
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
	s.hub.CloseAllNow()
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
		s.hub.Remove(c)
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
			data, err := out.Marshal()
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
	data, err := env.Marshal()
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
	data, err := protocol.New(protocol.TypeSystem, "", text).Marshal()
	if err != nil {
		s.log.Error("failed to marshal system message", "error", err)
		return
	}
	s.hub.Broadcast(nil, data)
}

func (s *Server) broadcastSystemExcept(c *hub.Client, text string) {
	data, err := protocol.New(protocol.TypeSystem, "", text).Marshal()
	if err != nil {
		s.log.Error("failed to marshal system message", "error", err)
		return
	}
	s.hub.Broadcast(c, data)
}

func (s *Server) fail(conn *websocket.Conn, text string) {
	data, err := protocol.New(protocol.TypeError, "", text).Marshal()
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
