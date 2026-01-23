package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFirst_PriorityAndParsing(t *testing.T) {
	dir := t.TempDir()

	prod := filepath.Join(dir, ".env.production")
	local := filepath.Join(dir, ".env.local")

	if err := os.WriteFile(prod, []byte("A=1\nB=\"two\"\n# comment\nC='3'\n"), 0o600); err != nil {
		t.Fatalf("write prod: %v", err)
	}
	if err := os.WriteFile(local, []byte("A=9\nD=four\n"), 0o600); err != nil {
		t.Fatalf("write local: %v", err)
	}

	loaded, kv, err := LoadFirst([]string{local, prod})
	if err != nil {
		t.Fatalf("LoadFirst: %v", err)
	}
	if loaded != local {
		t.Fatalf("loaded=%q, want %q", loaded, local)
	}
	if kv["A"] != "9" || kv["D"] != "four" {
		t.Fatalf("unexpected kv: %#v", kv)
	}

	loaded, kv, err = LoadFirst([]string{filepath.Join(dir, ".env.missing"), prod})
	if err != nil {
		t.Fatalf("LoadFirst: %v", err)
	}
	if loaded != prod {
		t.Fatalf("loaded=%q, want %q", loaded, prod)
	}
	if kv["A"] != "1" || kv["B"] != "two" || kv["C"] != "3" {
		t.Fatalf("unexpected kv: %#v", kv)
	}
}
