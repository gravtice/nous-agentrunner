package runnerd

import (
	"context"
	"fmt"
	"log"
	"net/http"
)

func Run(ctx context.Context) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	writeRuntimeFile(cfg)

	s, err := NewServer(cfg)
	if err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.ListenPort)
	srv := &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
	}

	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	log.Printf("nous-agent-runnerd listening on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
