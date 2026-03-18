package runnerd

import (
	"context"
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

	ln, cfg, alreadyRunning, err := listenRunnerdHTTP(cfg)
	if err != nil {
		return err
	}
	if alreadyRunning {
		log.Printf("agent-runnerd already running on http://%s:%d", cfg.ListenAddr, cfg.ListenPort)
		return nil
	}
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		cfg.ListenPort = tcp.Port
	}

	cleanupOrphanSSHTunnels(cfg)

	s, err := NewServer(ctx, cfg)
	if err != nil {
		_ = ln.Close()
		return err
	}
	if err := s.startVsockTunnelServer(ctx); err != nil {
		_ = ln.Close()
		return err
	}

	writeRuntimeFile(cfg)

	go s.runIdleServiceReaper(ctx)

	addr := ln.Addr().String()
	srv := &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
	}

	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	log.Printf("agent-runnerd listening on http://%s", ln.Addr().String())
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
