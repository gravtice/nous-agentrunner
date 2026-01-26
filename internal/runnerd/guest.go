package runnerd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func (s *Server) ensureGuestReady(ctx context.Context) (*guestClient, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("guest backend requires darwin host (AVF)")
	}

	step := func(name string, fn func() error) error {
		start := time.Now()
		log.Printf("%s: start", name)
		err := fn()
		if err != nil {
			log.Printf("%s: error after %s: %v", name, time.Since(start).Truncate(time.Millisecond), err)
			return err
		}
		log.Printf("%s: ok (%s)", name, time.Since(start).Truncate(time.Millisecond))
		return nil
	}

	if err := step("vm.ensure_running", func() error { return s.ensureVMRunning(ctx) }); err != nil {
		return nil, err
	}
	if err := step("vm.wait_ssh_config", func() error { return s.waitForFile(ctx, s.limaSSHConfigPath(), 5*time.Minute) }); err != nil {
		return nil, err
	}
	if err := step("vm.wait_guest_ssh", func() error { return s.waitForGuestSSH(ctx, 2*time.Minute) }); err != nil {
		return nil, err
	}

	if err := step("guest.install_runnerd", func() error { return s.ensureGuestRunnerdInstalled(ctx) }); err != nil {
		return nil, err
	}

	localPort, err := pickFreeLocalPort()
	if err != nil {
		return nil, err
	}

	tunnel, err := s.startSSHTunnel(ctx, localPort, s.cfg.GuestRunnerPort)
	if err != nil {
		return nil, err
	}
	go func() { _ = tunnel.Wait() }()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", localPort)
	client := &guestClient{baseURL: baseURL, http: &http.Client{Timeout: 30 * time.Second}}
	if err := step("guest.wait_health", func() error { return s.waitForGuestHealth(ctx, client, 30*time.Second) }); err != nil {
		if tunnel.Process != nil {
			_ = tunnel.Process.Kill()
		}
		return nil, err
	}

	go func() {
		<-ctx.Done()
		if tunnel.Process != nil {
			_ = tunnel.Process.Kill()
		}
	}()

	return client, nil
}

func (s *Server) startSSHTunnel(ctx context.Context, localPort, guestPort int) (*exec.Cmd, error) {
	sshConfig := s.limaSSHConfigPath()
	destHost := "lima-" + s.cfg.LimaInstanceName

	args := []string{
		"-F", sshConfig,
		"-o", "ExitOnForwardFailure=yes",
		"-N",
		"-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", localPort, guestPort),
		destHost,
	}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Give ssh a moment to establish the tunnel; health check follows.
	time.Sleep(300 * time.Millisecond)
	return cmd, nil
}

func (s *Server) startSSHReverseTunnel(ctx context.Context, guestPort, hostPort int) (*exec.Cmd, error) {
	sshConfig := s.limaSSHConfigPath()
	destHost := "lima-" + s.cfg.LimaInstanceName

	args := []string{
		"-F", sshConfig,
		"-o", "ExitOnForwardFailure=yes",
		"-N",
		"-R", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", guestPort, hostPort),
		destHost,
	}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Give ssh a moment to establish the tunnel.
	time.Sleep(300 * time.Millisecond)
	return cmd, nil
}

func (s *Server) ensureGuestRunnerdInstalled(ctx context.Context) error {
	hostBin := strings.TrimSpace(s.cfg.GuestBinaryPath)
	if hostBin == "" {
		return errors.New("NOUS_AGENT_RUNNER_GUEST_BINARY_PATH is not configured")
	}
	if _, err := os.Stat(hostBin); err != nil {
		return fmt.Errorf("guest runner binary not found at %q: %w", hostBin, err)
	}

	// If already installed, just ensure it's running.
	if err := s.runInGuest(ctx, "command -v nous-guest-runnerd >/dev/null 2>&1"); err == nil {
		upToDate, err := s.guestRunnerdMatchesHost(ctx, hostBin)
		if err != nil {
			log.Printf("guest.runnerd: version check failed: %v", err)
		}
		if upToDate {
			return s.runInGuest(ctx, fmt.Sprintf("sudo -n systemctl enable --now nous-guest-runnerd || sudo -n systemctl start nous-guest-runnerd; sudo -n systemctl is-active --quiet nous-guest-runnerd"))
		}
		log.Printf("guest.runnerd: reinstalling (host binary changed)")
	}

	// Copy to guest and install.
	remoteTmp := fmt.Sprintf("%s:/tmp/nous-guest-runnerd", s.cfg.LimaInstanceName)
	if _, err := s.runLimactl(ctx, "copy", hostBin, remoteTmp); err != nil {
		return err
	}
	if err := s.runInGuest(ctx, "sudo -n install -m 0755 /tmp/nous-guest-runnerd /usr/local/bin/nous-guest-runnerd"); err != nil {
		return err
	}

	unit := buildGuestRunnerdUnit(s.cfg.GuestRunnerPort)
	// Use a heredoc to avoid quoting pitfalls.
	if err := s.runInGuest(ctx, fmt.Sprintf("sudo -n tee /etc/systemd/system/nous-guest-runnerd.service >/dev/null <<'EOF'\n%s\nEOF", unit)); err != nil {
		return err
	}
	if err := s.runInGuest(ctx, "sudo -n systemctl daemon-reload && sudo -n systemctl enable --now nous-guest-runnerd && sudo -n systemctl restart nous-guest-runnerd"); err != nil {
		return err
	}
	return s.runInGuest(ctx, "sudo -n systemctl is-active --quiet nous-guest-runnerd")
}

func (s *Server) guestRunnerdMatchesHost(ctx context.Context, hostBin string) (bool, error) {
	hostSHA, err := sha256File(hostBin)
	if err != nil {
		return false, err
	}
	out, err := s.runInGuestOutput(ctx, "sha256sum /usr/local/bin/nous-guest-runnerd 2>/dev/null | awk '{print $1}'")
	if err != nil {
		return false, err
	}
	guestSHA := strings.ToLower(strings.TrimSpace(out))
	if guestSHA == "" {
		return false, errors.New("empty guest sha256")
	}
	return strings.ToLower(hostSHA) == guestSHA, nil
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

func buildGuestRunnerdUnit(port int) string {
	return fmt.Sprintf(`[Unit]
Description=Nous Guest Runner Daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=NOUS_GUEST_RUNNERD_PORT=%d
ExecStart=/usr/local/bin/nous-guest-runnerd
Restart=always
RestartSec=1

[Install]
WantedBy=multi-user.target
`, port)
}

func (s *Server) runInGuest(ctx context.Context, bashCmd string) error {
	_, err := s.runLimactl(ctx, "shell", s.cfg.LimaInstanceName, "--", "bash", "-lc", bashCmd)
	return err
}

func (s *Server) runInGuestOutput(ctx context.Context, bashCmd string) (string, error) {
	out, err := s.runLimactl(ctx, "shell", s.cfg.LimaInstanceName, "--", "bash", "-lc", bashCmd)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (s *Server) waitForGuestSSH(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := s.runInGuest(attemptCtx, "true")
		cancel()
		if err == nil {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if last != nil {
		return fmt.Errorf("timeout waiting for guest ssh: %w", last)
	}
	return fmt.Errorf("timeout waiting for guest ssh")
}

func (s *Server) waitForGuestHealth(ctx context.Context, c *guestClient, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		attemptCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := c.health(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	if last != nil {
		return fmt.Errorf("timeout waiting for guest health: %w", last)
	}
	return fmt.Errorf("timeout waiting for guest health")
}
