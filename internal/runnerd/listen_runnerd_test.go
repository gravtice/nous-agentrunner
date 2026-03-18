package runnerd

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gravtice/agent-runner/internal/envfile"
	"github.com/gravtice/agent-runner/internal/platformpaths"
)

func TestListenRunnerdHTTP_ReassignWhenPortInUse(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	ts.Listener = ln
	ts.Start()
	defer ts.Close()

	appSupportDir := t.TempDir()
	cfg := Config{
		ListenAddr:       "127.0.0.1",
		ListenPort:       port,
		GuestForwardPort: 0,
		Token:            "tok",
		Paths: platformpaths.Paths{
			InstanceID:    "test",
			AppSupportDir: appSupportDir,
		},
	}

	ln2, newCfg, alreadyRunning, err := listenRunnerdHTTP(cfg)
	if err != nil {
		t.Fatalf("listenRunnerdHTTP: %v", err)
	}
	defer ln2.Close()

	if alreadyRunning {
		t.Fatalf("expected alreadyRunning=false")
	}
	if newCfg.ListenPort == port {
		t.Fatalf("expected ListenPort to change from %d", port)
	}

	envPath := filepath.Join(appSupportDir, ".env.local")
	env, err := envfile.Load(envPath)
	if err != nil {
		t.Fatalf("load %s: %v", envPath, err)
	}
	if got := env["AGENT_RUNNER_PORT"]; got != strconv.Itoa(newCfg.ListenPort) {
		t.Fatalf("expected %s to contain AGENT_RUNNER_PORT=%d, got %q", envPath, newCfg.ListenPort, got)
	}
}

func TestListenRunnerdHTTP_AlreadyRunning(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	const token = "tok"
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/v1/system/status" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"protocols":{"asmp":1},"vm":{}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	ts.Listener = ln
	ts.Start()
	defer ts.Close()

	appSupportDir := t.TempDir()
	cfg := Config{
		ListenAddr:       "127.0.0.1",
		ListenPort:       port,
		GuestForwardPort: 0,
		Token:            token,
		Paths: platformpaths.Paths{
			InstanceID:    "test",
			AppSupportDir: appSupportDir,
		},
	}

	ln2, _, alreadyRunning, err := listenRunnerdHTTP(cfg)
	if err != nil {
		t.Fatalf("listenRunnerdHTTP: %v", err)
	}
	if ln2 != nil {
		_ = ln2.Close()
		t.Fatalf("expected listener to be nil when alreadyRunning=true")
	}
	if !alreadyRunning {
		t.Fatalf("expected alreadyRunning=true")
	}

	if _, err := os.Stat(filepath.Join(appSupportDir, ".env.local")); err == nil {
		t.Fatalf("expected .env.local to not be written")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat .env.local: %v", err)
	}
}
