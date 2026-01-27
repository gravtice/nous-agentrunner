package guestrunnerd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type createServiceReq struct {
	ServiceID        string            `json:"service_id"`
	Type             string            `json:"type"`
	ImageRef         string            `json:"image_ref"`
	Resources        resources         `json:"resources"`
	Shares           []string          `json:"shares"`
	RWMounts         []string          `json:"rw_mounts"`
	Env              map[string]string `json:"env"`
	ServiceConfigB64 string            `json:"service_config_b64"`
	SkillsDir        string            `json:"skills_dir"`
	MaxInlineBytes   int64             `json:"max_inline_bytes"`
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
	if err := validateServiceEnv(req.Env); err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error(), nil)
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
	if err := waitHTTPHealth(r.Context(), port, 30*time.Second); err != nil {
		_, _ = runNerdctl(r.Context(), "rm", "-f", containerName)
		writeError(w, 500, "SERVICE_UNHEALTHY", err.Error(), nil)
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

func waitHTTPHealth(ctx context.Context, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error

	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		resp, err := client.Do(req)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
			msg := strings.TrimSpace(string(body))
			if msg == "" {
				msg = resp.Status
			}
			lastErr = fmt.Errorf("health status %d: %s", resp.StatusCode, msg)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}

	if lastErr != nil {
		return fmt.Errorf("timeout waiting for service health: %w", lastErr)
	}
	return fmt.Errorf("timeout waiting for service health")
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
		// Claude Code refuses to run with --dangerously-skip-permissions under root.
		// Run as the VM's primary non-root user (usually the Lima user) for claude services.
	}
	if req.Type == "claude" {
		if uid, gid, ok := detectUserForWorkDir(req.ServiceConfigB64); ok {
			args = append(args, "--user", fmt.Sprintf("%d:%d", uid, gid))
		} else if uid, gid, ok := detectPrimaryUser(); ok {
			args = append(args, "--user", fmt.Sprintf("%d:%d", uid, gid))
		}
		// Ensure Claude CLI has a writable HOME even if the image defaults to /root.
		args = append(args, "-e", "HOME=/tmp")
	}
	args = append(args,
		"-e", fmt.Sprintf("NOUS_SERVICE_PORT=%d", port),
		"-e", fmt.Sprintf("NOUS_SERVICE_CONFIG_B64=%s", req.ServiceConfigB64),
		"-e", fmt.Sprintf("NOUS_SHARE_DIRS_B64=%s", shareDirsB64),
		"-e", fmt.Sprintf("NOUS_MAX_INLINE_BYTES=%d", req.MaxInlineBytes),
	)

	if len(req.Env) > 0 {
		keys := make([]string, 0, len(req.Env))
		for k := range req.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, "-e", fmt.Sprintf("%s=%s", k, req.Env[k]))
		}
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
	if err != nil {
		return err
	}
	s.bestEffortSetupSkillsSymlinks(ctx, containerName, req)
	return nil
}

func (s *Server) bestEffortSetupSkillsSymlinks(ctx context.Context, containerName string, req createServiceReq) {
	if req.Type != "claude" {
		return
	}
	skillsDir := strings.TrimSpace(req.SkillsDir)
	if skillsDir == "" {
		return
	}

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Printf("services.skills: warn read skills_dir=%s err=%v", skillsDir, err)
		return
	}

	skillNames := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := strings.TrimSpace(e.Name())
		if name == "" || strings.HasPrefix(name, ".") || name == "__MACOSX" {
			continue
		}
		if !isSafeSkillName(name) {
			continue
		}
		skillNames = append(skillNames, name)
	}
	if len(skillNames) == 0 {
		return
	}
	sort.Strings(skillNames)

	if _, err := runNerdctl(ctx, "exec", containerName, "mkdir", "-p", "/tmp/.claude/skills"); err != nil {
		log.Printf("services.skills: warn mkdir in container=%s err=%v", containerName, err)
		return
	}

	for _, name := range skillNames {
		target := filepath.Join(skillsDir, name)
		link := filepath.Join("/tmp/.claude/skills", name)
		if _, err := runNerdctl(ctx, "exec", containerName, "ln", "-sfn", target, link); err != nil {
			log.Printf("services.skills: warn link skill=%s err=%v", name, err)
		}
	}
}

func isSafeSkillName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

func detectUserForWorkDir(serviceConfigB64 string) (uid, gid int, ok bool) {
	if serviceConfigB64 == "" {
		return 0, 0, false
	}
	decoded, err := base64.StdEncoding.DecodeString(serviceConfigB64)
	if err != nil {
		return 0, 0, false
	}
	var cfg map[string]any
	if err := json.Unmarshal(decoded, &cfg); err != nil {
		return 0, 0, false
	}
	v, ok := cfg["cwd"].(string)
	if !ok {
		return 0, 0, false
	}
	cwd := strings.TrimSpace(v)
	if cwd == "" || !filepath.IsAbs(cwd) {
		return 0, 0, false
	}
	fi, err := os.Stat(cwd)
	if err != nil || !fi.IsDir() {
		return 0, 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	if st.Uid == 0 {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}

func detectPrimaryUser() (uid, gid int, ok bool) {
	// Lima templates typically create one non-root user that matches the host UID.
	// We keep this heuristic simple: choose the lowest UID >= 500 that has a real shell.
	b, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return 0, 0, false
	}
	bestUID := -1
	bestGID := 0
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 7 {
			continue
		}
		name := parts[0]
		if name == "root" {
			continue
		}
		u, err := strconv.Atoi(parts[2])
		if err != nil {
			continue
		}
		g, err := strconv.Atoi(parts[3])
		if err != nil {
			continue
		}
		shell := parts[6]
		if strings.Contains(shell, "nologin") {
			continue
		}
		if u < 500 || u == 65534 {
			continue
		}
		if bestUID == -1 || u < bestUID {
			bestUID = u
			bestGID = g
		}
	}
	if bestUID < 0 {
		return 0, 0, false
	}
	return bestUID, bestGID, true
}

func validateServiceEnv(in map[string]string) error {
	if len(in) == 0 {
		return nil
	}
	if len(in) > 128 {
		return fmt.Errorf("too many env vars")
	}
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key != k {
			return fmt.Errorf("env var name has leading/trailing spaces: %q", k)
		}
		if key == "" {
			return fmt.Errorf("env var name is empty")
		}
		if strings.HasPrefix(key, "NOUS_") {
			return fmt.Errorf("env var name is reserved: %q", key)
		}
		if !isValidEnvKey(key) {
			return fmt.Errorf("invalid env var name: %q", key)
		}
		if strings.IndexByte(v, 0) >= 0 {
			return fmt.Errorf("env var value contains NUL: %q", key)
		}
		if len(v) > 16*1024 {
			return fmt.Errorf("env var value too large: %q", key)
		}
	}
	return nil
}

func isValidEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			continue
		}
		if i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return key[0] < '0' || key[0] > '9'
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

func (s *Server) handleServiceStop(w http.ResponseWriter, r *http.Request) {
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
	if svc.State == "stopped" {
		writeJSON(w, 200, map[string]any{"service_id": id, "state": "stopped"})
		return
	}

	_, err := runNerdctl(r.Context(), "stop", svc.ContainerName)
	if err != nil {
		writeError(w, 500, "NERDCTL_ERROR", err.Error(), nil)
		return
	}

	s.mu.Lock()
	svc.State = "stopped"
	s.state.Services[id] = svc
	_ = s.saveStateLocked()
	s.mu.Unlock()

	writeJSON(w, 200, map[string]any{"service_id": id, "state": "stopped"})
}

func (s *Server) handleServiceStart(w http.ResponseWriter, r *http.Request) {
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
	if svc.State == "running" {
		writeJSON(w, 200, map[string]any{"service_id": id, "state": "running"})
		return
	}

	_, err := runNerdctl(r.Context(), "start", svc.ContainerName)
	if err != nil {
		writeError(w, 500, "NERDCTL_ERROR", err.Error(), nil)
		return
	}
	if err := waitHTTPHealth(r.Context(), svc.Port, 30*time.Second); err != nil {
		_, _ = runNerdctl(r.Context(), "stop", svc.ContainerName)
		writeError(w, 500, "SERVICE_UNHEALTHY", err.Error(), nil)
		return
	}

	s.mu.Lock()
	svc.State = "running"
	s.state.Services[id] = svc
	_ = s.saveStateLocked()
	s.mu.Unlock()

	writeJSON(w, 200, map[string]any{"service_id": id, "state": "running"})
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
