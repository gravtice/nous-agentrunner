package runnerd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
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
	cancel context.CancelFunc
	done   <-chan error
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

	s.mu.Lock()
	if existingID, ok := s.tunnelByHostPort[req.HostPort]; ok {
		if e, ok := s.tunnels[existingID]; ok && e.done != nil {
			select {
			case <-e.done:
			default:
				out := e.Tunnel
				s.mu.Unlock()
				writeJSON(w, http.StatusOK, out)
				return
			}
		}
		delete(s.tunnels, existingID)
		delete(s.tunnelByHostPort, req.HostPort)
	}
	s.mu.Unlock()

	tunnelID, err := newID("tun_", 12)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to allocate tunnel_id", nil)
		return
	}

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}

	guestPort := req.GuestPort
	if guestPort == 0 {
		var portResp struct {
			Port int `json:"port"`
		}
		if err := gc.getJSON(r.Context(), "/internal/ports/free", &portResp); err != nil {
			writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
			return
		}
		guestPort = portResp.Port
	}
	if guestPort <= 0 || guestPort > 65535 {
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", fmt.Sprintf("invalid guest port %d", guestPort), nil)
		return
	}

	cancel, done, err := s.startReverseSSHTunnel(s.ctx, req.HostPort, guestPort)
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
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		},
		cancel: cancel,
		done:   done,
	}

	s.mu.Lock()
	if oldID, ok := s.tunnelByHostPort[req.HostPort]; ok && oldID != tunnelID {
		delete(s.tunnels, oldID)
	}
	s.tunnels[tunnelID] = entry
	s.tunnelByHostPort[req.HostPort] = tunnelID
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, entry.Tunnel)
}

func (s *Server) handleTunnelsDelete(w http.ResponseWriter, r *http.Request) {
	tunnelID := strings.TrimSpace(r.PathValue("tunnel_id"))
	if tunnelID == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "tunnel_id is required", nil)
		return
	}

	var cancel context.CancelFunc

	s.mu.Lock()
	entry, ok := s.tunnels[tunnelID]
	if ok {
		delete(s.tunnels, tunnelID)
		if id, ok2 := s.tunnelByHostPort[entry.HostPort]; ok2 && id == tunnelID {
			delete(s.tunnelByHostPort, entry.HostPort)
		}
		cancel = entry.cancel
	}
	s.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "tunnel not found", nil)
		return
	}

	if cancel != nil {
		cancel()
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
