package runnerd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const guestRunnerDstPath = "/usr/local/bin/nous-guest-runnerd"

func (s *Server) syncGuestRunnerBinary(ctx context.Context) error {
	if runtime.GOOS != "darwin" {
		return nil
	}

	stagedPath := filepath.Join(s.cfg.Paths.DefaultSharedTmpDir, "nous-guest-runnerd")
	s.guestRunnerSyncMu.Lock()
	defer s.guestRunnerSyncMu.Unlock()

	stagedSum, err := s.stagedGuestRunnerSumLocked(stagedPath)
	if err != nil {
		return fmt.Errorf("sha256 staged guest runnerd: %w", err)
	}

	if strings.EqualFold(s.guestRunnerSyncSum, stagedSum) && s.guestRunnerSyncSum != "" {
		return nil
	}

	installedSum, err := s.guestRunnerInstalledSum(ctx)
	if err != nil {
		return err
	}
	if strings.EqualFold(installedSum, stagedSum) && installedSum != "" {
		s.guestRunnerSyncSum = stagedSum
		return nil
	}

	if installedSum == "" {
		log.Printf("guest.runnerd: install (host=%s)", stagedSum)
	} else {
		log.Printf("guest.runnerd: update (vm=%s host=%s)", installedSum, stagedSum)
	}

	if err := s.installGuestRunnerBinary(ctx, stagedPath, guestRunnerDstPath); err != nil {
		return err
	}

	installedSum2, err := s.guestRunnerInstalledSum(ctx)
	if err != nil {
		return err
	}
	if !strings.EqualFold(installedSum2, stagedSum) {
		return fmt.Errorf("guest runnerd sha mismatch after update: vm=%q host=%q", installedSum2, stagedSum)
	}

	s.guestRunnerSyncSum = stagedSum
	return nil
}

func (s *Server) stagedGuestRunnerSumLocked(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return "", fmt.Errorf("guest runnerd binary is a directory: %q", path)
	}

	size := fi.Size()
	mtime := fi.ModTime().UnixNano()
	if size == s.guestRunnerStageSize && mtime == s.guestRunnerStageMTime && s.guestRunnerStageSum != "" {
		return s.guestRunnerStageSum, nil
	}

	sum, err := sha256File(path)
	if err != nil {
		return "", err
	}
	s.guestRunnerStageSize = size
	s.guestRunnerStageMTime = mtime
	s.guestRunnerStageSum = sum
	return sum, nil
}

func (s *Server) guestRunnerInstalledSum(ctx context.Context) (string, error) {
	script := `set -euo pipefail
dst="$1"
if [ ! -x "$dst" ]; then
  echo missing
  exit 0
fi
sha256sum "$dst" | awk '{print $1}'`
	out, err := s.runLimactl(ctx, "shell", s.cfg.LimaInstanceName, "--", "bash", "-c", script, "--", guestRunnerDstPath)
	if err != nil {
		return "", err
	}
	sum := strings.TrimSpace(string(out))
	if sum == "" || sum == "missing" {
		return "", nil
	}
	sum = strings.ToLower(sum)
	if !isSHA256Hex(sum) {
		return "", fmt.Errorf("unexpected guest runnerd sha256 output: %q", sum)
	}
	return sum, nil
}

func (s *Server) installGuestRunnerBinary(ctx context.Context, src, dst string) error {
	script := `set -euo pipefail
src="$1"
dst="$2"
i=0
while [ $i -lt 60 ] && [ ! -x "$src" ]; do
  sleep 1
  i=$((i+1))
done
if [ ! -x "$src" ]; then
  echo >&2 "missing guest runnerd binary: $src"
  exit 2
fi
sudo -n install -m 0755 "$src" "$dst"
sudo -n systemctl restart nous-guest-runnerd
sudo -n systemctl is-active --quiet nous-guest-runnerd`
	_, err := s.runLimactl(ctx, "shell", s.cfg.LimaInstanceName, "--", "bash", "-c", script, "--", src, dst)
	return err
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}
