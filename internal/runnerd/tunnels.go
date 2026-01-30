package runnerd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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
			CreatedAt: nowISO8601(),
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

	go func() {
		<-done
		s.mu.Lock()
		cur, ok := s.tunnels[tunnelID]
		if !ok || cur != entry {
			s.mu.Unlock()
			return
		}
		delete(s.tunnels, tunnelID)
		if id, ok := s.tunnelByHostPort[entry.HostPort]; ok && id == tunnelID {
			delete(s.tunnelByHostPort, entry.HostPort)
		}
		s.mu.Unlock()
	}()

	writeJSON(w, http.StatusOK, entry.Tunnel)
}

func (s *Server) handleTunnelsList(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	out := make([]Tunnel, 0, len(s.tunnels))
	for tunnelID, entry := range s.tunnels {
		if entry == nil || entry.done == nil {
			delete(s.tunnels, tunnelID)
			if entry != nil {
				if id, ok := s.tunnelByHostPort[entry.HostPort]; ok && id == tunnelID {
					delete(s.tunnelByHostPort, entry.HostPort)
				}
			}
			continue
		}
		select {
		case <-entry.done:
			delete(s.tunnels, tunnelID)
			if id, ok := s.tunnelByHostPort[entry.HostPort]; ok && id == tunnelID {
				delete(s.tunnelByHostPort, entry.HostPort)
			}
			continue
		default:
		}
		out = append(out, entry.Tunnel)
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"tunnels": out})
}

func (s *Server) handleTunnelsGetByHostPort(w http.ResponseWriter, r *http.Request) {
	hostPortRaw := strings.TrimSpace(r.PathValue("host_port"))
	if hostPortRaw == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "host_port is required", nil)
		return
	}
	hostPort, err := strconv.Atoi(hostPortRaw)
	if err != nil || hostPort <= 0 || hostPort > 65535 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "host_port must be 1..65535", nil)
		return
	}

	s.mu.Lock()
	entry, ok := s.findRunningTunnelByHostPortLocked(hostPort)
	var out Tunnel
	if ok {
		out = entry.Tunnel
	}
	s.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "tunnel not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTunnelsDelete(w http.ResponseWriter, r *http.Request) {
	tunnelID := strings.TrimSpace(r.PathValue("tunnel_id"))
	if tunnelID == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "tunnel_id is required", nil)
		return
	}

	s.mu.Lock()
	cancel, ok := s.deleteTunnelLocked(tunnelID)
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

func (s *Server) handleTunnelsDeleteByHostPort(w http.ResponseWriter, r *http.Request) {
	hostPortRaw := strings.TrimSpace(r.PathValue("host_port"))
	if hostPortRaw == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "host_port is required", nil)
		return
	}
	hostPort, err := strconv.Atoi(hostPortRaw)
	if err != nil || hostPort <= 0 || hostPort > 65535 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "host_port must be 1..65535", nil)
		return
	}

	s.mu.Lock()
	entry, ok := s.findRunningTunnelByHostPortLocked(hostPort)
	var cancel context.CancelFunc
	if ok {
		cancel, ok = s.deleteTunnelLocked(entry.TunnelID)
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

func (s *Server) findRunningTunnelByHostPortLocked(hostPort int) (*tunnelEntry, bool) {
	tunnelID, ok := s.tunnelByHostPort[hostPort]
	if !ok {
		return nil, false
	}

	entry, ok := s.tunnels[tunnelID]
	if !ok || entry == nil || entry.done == nil {
		delete(s.tunnelByHostPort, hostPort)
		delete(s.tunnels, tunnelID)
		return nil, false
	}

	select {
	case <-entry.done:
		delete(s.tunnels, tunnelID)
		if id, ok := s.tunnelByHostPort[hostPort]; ok && id == tunnelID {
			delete(s.tunnelByHostPort, hostPort)
		}
		return nil, false
	default:
	}

	return entry, true
}

func (s *Server) deleteTunnelLocked(tunnelID string) (context.CancelFunc, bool) {
	entry, ok := s.tunnels[tunnelID]
	if !ok {
		return nil, false
	}

	delete(s.tunnels, tunnelID)
	if entry != nil {
		if id, ok := s.tunnelByHostPort[entry.HostPort]; ok && id == tunnelID {
			delete(s.tunnelByHostPort, entry.HostPort)
		}
	}

	if entry == nil {
		return nil, true
	}
	return entry.cancel, true
}
