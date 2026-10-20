package main

import (
	"errors"
	"flag"
	"log/slog"
	"slices"
	"testing"
	"time"

	"codeberg.org/Dehydration/lentitud/internal/server"
)

func TestSplitList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty", in: "", want: nil},
		{name: "whitespace", in: "   ", want: nil},
		{name: "single", in: "http://a.example", want: []string{"http://a.example"}},
		{name: "commas", in: "a, b ,c", want: []string{"a", "b", "c"}},
		{name: "empty parts", in: "a,,b,", want: []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := splitList(tt.in); !slices.Equal(got, tt.want) {
				t.Fatalf("splitList(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want slog.Level
	}{
		{in: "debug", want: slog.LevelDebug},
		{in: "DEBUG", want: slog.LevelDebug},
		{in: " info ", want: slog.LevelInfo},
		{in: "warn", want: slog.LevelWarn},
		{in: "error", want: slog.LevelError},
		{in: "nonsense", want: slog.LevelInfo},
		{in: "", want: slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if got := parseLevel(tt.in); got != tt.want {
				t.Fatalf("parseLevel(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseConfig(t *testing.T) {
	t.Parallel()

	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()
		cfg, level, err := parseConfig(nil, env(nil))
		if err != nil {
			t.Fatalf("parseConfig() error = %v", err)
		}
		want := server.DefaultConfig()
		if cfg.Addr != want.Addr {
			t.Fatalf("Addr = %q, want %q", cfg.Addr, want.Addr)
		}
		if cfg.MaxMessageBytes != want.MaxMessageBytes {
			t.Fatalf("MaxMessageBytes = %d, want %d", cfg.MaxMessageBytes, want.MaxMessageBytes)
		}
		if level != slog.LevelInfo {
			t.Fatalf("level = %v, want info", level)
		}
	})

	t.Run("environment", func(t *testing.T) {
		t.Parallel()
		cfg, level, err := parseConfig(nil, env(map[string]string{
			"NKIT_ADDR":            ":9000",
			"NKIT_TLS_CERT":        "cert.pem",
			"NKIT_TLS_KEY":         "key.pem",
			"NKIT_ALLOWED_ORIGINS": "http://a.example, http://b.example",
			"NKIT_LOG_LEVEL":       "debug",
		}))
		if err != nil {
			t.Fatalf("parseConfig() error = %v", err)
		}
		if cfg.Addr != ":9000" || cfg.TLSCert != "cert.pem" || cfg.TLSKey != "key.pem" {
			t.Fatalf("environment not applied: %#v", cfg)
		}
		if want := []string{"http://a.example", "http://b.example"}; !slices.Equal(cfg.AllowedOrigins, want) {
			t.Fatalf("AllowedOrigins = %#v, want %#v", cfg.AllowedOrigins, want)
		}
		if level != slog.LevelDebug {
			t.Fatalf("level = %v, want debug", level)
		}
	})

	t.Run("flags override environment", func(t *testing.T) {
		t.Parallel()
		cfg, _, err := parseConfig(
			[]string{"-addr", ":7000", "-allowed-origins", "http://flag.example", "-log-level", "warn"},
			env(map[string]string{"NKIT_ADDR": ":9000", "NKIT_LOG_LEVEL": "debug", "NKIT_ALLOWED_ORIGINS": "http://env.example"}),
		)
		if err != nil {
			t.Fatalf("parseConfig() error = %v", err)
		}
		if cfg.Addr != ":7000" {
			t.Fatalf("Addr = %q, want :7000", cfg.Addr)
		}
		if want := []string{"http://flag.example"}; !slices.Equal(cfg.AllowedOrigins, want) {
			t.Fatalf("AllowedOrigins = %#v, want %#v", cfg.AllowedOrigins, want)
		}
	})

	t.Run("durations and limits", func(t *testing.T) {
		t.Parallel()
		cfg, _, err := parseConfig(
			[]string{"-ping-interval", "45s", "-pong-wait", "90s", "-rate-limit", "3", "-max-message-bytes", "512"},
			env(nil),
		)
		if err != nil {
			t.Fatalf("parseConfig() error = %v", err)
		}
		if cfg.PingInterval != 45*time.Second || cfg.PongWait != 90*time.Second {
			t.Fatalf("durations = %v/%v", cfg.PingInterval, cfg.PongWait)
		}
		if cfg.RateLimitPerSecond != 3 || cfg.MaxMessageBytes != 512 {
			t.Fatalf("limits = %d/%d", cfg.RateLimitPerSecond, cfg.MaxMessageBytes)
		}
	})

	t.Run("invalid value", func(t *testing.T) {
		t.Parallel()
		if _, _, err := parseConfig([]string{"-ping-interval", "nope"}, env(nil)); err == nil {
			t.Fatal("parseConfig() error = nil, want error")
		}
	})

	t.Run("help", func(t *testing.T) {
		t.Parallel()
		if _, _, err := parseConfig([]string{"-h"}, env(nil)); !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("parseConfig(-h) error = %v, want flag.ErrHelp", err)
		}
	})
}
