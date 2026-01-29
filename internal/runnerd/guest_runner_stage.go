package runnerd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func stageGuestRunnerBinary(cfg Config) error {
	src := strings.TrimSpace(cfg.GuestBinaryPath)
	if src == "" {
		return errors.New("NOUS_AGENT_RUNNER_GUEST_BINARY_PATH is not configured")
	}
	fi, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("guest runner binary not found at %q: %w", src, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("guest runner binary is a directory: %q", src)
	}

	dst := filepath.Join(cfg.Paths.DefaultSharedTmpDir, "nous-guest-runnerd")
	tmp := dst + ".tmp"

	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

