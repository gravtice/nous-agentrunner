package runnerd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTunnelsList_Empty(t *testing.T) {
	s := &Server{
		cfg: Config{Token: "tok"},
		// Minimal state for tunnels endpoints.
		tunnels:          make(map[string]*tunnelEntry),
		tunnelByHostPort: make(map[int]string),
	}
	h := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/v1/tunnels", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var out struct {
		Tunnels []Tunnel `json:"tunnels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	if len(out.Tunnels) != 0 {
		t.Fatalf("tunnels=%d, want 0", len(out.Tunnels))
	}
}

func TestTunnelsGetByHostPort_ReturnsTunnel(t *testing.T) {
	done := make(chan error)
	cancelCalled := false

	s := &Server{
		cfg:              Config{Token: "tok"},
		tunnels:          make(map[string]*tunnelEntry),
		tunnelByHostPort: make(map[int]string),
	}
	entry := &tunnelEntry{
		Tunnel: Tunnel{
			TunnelID:  "tun_1",
			HostPort:  9222,
			GuestPort: 18080,
			State:     "running",
			CreatedAt: "2026-01-30T00:00:00Z",
		},
		cancel: func() { cancelCalled = true },
		done:   done,
	}
	s.tunnels[entry.TunnelID] = entry
	s.tunnelByHostPort[entry.HostPort] = entry.TunnelID

	h := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/v1/tunnels/by_host_port/9222", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if cancelCalled {
		t.Fatalf("cancel should not be called")
	}
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var out Tunnel
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	if out.TunnelID != entry.TunnelID || out.HostPort != entry.HostPort || out.GuestPort != entry.GuestPort {
		t.Fatalf("tunnel=%+v, want %+v", out, entry.Tunnel)
	}
}

func TestTunnelsGetByHostPort_StalePrunesAnd404(t *testing.T) {
	done := make(chan error)
	close(done)

	s := &Server{
		cfg:              Config{Token: "tok"},
		tunnels:          make(map[string]*tunnelEntry),
		tunnelByHostPort: make(map[int]string),
	}
	entry := &tunnelEntry{
		Tunnel: Tunnel{
			TunnelID:  "tun_1",
			HostPort:  9222,
			GuestPort: 18080,
			State:     "running",
			CreatedAt: "2026-01-30T00:00:00Z",
		},
		done: done,
	}
	s.tunnels[entry.TunnelID] = entry
	s.tunnelByHostPort[entry.HostPort] = entry.TunnelID

	h := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/v1/tunnels/by_host_port/9222", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := s.tunnels[entry.TunnelID]; ok {
		t.Fatalf("stale tunnel entry should be pruned")
	}
	if _, ok := s.tunnelByHostPort[entry.HostPort]; ok {
		t.Fatalf("stale tunnel mapping should be pruned")
	}
}

func TestTunnelsDeleteByHostPort_DeletesAndCancels(t *testing.T) {
	done := make(chan error)
	cancelCalled := false

	s := &Server{
		cfg:              Config{Token: "tok"},
		tunnels:          make(map[string]*tunnelEntry),
		tunnelByHostPort: make(map[int]string),
	}
	entry := &tunnelEntry{
		Tunnel: Tunnel{
			TunnelID:  "tun_1",
			HostPort:  9222,
			GuestPort: 18080,
			State:     "running",
			CreatedAt: "2026-01-30T00:00:00Z",
		},
		cancel: func() { cancelCalled = true },
		done:   done,
	}
	s.tunnels[entry.TunnelID] = entry
	s.tunnelByHostPort[entry.HostPort] = entry.TunnelID

	h := s.Handler()
	req := httptest.NewRequest(http.MethodDelete, "/v1/tunnels/by_host_port/9222", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Deleted bool `json:"deleted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	if !out.Deleted {
		t.Fatalf("deleted=false, want true")
	}
	if !cancelCalled {
		t.Fatalf("cancel should be called")
	}
	if _, ok := s.tunnels[entry.TunnelID]; ok {
		t.Fatalf("tunnel entry should be deleted")
	}
	if _, ok := s.tunnelByHostPort[entry.HostPort]; ok {
		t.Fatalf("tunnel mapping should be deleted")
	}
}

func TestTunnelsList_PrunedStale(t *testing.T) {
	runningDone := make(chan error)
	staleDone := make(chan error)
	close(staleDone)

	s := &Server{
		cfg:              Config{Token: "tok"},
		tunnels:          make(map[string]*tunnelEntry),
		tunnelByHostPort: make(map[int]string),
	}
	running := &tunnelEntry{
		Tunnel: Tunnel{
			TunnelID:  "tun_run",
			HostPort:  9222,
			GuestPort: 18080,
			State:     "running",
			CreatedAt: "2026-01-30T00:00:00Z",
		},
		done: runningDone,
	}
	stale := &tunnelEntry{
		Tunnel: Tunnel{
			TunnelID:  "tun_stale",
			HostPort:  9333,
			GuestPort: 18081,
			State:     "running",
			CreatedAt: "2026-01-30T00:00:00Z",
		},
		done: staleDone,
	}
	s.tunnels[running.TunnelID] = running
	s.tunnelByHostPort[running.HostPort] = running.TunnelID
	s.tunnels[stale.TunnelID] = stale
	s.tunnelByHostPort[stale.HostPort] = stale.TunnelID

	h := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/v1/tunnels", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Tunnels []Tunnel `json:"tunnels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	if len(out.Tunnels) != 1 {
		t.Fatalf("tunnels=%d, want 1", len(out.Tunnels))
	}
	if out.Tunnels[0].TunnelID != running.TunnelID {
		t.Fatalf("tunnel_id=%q, want %q", out.Tunnels[0].TunnelID, running.TunnelID)
	}
	if _, ok := s.tunnels[stale.TunnelID]; ok {
		t.Fatalf("stale tunnel entry should be pruned")
	}
	if _, ok := s.tunnelByHostPort[stale.HostPort]; ok {
		t.Fatalf("stale tunnel mapping should be pruned")
	}
}

func TestTunnelsByHostPort_InvalidHostPort(t *testing.T) {
	s := &Server{
		cfg:              Config{Token: "tok"},
		tunnels:          make(map[string]*tunnelEntry),
		tunnelByHostPort: make(map[int]string),
	}
	h := s.Handler()

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/v1/tunnels/by_host_port/0"},
		{method: http.MethodGet, path: "/v1/tunnels/by_host_port/not-a-port"},
		{method: http.MethodDelete, path: "/v1/tunnels/by_host_port/70000"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}
