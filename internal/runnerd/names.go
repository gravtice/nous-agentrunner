package runnerd

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	runnerConfigFileName   = "AgentRunnerConfig.json"
	runnerdBinaryName      = "agent-runnerd"
	guestRunnerBinaryName  = "guest-runnerd"
	guestRunnerServiceName = "guest-runnerd"
	limaInstancePrefix     = "agent-"
)

func runnerEnvValue(env map[string]string, key string) string {
	return strings.TrimSpace(env[key])
}

func runnerEnvInt(env map[string]string, key string) int {
	return mustParseInt(runnerEnvValue(env, key), 0)
}

func runnerRuntimeEnv(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func runnerConfigCandidates(exe string) []string {
	return bundledFileCandidates(exe, runnerConfigFileName)
}

func bundledExecutableCandidates(exe string, names ...string) []string {
	return bundledFileCandidates(exe, names...)
}

func bundledFileCandidates(exe string, names ...string) []string {
	baseDir := filepath.Dir(exe)
	resourceDir := filepath.Clean(filepath.Join(baseDir, "..", "Resources"))
	out := make([]string, 0, len(names)*2)
	for _, name := range names {
		out = append(out,
			filepath.Join(baseDir, name),
			filepath.Join(resourceDir, name),
		)
	}
	return out
}

func firstExistingFile(paths ...string) string {
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}
