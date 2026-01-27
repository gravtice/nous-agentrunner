package guestrunnerd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFakeNerdctl(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "nerdctl")
	script := `#!/bin/sh
set -eu
log="${NERDCTL_LOG:-}"
if [ -n "$log" ]; then
  printf "%s\n" "$*" >> "$log"
fi
cmd="${1:-}"
case "$cmd" in
  run)
    port=""
    prev=""
    for a in "$@"; do
      if [ "$prev" = "-e" ]; then
        case "$a" in
          NOUS_SERVICE_PORT=*)
            port="${a#NOUS_SERVICE_PORT=}"
            ;;
        esac
      fi
      prev="$a"
    done
    if [ -n "$port" ]; then
      # One-shot health server for waitHTTPHealth.
      # Redirect output so exec.Cmd doesn't wait for the background process.
      python3 - "$port" >/dev/null 2>&1 <<'PY' &
import socket
import sys

port = int(sys.argv[1])
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", port))
s.listen(1)
conn, _ = s.accept()
_ = conn.recv(4096)
body = b'{"ok":true}'
resp = b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " + str(len(body)).encode("ascii") + b"\r\n\r\n" + body
conn.sendall(resp)
conn.close()
s.close()
PY
    fi
    ;;
  images)
    if [ -n "${NERDCTL_IMAGES_OUTPUT:-}" ]; then
      printf "%b" "$NERDCTL_IMAGES_OUTPUT"
    fi
    ;;
  load)
    if [ -n "${NERDCTL_LOAD_OUTPUT:-}" ]; then
      printf "%b" "$NERDCTL_LOAD_OUTPUT"
    fi
    ;;
esac
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake nerdctl: %v", err)
	}
	return path
}

func writeFailingNerdctl(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "nerdctl")
	script := `#!/bin/sh
echo "boom" >&2
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write failing nerdctl: %v", err)
	}
	return path
}

func TestM2_HandleImagesList_ParsesNerdctlOutput(t *testing.T) {
	binDir := t.TempDir()
	_ = writeFakeNerdctl(t, binDir)

	logPath := filepath.Join(t.TempDir(), "nerdctl.log")
	t.Setenv("NERDCTL_LOG", logPath)
	t.Setenv("NERDCTL_IMAGES_OUTPUT", "local/a:1\n<none>:<none>\nlocal/b:2\n\n")
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	s, err := NewServer(Config{ListenAddr: "127.0.0.1", ListenPort: 0, StateDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/internal/images")
	if err != nil {
		t.Fatalf("GET /internal/images: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var out struct {
		Images []string `json:"images"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if strings.Join(out.Images, ",") != "local/a:1,local/b:2" {
		t.Fatalf("unexpected images: %#v", out.Images)
	}
}

func TestM2_HandleServiceCreate_BuildsMounts(t *testing.T) {
	binDir := t.TempDir()
	_ = writeFakeNerdctl(t, binDir)

	logPath := filepath.Join(t.TempDir(), "nerdctl.log")
	t.Setenv("NERDCTL_LOG", logPath)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	s, err := NewServer(Config{ListenAddr: "127.0.0.1", ListenPort: 0, StateDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	shareDir := t.TempDir()
	rwDir := filepath.Join(shareDir, "rw")
	if err := os.MkdirAll(rwDir, 0o700); err != nil {
		t.Fatalf("mkdir rw: %v", err)
	}

	cfgB64 := base64.StdEncoding.EncodeToString([]byte(`{"k":"v"}`))
	reqBody, _ := json.Marshal(createServiceReq{
		ServiceID:        "abc123",
		Type:             "claude",
		ImageRef:         "local/claude-agent-service:0.1.0",
		Shares:           []string{shareDir},
		RWMounts:         []string{rwDir},
		Env:              map[string]string{"ANTHROPIC_API_KEY": "shh"},
		ServiceConfigB64: cfgB64,
		MaxInlineBytes:   1234,
	})
	resp, err := http.Post(ts.URL+"/internal/services", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /internal/services: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read nerdctl log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "rm -f svc-abc123") {
		t.Fatalf("expected rm in log, got:\n%s", log)
	}
	if !strings.Contains(log, "--name svc-abc123") {
		t.Fatalf("expected --name in log, got:\n%s", log)
	}
	if !strings.Contains(log, "--network=host") || !strings.Contains(log, "--restart=unless-stopped") {
		t.Fatalf("expected network/restart flags in log, got:\n%s", log)
	}
	if !strings.Contains(log, "type=bind,src="+shareDir+",dst="+shareDir+",ro") {
		t.Fatalf("expected ro mount in log, got:\n%s", log)
	}
	if !strings.Contains(log, "type=bind,src="+rwDir+",dst="+rwDir+",rw") {
		t.Fatalf("expected rw mount in log, got:\n%s", log)
	}
	if !strings.Contains(log, "-e NOUS_MAX_INLINE_BYTES=1234") {
		t.Fatalf("expected max bytes env in log, got:\n%s", log)
	}
	if !strings.Contains(log, "-e ANTHROPIC_API_KEY=shh") {
		t.Fatalf("expected user env in log, got:\n%s", log)
	}
	if !strings.Contains(log, "local/claude-agent-service:0.1.0") {
		t.Fatalf("expected image ref in log, got:\n%s", log)
	}
}

func TestM2_HandleServiceCreate_SetsUpSkillsSymlinks(t *testing.T) {
	binDir := t.TempDir()
	_ = writeFakeNerdctl(t, binDir)

	logPath := filepath.Join(t.TempDir(), "nerdctl.log")
	t.Setenv("NERDCTL_LOG", logPath)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	skillsDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(skillsDir, "alpha"), 0o700); err != nil {
		t.Fatalf("mkdir alpha: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(skillsDir, "beta"), 0o700); err != nil {
		t.Fatalf("mkdir beta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "not-a-skill.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	s, err := NewServer(Config{ListenAddr: "127.0.0.1", ListenPort: 0, StateDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	shareDir := t.TempDir()
	rwDir := filepath.Join(shareDir, "rw")
	if err := os.MkdirAll(rwDir, 0o700); err != nil {
		t.Fatalf("mkdir rw: %v", err)
	}

	cfgB64 := base64.StdEncoding.EncodeToString([]byte(`{"k":"v"}`))
	reqBody, _ := json.Marshal(createServiceReq{
		ServiceID:        "abc123",
		Type:             "claude",
		ImageRef:         "local/claude-agent-service:0.1.0",
		Shares:           []string{shareDir},
		RWMounts:         []string{rwDir},
		Env:              map[string]string{"ANTHROPIC_API_KEY": "shh"},
		ServiceConfigB64: cfgB64,
		SkillsDir:        skillsDir,
		MaxInlineBytes:   1234,
	})
	resp, err := http.Post(ts.URL+"/internal/services", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /internal/services: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read nerdctl log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "exec svc-abc123 mkdir -p /tmp/.claude/skills") {
		t.Fatalf("expected mkdir exec in log, got:\n%s", log)
	}
	if !strings.Contains(log, "exec svc-abc123 ln -sfn "+filepath.Join(skillsDir, "alpha")+" /tmp/.claude/skills/alpha") {
		t.Fatalf("expected alpha link exec in log, got:\n%s", log)
	}
	if !strings.Contains(log, "exec svc-abc123 ln -sfn "+filepath.Join(skillsDir, "beta")+" /tmp/.claude/skills/beta") {
		t.Fatalf("expected beta link exec in log, got:\n%s", log)
	}
}

func TestRunNerdctl_RedactsEnvValuesInError(t *testing.T) {
	binDir := t.TempDir()
	_ = writeFailingNerdctl(t, binDir)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	_, err := runNerdctl(context.Background(), "run", "-e", "SECRET=shh", "img")
	if err == nil {
		t.Fatalf("expected error")
	}
	if strings.Contains(err.Error(), "shh") {
		t.Fatalf("error leaked secret value: %v", err)
	}
	if !strings.Contains(err.Error(), "SECRET=<redacted>") {
		t.Fatalf("expected redacted env in error: %v", err)
	}
}
