package runnerd

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func cleanupOrphanSSHTunnels(cfg Config) {
	if runtime.GOOS != "darwin" {
		return
	}
	if strings.TrimSpace(cfg.LimaHome) == "" || strings.TrimSpace(cfg.LimaInstanceName) == "" {
		return
	}

	sshConfigFile := filepath.Join(cfg.LimaHome, cfg.LimaInstanceName, "ssh.config")
	if fi, err := os.Stat(sshConfigFile); err != nil || fi.IsDir() {
		return
	}
	sshHost := "lima-" + cfg.LimaInstanceName

	out, err := exec.Command("ps", "-ax", "-o", "pid=", "-o", "command=").Output()
	if err != nil {
		log.Printf("ssh.cleanup: ps failed: %v", err)
		return
	}

	lines := strings.Split(string(out), "\n")
	killed := 0
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 1 || pid == os.Getpid() {
			continue
		}
		cmd := strings.Join(fields[1:], " ")

		// Match runnerd-managed SSH port forwards only.
		if !strings.Contains(cmd, "-F "+sshConfigFile) {
			continue
		}
		if !strings.Contains(cmd, sshHost) {
			continue
		}
		if !strings.Contains(cmd, "ExitOnForwardFailure=yes") {
			continue
		}
		if !strings.Contains(cmd, " -N") {
			continue
		}
		if !(strings.Contains(cmd, " -R ") || strings.Contains(cmd, " -L ")) {
			continue
		}

		log.Printf("ssh.cleanup: kill orphan ssh tunnel pid=%d cmd=%q", pid, cmd)
		_ = syscall.Kill(pid, syscall.SIGTERM)

		deadline := time.Now().Add(400 * time.Millisecond)
		for time.Now().Before(deadline) {
			if err := syscall.Kill(pid, 0); err != nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err := syscall.Kill(pid, 0); err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		killed++
	}
	if killed > 0 {
		log.Printf("ssh.cleanup: killed %d orphan tunnel(s)", killed)
	}
}
