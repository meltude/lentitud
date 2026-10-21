package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"golang.org/x/term"

	"codeberg.org/Dehydration/lentitud/internal/client"
)

func main() {
	log.SetFlags(0)
	cfg, tui, err := parseConfig()
	if err != nil {
		log.Fatal(err)
	}

	c := client.New(cfg)
	if tui && term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		if err := runInteractive(c); err != nil {
			fmt.Fprintln(os.Stderr, "client failed:", err)
			os.Exit(1)
		}
		return
	}
	if err := c.Run(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "client failed:", err)
		os.Exit(1)
	}
}

func parseConfig() (*client.Config, bool, error) {
	cfg := client.DefaultConfig()
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
