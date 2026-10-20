package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"codeberg.org/Dehydration/lentitud/internal/client"
)

func main() {
	cfg := client.DefaultConfig()

	if v := os.Getenv("LENTITUD_SERVER"); v != "" {
		cfg.ServerURL = v
	}
	if v := os.Getenv("LENTITUD_NICK"); v != "" {
		cfg.Nick = v
	}

	flag.StringVar(&cfg.ServerURL, "server", cfg.ServerURL, "server URL (ws:// or wss://)")
	flag.StringVar(&cfg.Nick, "nick", cfg.Nick, "nickname to use in chat")
	flag.BoolVar(&cfg.Insecure, "insecure", false, "skip TLS certificate verification")
	flag.Parse()

	if cfg.Nick == "" {
		fmt.Fprintln(os.Stderr, "a nickname is required: use -nick or LENTITUD_NICK")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := client.New(cfg)
	if err := c.Run(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "client failed:", err)
		os.Exit(1)
	}
}