package runnerd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type imagesPullRequest struct {
	Ref string `json:"ref"`
}

func (s *Server) handleImagesPull(w http.ResponseWriter, r *http.Request) {
	var req imagesPullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid json", nil)
		return
	}
	req.Ref = normalizeImageRef(req.Ref)
	if req.Ref == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "ref is required", nil)
		return
	}
	if !strings.HasPrefix(req.Ref, s.cfg.RegistryBase) {
		writeError(w, http.StatusBadRequest, "REGISTRY_NOT_ALLOWED", "ref must use official registry base", map[string]any{"registry_base": s.cfg.RegistryBase})
		return
	}

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}

	if err := s.ensureImageAvailable(r.Context(), gc, req.Ref); err != nil {
		writeError(w, http.StatusInternalServerError, "IMAGE_UNAVAILABLE", err.Error(), nil)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

type imagesImportRequest struct {
	Path string `json:"path"`
}

func (s *Server) handleImagesImport(w http.ResponseWriter, r *http.Request) {
	var req imagesImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid json", nil)
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "path is required", nil)
		return
	}
	if _, _, ok := s.validateAllowedPath(req.Path); !ok {
		writeError(w, http.StatusBadRequest, "PATH_NOT_ALLOWED", "path is not under any shared directory", nil)
		return
	}

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}

	var out any
	if err := gc.postJSON(r.Context(), "/internal/images/import", req, &out); err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
		return
	}
	writeJSON(w, 200, out)
}

type imagesPruneRequest struct {
	All *bool `json:"all,omitempty"`
}

func (s *Server) handleImagesPrune(w http.ResponseWriter, r *http.Request) {
	var req imagesPruneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid json", nil)
		return
	}
	all := true
	if req.All != nil {
		all = *req.All
	}

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}

	var out any
	if err := gc.postJSON(r.Context(), "/internal/images/prune", map[string]any{"all": all}, &out); err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
		return
	}
	writeJSON(w, 200, out)
}

type imagesDeleteRequest struct {
	Ref string `json:"ref"`
}

func (s *Server) isAgentServiceImageRef(ref string) bool {
	return strings.HasPrefix(ref, s.cfg.RegistryBase) || strings.HasPrefix(ref, "local/")
}

func (s *Server) handleImagesDelete(w http.ResponseWriter, r *http.Request) {
	var req imagesDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid json", nil)
		return
	}
	req.Ref = normalizeImageRef(req.Ref)
	if req.Ref == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "ref is required", nil)
		return
	}
	if !s.isAgentServiceImageRef(req.Ref) {
		writeError(w, http.StatusBadRequest, "REGISTRY_NOT_ALLOWED", "ref must be official registry or local tag", map[string]any{"registry_base": s.cfg.RegistryBase})
		return
	}

	associatedAgents := 0
	s.mu.Lock()
	for _, svc := range s.services {
		if normalizeImageRef(svc.ImageRef) == req.Ref {
			associatedAgents++
		}
	}
	s.mu.Unlock()
	if associatedAgents > 0 {
		writeError(
			w,
			http.StatusConflict,
			"IMAGE_IN_USE",
			"image has associated agents",
			map[string]any{"associated_agents": associatedAgents},
		)
		return
	}

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}
	var out any
	if err := gc.postJSON(r.Context(), "/internal/images/delete", map[string]any{"ref": req.Ref}, &out); err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleImagesList(w http.ResponseWriter, r *http.Request) {
	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}
	var out any
	if err := gc.getJSON(r.Context(), "/internal/images", &out); err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_ERROR", err.Error(), nil)
		return
	}
	writeJSON(w, 200, out)
}
