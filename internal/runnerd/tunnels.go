package runnerd

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
)

type Tunnel struct {
	TunnelID  string `json:"tunnel_id"`
	HostPort  int    `json:"host_port"`
	GuestPort int    `json:"guest_port"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
}

type tunnelEntry struct {
	Tunnel
	cmd *exec.Cmd
}

type tunnelsCreateRequest struct {
	HostPort  int `json:"host_port"`
	GuestPort int `json:"guest_port"`
}

func (s *Server) handleTunnelsCreate(w http.ResponseWriter, r *http.Request) {
	var req tunnelsCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid json", nil)
		return
	}
	if req.HostPort <= 0 || req.HostPort > 65535 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "host_port must be 1..65535", nil)
		return
	}
	if req.GuestPort < 0 || req.GuestPort > 65535 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "guest_port must be 0..65535", nil)
		return
	}

	// Idempotent: if the same host_port already has a live tunnel, reuse it.
	s.mu.Lock()
	if id, ok := s.tunnelByHostPort[req.HostPort]; ok {
		if e, ok := s.tunnels[id]; ok && isProcessRunning(e.cmd) {
			out := e.Tunnel
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, out)
			return
		}
		delete(s.tunnelByHostPort, req.HostPort)
		delete(s.tunnels, id)
	}
	s.mu.Unlock()

	guestPort := req.GuestPort
	if guestPort == 0 {
		gc, err := s.ensureGuestReady(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
			return
		}
		var resp struct {
			Port int `json:"port"`
		}
		if err := gc.getJSON(r.Context(), "/internal/ports/free", &resp); err != nil {
			writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
			return
		}
		if resp.Port <= 0 || resp.Port > 65535 {
			writeError(w, http.StatusInternalServerError, "GUEST_ERROR", "guest returned invalid port", nil)
			return
		}
		guestPort = resp.Port
	}

	tunnelID, err := newID("tun_", 12)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to allocate tunnel_id", nil)
		return
	}
	cmd, err := s.startSSHReverseTunnel(s.ctx, guestPort, req.HostPort)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TUNNEL_FAILED", err.Error(), nil)
		return
	}

	entry := &tunnelEntry{
		Tunnel: Tunnel{
			TunnelID:  tunnelID,
			HostPort:  req.HostPort,
			GuestPort: guestPort,
			State:     "running",
			CreatedAt: nowISO8601(),
		},
		cmd: cmd,
	}

	s.mu.Lock()
	s.tunnels[tunnelID] = entry
	s.tunnelByHostPort[req.HostPort] = tunnelID
	s.mu.Unlock()

	go func() {
		_ = cmd.Wait()
		s.mu.Lock()
		defer s.mu.Unlock()
		cur, ok := s.tunnels[tunnelID]
		if !ok || cur.cmd != cmd {
			return
		}
		delete(s.tunnels, tunnelID)
		if id, ok := s.tunnelByHostPort[req.HostPort]; ok && id == tunnelID {
			delete(s.tunnelByHostPort, req.HostPort)
		}
	}()

	writeJSON(w, http.StatusOK, entry.Tunnel)
}

func (s *Server) handleTunnelsDelete(w http.ResponseWriter, r *http.Request) {
	tunnelID := strings.TrimSpace(r.PathValue("tunnel_id"))
	if tunnelID == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "tunnel_id is required", nil)
		return
	}

	s.mu.Lock()
	entry, ok := s.tunnels[tunnelID]
	if ok {
		delete(s.tunnels, tunnelID)
		if id, ok := s.tunnelByHostPort[entry.HostPort]; ok && id == tunnelID {
			delete(s.tunnelByHostPort, entry.HostPort)
		}
	}
	s.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "tunnel not found", nil)
		return
	}

	if entry.cmd != nil && entry.cmd.Process != nil {
		_ = entry.cmd.Process.Kill()
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func isProcessRunning(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return false
	}
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return false
	}
	return cmd.Process.Signal(syscall.Signal(0)) == nil
}
