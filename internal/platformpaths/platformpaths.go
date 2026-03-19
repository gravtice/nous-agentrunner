package platformpaths

import (
	"fmt"
	"os"
	"path/filepath"
)

type Paths struct {
	InstanceID          string
	AppSupportDir       string
	LogsDir             string
	CachesDir           string
	DefaultSharedTmpDir string
}

func Resolve(instanceID string) (Paths, error) {
	if instanceID == "" {
		instanceID = "default"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	return buildPaths(home, instanceID), nil
}

func buildPaths(home, instanceID string) Paths {
	root := filepath.Join(home, ".agentrunner")
	cachesRoot := filepath.Join(root, "caches")
	logsRoot := filepath.Join(root, "logs")
	appSupport := filepath.Join(root, instanceID)
	caches := filepath.Join(cachesRoot, instanceID)
	logs := filepath.Join(logsRoot, instanceID)
	return Paths{
		InstanceID:          instanceID,
		AppSupportDir:       appSupport,
		LogsDir:             logs,
		CachesDir:           caches,
		DefaultSharedTmpDir: filepath.Join(caches, "SharedTmp"),
	}
}

func EnsureDirs(p Paths) error {
	for _, dir := range []string{p.AppSupportDir, p.LogsDir, p.CachesDir, p.DefaultSharedTmpDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("mkdir %q: %w", dir, err)
		}
	}
	return nil
}
