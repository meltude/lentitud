package main

import (
	"errors"
	"flag"
	"testing"
)

func TestParseConfig(t *testing.T) {
	t.Parallel()

	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}

	t.Run("missing nickname", func(t *testing.T) {
		t.Parallel()
		if _, err := parseConfig(nil, env(nil)); err == nil {
			t.Fatal("parseConfig() error = nil, want nickname error")
		}
	})

	t.Run("environment", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseConfig(nil, env(map[string]string{
			"NKIT_SERVER": "wss://chat.example/ws",
			"NKIT_NICK":   "alice",
		}))
		if err != nil {
			t.Fatalf("parseConfig() error = %v", err)
		}
		if cfg.ServerURL != "wss://chat.example/ws" || cfg.Nick != "alice" {
			t.Fatalf("environment not applied: %#v", cfg)
		}
	})

	t.Run("flags override environment", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseConfig(
			[]string{"-server", "ws://localhost:9000/ws", "-nick", "bob", "-insecure"},
			env(map[string]string{"NKIT_SERVER": "ws://env.example/ws", "NKIT_NICK": "alice"}),
		)
		if err != nil {
			t.Fatalf("parseConfig() error = %v", err)
		}
		if cfg.ServerURL != "ws://localhost:9000/ws" || cfg.Nick != "bob" || !cfg.Insecure {
			t.Fatalf("flags not applied: %#v", cfg)
		}
	})

	t.Run("help", func(t *testing.T) {
		t.Parallel()
		if _, err := parseConfig([]string{"-h"}, env(nil)); !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("parseConfig(-h) error = %v, want flag.ErrHelp", err)
		}
	})
}
