package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"codeberg.org/Dehydration/lentitud/internal/client"
	"github.com/chzyer/readline"
)

func runInteractive(c *client.Client) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "~> ",
		InterruptPrompt: "",
		EOFPrompt:       "",
	})
	if err != nil {
		return fmt.Errorf("initialize input: %w", err)
	}
	rl.CaptureExitSignal()
	defer rl.Close()

	inR, inW := io.Pipe()
	defer inR.Close()
	clientDone := make(chan error, 1)
	go func() {
		clientDone <- c.Run(ctx, inR, rl.Stdout())
	}()

	for {
		line, readErr := rl.Readline()
		if readErr != nil {
			if errors.Is(readErr, readline.ErrInterrupt) || errors.Is(readErr, io.EOF) {
				break
			}
			if ctx.Err() != nil {
				break
			}
			_ = inW.CloseWithError(readErr)
			cancel()
			<-clientDone
			return fmt.Errorf("read input: %w", readErr)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := fmt.Fprintln(inW, line); err != nil {
			if ctx.Err() != nil {
				break
			}
			cancel()
			<-clientDone
			return fmt.Errorf("send input: %w", err)
		}
	}

	cancel()
	_ = inW.Close()
	return <-clientDone
}
