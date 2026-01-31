package runnerd

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"
)

func isAddrInUseError(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

func runnerdResponding(addr string, port int, token string) bool {
	url := fmt.Sprintf("http://%s:%d/v1/system/status", addr, port)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func listenEphemeralDistinct(addr string, exclude int) (net.Listener, int, error) {
	for range 16 {
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:0", addr))
		if err != nil {
			return nil, 0, err
		}
		tcp, ok := ln.Addr().(*net.TCPAddr)
		if !ok {
			_ = ln.Close()
			return nil, 0, fmt.Errorf("unexpected addr type %T", ln.Addr())
		}
		if tcp.Port != exclude {
			return ln, tcp.Port, nil
		}
		_ = ln.Close()
	}
	return nil, 0, fmt.Errorf("failed to allocate ephemeral port distinct from %d", exclude)
}

// listenRunnerdHTTP attempts to listen on cfg.ListenAddr:cfg.ListenPort.
//
// If the configured port is already in use:
// - If an existing runnerd responds with our instance token, treat it as already running.
// - Otherwise, reassign to a new ephemeral port and persist it to .env.local.
func listenRunnerdHTTP(cfg Config) (net.Listener, Config, bool, error) {
	if cfg.ListenPort <= 0 {
		ln, port, err := listenEphemeralDistinct(cfg.ListenAddr, cfg.GuestForwardPort)
		if err != nil {
			return nil, cfg, false, err
		}
		cfg.ListenPort = port
		if err := persistEnvLocalUpdates(cfg.Paths, map[string]string{
			"NOUS_AGENT_RUNNER_PORT": strconv.Itoa(cfg.ListenPort),
		}); err != nil {
			_ = ln.Close()
			return nil, cfg, false, err
		}
		return ln, cfg, false, nil
	}

	addr := fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.ListenPort)
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, cfg, false, nil
	}
	if !isAddrInUseError(err) {
		return nil, cfg, false, err
	}
	if runnerdResponding(cfg.ListenAddr, cfg.ListenPort, cfg.Token) {
		return nil, cfg, true, nil
	}

	ln, port, err := listenEphemeralDistinct(cfg.ListenAddr, cfg.GuestForwardPort)
	if err != nil {
		return nil, cfg, false, err
	}
	cfg.ListenPort = port
	if err := persistEnvLocalUpdates(cfg.Paths, map[string]string{
		"NOUS_AGENT_RUNNER_PORT": strconv.Itoa(cfg.ListenPort),
	}); err != nil {
		_ = ln.Close()
		return nil, cfg, false, err
	}
	return ln, cfg, false, nil
}
