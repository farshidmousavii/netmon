package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/farshidmousavii/netmon/cmd/cli"
	"github.com/farshidmousavii/netmon/internal/logger"
)

func main() {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		sig := <-sigChan
		fmt.Println()
		logger.Warning("Received signal: %v - shutting down gracefully...", sig)
		cancel()

	}()

	if err := cli.ExecuteContext(ctx); err != nil {
		if ctx.Err() == context.Canceled {
			logger.Info("Shutdown completed")
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
