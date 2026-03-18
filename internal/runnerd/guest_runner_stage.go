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
		return errors.New("AGENT_RUNNER_GUEST_BINARY_PATH is not configured")
	}
	fi, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("guest runner binary not found at %q: %w", src, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("guest runner binary is a directory: %q", src)
	}

	if err := os.MkdirAll(cfg.Paths.DefaultSharedTmpDir, 0o700); err != nil {
		return err
	}

	dst := filepath.Join(cfg.Paths.DefaultSharedTmpDir, guestRunnerBinaryName)
	if err := copyExecutableFile(src, dst); err != nil {
		return err
	}
	return nil
}

func copyExecutableFile(src, dst string) error {
	tmp := dst + ".tmp"

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
