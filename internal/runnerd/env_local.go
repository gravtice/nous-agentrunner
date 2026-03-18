package runnerd

import (
	"path/filepath"

	"github.com/gravtice/agent-runner/internal/envfile"
	"github.com/gravtice/agent-runner/internal/platformpaths"
)

func envCandidatePaths(appSupportDir string) []string {
	return []string{
		filepath.Join(appSupportDir, ".env.local"),
		filepath.Join(appSupportDir, ".env.production"),
		filepath.Join(appSupportDir, ".env.development"),
		filepath.Join(appSupportDir, ".env.test"),
	}
}

func persistEnvLocalUpdates(paths platformpaths.Paths, set map[string]string) error {
	_, env, err := envfile.LoadFirst(envCandidatePaths(paths.AppSupportDir))
	if err != nil {
		return err
	}
	return persistEnv(filepath.Join(paths.AppSupportDir, ".env.local"), env, set)
}
