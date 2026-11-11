package server

import (
	"flag"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

func ParseConfig() (Config, slog.Level, error) {
	cfg := DefaultConfig()
	applyEnv(&cfg)

	origins := os.Getenv("LENTITUD_ALLOWED_ORIGINS")
	logLevel := os.Getenv("LENTITUD_LOG_LEVEL")

	fs := flag.NewFlagSet("server", flag.ExitOnError)
	stringVar := func(dst *string, name, usage string) { fs.StringVar(dst, name, *dst, usage) }
	intVar := func(dst *int, name, usage string) { fs.IntVar(dst, name, *dst, usage) }
	durationVar := func(dst *time.Duration, name, usage string) { fs.DurationVar(dst, name, *dst, usage) }

	stringVar(&cfg.Addr, "addr", "HTTP listen address")
	stringVar(&origins, "allowed-origins", "comma-separated list of additional allowed origins")
	intVar(&cfg.MaxMessageBytes, "max-message-bytes", "maximum message size in bytes")
	durationVar(&cfg.JoinTimeout, "join-timeout", "time allowed to send the join message")
	durationVar(&cfg.PingInterval, "ping-interval", "WebSocket ping interval")
	durationVar(&cfg.PongWait, "pong-wait", "time to wait for a pong before disconnecting")
	intVar(&cfg.RateLimitPerSecond, "rate-limit", "maximum messages per second per client")
	stringVar(&cfg.TLSCert, "tls-cert", "TLS certificate file")
	stringVar(&cfg.TLSKey, "tls-key", "TLS private key file")
	stringVar(&cfg.WebhookSecret, "webhook-secret", "shared secret for POST /webhook; empty disables the endpoint")
	intVar(&cfg.MaxClients, "max-users", "maximum number of connected users")
	stringVar(&logLevel, "log-level", "log level: debug, info, warn, error")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return cfg, 0, err
	}

	if origins = strings.TrimSpace(origins); origins != "" {
		cfg.AllowedOrigins = make(map[string]struct{})
		for _, part := range strings.Split(origins, ",") {
			if part = strings.TrimSpace(part); part != "" {
				cfg.AllowedOrigins[part] = struct{}{}
			}
		}
	}

	return cfg, parseLevel(logLevel), nil
}

func applyEnv(cfg *Config) {
	for _, env := range []struct {
		name string
		dst  *string
	}{
		{"LENTITUD_ADDR", &cfg.Addr},
		{"LENTITUD_TLS_CERT", &cfg.TLSCert},
		{"LENTITUD_TLS_KEY", &cfg.TLSKey},
		{"LENTITUD_WEBHOOK_SECRET", &cfg.WebhookSecret},
	} {
		if v := os.Getenv(env.name); v != "" {
			*env.dst = v
		}
	}

	for _, env := range []struct {
		name string
		dst  *int
	}{
		{"LENTITUD_MAX_USERS", &cfg.MaxClients},
	} {
		if v := os.Getenv(env.name); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*env.dst = n
			}
		}
	}
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
