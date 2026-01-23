package runnerd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestM2_ValidateAndPrepareRWMounts_CreatesOnlyInsideShare(t *testing.T) {
	shareRoot := t.TempDir()
	canonShare, err := canonicalizeExistingPath(shareRoot)
	if err != nil {
		t.Fatalf("canonicalizeExistingPath(shareRoot): %v", err)
	}
	s := &Server{
		shares: []shareEntry{
			{Share: Share{ShareID: makeShareID(canonShare), HostPath: shareRoot}, CanonicalHostPath: canonShare},
		},
	}

	inside := filepath.Join(shareRoot, "rw1")
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatalf("expected inside not exist")
	}
	out, err := s.validateAndPrepareRWMounts([]string{inside})
	if err != nil {
		t.Fatalf("validateAndPrepareRWMounts(inside): %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("out=%#v", out)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Fatalf("inside dir not created: %v", err)
	}

	outsideParent := t.TempDir()
	outside := filepath.Join(outsideParent, "rw2")
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("expected outside not exist")
	}
	if _, err := s.validateAndPrepareRWMounts([]string{outside}); err == nil {
		t.Fatalf("expected error for outside rw mount")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside dir should not be created, stat err=%v", err)
	}
}
