package guestrunnerd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
)

type createServiceReq struct {
	ServiceID        string    `json:"service_id"`
	Type             string    `json:"type"`
	ImageRef         string    `json:"image_ref"`
	Resources        resources `json:"resources"`
	Shares           []string  `json:"shares"`
	RWMounts         []string  `json:"rw_mounts"`
	ServiceConfigB64 string    `json:"service_config_b64"`
	MaxInlineBytes   int64     `json:"max_inline_bytes"`
}

type resources struct {
	CPUCores int `json:"cpu_cores"`
	MemoryMB int `json:"memory_mb"`
	Pids     int `json:"pids"`
}

func (s *Server) handleServiceCreate(w http.ResponseWriter, r *http.Request) {
	var req createServiceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "BAD_REQUEST", "invalid json", nil)
		return
	}
	req.ServiceID = strings.TrimSpace(req.ServiceID)
	req.Type = strings.TrimSpace(req.Type)
	req.ImageRef = strings.TrimSpace(req.ImageRef)
	if req.ServiceID == "" || req.Type == "" || req.ImageRef == "" {
		writeError(w, 400, "BAD_REQUEST", "service_id/type/image_ref required", nil)
		return
	}
	if req.Type != "claude" {
		writeError(w, 400, "BAD_REQUEST", "unsupported service type", map[string]any{"type": req.Type})
		return
	}
	if _, err := base64.StdEncoding.DecodeString(req.ServiceConfigB64); err != nil {
		writeError(w, 400, "BAD_REQUEST", "invalid service_config_b64", nil)
		return
	}

	port, err := pickFreePort()
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	containerName := "svc-" + req.ServiceID
	if err := s.startServiceContainer(r.Context(), containerName, port, req); err != nil {
		writeError(w, 500, "NERDCTL_ERROR", err.Error(), nil)
		return
	}

	s.mu.Lock()
	s.state.Services[req.ServiceID] = Service{
		ServiceID:     req.ServiceID,
		Type:          req.Type,
		ImageRef:      req.ImageRef,
		ContainerName: containerName,
		Port:          port,
		State:         "running",
		CreatedAt:     nowISO8601(),
	}
	_ = s.saveStateLocked()
	s.mu.Unlock()

	writeJSON(w, 200, map[string]any{"service_id": req.ServiceID, "state": "running"})
}

func (s *Server) startServiceContainer(ctx context.Context, containerName string, port int, req createServiceReq) error {
	shareDirsJSON, _ := json.Marshal(req.Shares)
	shareDirsB64 := base64.StdEncoding.EncodeToString(shareDirsJSON)
	args := []string{
		"run",
		"-d",
		"--name", containerName,
		"--network=host",
		"--restart=unless-stopped",
		"-e", fmt.Sprintf("NOUS_SERVICE_PORT=%d", port),
		"-e", fmt.Sprintf("NOUS_SERVICE_CONFIG_B64=%s", req.ServiceConfigB64),
		"-e", fmt.Sprintf("NOUS_SHARE_DIRS_B64=%s", shareDirsB64),
		"-e", fmt.Sprintf("NOUS_MAX_INLINE_BYTES=%d", req.MaxInlineBytes),
	}

	if req.Resources.CPUCores > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%d", req.Resources.CPUCores))
	}
	if req.Resources.MemoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", req.Resources.MemoryMB))
	}
	if req.Resources.Pids > 0 {
		args = append(args, "--pids-limit", fmt.Sprintf("%d", req.Resources.Pids))
	}

	for _, p := range req.Shares {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		args = append(args, "--mount", fmt.Sprintf("type=bind,src=%s,dst=%s,ro", p, p))
	}
	for _, p := range req.RWMounts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		args = append(args, "--mount", fmt.Sprintf("type=bind,src=%s,dst=%s,rw", p, p))
	}

	args = append(args, req.ImageRef)
	// Ensure idempotency if caller retries.
	_, _ = runNerdctl(ctx, "rm", "-f", containerName)
	_, err := runNerdctl(ctx, args...)
	return err
}

func (s *Server) handleServicesList(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Service, 0, len(s.state.Services))
	for _, svc := range s.state.Services {
		out = append(out, svc)
	}
	writeJSON(w, 200, map[string]any{"services": out})
}

func (s *Server) handleServiceGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("service_id"))
	if id == "" {
		writeError(w, 400, "BAD_REQUEST", "service_id required", nil)
		return
	}
	s.mu.Lock()
	svc, ok := s.state.Services[id]
	s.mu.Unlock()
	if !ok {
		writeError(w, 404, "NOT_FOUND", "service not found", nil)
		return
	}
	writeJSON(w, 200, svc)
}

func (s *Server) handleServiceDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("service_id"))
	if id == "" {
		writeError(w, 400, "BAD_REQUEST", "service_id required", nil)
		return
	}
	s.mu.Lock()
	svc, ok := s.state.Services[id]
	s.mu.Unlock()
	if !ok {
		writeError(w, 404, "NOT_FOUND", "service not found", nil)
		return
	}

	_, _ = runNerdctl(r.Context(), "rm", "-f", svc.ContainerName)

	s.mu.Lock()
	delete(s.state.Services, id)
	_ = s.saveStateLocked()
	s.mu.Unlock()

	writeJSON(w, 200, map[string]any{"deleted": true})
}

type snapshotReq struct {
	NewTag string `json:"new_tag"`
}

func (s *Server) handleServiceSnapshot(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("service_id"))
	if id == "" {
		writeError(w, 400, "BAD_REQUEST", "service_id required", nil)
		return
	}
	var req snapshotReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "BAD_REQUEST", "invalid json", nil)
		return
	}
	req.NewTag = strings.TrimSpace(req.NewTag)
	if req.NewTag == "" {
		writeError(w, 400, "BAD_REQUEST", "new_tag required", nil)
		return
	}
	s.mu.Lock()
	svc, ok := s.state.Services[id]
	s.mu.Unlock()
	if !ok {
		writeError(w, 404, "NOT_FOUND", "service not found", nil)
		return
	}
	if _, err := runNerdctl(r.Context(), "commit", svc.ContainerName, req.NewTag); err != nil {
		writeError(w, 500, "NERDCTL_ERROR", err.Error(), nil)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected addr type %T", l.Addr())
	}
	return addr.Port, nil
}
