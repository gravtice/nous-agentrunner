package runnerd

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
)

type Forward struct {
	ForwardID string `json:"forward_id"`
	HostPort  int    `json:"host_port"`
	GuestPort int    `json:"guest_port"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
}

type forwardEntry struct {
	Forward
	cmd *exec.Cmd
}

type forwardsCreateRequest struct {
	GuestPort int `json:"guest_port"`
	HostPort  int `json:"host_port"`
}

func (s *Server) handleForwardsCreate(w http.ResponseWriter, r *http.Request) {
	var req forwardsCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid json", nil)
		return
	}
	if req.GuestPort < 0 || req.GuestPort > 65535 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "guest_port must be 0..65535", nil)
		return
	}
	if req.HostPort < 0 || req.HostPort > 65535 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "host_port must be 0..65535", nil)
		return
	}

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

	// Idempotent: if the same guest_port already has a live forward, reuse it.
	s.mu.Lock()
	if id, ok := s.forwardByGuestPort[guestPort]; ok {
		if e, ok := s.forwards[id]; ok && isProcessRunningForward(e.cmd) {
			out := e.Forward
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, out)
			return
		}
		delete(s.forwardByGuestPort, guestPort)
		delete(s.forwards, id)
	}
	s.mu.Unlock()

	hostPort := req.HostPort
	if hostPort == 0 {
		p, err := pickFreeLocalPort()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		hostPort = p
	}

	tunnel, err := s.startSSHTunnel(s.ctx, hostPort, guestPort)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FORWARD_FAILED", err.Error(), nil)
		return
	}

	forwardID, err := newID("fwd_", 12)
	if err != nil {
		if tunnel.Process != nil {
			_ = tunnel.Process.Kill()
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to allocate forward_id", nil)
		return
	}

	entry := &forwardEntry{
		Forward: Forward{
			ForwardID: forwardID,
			HostPort:  hostPort,
			GuestPort: guestPort,
			State:     "running",
			CreatedAt: nowISO8601(),
		},
		cmd: tunnel,
	}

	s.mu.Lock()
	s.forwards[forwardID] = entry
	s.forwardByGuestPort[guestPort] = forwardID
	s.mu.Unlock()

	go func() {
		_ = tunnel.Wait()
		s.mu.Lock()
		defer s.mu.Unlock()
		cur, ok := s.forwards[forwardID]
		if !ok || cur.cmd != tunnel {
			return
		}
		delete(s.forwards, forwardID)
		if id, ok := s.forwardByGuestPort[guestPort]; ok && id == forwardID {
			delete(s.forwardByGuestPort, guestPort)
		}
	}()

	writeJSON(w, http.StatusOK, entry.Forward)
}

func (s *Server) handleForwardsDelete(w http.ResponseWriter, r *http.Request) {
	forwardID := strings.TrimSpace(r.PathValue("forward_id"))
	if forwardID == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "forward_id is required", nil)
		return
	}

	s.mu.Lock()
	entry, ok := s.forwards[forwardID]
	if ok {
		delete(s.forwards, forwardID)
		if id, ok := s.forwardByGuestPort[entry.GuestPort]; ok && id == forwardID {
			delete(s.forwardByGuestPort, entry.GuestPort)
		}
	}
	s.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "forward not found", nil)
		return
	}

	if entry.cmd != nil && entry.cmd.Process != nil {
		_ = entry.cmd.Process.Kill()
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func isProcessRunningForward(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return false
	}
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return false
	}
	return cmd.Process.Signal(syscall.Signal(0)) == nil
}
