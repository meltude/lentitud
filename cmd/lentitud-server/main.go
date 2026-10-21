package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"codeberg.org/Dehydration/lentitud/internal/server"
)

func main() {
	cfg, level, err := parseConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(cfg, logger)
	if err := srv.Run(ctx); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func parseConfig() (server.Config, slog.Level, error) {
	cfg := server.DefaultConfig()

	if v := os.Getenv("LENTITUD_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := os.Getenv("LENTITUD_TLS_CERT"); v != "" {
		cfg.TLSCert = v
	}
	if v := os.Getenv("LENTITUD_TLS_KEY"); v != "" {
		cfg.TLSKey = v
	}
	if v := os.Getenv("LENTITUD_WEBHOOK_SECRET"); v != "" {
		cfg.WebhookSecret = v
	}

	originsDefault := os.Getenv("LENTITUD_ALLOWED_ORIGINS")
	logLevelDefault := os.Getenv("LENTITUD_LOG_LEVEL")

	fs := flag.NewFlagSet("server", flag.ExitOnError)
	origins := fs.String("allowed-origins", originsDefault, "comma-separated list of additional allowed origins")
	addr := fs.String("addr", cfg.Addr, "HTTP listen address")
	maxMessageBytes := fs.Int("max-message-bytes", cfg.MaxMessageBytes, "maximum message size in bytes")
	joinTimeout := fs.Duration("join-timeout", cfg.JoinTimeout, "time allowed to send the join message")
	pingInterval := fs.Duration("ping-interval", cfg.PingInterval, "WebSocket ping interval")
	pongWait := fs.Duration("pong-wait", cfg.PongWait, "time to wait for a pong before disconnecting")
	rateLimit := fs.Int("rate-limit", cfg.RateLimitPerSecond, "maximum messages per second per client")
	tlsCert := fs.String("tls-cert", cfg.TLSCert, "TLS certificate file")
	tlsKey := fs.String("tls-key", cfg.TLSKey, "TLS private key file")
	webhookSecret := fs.String("webhook-secret", cfg.WebhookSecret, "shared secret for POST /webhook; empty disables the endpoint")
	logLevel := fs.String("log-level", logLevelDefault, "log level: debug, info, warn, error")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return cfg, 0, err
	}

	cfg.Addr = *addr
	cfg.MaxMessageBytes = *maxMessageBytes
	cfg.JoinTimeout = *joinTimeout
	cfg.PingInterval = *pingInterval
	cfg.PongWait = *pongWait
	cfg.RateLimitPerSecond = *rateLimit
	cfg.TLSCert = *tlsCert
	cfg.TLSKey = *tlsKey
	cfg.WebhookSecret = *webhookSecret

	if strings.TrimSpace(*origins) == "" {
		cfg.AllowedOrigins = nil
	}
	parts := strings.Split(*origins, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	for _, origin := range out {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			cfg.AllowedOrigins[origin] = struct{}{}
		}
	}

	return cfg, parseLevel(*logLevel), nil
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
