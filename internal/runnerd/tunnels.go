package runnerd

import (
	"encoding/json"
	"net/http"
	"strings"
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

	var guestResp Tunnel
	guestReq := map[string]any{
		"tunnel_id":  tunnelID,
		"host_port":  req.HostPort,
		"guest_port": req.GuestPort,
	}
	if err := gc.postJSON(r.Context(), "/internal/tunnels", guestReq, &guestResp); err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
		return
	}
	if guestResp.HostPort != req.HostPort || guestResp.HostPort <= 0 || guestResp.HostPort > 65535 {
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", "guest returned invalid host_port", nil)
		return
	}
	if guestResp.GuestPort <= 0 || guestResp.GuestPort > 65535 {
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", "guest returned invalid guest_port", nil)
		return
	}
	if strings.TrimSpace(guestResp.TunnelID) == "" {
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", "guest returned empty tunnel_id", nil)
		return
	}

	entry := &tunnelEntry{Tunnel: guestResp}

	s.mu.Lock()
	if oldID, ok := s.tunnelByHostPort[guestResp.HostPort]; ok && oldID != guestResp.TunnelID {
		delete(s.tunnels, oldID)
	}
	s.tunnels[guestResp.TunnelID] = entry
	s.tunnelByHostPort[guestResp.HostPort] = guestResp.TunnelID
	s.mu.Unlock()

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
		if id, ok2 := s.tunnelByHostPort[entry.HostPort]; ok2 && id == tunnelID {
			delete(s.tunnelByHostPort, entry.HostPort)
		}
	}
	s.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "tunnel not found", nil)
		return
	}

	if gc, err := s.ensureGuestReady(r.Context()); err == nil {
		_ = gc.delete(r.Context(), "/internal/tunnels/"+tunnelID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
