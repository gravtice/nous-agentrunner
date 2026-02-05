package runnerd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestM1_NormalizeShares_IncludesDefaultTmp(t *testing.T) {
	shareDir := t.TempDir()
	defaultTmp := filepath.Join(t.TempDir(), "SharedTmp")

	changed, out, _, err := normalizeShareConfig([]Share{{HostPath: shareDir}}, nil, defaultTmp)
	if err != nil {
		t.Fatalf("normalizeShareConfig: %v", err)
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
	_, shares, excludes, err := normalizeShareConfig([]Share{{HostPath: shareDir}}, []string{excludeDir}, defaultTmp)
	if err != nil {
		t.Fatalf("normalizeShareConfig: %v", err)
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
