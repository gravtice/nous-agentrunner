package runnerd

import (
	"context"
	"strings"
	"testing"
)

func TestM1_BuildLimaYAML(t *testing.T) {
	cfg := Config{
		VMCPU:            2,
		VMMemoryMiB:      1024,
		LimaBaseTemplate: "debian-12",
	}
	shares := []shareEntry{
		{Share: Share{ShareID: "shr_1", HostPath: "/Users/test"}, CanonicalHostPath: `/Users/test/dir with "quote"`},
		{Share: Share{ShareID: "shr_2", HostPath: "/Volumes/data"}, CanonicalHostPath: "/Volumes/data"},
	}

	got := buildLimaYAML(cfg, shares)
	for _, want := range []string{
		"vmType: \"vz\"",
		"mountType: \"virtiofs\"",
		"cpus: 2",
		"memory: \"1024MiB\"",
		"template:debian-12",
		"containerd:",
		"system: true",
		"user: false",
		"mounts:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("YAML missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "location: \"/Users/test/dir with \\\"quote\\\"\"") {
		t.Fatalf("YAML did not quote path correctly:\n%s", got)
	}
	if !strings.Contains(got, "writable: true") {
		t.Fatalf("YAML missing writable: true:\n%s", got)
	}
}

func TestM1_LimaInstanceState_NotCreatedWithoutDir(t *testing.T) {
	s := &Server{cfg: Config{LimaHome: t.TempDir(), LimaInstanceName: "nous-test"}}
	state, err := s.limaInstanceState(context.Background())
	if err != nil {
		t.Fatalf("limaInstanceState: %v", err)
	}
	if state != "not_created" {
		t.Fatalf("state=%q, want %q", state, "not_created")
	}
}

func TestM1_LimaInstanceStateFromListOutput_Object(t *testing.T) {
	out := []byte(`{"name":"nous-demo","status":"Running","sshConfigFile":"/tmp/ssh.config"}` + "\n")
	state, err := limaInstanceStateFromListOutput(out, "nous-demo")
	if err != nil {
		t.Fatalf("limaInstanceStateFromListOutput: %v", err)
	}
	if state != "running" {
		t.Fatalf("state=%q, want %q", state, "running")
	}
}

func TestM1_LimaInstanceStateFromListOutput_Array(t *testing.T) {
	out := []byte(`[{"name":"nous-demo","status":"Stopped"}]`)
	state, err := limaInstanceStateFromListOutput(out, "nous-demo")
	if err != nil {
		t.Fatalf("limaInstanceStateFromListOutput: %v", err)
	}
	if state != "stopped" {
		t.Fatalf("state=%q, want %q", state, "stopped")
	}
}

func TestM1_LimaInstanceStateFromListOutput_NDJSONSelectByName(t *testing.T) {
	out := []byte(`{"name":"a","status":"Stopped"}` + "\n" + `{"name":"b","status":"Running"}` + "\n")
	state, err := limaInstanceStateFromListOutput(out, "b")
	if err != nil {
		t.Fatalf("limaInstanceStateFromListOutput: %v", err)
	}
	if state != "running" {
		t.Fatalf("state=%q, want %q", state, "running")
	}
}
