package runnerd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gravtice/nous-agent-runner/internal/platformpaths"
)

func TestServices_VMNotRunning_ReportsStopped(t *testing.T) {
	appSupport := t.TempDir()
	limaHome := t.TempDir()

	s := &Server{
		cfg: Config{
			Token:            "tok",
			LimaHome:         limaHome,
			LimaInstanceName: "nous-test",
			Paths:            platformpaths.Paths{AppSupportDir: appSupport},
		},
		services: make(map[string]Service),
	}
	s.services["svc_a"] = Service{
		ServiceID: "svc_a",
		Type:      "claude",
		ImageRef:  "local/x",
		State:     "running",
		CreatedAt: "2026-01-01T00:00:00Z",
	}
	h := s.Handler()

	t.Run("list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/services", nil)
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out struct {
			Services []Service `json:"services"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
		}
		if len(out.Services) != 1 {
			t.Fatalf("services=%d body=%s", len(out.Services), rec.Body.String())
		}
		if out.Services[0].State != "stopped" {
			t.Fatalf("state=%q", out.Services[0].State)
		}
	})

	t.Run("get", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/services/svc_a", nil)
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out Service
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
		}
		if out.State != "stopped" {
			t.Fatalf("state=%q", out.State)
		}
	})

	t.Run("delete", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/services/svc_a", nil)
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		s.mu.Lock()
		_, ok := s.services["svc_a"]
		s.mu.Unlock()
		if ok {
			t.Fatalf("service still present after delete")
		}
	})
}
