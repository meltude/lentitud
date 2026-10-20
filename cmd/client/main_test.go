package main

import (
	"os"
	"testing"
)

func TestParseConfig(t *testing.T) {
	oldArgs := os.Args
	defer func() {
		os.Args = oldArgs
	}()
	os.Args = []string{"somebody kill me"}

	t.Run("missing nickname", func(t *testing.T) {
		if _, err := parseConfig(); err == nil {
			t.Fatal("parseConfig() error = nil, want nickname error")
		}
	})

	t.Run("environment", func(t *testing.T) {
		t.Setenv("LENTITUD_SERVER", "wss://chat.example/ws")
		t.Setenv("LENTITUD_NICK", "alice")
		cfg, err := parseConfig()
		if err != nil {
			t.Fatalf("parseConfig() error = %v", err)
		}
		if cfg.ServerURL != "wss://chat.example/ws" || cfg.Nick != "alice" {
			t.Fatalf("environment not applied: %#v", cfg)
		}
	})

	t.Run("flags override environment", func(t *testing.T) {
		oldArgs := os.Args
		defer func() {
			os.Args = oldArgs
		}()
		os.Args = []string{"ihatethisfuckingcode", "-server", "ws://localhost:9000/ws", "-nick", "bob", "-insecure"}
		t.Setenv("LENTITUD_SERVER", "wss://chat.example/ws")
		t.Setenv("LENTITUD_NICK", "alice")
		cfg, err := parseConfig()
		if err != nil {
			t.Fatalf("parseConfig() error = %v", err)
		}
		if cfg.ServerURL != "ws://localhost:9000/ws" || cfg.Nick != "bob" || !cfg.Insecure {
			t.Fatalf("flags not applied: %#v", cfg)
		}
	})
}
