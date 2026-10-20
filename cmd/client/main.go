package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"codeberg.org/Dehydration/lentitud/internal/client"
)

func main() {
	cfg, err := parseConfig(os.Args[1:], os.Getenv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 
		}
		fmt.Fprintln(os.Stderr, err)
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

func parseConfig(args []string, getenv func(string) string) (client.Config, error) {
	cfg := client.DefaultConfig()


	if v := getenv("LENTITUD_SERVER"); v != "" {
		cfg.ServerURL = v
	}
	if v := getenv("LENTITUD_NICK"); v != "" {
		cfg.Nick = v
	}
  
	fs := flag.NewFlagSet("client", flag.ContinueOnError)
	fs.StringVar(&cfg.ServerURL, "server", cfg.ServerURL, "server URL (ws:// or wss://)")
	fs.StringVar(&cfg.Nick, "nick", cfg.Nick, "nickname to use in chat")
	fs.BoolVar(&cfg.Insecure, "insecure", false, "skip TLS certificate verification")

	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if cfg.Nick == "" {
		return cfg, errors.New("a nickname is required: use -nick or LENTITUD_NICK")
	}
	return cfg, nil
}