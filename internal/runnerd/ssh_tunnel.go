package runnerd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func (s *Server) limaSSHConfigFile(ctx context.Context) (string, error) {
	cand := filepath.Join(s.limaInstanceDir(), "ssh.config")
	if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
		return cand, nil
	}

	out, err := s.runLimactl(ctx, "list", "--format", "json", s.cfg.LimaInstanceName)
	if err != nil {
		return "", err
	}
	items, err := parseLimactlListOutput(out)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", errors.New("lima instance not found")
	}

	item := items[0]
	for _, it := range items {
		if it.Name == s.cfg.LimaInstanceName {
			item = it
			break
		}
	}
	cfgPath := strings.TrimSpace(item.SSHConfigFile)
	if cfgPath == "" {
		return "", errors.New("lima ssh config file not found")
	}
	if fi, err := os.Stat(cfgPath); err != nil || fi.IsDir() {
		return "", fmt.Errorf("lima ssh config file missing: %s", cfgPath)
	}
	return cfgPath, nil
}

func (s *Server) startReverseSSHTunnel(ctx context.Context, hostPort, guestPort int) (context.CancelFunc, <-chan error, error) {
	if hostPort <= 0 || hostPort > 65535 {
		return nil, nil, fmt.Errorf("invalid host port %d", hostPort)
	}
	if guestPort <= 0 || guestPort > 65535 {
		return nil, nil, fmt.Errorf("invalid guest port %d", guestPort)
	}

	sshConfigFile, err := s.limaSSHConfigFile(ctx)
	if err != nil {
		return nil, nil, err
	}

	sshHost := "lima-" + s.cfg.LimaInstanceName
	spec := fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", guestPort, hostPort)

	tunnelCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(tunnelCtx, "ssh",
		"-F", sshConfigFile,
		"-T",
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "ControlPersist=no",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-R", spec,
		sshHost,
	)
	cmd.Stdout = io.Discard

	var stderr cappedBuffer
	stderr.max = 32 * 1024
	stderrLog := newLineLogger("ssh(tunnel): ")
	cmd.Stderr = io.MultiWriter(&stderr, stderrLog)

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, err
	}

	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if err != nil && tunnelCtx.Err() == nil {
			log.Printf("ssh tunnel guest_port=%d host_port=%d exited: %v", guestPort, hostPort, err)
		}
		done <- err
		close(done)
	}()

	select {
	case err := <-done:
		cancel()
		msg := strings.TrimSpace(stderr.String())
		if msg == "" && err != nil {
			msg = err.Error()
		}
		if msg == "" {
			msg = "ssh tunnel exited"
		}
		return nil, nil, errors.New(msg)
	case <-time.After(250 * time.Millisecond):
	}

	return cancel, done, nil
}

func (s *Server) startSSHTunnel(ctx context.Context, hostPort, guestPort int) (*exec.Cmd, error) {
	if hostPort <= 0 || hostPort > 65535 {
		return nil, fmt.Errorf("invalid host port %d", hostPort)
	}
	if guestPort <= 0 || guestPort > 65535 {
		return nil, fmt.Errorf("invalid guest port %d", guestPort)
	}

	sshConfigFile, err := s.limaSSHConfigFile(ctx)
	if err != nil {
		return nil, err
	}

	sshHost := "lima-" + s.cfg.LimaInstanceName
	spec := fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", hostPort, guestPort)

	cmd := exec.CommandContext(ctx, "ssh",
		"-F", sshConfigFile,
		"-T",
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "ControlPersist=no",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-L", spec,
		sshHost,
	)
	cmd.Stdout = io.Discard

	var stderr cappedBuffer
	stderr.max = 32 * 1024
	stderrLog := newLineLogger("ssh(forward): ")
	cmd.Stderr = io.MultiWriter(&stderr, stderrLog)

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		_ = cmd.Wait()
		return nil, ctx.Err()
	case <-time.After(250 * time.Millisecond):
	}

	if cmd.Process == nil {
		return nil, errors.New("ssh tunnel failed to start")
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		waitErr := cmd.Wait()
		stderrLog.Flush()
		msg := strings.TrimSpace(stderr.String())
		if msg == "" && waitErr != nil {
			msg = waitErr.Error()
		}
		if msg == "" {
			msg = "ssh tunnel exited"
		}
		return nil, errors.New(msg)
	}

	return cmd, nil
}
