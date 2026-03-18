package runnerd

import "testing"

func TestNormalizeImageRef(t *testing.T) {
	if got := normalizeImageRef("  "); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeImageRef("local/x:1"); got != "local/x:1" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeImageRef("gravtice/claude-agent-service:0.1.0"); got != "docker.io/gravtice/claude-agent-service:0.1.0" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeImageRef("docker.io/gravtice/claude-agent-service:0.1.0"); got != "docker.io/gravtice/claude-agent-service:0.1.0" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeImageRef("ghcr.io/x/y:1"); got != "ghcr.io/x/y:1" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeImageRef("localhost:5000/x/y:1"); got != "localhost:5000/x/y:1" {
		t.Fatalf("got %q", got)
	}
}
