package guestrunnerd

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
)

func Run(ctx context.Context) error {
	port := 17777
	if v := os.Getenv("NOUS_GUEST_RUNNERD_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			port = n
		}
	}

	hostTunnelVsockPort := 0
	if v := os.Getenv("NOUS_HOST_TUNNEL_VSOCK_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			hostTunnelVsockPort = n
		}
	}

	stateDir := os.Getenv("NOUS_GUEST_RUNNERD_STATE_DIR")
	if stateDir == "" {
		stateDir = "/var/lib/nous-guest-runnerd"
	}

	s, err := NewServer(Config{
		ListenAddr:          "127.0.0.1",
		ListenPort:          port,
		StateDir:            stateDir,
		HostTunnelVsockPort: hostTunnelVsockPort,
	})
	if err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", s.config.ListenAddr, s.config.ListenPort)
	srv := &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
	}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	log.Printf("nous-guest-runnerd listening on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
