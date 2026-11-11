package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

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
	MaxClients         int
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
		MaxClients:         20,
		ShutdownTimeout:    5 * time.Second,
	}
}

// withDefaults fills unset fields of cfg with the values from DefaultConfig.
func withDefaults(cfg Config) Config {
	def := reflect.ValueOf(DefaultConfig())
	out := reflect.ValueOf(&cfg).Elem()
	for i := range out.NumField() {
		if out.Field(i).IsZero() {
			out.Field(i).Set(def.Field(i))
		}
	}
	return cfg
}

type Server struct {
	cfg      Config
	hub      *hub.Hub
	log      *slog.Logger
	allowed  map[string]struct{}
	upgrader websocket.Upgrader

	shutdown atomic.Bool
}

func New(cfg Config, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	s := &Server{
		cfg:     withDefaults(cfg),
		hub:     hub.New(),
		log:     logger,
		allowed: cfg.AllowedOrigins,
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
		s.shutdown.Store(true)
		s.broadcastSystem(nil, "server shutting down")
		s.hub.Close()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()

		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
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
