package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"codeberg.org/samposa_1337/Nkit/internal/server"
)

func main() {
	cfg := server.DefaultConfig()

	if v := os.Getenv("NKIT_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := os.Getenv("NKIT_TLS_CERT"); v != "" {
		cfg.TLSCert = v
	}
	if v := os.Getenv("NKIT_TLS_KEY"); v != "" {
		cfg.TLSKey = v
	}

	originsDefault := os.Getenv("NKIT_ALLOWED_ORIGINS")
	logLevelDefault := os.Getenv("NKIT_LOG_LEVEL")

	origins := flag.String("allowed-origins", originsDefault, "comma-separated list of additional allowed origins")
	addr := flag.String("addr", cfg.Addr, "HTTP listen address")
	maxMessageBytes := flag.Int("max-message-bytes", cfg.MaxMessageBytes, "maximum message size in bytes")
	joinTimeout := flag.Duration("join-timeout", cfg.JoinTimeout, "time allowed to send the join message")
	pingInterval := flag.Duration("ping-interval", cfg.PingInterval, "WebSocket ping interval")
	pongWait := flag.Duration("pong-wait", cfg.PongWait, "time to wait for a pong before disconnecting")
	rateLimit := flag.Int("rate-limit", cfg.RateLimitPerSecond, "maximum messages per second per client")
	tlsCert := flag.String("tls-cert", cfg.TLSCert, "TLS certificate file")
	tlsKey := flag.String("tls-key", cfg.TLSKey, "TLS private key file")
	logLevel := flag.String("log-level", logLevelDefault, "log level: debug, info, warn, error")
	flag.Parse()

	cfg.Addr = *addr
	cfg.MaxMessageBytes = *maxMessageBytes
	cfg.JoinTimeout = *joinTimeout
	cfg.PingInterval = *pingInterval
	cfg.PongWait = *pongWait
	cfg.RateLimitPerSecond = *rateLimit
	cfg.TLSCert = *tlsCert
	cfg.TLSKey = *tlsKey
	cfg.AllowedOrigins = splitList(*origins)

	level := parseLevel(*logLevel)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(cfg, logger)
	if err := srv.Run(ctx); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
