package runnerd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
	state, err := limaInstanceStateFromListOutput(out, s.cfg.LimaInstanceName)
	if err != nil {
		return "", fmt.Errorf("parse limactl list output: %w", err)
	}
	return state, nil
}

func limaInstanceStateFromListOutput(out []byte, instanceName string) (string, error) {
	items, err := parseLimactlListOutput(out)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "not_created", nil
	}

	item := items[0]
	if instanceName != "" && len(items) > 1 {
		for _, it := range items {
			if it.Name == instanceName {
				item = it
				break
			}
		}
	}

	switch item.Status {
	case "Running":
		return "running", nil
	case "Stopped":
		return "stopped", nil
	default:
		if item.Status == "" {
			return "unknown", nil
		}
		return strings.ToLower(item.Status), nil
	}
}

func parseLimactlListOutput(out []byte) ([]limaListItem, error) {
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, nil
	}

	switch out[0] {
	case '[':
		var items []limaListItem
		if err := json.Unmarshal(out, &items); err != nil {
			return nil, err
		}
		return items, nil
	case '{':
		var item limaListItem
		if err := json.Unmarshal(out, &item); err == nil {
			return []limaListItem{item}, nil
		}
	}

	// Lima v2 may output NDJSON (one JSON object per line).
	var items []limaListItem
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 1024), 1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var item limaListItem
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return items, nil
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

	assets, err := s.prepareOfflineAssets()
	if err != nil {
		return err
	}
	if assets != nil {
		log.Printf("vm.offline_assets: enabled (vm_image=%s nerdctl=%s)", assets.VMImagePath, assets.NerdctlArchivePath)
	}

	s.mu.Lock()
	cfgYAML := buildLimaYAML(s.cfg, s.shares, assets)
	s.mu.Unlock()

	if err := os.WriteFile(s.limaConfigPath(), []byte(cfgYAML), 0o600); err != nil {
		return err
	}

	// Create+start if instance is not present (or has incomplete state).
	// limactl expects $LIMA_HOME/<name>/lima.yaml to exist for existing instances.
	instanceDir := s.limaInstanceDir()
	if _, err := os.Stat(instanceDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, err := s.runLimactl(ctx, "start", "--name", s.cfg.LimaInstanceName, s.limaConfigPath())
			return err
		}
		return err
	}
	if _, err := os.Stat(filepath.Join(instanceDir, "lima.yaml")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_ = os.RemoveAll(instanceDir)
			_, err := s.runLimactl(ctx, "start", "--name", s.cfg.LimaInstanceName, s.limaConfigPath())
			return err
		}
		return err
	}

	_, err = s.runLimactl(ctx, "start", s.cfg.LimaInstanceName)
	if err == nil {
		return nil
	}
	// Prior versions wrote the template-based YAML (with "base") directly into the instance dir,
	// which breaks `limactl start` for existing instances (instance YAML must have `images` and `base` must be empty).
	// Repair by deleting and recreating the instance from our current config.
	if isLimaInstanceYAMLInvalid(err) {
		_, _ = s.runLimactl(ctx, "delete", "-f", s.cfg.LimaInstanceName)
		_, err2 := s.runLimactl(ctx, "start", "--name", s.cfg.LimaInstanceName, s.limaConfigPath())
		return err2
	}
	return err
}

func (s *Server) runLimactl(ctx context.Context, args ...string) ([]byte, error) {
	limactlArgs := append([]string{"--tty=false"}, args...)
	if len(args) > 0 && args[0] == "start" {
		limactlArgs = append([]string{"--tty=false", "start", "--timeout=30m"}, args[1:]...)
	}

	start := time.Now()
	log.Printf("limactl %v: start", args)
	cmd := exec.CommandContext(ctx, s.cfg.LimactlPath, limactlArgs...)
	env := os.Environ()
	env = setEnv(env, "LIMA_HOME", s.cfg.LimaHome)
	if s.cfg.LimaTemplatesPath != "" {
		env = setEnv(env, "LIMA_TEMPLATES_PATH", s.cfg.LimaTemplatesPath)
	}
	if s.cfg.HTTPProxy != "" {
		env = setEnv(env, "HTTP_PROXY", s.cfg.HTTPProxy)
		env = setEnv(env, "http_proxy", s.cfg.HTTPProxy)
	}
	if s.cfg.HTTPSProxy != "" {
		env = setEnv(env, "HTTPS_PROXY", s.cfg.HTTPSProxy)
		env = setEnv(env, "https_proxy", s.cfg.HTTPSProxy)
	}
	if s.cfg.NoProxy != "" {
		env = setEnv(env, "NO_PROXY", s.cfg.NoProxy)
		env = setEnv(env, "no_proxy", s.cfg.NoProxy)
	}
	cmd.Env = env
	var stdout cappedBuffer
	stdout.max = 64 * 1024
	var stderr cappedBuffer
	stderr.max = 256 * 1024
	cmd.Stdout = &stdout
	stderrLog := newLineLogger("limactl(" + firstArg(args) + "): ")
	cmd.Stderr = io.MultiWriter(&stderr, stderrLog)

	var stillDone chan struct{}
	if len(args) > 0 && args[0] == "start" {
		stillDone = make(chan struct{})
		go logStillRunning(stillDone, "limactl start", 30*time.Second)
	}

	if err := cmd.Run(); err != nil {
		if stillDone != nil {
			close(stillDone)
		}
		stderrLog.Flush()

		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if strings.Contains(msg, "com.apple.security.virtualization") {
			log.Printf("limactl %v: missing com.apple.security.virtualization entitlement", args)
			return nil, fmt.Errorf("missing com.apple.security.virtualization entitlement (codesign limactl with vz entitlements)")
		}
		log.Printf("limactl %v: error after %s: %s", args, time.Since(start).Truncate(time.Millisecond), msg)
		return nil, fmt.Errorf("limactl %v: %s", args, msg)
	}
	if stillDone != nil {
		close(stillDone)
	}
	stderrLog.Flush()

	log.Printf("limactl %v: ok (%s)", args, time.Since(start).Truncate(time.Millisecond))
	return stdout.Bytes(), nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return "?"
	}
	return args[0]
}

type lineLogger struct {
	prefix string
	buf    []byte
}

func newLineLogger(prefix string) *lineLogger {
	return &lineLogger{prefix: prefix}
}

func (l *lineLogger) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		idx := bytes.IndexByte(l.buf, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(string(l.buf[:idx]), "\r")
		l.buf = l.buf[idx+1:]
		if line == "" {
			continue
		}
		log.Printf("%s%s", l.prefix, line)
	}
	if len(l.buf) > 4096 {
		log.Printf("%s%s", l.prefix, strings.TrimRight(string(l.buf), "\r"))
		l.buf = l.buf[:0]
	}
	return len(p), nil
}

func (l *lineLogger) Flush() {
	if len(l.buf) == 0 {
		return
	}
	line := strings.TrimRight(string(l.buf), "\r")
	if line != "" {
		log.Printf("%s%s", l.prefix, line)
	}
	l.buf = l.buf[:0]
}

func logStillRunning(done <-chan struct{}, label string, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	start := time.Now()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			log.Printf("%s: still running (%s)", label, time.Since(start).Truncate(time.Second))
		}
	}
}

func setEnv(env []string, key, val string) []string {
	prefix := key + "="
	out := env[:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, prefix+val)
}

func isLimaInstanceYAMLInvalid(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "errors inspecting instance") ||
		strings.Contains(msg, "field `base` must be empty") ||
		strings.Contains(msg, "field `images` must be set")
}

type cappedBuffer struct {
	buf []byte
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.max <= 0 {
		return len(p), nil
	}
	if len(p) >= c.max {
		c.buf = append(c.buf[:0], p[len(p)-c.max:]...)
		return len(p), nil
	}
	if len(c.buf)+len(p) <= c.max {
		c.buf = append(c.buf, p...)
		return len(p), nil
	}
	overflow := len(c.buf) + len(p) - c.max
	if overflow > len(c.buf) {
		overflow = len(c.buf)
	}
	n := copy(c.buf, c.buf[overflow:])
	c.buf = c.buf[:n]
	c.buf = append(c.buf, p...)
	return len(p), nil
}

func (c *cappedBuffer) String() string { return string(c.buf) }
func (c *cappedBuffer) Bytes() []byte  { return c.buf }

func buildLimaYAML(cfg Config, shares []shareEntry, assets *offlineAssets) string {
	var b strings.Builder
	b.WriteString("base:\n")
	baseTmpl := cfg.LimaBaseTemplate
	if baseTmpl == "" {
		baseTmpl = "_images/debian-12"
	}
	b.WriteString("- template:")
	b.WriteString(baseTmpl)
	b.WriteString("\n\n")
	if assets != nil && assets.VMImagePath != "" {
		b.WriteString("images:\n")
		b.WriteString("- location: ")
		b.WriteString(yamlQuote(assets.VMImagePath))
		b.WriteString("\n  arch: \"aarch64\"\n")
		if assets.VMImageDigest != "" {
			b.WriteString("  digest: ")
			b.WriteString(yamlQuote(assets.VMImageDigest))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
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
	if assets != nil && assets.NerdctlArchivePath != "" {
		b.WriteString("  archives:\n")
		b.WriteString("  - location: ")
		b.WriteString(yamlQuote(assets.NerdctlArchivePath))
		b.WriteString("\n    arch: \"aarch64\"\n")
		if assets.NerdctlArchiveDigest != "" {
			b.WriteString("    digest: ")
			b.WriteString(yamlQuote(assets.NerdctlArchiveDigest))
			b.WriteString("\n")
		}
	}
	b.WriteString("mounts:\n")
	for _, e := range shares {
		b.WriteString("- location: ")
		b.WriteString(yamlQuote(e.CanonicalHostPath))
		b.WriteString("\n  mountPoint: ")
		// Preserve the original host path in the guest for path transparency (/Users/... stays /Users/...).
		b.WriteString(yamlQuote(filepath.Clean(e.HostPath)))
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
