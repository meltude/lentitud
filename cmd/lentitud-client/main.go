package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"golang.org/x/term"

	"codeberg.org/Dehydration/lentitud/internal/client"
)

func main() {
	log.SetFlags(0)
	cfg, tui, err := client.ParseConfig()
	if err != nil {
		log.Fatal(err)
	}

	c := client.New(cfg)
	if tui && term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		if err := client.RunInteractive(c); err != nil {
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
