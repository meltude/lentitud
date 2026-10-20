package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"codeberg.org/Dehydration/lentitud/internal/client"
)

func main() {
	log.SetFlags(0)
	cfg, err := parseConfig()
	if err != nil {
		log.Fatal(err)
	}

	c := client.New(cfg)
	go c.HandleShutdown()
	if err := c.Run(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "client failed:", err)
		os.Exit(1)
	}
}

func parseConfig() (*client.Config, error) {
	cfg := client.DefaultConfig()

	if v := os.Getenv("LENTITUD_SERVER"); v != "" {
		cfg.ServerURL = v
	}
	if v := os.Getenv("LENTITUD_NICK"); v != "" {
		cfg.Nick = v
	}

	fs := flag.NewFlagSet("client", flag.ExitOnError)
	fs.StringVar(&cfg.ServerURL, "server", cfg.ServerURL, "server URL (ws:// or wss://)")
	fs.StringVar(&cfg.Nick, "nick", cfg.Nick, "nickname to use in chat")
	fs.BoolVar(&cfg.Insecure, "insecure", false, "skip TLS certificate verification")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, err
	}
	if cfg.Nick == "" {
		return nil, errors.New("a nickname is required: use -nick or LENTITUD_NICK")
	}
	return &cfg, nil
}
