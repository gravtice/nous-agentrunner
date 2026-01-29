package runnerd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"time"
)

func (s *Server) ensureGuestReady(ctx context.Context) (*guestClient, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("guest backend requires darwin host (AVF)")
	}

	step := func(name string, fn func() error) error {
		start := time.Now()
		log.Printf("%s: start", name)
		err := fn()
		if err != nil {
			log.Printf("%s: error after %s: %v", name, time.Since(start).Truncate(time.Millisecond), err)
			return err
		}
		log.Printf("%s: ok (%s)", name, time.Since(start).Truncate(time.Millisecond))
		return nil
	}

	if err := step("vm.ensure_running", func() error { return s.ensureVMRunning(ctx) }); err != nil {
		return nil, err
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", s.cfg.GuestForwardPort)
	client := &guestClient{baseURL: baseURL, http: &http.Client{Timeout: 30 * time.Second}}
	if err := step("guest.wait_health", func() error { return s.waitForGuestHealth(ctx, client, 5*time.Minute) }); err != nil {
		return nil, err
	}

	return client, nil
}

func (s *Server) waitForGuestHealth(ctx context.Context, c *guestClient, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		attemptCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := c.health(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	if last != nil {
		return fmt.Errorf("timeout waiting for guest health: %w", last)
	}
	return fmt.Errorf("timeout waiting for guest health")
}

