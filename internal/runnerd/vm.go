package runnerd

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func (s *Server) handleVMRestart(w http.ResponseWriter, r *http.Request) {
	if runtime.GOOS != "darwin" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "vm backend only supported on darwin", nil)
		return
	}

	if err := s.restartVM(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "VM_RESTART_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) restartVM(ctx context.Context) error {
	start := time.Now()
	s.mu.Lock()
	recreate := s.vmRestartRequired
	s.mu.Unlock()

	log.Printf("vm.restart: start (recreate=%v)", recreate)
	defer func() {
		log.Printf("vm.restart: done (%s)", time.Since(start).Truncate(time.Millisecond))
	}()

	// Stop may fail if the VM is not running; ignore common failures.
	if _, err := os.Stat(filepath.Join(s.limaInstanceDir(), "lima.yaml")); err == nil {
		_, err := s.runLimactl(ctx, "stop", s.cfg.LimaInstanceName)
		if err != nil && !errors.Is(err, context.Canceled) {
			// Continue; start will create/start anyway.
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("vm.restart: stat instance: %v", err)
	}
	if recreate {
		// Shares are configured at VM creation time; to apply share changes, recreate the instance.
		_, _ = s.runLimactl(ctx, "delete", "-f", s.cfg.LimaInstanceName)
		s.mu.Lock()
		if len(s.services) > 0 || len(s.serviceCreateCfgs) > 0 {
			clear(s.services)
			clear(s.serviceCreateCfgs)
			_ = s.saveServicesLocked()
		}
		s.mu.Unlock()
	}
	// Give Lima a moment to release resources.
	time.Sleep(500 * time.Millisecond)

	if err := s.ensureVMRunning(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	s.vmRestartRequired = false
	s.mu.Unlock()
	return nil
}
