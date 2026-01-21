package runnerd

import (
	"context"
	"errors"
	"fmt"
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

	if err := s.ensureVMRunning(ctx); err != nil {
		return nil, err
	}
	if err := s.waitForFile(ctx, s.limaSSHConfigPath(), 60*time.Second); err != nil {
		return nil, err
	}
	if err := s.ensureGuestRunnerdInstalled(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.guestClient != nil {
		c := s.guestClient
		s.mu.Unlock()
		return c, nil
	}
	s.mu.Unlock()

	localPort, err := pickFreeLocalPort()
	if err != nil {
		return nil, err
	}

	tunnel, err := s.startSSHTunnel(ctx, localPort, s.cfg.GuestRunnerPort)
	if err != nil {
		return nil, err
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", localPort)
	client := &guestClient{baseURL: baseURL, http: &http.Client{Timeout: 30 * time.Second}}
	if err := client.health(ctx); err != nil {
		_ = tunnel.Process.Kill()
		return nil, err
	}

	s.mu.Lock()
	s.guestLocalPort = localPort
	s.sshTunnelCmd = tunnel
	s.guestClient = client
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = tunnel.Process.Kill()
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
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Give ssh a moment to establish the tunnel; health check follows.
	time.Sleep(300 * time.Millisecond)
	return cmd, nil
}

func (s *Server) ensureGuestRunnerdInstalled(ctx context.Context) error {
	// If already installed, just ensure it's running.
	if err := s.runInGuest(ctx, "command -v nous-guest-runnerd >/dev/null 2>&1"); err == nil {
		return s.runInGuest(ctx, fmt.Sprintf("sudo systemctl enable --now nous-guest-runnerd || sudo systemctl start nous-guest-runnerd; sudo systemctl is-active --quiet nous-guest-runnerd"))
	}

	hostBin := strings.TrimSpace(s.cfg.GuestBinaryPath)
	if hostBin == "" {
		return errors.New("NOUS_AGENT_RUNNER_GUEST_BINARY_PATH is not configured")
	}
	if _, err := os.Stat(hostBin); err != nil {
		return fmt.Errorf("guest runner binary not found at %q: %w", hostBin, err)
	}

	// Copy to guest and install.
	remoteTmp := fmt.Sprintf("%s:/tmp/nous-guest-runnerd", s.cfg.LimaInstanceName)
	if _, err := s.runLimactl(ctx, "copy", hostBin, remoteTmp); err != nil {
		return err
	}
	if err := s.runInGuest(ctx, "sudo install -m 0755 /tmp/nous-guest-runnerd /usr/local/bin/nous-guest-runnerd"); err != nil {
		return err
	}

	unit := buildGuestRunnerdUnit(s.cfg.GuestRunnerPort)
	// Use a heredoc to avoid quoting pitfalls.
	if err := s.runInGuest(ctx, fmt.Sprintf("sudo tee /etc/systemd/system/nous-guest-runnerd.service >/dev/null <<'EOF'\n%s\nEOF", unit)); err != nil {
		return err
	}
	if err := s.runInGuest(ctx, "sudo systemctl daemon-reload && sudo systemctl enable --now nous-guest-runnerd"); err != nil {
		return err
	}
	return s.runInGuest(ctx, "sudo systemctl is-active --quiet nous-guest-runnerd")
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
