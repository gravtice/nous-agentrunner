package runnerd

import (
	"context"
	"errors"
	"net/http"
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
	s.mu.Lock()
	if s.sshTunnelCmd != nil && s.sshTunnelCmd.Process != nil {
		_ = s.sshTunnelCmd.Process.Kill()
	}
	s.sshTunnelCmd = nil
	s.guestClient = nil
	s.guestLocalPort = 0
	s.mu.Unlock()

	// Stop may fail if the VM is not running; ignore common failures.
	_, err := s.runLimactl(ctx, "stop", s.cfg.LimaInstanceName)
	if err != nil && !errors.Is(err, context.Canceled) {
		// Continue; start will create/start anyway.
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
