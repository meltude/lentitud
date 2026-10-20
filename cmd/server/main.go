package main

import (
	"context"
	"errors"
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
	cfg, level, err := parseConfig(os.Args[1:], os.Getenv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
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

func parseConfig(args []string, getenv func(string) string) (server.Config, slog.Level, error) {
	cfg := server.DefaultConfig()

	if v := getenv("NKIT_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := getenv("NKIT_TLS_CERT"); v != "" {
		cfg.TLSCert = v
	}
	if v := getenv("NKIT_TLS_KEY"); v != "" {
		cfg.TLSKey = v
	}

	originsDefault := getenv("NKIT_ALLOWED_ORIGINS")
	logLevelDefault := getenv("NKIT_LOG_LEVEL")

	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	origins := fs.String("allowed-origins", originsDefault, "comma-separated list of additional allowed origins")
	addr := fs.String("addr", cfg.Addr, "HTTP listen address")
	maxMessageBytes := fs.Int("max-message-bytes", cfg.MaxMessageBytes, "maximum message size in bytes")
	joinTimeout := fs.Duration("join-timeout", cfg.JoinTimeout, "time allowed to send the join message")
	pingInterval := fs.Duration("ping-interval", cfg.PingInterval, "WebSocket ping interval")
	pongWait := fs.Duration("pong-wait", cfg.PongWait, "time to wait for a pong before disconnecting")
	rateLimit := fs.Int("rate-limit", cfg.RateLimitPerSecond, "maximum messages per second per client")
	tlsCert := fs.String("tls-cert", cfg.TLSCert, "TLS certificate file")
	tlsKey := fs.String("tls-key", cfg.TLSKey, "TLS private key file")
	logLevel := fs.String("log-level", logLevelDefault, "log level: debug, info, warn, error")

	if err := fs.Parse(args); err != nil {
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
	cfg.AllowedOrigins = splitList(*origins)

	return cfg, parseLevel(*logLevel), nil
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
