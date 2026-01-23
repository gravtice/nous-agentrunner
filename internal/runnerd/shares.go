package runnerd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

func normalizeShares(in []Share, defaultSharedTmp string) (changed bool, out []shareEntry, _ error) {
	shareSet := make(map[string]Share) // canonicalPath -> Share

	addIfDir := func(hostPath string) error {
		if hostPath == "" {
			return nil
		}
		hostPath = filepath.Clean(hostPath)
		if !filepath.IsAbs(hostPath) {
			return fmt.Errorf("share host_path must be absolute: %q", hostPath)
		}
		fi, err := os.Stat(hostPath)
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			return fmt.Errorf("share host_path must be a directory: %q", hostPath)
		}
		canon, err := canonicalizeExistingPath(hostPath)
		if err != nil {
			return err
		}
		shareID := makeShareID(canon)
		shareSet[canon] = Share{ShareID: shareID, HostPath: hostPath}
		return nil
	}

	// 1) start from existing shares.
	for _, s := range in {
		if s.HostPath == "" {
			changed = true
			continue
		}
		if err := addIfDir(s.HostPath); err != nil {
			return false, nil, err
		}
	}

	// 2) default shares.
	if len(shareSet) == 0 {
		switch runtime.GOOS {
		case "darwin":
			_ = addIfDir("/Users")
			_ = addIfDir("/Volumes")
		default:
			home, _ := os.UserHomeDir()
			_ = addIfDir(home)
		}
		changed = true
	}

	// 3) always include the default temp directory (ensure exists).
	if err := os.MkdirAll(defaultSharedTmp, 0o700); err != nil {
		return false, nil, err
	}
	_ = addIfDir(defaultSharedTmp)

	// 4) output.
	out = make([]shareEntry, 0, len(shareSet))
	for canon, s := range shareSet {
		out = append(out, shareEntry{Share: s, CanonicalHostPath: canon})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CanonicalHostPath < out[j].CanonicalHostPath })

	// Determine whether input and output differ materially.
	if len(out) != len(in) {
		changed = true
	} else {
		byID := make(map[string]Share, len(in))
		for _, s := range in {
			byID[s.ShareID] = s
		}
		for _, e := range out {
			old, ok := byID[e.ShareID]
			if !ok || old.HostPath != e.HostPath {
				changed = true
				break
			}
		}
	}

	return changed, out, nil
}

func makeShareID(canon string) string {
	sum := sha256.Sum256([]byte(canon))
	return "shr_" + hex.EncodeToString(sum[:6])
}

func canonicalizeExistingPath(p string) (string, error) {
	p = filepath.Clean(p)
	canon, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	return filepath.Clean(canon), nil
}

func hasPathPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	prefix = strings.TrimRight(prefix, string(filepath.Separator))
	if prefix == "" {
		return false
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	if len(path) == len(prefix) {
		return true
	}
	return path[len(prefix)] == byte(filepath.Separator)
}

func (s *Server) findShareByID(id string) (int, shareEntry, bool) {
	for i, e := range s.shares {
		if e.ShareID == id {
			return i, e, true
		}
	}
	return -1, shareEntry{}, false
}

func (s *Server) validateAllowedPath(path string) (string, shareEntry, bool) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", shareEntry{}, false
	}
	path = filepath.Clean(path)

	// Ensure the caller is using a path that exists inside the Guest/Container mount namespace
	// (i.e., under some share's mountPoint/HostPath). Canonical checks are for security; this is for usability.
	inMountNS := false
	for _, e := range s.shares {
		if hasPathPrefix(path, filepath.Clean(e.HostPath)) {
			inMountNS = true
			break
		}
	}
	if !inMountNS {
		return "", shareEntry{}, false
	}

	canon, err := canonicalizeExistingPath(path)
	if err != nil {
		return "", shareEntry{}, false
	}
	for _, e := range s.shares {
		if hasPathPrefix(canon, e.CanonicalHostPath) {
			return canon, e, true
		}
	}
	return "", shareEntry{}, false
}

func (s *Server) handleSharesList(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Share, 0, len(s.shares))
	for _, e := range s.shares {
		out = append(out, e.Share)
	}
	writeJSON(w, 200, map[string]any{"shares": out})
}

type sharesAddRequest struct {
	HostPath string `json:"host_path"`
}

func (s *Server) handleSharesAdd(w http.ResponseWriter, r *http.Request) {
	var req sharesAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid json", nil)
		return
	}
	req.HostPath = strings.TrimSpace(req.HostPath)
	req.HostPath = filepath.Clean(req.HostPath)
	if req.HostPath == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "host_path is required", nil)
		return
	}
	if !filepath.IsAbs(req.HostPath) {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "host_path must be absolute", nil)
		return
	}
	fi, err := os.Stat(req.HostPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "host_path not accessible", map[string]any{"error": err.Error()})
		return
	}
	if !fi.IsDir() {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "host_path must be a directory", nil)
		return
	}
	canon, err := canonicalizeExistingPath(req.HostPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "host_path cannot be canonicalized", map[string]any{"error": err.Error()})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.shares {
		if e.CanonicalHostPath == canon {
			writeJSON(w, 200, map[string]any{"share": e.Share, "vm_restart_required": false})
			return
		}
	}
	share := Share{ShareID: makeShareID(canon), HostPath: req.HostPath}
	s.shares = append(s.shares, shareEntry{Share: share, CanonicalHostPath: canon})
	sort.Slice(s.shares, func(i, j int) bool { return s.shares[i].CanonicalHostPath < s.shares[j].CanonicalHostPath })

	if err := s.saveSharesLocked(); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to persist shares", map[string]any{"error": err.Error()})
		return
	}
	s.vmRestartRequired = true
	writeJSON(w, 200, map[string]any{"share": share, "vm_restart_required": true})
}

func (s *Server) handleSharesDelete(w http.ResponseWriter, r *http.Request) {
	shareID := strings.TrimSpace(r.PathValue("share_id"))
	if shareID == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "share_id is required", nil)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	i, e, ok := s.findShareByID(shareID)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "share not found", nil)
		return
	}
	if canonTmp, err := canonicalizeExistingPath(s.cfg.Paths.DefaultSharedTmpDir); err == nil && e.CanonicalHostPath == canonTmp {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "default temp dir share cannot be removed", nil)
		return
	}

	s.shares = append(s.shares[:i], s.shares[i+1:]...)
	if err := s.saveSharesLocked(); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to persist shares", map[string]any{"error": err.Error()})
		return
	}
	s.vmRestartRequired = true
	writeJSON(w, 200, map[string]any{"deleted": true, "vm_restart_required": true})
}

var errPathNotAllowed = errors.New("path not allowed")
