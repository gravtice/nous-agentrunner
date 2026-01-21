package guestrunnerd

import (
	"encoding/json"
	"net/http"
	"strings"
)

type pullReq struct {
	Ref string `json:"ref"`
}

func (s *Server) handleImagePull(w http.ResponseWriter, r *http.Request) {
	var req pullReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "BAD_REQUEST", "invalid json", nil)
		return
	}
	req.Ref = strings.TrimSpace(req.Ref)
	if req.Ref == "" {
		writeError(w, 400, "BAD_REQUEST", "ref is required", nil)
		return
	}
	if _, err := runNerdctl(r.Context(), "pull", req.Ref); err != nil {
		writeError(w, 500, "NERDCTL_ERROR", err.Error(), nil)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

type importReq struct {
	Path string `json:"path"`
}

func (s *Server) handleImageImport(w http.ResponseWriter, r *http.Request) {
	var req importReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "BAD_REQUEST", "invalid json", nil)
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		writeError(w, 400, "BAD_REQUEST", "path is required", nil)
		return
	}
	out, err := runNerdctl(r.Context(), "load", "-i", req.Path)
	if err != nil {
		writeError(w, 500, "NERDCTL_ERROR", err.Error(), nil)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "output": string(out)})
}

func (s *Server) handleImagesList(w http.ResponseWriter, r *http.Request) {
	// KISS: return a list of refs by parsing `nerdctl images --format`.
	out, err := runNerdctl(r.Context(), "images", "--format", "{{.Repository}}:{{.Tag}}")
	if err != nil {
		writeError(w, 500, "NERDCTL_ERROR", err.Error(), nil)
		return
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	images := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "<none>") {
			continue
		}
		images = append(images, line)
	}
	writeJSON(w, 200, map[string]any{"images": images})
}
