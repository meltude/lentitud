package client

import (
	"errors"
	"flag"
	"os"
)

func ParseConfig() (*Config, bool, error) {
	cfg := DefaultConfig()
	tui := true

	if v := os.Getenv("LENTITUD_SERVER"); v != "" {
		cfg.ServerURL = v
	}
	if v := os.Getenv("LENTITUD_NICK"); v != "" {
		cfg.Nick = v
	}

	fs := flag.NewFlagSet("client", flag.ExitOnError)
	fs.BoolVar(&tui, "tui", true, "use the interactive terminal UI when attached to a terminal")
	fs.StringVar(&cfg.ServerURL, "server", cfg.ServerURL, "server URL (ws:// or wss://)")
	fs.StringVar(&cfg.Nick, "nick", cfg.Nick, "nickname to use in chat")
	fs.BoolVar(&cfg.Insecure, "insecure", false, "skip TLS certificate verification")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, false, err
	}
	if cfg.Nick == "" {
		return nil, false, errors.New("a nickname is required: use -nick or define LENTITUD_NICK variable")
	}
	return &cfg, tui, nil
}
