package platformpaths

import "testing"

func TestResolveUsesDotAgentRunnerLayout(t *testing.T) {
	t.Setenv("HOME", "/tmp/home")

	paths, err := Resolve("inst")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got, want := paths.AppSupportDir, "/tmp/home/.agentrunner/inst"; got != want {
		t.Fatalf("AppSupportDir=%q, want %q", got, want)
	}
	if got, want := paths.CachesDir, "/tmp/home/.agentrunner/caches/inst"; got != want {
		t.Fatalf("CachesDir=%q, want %q", got, want)
	}
	if got, want := paths.LogsDir, "/tmp/home/.agentrunner/logs/inst"; got != want {
		t.Fatalf("LogsDir=%q, want %q", got, want)
	}
	if got, want := paths.DefaultSharedTmpDir, "/tmp/home/.agentrunner/caches/inst/SharedTmp"; got != want {
		t.Fatalf("DefaultSharedTmpDir=%q, want %q", got, want)
	}
}
