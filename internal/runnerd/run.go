package runnerd

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
)

func Run(ctx context.Context) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	setupLogging(cfg)

	s, err := NewServer(ctx, cfg)
	if err != nil {
		return err
	}
	go s.runIdleServiceReaper(ctx)

	addr := fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.ListenPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		cfg.ListenPort = tcp.Port
	}

	writeRuntimeFile(cfg)

	srv := &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
	}

	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	log.Printf("nous-agent-runnerd listening on http://%s", ln.Addr().String())
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
