package runnerd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestM1_NormalizeShares_IncludesDefaultTmp(t *testing.T) {
	shareDir := t.TempDir()
	defaultTmp := filepath.Join(t.TempDir(), "SharedTmp")

	changed, out, err := normalizeShares([]Share{{HostPath: shareDir}}, defaultTmp)
	if err != nil {
		t.Fatalf("normalizeShares: %v", err)
	}
	if !changed {
		t.Fatalf("changed=false, want true")
	}
	if _, err := os.Stat(defaultTmp); err != nil {
		t.Fatalf("default tmp not created: %v", err)
	}

	canonShareDir, err := canonicalizeExistingPath(shareDir)
	if err != nil {
		t.Fatalf("canonicalizeExistingPath(shareDir): %v", err)
	}
	canonDefaultTmp, err := canonicalizeExistingPath(defaultTmp)
	if err != nil {
		t.Fatalf("canonicalizeExistingPath(defaultTmp): %v", err)
	}

	foundShare := false
	foundTmp := false
	for _, e := range out {
		if hasPathPrefix(e.CanonicalHostPath, canonShareDir) {
			foundShare = true
		}
		if hasPathPrefix(e.CanonicalHostPath, canonDefaultTmp) {
			foundTmp = true
		}
	}
	if !foundShare || !foundTmp {
		t.Fatalf("missing expected shares: foundShare=%v foundTmp=%v out=%#v", foundShare, foundTmp, out)
	}
}

func TestM1_HasPathPrefix(t *testing.T) {
	tests := []struct {
		path   string
		prefix string
		want   bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/c", "/a/b", true},
		{"/a/bc", "/a/b", false},
		{"/a/b", "/a/b/", true},
		{"/a/b", "/a/b//", true},
		{"/a", "/", false},
	}
	for _, tt := range tests {
		if got := hasPathPrefix(tt.path, tt.prefix); got != tt.want {
			t.Fatalf("hasPathPrefix(%q,%q)=%v, want %v", tt.path, tt.prefix, got, tt.want)
		}
	}
}

func TestM1_ValidateAllowedPath_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatalf("mkdir inside: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}

	link := filepath.Join(inside, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	escapedPath := filepath.Join(link, "data.txt")
	if err := os.WriteFile(filepath.Join(outside, "data.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	canonRoot, err := canonicalizeExistingPath(root)
	if err != nil {
		t.Fatalf("canonicalizeExistingPath(root): %v", err)
	}

	s := &Server{
		shares: []shareEntry{
			{Share: Share{ShareID: makeShareID(canonRoot), HostPath: root}, CanonicalHostPath: canonRoot},
		},
	}
	if _, _, ok := s.validateAllowedPath(escapedPath); ok {
		t.Fatalf("validateAllowedPath unexpectedly allowed symlink-escaped path %q", escapedPath)
	}
}

func TestM1_ValidateAllowedPath_RejectsExcludedDir(t *testing.T) {
	shareDir := t.TempDir()
	excludeDir := filepath.Join(shareDir, "excluded")
	if err := os.MkdirAll(excludeDir, 0o700); err != nil {
		t.Fatalf("mkdir excluded: %v", err)
	}
	allowedDir := filepath.Join(shareDir, "allowed")
	if err := os.MkdirAll(allowedDir, 0o700); err != nil {
		t.Fatalf("mkdir allowed: %v", err)
	}

	defaultTmp := filepath.Join(t.TempDir(), "SharedTmp")
	_, shares, err := normalizeShares([]Share{{HostPath: shareDir}}, defaultTmp)
	if err != nil {
		t.Fatalf("normalizeShares: %v", err)
	}
	_, excludes, err := normalizeShareExcludes([]string{excludeDir}, shares, defaultTmp)
	if err != nil {
		t.Fatalf("normalizeShareExcludes: %v", err)
	}

	s := &Server{
		shares:        shares,
		shareExcludes: excludes,
	}

	blockedFile := filepath.Join(excludeDir, "x.txt")
	if err := os.WriteFile(blockedFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocked file: %v", err)
	}
	if _, _, ok := s.validateAllowedPath(blockedFile); ok {
		t.Fatalf("validateAllowedPath unexpectedly allowed excluded path %q", blockedFile)
	}

	okFile := filepath.Join(allowedDir, "y.txt")
	if err := os.WriteFile(okFile, []byte("y"), 0o600); err != nil {
		t.Fatalf("write allowed file: %v", err)
	}
	if _, _, ok := s.validateAllowedPath(okFile); !ok {
		t.Fatalf("validateAllowedPath unexpectedly rejected allowed path %q", okFile)
	}
}

func TestM6_ShareExcludes_BuiltinCannotBeRemoved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeDir := filepath.Join(home, ".claude")
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir ~/.claude: %v", err)
	}
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatalf("mkdir ~/.codex: %v", err)
	}

	defaultTmp := filepath.Join(t.TempDir(), "SharedTmp")
	_, shares, err := normalizeShares([]Share{{HostPath: home}}, defaultTmp)
	if err != nil {
		t.Fatalf("normalizeShares: %v", err)
	}

	cfg := Config{Token: "tok"}
	cfg.Paths.AppSupportDir = t.TempDir()
	cfg.Paths.DefaultSharedTmpDir = defaultTmp

	s := &Server{cfg: cfg, shares: shares, services: make(map[string]Service)}
	h := s.Handler()

	reqBody, _ := json.Marshal(map[string]any{"excludes": []string{}})
	req := httptest.NewRequest(http.MethodPut, "/v1/shares/excludes", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Excludes []string `json:"excludes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]bool{claudeDir: true, codexDir: true}
	for _, p := range out.Excludes {
		delete(want, p)
	}
	if len(want) != 0 {
		t.Fatalf("missing builtin excludes: %#v (got=%#v)", want, out.Excludes)
	}

	// Builtins are forced; they should not be persisted in shares.json as user-configured excludes.
	b, err := os.ReadFile(s.sharesPath())
	if err != nil {
		t.Fatalf("read shares.json: %v", err)
	}
	var sf sharesFile
	if err := json.Unmarshal(b, &sf); err != nil {
		t.Fatalf("parse shares.json: %v", err)
	}
	if len(sf.Excludes) != 0 {
		t.Fatalf("unexpected persisted excludes: %#v", sf.Excludes)
	}
}

func TestM6_SharesDelete_NotBlockedByBuiltinExcludes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatalf("mkdir ~/.claude: %v", err)
	}

	defaultTmp := filepath.Join(t.TempDir(), "SharedTmp")
	_, shares, err := normalizeShares([]Share{{HostPath: home}}, defaultTmp)
	if err != nil {
		t.Fatalf("normalizeShares: %v", err)
	}

	cfg := Config{Token: "tok"}
	cfg.Paths.AppSupportDir = t.TempDir()
	cfg.Paths.DefaultSharedTmpDir = defaultTmp

	s := &Server{cfg: cfg, shares: shares, services: make(map[string]Service)}
	builtin := builtinShareExcludes(shares, defaultTmp)
	s.shareExcludes = builtin

	homeShareID := ""
	for _, e := range shares {
		if filepath.Clean(e.HostPath) == filepath.Clean(home) {
			homeShareID = e.ShareID
			break
		}
	}
	if homeShareID == "" {
		t.Fatalf("home share not found in shares: %#v", shares)
	}

	h := s.Handler()
	req := httptest.NewRequest(http.MethodDelete, "/v1/shares/"+homeShareID, nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
