package runnerd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type createServiceRequest struct {
	Type          string           `json:"type"`
	ImageRef      string           `json:"image_ref"`
	Resources     serviceResources `json:"resources"`
	RWMounts      []string         `json:"rw_mounts"`
	ServiceConfig map[string]any   `json:"service_config"`
}

type serviceResources struct {
	CPUCores int `json:"cpu_cores"`
	MemoryMB int `json:"memory_mb"`
	Pids     int `json:"pids"`
}

func (s *Server) handleServicesCreate(w http.ResponseWriter, r *http.Request) {
	var req createServiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid json", nil)
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	req.ImageRef = strings.TrimSpace(req.ImageRef)
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "type is required", nil)
		return
	}
	if req.Type != "claude" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "unsupported service type", map[string]any{"type": req.Type})
		return
	}
	if req.ImageRef == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "image_ref is required", nil)
		return
	}
	if !(strings.HasPrefix(req.ImageRef, s.cfg.RegistryBase) || strings.HasPrefix(req.ImageRef, "local/")) {
		writeError(w, http.StatusBadRequest, "REGISTRY_NOT_ALLOWED", "image_ref must be official registry or local tag", map[string]any{"registry_base": s.cfg.RegistryBase})
		return
	}

	canonRW, err := s.validateAndPrepareRWMounts(req.RWMounts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "PATH_NOT_ALLOWED", err.Error(), nil)
		return
	}

	// Validate mcp_servers path if provided as a string.
	if v, ok := req.ServiceConfig["mcp_servers"]; ok {
		if p, ok := v.(string); ok && strings.TrimSpace(p) != "" {
			if _, _, ok := s.validateAllowedPath(p); !ok {
				writeError(w, http.StatusBadRequest, "PATH_NOT_ALLOWED", "mcp_servers path is not under any shared directory", nil)
				return
			}
		}
	}

	serviceID, err := newID("svc_", 12)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to allocate service_id", nil)
		return
	}

	s.mu.Lock()
	shares := make([]string, 0, len(s.shares))
	for _, e := range s.shares {
		shares = append(shares, e.CanonicalHostPath)
	}
	s.mu.Unlock()

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}

	payload, err := encodeServiceConfig(req.ServiceConfig)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid service_config", map[string]any{"error": err.Error()})
		return
	}

	guestReq := map[string]any{
		"service_id":         serviceID,
		"type":               req.Type,
		"image_ref":          req.ImageRef,
		"resources":          req.Resources,
		"shares":             shares,
		"rw_mounts":          canonRW,
		"service_config_b64": payload,
		"max_inline_bytes":   s.cfg.MaxInlineBytes,
	}

	var guestResp struct {
		ServiceID string `json:"service_id"`
		State     string `json:"state"`
	}
	if err := gc.postJSON(r.Context(), "/internal/services", guestReq, &guestResp); err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
		return
	}

	s.mu.Lock()
	s.services[serviceID] = Service{
		ServiceID: serviceID,
		Type:      req.Type,
		ImageRef:  req.ImageRef,
		State:     guestResp.State,
		CreatedAt: nowISO8601(),
	}
	_ = s.saveServicesLocked()
	s.mu.Unlock()

	writeJSON(w, 200, map[string]any{
		"service_id": serviceID,
		"state":      guestResp.State,
		"asp_url":    s.serviceASPURL(serviceID),
	})
}

func (s *Server) handleServicesList(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Service, 0, len(s.services))
	for _, svc := range s.services {
		out = append(out, svc)
	}
	writeJSON(w, 200, map[string]any{"services": out})
}

func (s *Server) handleServicesGet(w http.ResponseWriter, r *http.Request) {
	serviceID := strings.TrimSpace(r.PathValue("service_id"))
	if serviceID == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "service_id is required", nil)
		return
	}
	s.mu.Lock()
	svc, ok := s.services[serviceID]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "service not found", nil)
		return
	}
	writeJSON(w, 200, svc)
}

func (s *Server) handleServicesDelete(w http.ResponseWriter, r *http.Request) {
	serviceID := strings.TrimSpace(r.PathValue("service_id"))
	if serviceID == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "service_id is required", nil)
		return
	}
	s.mu.Lock()
	_, ok := s.services[serviceID]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "service not found", nil)
		return
	}

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}
	if err := gc.delete(r.Context(), "/internal/services/"+serviceID); err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
		return
	}

	s.mu.Lock()
	delete(s.services, serviceID)
	_ = s.saveServicesLocked()
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"deleted": true})
}

type snapshotRequest struct {
	NewTag string `json:"new_tag"`
}

func (s *Server) handleServicesSnapshot(w http.ResponseWriter, r *http.Request) {
	serviceID := strings.TrimSpace(r.PathValue("service_id"))
	if serviceID == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "service_id is required", nil)
		return
	}
	var req snapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid json", nil)
		return
	}
	req.NewTag = strings.TrimSpace(req.NewTag)
	if req.NewTag == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "new_tag is required", nil)
		return
	}
	if !strings.HasPrefix(req.NewTag, "local/") {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "new_tag must use local/* namespace", nil)
		return
	}

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}
	if err := gc.postJSON(r.Context(), "/internal/services/"+serviceID+"/snapshot", req, &map[string]any{}); err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) serviceASPURL(serviceID string) string {
	return "ws://" + s.cfg.ListenAddr + ":" + itoa(s.cfg.ListenPort) + "/v1/services/" + serviceID + "/chat"
}

func itoa(n int) string { return strconv.Itoa(n) }

func (s *Server) validateAndPrepareRWMounts(rw []string) ([]string, error) {
	out := make([]string, 0, len(rw))
	for _, p := range rw {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("rw_mount must be absolute: %q", p)
		}
		if err := os.MkdirAll(p, 0o700); err != nil {
			return nil, fmt.Errorf("rw_mount not writable: %q", p)
		}
		canon, _, ok := s.validateAllowedPath(p)
		if !ok {
			return nil, fmt.Errorf("rw_mount not allowed: %q", p)
		}
		out = append(out, canon)
	}
	return out, nil
}
