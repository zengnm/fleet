package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"fleetd/internal/fleetd"
)

func main() {
	cfg := fleetd.LoadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server, err := fleetd.NewServer(ctx, cfg)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := server.Run(ctx); err != nil && err.Error() != "http: Server closed" {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
