package runnerd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuiltinTools_ClaudeOK(t *testing.T) {
	s := &Server{cfg: Config{Token: "tok"}, services: make(map[string]Service)}
	h := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/v1/services/types/claude/builtin_tools", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Type         string   `json:"type"`
		BuiltinTools []string `json:"builtin_tools"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	if out.Type != "claude" {
		t.Fatalf("type=%q", out.Type)
	}
	if len(out.BuiltinTools) == 0 {
		t.Fatalf("builtin_tools empty")
	}
}

func TestBuiltinTools_UnknownType404(t *testing.T) {
	s := &Server{cfg: Config{Token: "tok"}, services: make(map[string]Service)}
	h := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/v1/services/types/nope/builtin_tools", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out apiErrorResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Code != "NOT_FOUND" {
		t.Fatalf("code=%q body=%s", out.Error.Code, rec.Body.String())
	}
}
