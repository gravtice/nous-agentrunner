package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gravtice/agent-runner/internal/guestrunnerd"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := guestrunnerd.Run(ctx); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
