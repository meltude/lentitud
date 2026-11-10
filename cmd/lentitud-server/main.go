package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"codeberg.org/Dehydration/lentitud/internal/server"
)

func main() {
	cfg, level, err := server.ParseConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(cfg, logger)
	if err := srv.Run(ctx); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}
