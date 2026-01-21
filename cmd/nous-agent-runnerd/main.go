package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gravtice/nous-agent-runner/internal/runnerd"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := runnerd.Run(ctx); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
