package runnerd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type createServiceRequest struct {
	Type               string            `json:"type"`
	ImageRef           string            `json:"image_ref"`
	Resources          serviceResources  `json:"resources"`
	RWMounts           []string          `json:"rw_mounts"`
	Env                map[string]string `json:"env"`
	ServiceConfig      map[string]any    `json:"service_config"`
	IdleTimeoutSeconds int               `json:"idle_timeout_seconds"`
}

type serviceResources struct {
	CPUCores int `json:"cpu_cores"`
	MemoryMB int `json:"memory_mb"`
	Pids     int `json:"pids"`
}

func applyClaudeServiceConfigDefaults(cfg map[string]any) {
	if cfg == nil {
		return
	}
	if _, ok := cfg["setting_sources"]; !ok {
		cfg["setting_sources"] = []string{"user", "project"}
	}
}

func effectiveServiceState(vmState, serviceState string) string {
	vmState = strings.TrimSpace(strings.ToLower(vmState))
	switch vmState {
	case "running":
		if strings.TrimSpace(serviceState) == "" {
			return "unknown"
		}
		return serviceState
	case "stopped", "not_created":
		return "stopped"
	case "unknown":
		return "unknown"
	default:
		// Transitional states like "starting" are not reliably actionable for services yet.
		return "unknown"
	}
}

func (s *Server) handleServicesCreate(w http.ResponseWriter, r *http.Request) {
	var req createServiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid json", nil)
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	req.ImageRef = normalizeImageRef(req.ImageRef)
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "type is required", nil)
		return
	}
	if req.Type != "claude" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "unsupported service type", map[string]any{"type": req.Type})
		return
	}
	if req.ServiceConfig == nil {
		req.ServiceConfig = map[string]any{}
	}
	applyClaudeServiceConfigDefaults(req.ServiceConfig)
	if req.ImageRef == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "image_ref is required", nil)
		return
	}
	if !(strings.HasPrefix(req.ImageRef, s.cfg.RegistryBase) || strings.HasPrefix(req.ImageRef, "local/")) {
		writeError(w, http.StatusBadRequest, "REGISTRY_NOT_ALLOWED", "image_ref must be official registry or local tag", map[string]any{"registry_base": s.cfg.RegistryBase})
		return
	}

	rwMounts, err := s.validateAndPrepareRWMounts(req.RWMounts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "PATH_NOT_ALLOWED", err.Error(), nil)
		return
	}

	env, err := validateServiceEnv(req.Env)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
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

	if req.IdleTimeoutSeconds < 0 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "idle_timeout_seconds must be >= 0", nil)
		return
	}
	if req.IdleTimeoutSeconds > 0 {
		d := time.Duration(req.IdleTimeoutSeconds) * time.Second
		if d/time.Second != time.Duration(req.IdleTimeoutSeconds) {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "idle_timeout_seconds is too large", nil)
			return
		}
	}

	serviceID, err := newID("svc_", 12)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to allocate service_id", nil)
		return
	}
	sessionID, err := newID("sess_", 12)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to allocate session_id", nil)
		return
	}

	log.Printf("services.create: start service_id=%s type=%s image_ref=%s rw_mounts=%d env=%d", serviceID, req.Type, req.ImageRef, len(rwMounts), len(env))

	s.mu.Lock()
	shares := make([]string, 0, len(s.shares))
	for _, e := range s.shares {
		shares = append(shares, filepath.Clean(e.HostPath))
	}
	excludes := make([]string, 0, len(s.shareExcludes))
	for _, e := range s.shareExcludes {
		excludes = append(excludes, filepath.Clean(e.HostPath))
	}
	s.mu.Unlock()

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}

	if err := s.ensureImageAvailable(r.Context(), gc, req.ImageRef); err != nil {
		writeError(w, http.StatusInternalServerError, "IMAGE_UNAVAILABLE", err.Error(), nil)
		return
	}

	skillsDir := filepath.Join(s.cfg.Paths.AppSupportDir, "skills")
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		log.Printf("services.create: warn mkdir skills_dir=%s err=%v", skillsDir, err)
		skillsDir = ""
	} else if _, _, ok := s.validateAllowedPath(skillsDir); !ok {
		log.Printf("services.create: warn skills_dir not under shared mounts: %s", skillsDir)
		skillsDir = ""
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
		"share_excludes":     excludes,
		"rw_mounts":          rwMounts,
		"env":                env,
		"service_config_b64": payload,
		"skills_dir":         skillsDir,
		"max_inline_bytes":   s.cfg.MaxInlineBytes,
	}

	var guestResp struct {
		ServiceID string `json:"service_id"`
		State     string `json:"state"`
	}
	if err := gc.postJSON(r.Context(), "/internal/services", guestReq, &guestResp); err != nil {
		log.Printf("services.create: guest error service_id=%s err=%v", serviceID, err)
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
		return
	}

	log.Printf("services.create: ok service_id=%s state=%s", serviceID, guestResp.State)

	now := nowISO8601()
	s.mu.Lock()
	s.services[serviceID] = Service{
		ServiceID:          serviceID,
		SessionID:          sessionID,
		Type:               req.Type,
		ImageRef:           req.ImageRef,
		State:              guestResp.State,
		CreatedAt:          now,
		IdleTimeoutSeconds: req.IdleTimeoutSeconds,
		LastActivityAt:     now,
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
	vmState := s.getVMState(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Service, 0, len(s.services))
	for _, svc := range s.services {
		svc.State = effectiveServiceState(vmState, svc.State)
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
	vmState := s.getVMState(r)
	svc.State = effectiveServiceState(vmState, svc.State)
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

	vmState := s.getVMState(r)
	if vmState != "running" && vmState != "unknown" {
		s.mu.Lock()
		delete(s.services, serviceID)
		_ = s.saveServicesLocked()
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"deleted": true})
		return
	}

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}
	if err := gc.delete(r.Context(), "/internal/services/"+serviceID); err != nil {
		var ge *guestHTTPError
		if !errors.As(err, &ge) || ge.Status != http.StatusNotFound {
			writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
			return
		}
	}

	s.mu.Lock()
	delete(s.services, serviceID)
	_ = s.saveServicesLocked()
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"deleted": true})
}

func (s *Server) handleServicesStop(w http.ResponseWriter, r *http.Request) {
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

	state, err := s.stopServiceWithGuest(r.Context(), gc, serviceID, "manual")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"service_id": serviceID, "state": state})
}

func (s *Server) handleServicesStart(w http.ResponseWriter, r *http.Request) {
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

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}

	var guestResp struct {
		ServiceID string `json:"service_id"`
		State     string `json:"state"`
	}
	if err := gc.postJSON(r.Context(), "/internal/services/"+serviceID+"/start", map[string]any{}, &guestResp); err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
		return
	}

	svc.State = guestResp.State
	svc.StopReason = ""
	svc.LastActivityAt = nowISO8601()
	s.mu.Lock()
	s.services[serviceID] = svc
	_ = s.saveServicesLocked()
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"service_id": serviceID, "state": guestResp.State})
}

func (s *Server) handleServicesResume(w http.ResponseWriter, r *http.Request) {
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
	if strings.TrimSpace(svc.SessionID) == "" {
		writeError(w, http.StatusConflict, "RESUME_UNAVAILABLE", "service is missing session_id; recreate service", nil)
		return
	}

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}

	var guestResp struct {
		ServiceID string `json:"service_id"`
		State     string `json:"state"`
	}
	if err := gc.postJSON(r.Context(), "/internal/services/"+serviceID+"/start", map[string]any{}, &guestResp); err != nil {
		var ge *guestHTTPError
		if errors.As(err, &ge) && ge.Status == http.StatusNotFound {
			writeError(w, http.StatusConflict, "RESUME_UNAVAILABLE", "service is missing in guest; recreate service", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
		return
	}

	svc.State = guestResp.State
	svc.StopReason = ""
	svc.LastActivityAt = nowISO8601()
	s.mu.Lock()
	s.services[serviceID] = svc
	_ = s.saveServicesLocked()
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"service_id": serviceID,
		"state":      guestResp.State,
		"asp_url":    s.serviceASPURL(serviceID),
	})
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

func (s *Server) stopServiceWithGuest(ctx context.Context, gc *guestClient, serviceID, stopReason string) (string, error) {
	var guestResp struct {
		ServiceID string `json:"service_id"`
		State     string `json:"state"`
	}
	if err := gc.postJSON(ctx, "/internal/services/"+serviceID+"/stop", map[string]any{}, &guestResp); err != nil {
		var ge *guestHTTPError
		if !errors.As(err, &ge) || ge.Status != http.StatusNotFound {
			return "", err
		}
		// If the guest forgot the service, stopping is effectively a no-op.
		guestResp = struct {
			ServiceID string `json:"service_id"`
			State     string `json:"state"`
		}{ServiceID: serviceID, State: "stopped"}
	}

	s.mu.Lock()
	svc, ok := s.services[serviceID]
	if ok {
		svc.State = guestResp.State
		if stopReason == "manual" || (stopReason != "" && strings.TrimSpace(svc.StopReason) == "") {
			svc.StopReason = stopReason
		}
		svc.LastActivityAt = nowISO8601()
		s.services[serviceID] = svc
		_ = s.saveServicesLocked()
	}
	s.mu.Unlock()

	return guestResp.State, nil
}

func (s *Server) ensureImageAvailable(ctx context.Context, gc *guestClient, imageRef string) error {
	imageRef = normalizeImageRef(imageRef)
	if imageRef == "" || strings.HasPrefix(imageRef, "local/") {
		return nil
	}

	tarPath, err := s.offlineImageTarPath(imageRef)
	if err != nil {
		return err
	}
	if tarPath != "" {
		return s.ensureOfflineImageAvailable(ctx, gc, imageRef, tarPath)
	}

	var list struct {
		Images []string `json:"images"`
	}
	if err := gc.getJSON(ctx, "/internal/images", &list); err != nil {
		return err
	}
	for _, existing := range list.Images {
		if normalizeImageRef(existing) == imageRef {
			return nil
		}
	}

	var out any
	if err := gc.postJSON(ctx, "/internal/images/pull", map[string]any{"ref": imageRef}, &out); err != nil {
		return err
	}
	return nil
}

func (s *Server) ensureOfflineImageAvailable(ctx context.Context, gc *guestClient, imageRef, tarPath string) error {
	tarPath = strings.TrimSpace(tarPath)
	if tarPath == "" {
		return nil
	}
	if _, _, ok := s.validateAllowedPath(tarPath); !ok {
		return fmt.Errorf("offline image tar is not under any shared directory: %q", tarPath)
	}

	var list struct {
		Images []string `json:"images"`
	}
	if err := gc.getJSON(ctx, "/internal/images", &list); err != nil {
		return err
	}
	for _, existing := range list.Images {
		if normalizeImageRef(existing) == imageRef {
			return nil
		}
	}

	log.Printf("images.import_offline: start ref=%s path=%s", imageRef, tarPath)
	var out any
	if err := gc.postJSON(ctx, "/internal/images/import", map[string]any{"path": tarPath}, &out); err != nil {
		return err
	}
	log.Printf("images.import_offline: ok ref=%s", imageRef)
	return nil
}

func (s *Server) validateAndPrepareRWMounts(rw []string) ([]string, error) {
	s.mu.Lock()
	shares := append([]shareEntry(nil), s.shares...)
	excludes := append([]excludeEntry(nil), s.shareExcludes...)
	s.mu.Unlock()

	out := make([]string, 0, len(rw))
	for _, p := range rw {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("rw_mount must be absolute: %q", p)
		}

		canonIntended, err := canonicalizePathForCreate(p)
		if err != nil {
			return nil, fmt.Errorf("rw_mount cannot be canonicalized: %q", p)
		}

		for _, e := range excludes {
			if hasPathPrefix(canonIntended, e.CanonicalHostPath) {
				return nil, fmt.Errorf("rw_mount not allowed: %q", p)
			}
		}

		allowedMountNS := false
		for _, e := range shares {
			if hasPathPrefix(p, filepath.Clean(e.HostPath)) {
				allowedMountNS = true
				break
			}
		}
		if !allowedMountNS {
			return nil, fmt.Errorf("rw_mount not allowed: %q", p)
		}

		allowedCanon := false
		for _, e := range shares {
			if hasPathPrefix(canonIntended, e.CanonicalHostPath) {
				allowedCanon = true
				break
			}
		}
		if !allowedCanon {
			return nil, fmt.Errorf("rw_mount not allowed: %q", p)
		}

		if err := os.MkdirAll(p, 0o700); err != nil {
			return nil, fmt.Errorf("rw_mount not writable: %q", p)
		}

		canon, err := canonicalizeExistingPath(p)
		if err != nil {
			return nil, fmt.Errorf("rw_mount cannot be canonicalized: %q", p)
		}
		for _, e := range excludes {
			if hasPathPrefix(canon, e.CanonicalHostPath) {
				return nil, fmt.Errorf("rw_mount not allowed: %q", p)
			}
		}
		// Safety: re-check after creation; avoid accidentally writing outside shares.
		allowedCanon = false
		for _, e := range shares {
			if hasPathPrefix(canon, e.CanonicalHostPath) {
				allowedCanon = true
				break
			}
		}
		if !allowedCanon {
			return nil, fmt.Errorf("rw_mount not allowed: %q", p)
		}
		out = append(out, p)
	}
	return out, nil
}

func canonicalizePathForCreate(path string) (string, error) {
	path = filepath.Clean(path)
	if path == "" {
		return "", fmt.Errorf("empty path")
	}

	// If it already exists, we can canonicalize directly.
	if _, err := os.Stat(path); err == nil {
		return canonicalizeExistingPath(path)
	}

	// Find the nearest existing parent, canonicalize it, then append the missing suffix.
	existing := path
	var suffix []string
	for {
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		if _, err := os.Stat(existing); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		suffix = append([]string{filepath.Base(existing)}, suffix...)
		existing = parent
	}

	fi, err := os.Stat(existing)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("parent is not a directory: %q", existing)
	}

	canonParent, err := canonicalizeExistingPath(existing)
	if err != nil {
		return "", err
	}
	if len(suffix) == 0 {
		return canonParent, nil
	}

	parts := append([]string{canonParent}, suffix...)
	return filepath.Clean(filepath.Join(parts...)), nil
}

func validateServiceEnv(in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > 128 {
		return nil, fmt.Errorf("too many env vars")
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key == "" {
			return nil, fmt.Errorf("env var name is empty")
		}
		if isReservedServiceEnvKey(key) {
			return nil, fmt.Errorf("env var name is reserved: %q", key)
		}
		if !isValidEnvKey(key) {
			return nil, fmt.Errorf("invalid env var name: %q", key)
		}
		if _, ok := out[key]; ok {
			return nil, fmt.Errorf("duplicate env var name: %q", key)
		}
		if strings.IndexByte(v, 0) >= 0 {
			return nil, fmt.Errorf("env var value contains NUL: %q", key)
		}
		if len(v) > 16*1024 {
			return nil, fmt.Errorf("env var value too large: %q", key)
		}
		out[key] = v
	}
	return out, nil
}

func isReservedServiceEnvKey(key string) bool {
	return strings.HasPrefix(key, "NOUS_RUNNER_")
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
