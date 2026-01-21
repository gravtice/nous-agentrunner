package runnerd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type limaListItem struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	SSHConfigFile string `json:"sshConfigFile"`
}

func (s *Server) limaInstanceDir() string {
	return filepath.Join(s.cfg.LimaHome, s.cfg.LimaInstanceName)
}

func (s *Server) limaSSHConfigPath() string {
	return filepath.Join(s.limaInstanceDir(), "ssh.config")
}

func (s *Server) limaConfigPath() string {
	return filepath.Join(s.cfg.Paths.AppSupportDir, "lima", "instance.yaml")
}

func (s *Server) limaInstanceState(ctx context.Context) (string, error) {
	if _, err := os.Stat(s.limaInstanceDir()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "not_created", nil
		}
		return "", err
	}
	out, err := s.runLimactl(ctx, "list", "--format", "json", s.cfg.LimaInstanceName)
	if err != nil {
		return "", err
	}
	var items []limaListItem
	if err := json.Unmarshal(out, &items); err != nil {
		return "", fmt.Errorf("parse limactl list output: %w", err)
	}
	if len(items) == 0 {
		return "not_created", nil
	}
	switch items[0].Status {
	case "Running":
		return "running", nil
	case "Stopped":
		return "stopped", nil
	default:
		if items[0].Status == "" {
			return "unknown", nil
		}
		return strings.ToLower(items[0].Status), nil
	}
}

func (s *Server) ensureVMRunning(ctx context.Context) error {
	if runtime.GOOS != "darwin" {
		// VM backend is only meaningful on macOS; allow dev runs elsewhere.
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.limaConfigPath()), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(s.cfg.LimaHome, 0o700); err != nil {
		return err
	}

	s.mu.Lock()
	cfgYAML := buildLimaYAML(s.cfg, s.shares)
	s.mu.Unlock()

	if err := os.WriteFile(s.limaConfigPath(), []byte(cfgYAML), 0o600); err != nil {
		return err
	}

	// Create+start if instance dir doesn't exist.
	if _, err := os.Stat(s.limaInstanceDir()); errors.Is(err, os.ErrNotExist) {
		_, err := s.runLimactl(ctx, "start", "--name", s.cfg.LimaInstanceName, s.limaConfigPath())
		return err
	}

	// Best-effort: update config in-place for the next restart.
	_ = os.WriteFile(filepath.Join(s.limaInstanceDir(), "lima.yaml"), []byte(cfgYAML), 0o600)
	_, err := s.runLimactl(ctx, "start", s.cfg.LimaInstanceName)
	return err
}

func (s *Server) runLimactl(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, s.cfg.LimactlPath, args...)
	cmd.Env = append(os.Environ(), "LIMA_HOME="+s.cfg.LimaHome)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("limactl %v: %s", args, msg)
	}
	return stdout.Bytes(), nil
}

func buildLimaYAML(cfg Config, shares []shareEntry) string {
	var b strings.Builder
	b.WriteString("base:\n")
	b.WriteString("- template://default\n\n")
	b.WriteString("vmType: \"vz\"\n")
	b.WriteString("mountType: \"virtiofs\"\n")
	if cfg.VMCPU > 0 {
		fmt.Fprintf(&b, "cpus: %d\n", cfg.VMCPU)
	}
	if cfg.VMMemoryMiB > 0 {
		fmt.Fprintf(&b, "memory: \"%dMiB\"\n", cfg.VMMemoryMiB)
	}
	b.WriteString("containerd:\n")
	b.WriteString("  system: true\n")
	b.WriteString("  user: false\n")
	b.WriteString("mounts:\n")
	for _, e := range shares {
		b.WriteString("- location: ")
		b.WriteString(yamlQuote(e.CanonicalHostPath))
		b.WriteString("\n  mountPoint: ")
		b.WriteString(yamlQuote(e.CanonicalHostPath))
		b.WriteString("\n  writable: true\n")
	}
	return b.String()
}

func yamlQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}

func (s *Server) waitForFile(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		_, err := os.Stat(path)
		if err == nil {
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %q", path)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
