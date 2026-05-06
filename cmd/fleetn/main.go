package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"fleetd/internal/fleetnode"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := fleetnode.RunCLI(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
