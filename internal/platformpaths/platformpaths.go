package platformpaths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Paths struct {
	InstanceID          string
	AppSupportDir       string
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

	switch runtime.GOOS {
	case "darwin":
		appSupport := filepath.Join(home, "Library", "Application Support", "NousAgentRunner", instanceID)
		caches := filepath.Join(home, "Library", "Caches", "NousAgentRunner", instanceID)
		return Paths{
			InstanceID:          instanceID,
			AppSupportDir:       appSupport,
			CachesDir:           caches,
			DefaultSharedTmpDir: filepath.Join(caches, "SharedTmp"),
		}, nil
	default:
		// Dev-friendly fallback (not a product target).
		appSupport := filepath.Join(home, ".local", "share", "NousAgentRunner", instanceID)
		caches := filepath.Join(home, ".cache", "NousAgentRunner", instanceID)
		return Paths{
			InstanceID:          instanceID,
			AppSupportDir:       appSupport,
			CachesDir:           caches,
			DefaultSharedTmpDir: filepath.Join(caches, "SharedTmp"),
		}, nil
	}
}

func EnsureDirs(p Paths) error {
	for _, dir := range []string{p.AppSupportDir, p.CachesDir, p.DefaultSharedTmpDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("mkdir %q: %w", dir, err)
		}
	}
	return nil
}
