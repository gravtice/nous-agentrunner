package runnerd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type apiErrorResp struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestM5_ImagesPull_RejectsNonOfficialRegistry(t *testing.T) {
	s := &Server{cfg: Config{Token: "tok", RegistryBase: "docker.io/gravtice/"}, services: make(map[string]Service)}
	h := s.Handler()

	reqBody := []byte(`{"ref":"docker.io/library/alpine:3.19"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/pull", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out apiErrorResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Code != "REGISTRY_NOT_ALLOWED" {
		t.Fatalf("code=%q body=%s", out.Error.Code, rec.Body.String())
	}
}

func TestM5_ServicesCreate_RejectsNonAllowedImageRef(t *testing.T) {
	s := &Server{cfg: Config{Token: "tok", RegistryBase: "docker.io/gravtice/"}, services: make(map[string]Service)}
	h := s.Handler()

	reqBody := []byte(`{"type":"claude","image_ref":"docker.io/library/alpine:3.19","resources":{},"rw_mounts":[],"service_config":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/services", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out apiErrorResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Code != "REGISTRY_NOT_ALLOWED" {
		t.Fatalf("code=%q body=%s", out.Error.Code, rec.Body.String())
	}
}

func TestM5_ServicesSnapshot_RejectsNonLocalTag(t *testing.T) {
	s := &Server{cfg: Config{Token: "tok"}, services: make(map[string]Service)}
	h := s.Handler()

	reqBody := []byte(`{"new_tag":"docker.io/gravtice/whatever:1"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/services/svc_x/snapshot", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out apiErrorResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Code != "BAD_REQUEST" {
		t.Fatalf("code=%q body=%s", out.Error.Code, rec.Body.String())
	}
}

func TestM5_ImagesImport_RejectsPathNotShared(t *testing.T) {
	shareRoot := t.TempDir()
	canonShare, err := canonicalizeExistingPath(shareRoot)
	if err != nil {
		t.Fatalf("canonicalizeExistingPath(shareRoot): %v", err)
	}
	outsideRoot := t.TempDir()
	outsideFile := filepath.Join(outsideRoot, "img.tar")
	if err := os.WriteFile(outsideFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	s := &Server{
		cfg:      Config{Token: "tok"},
		services: make(map[string]Service),
		shares: []shareEntry{
			{Share: Share{ShareID: makeShareID(canonShare), HostPath: shareRoot}, CanonicalHostPath: canonShare},
		},
	}
	h := s.Handler()

	body, _ := json.Marshal(map[string]any{"path": outsideFile})
	req := httptest.NewRequest(http.MethodPost, "/v1/images/import", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out apiErrorResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Code != "PATH_NOT_ALLOWED" {
		t.Fatalf("code=%q body=%s", out.Error.Code, rec.Body.String())
	}
}

func TestM5_ImagesDelete_RejectsNonAllowedRef(t *testing.T) {
	s := &Server{cfg: Config{Token: "tok", RegistryBase: "docker.io/gravtice/"}, services: make(map[string]Service)}
	h := s.Handler()

	reqBody := []byte(`{"ref":"docker.io/library/alpine:3.19"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/delete", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out apiErrorResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Code != "REGISTRY_NOT_ALLOWED" {
		t.Fatalf("code=%q body=%s", out.Error.Code, rec.Body.String())
	}
}

func TestM5_ImagesDelete_RejectsImageInUse(t *testing.T) {
	s := &Server{
		cfg: Config{
			Token:        "tok",
			RegistryBase: "docker.io/gravtice/",
		},
		services: map[string]Service{
			"svc_x": {
				ServiceID: "svc_x",
				Type:      "claude",
				ImageRef:  "docker.io/gravtice/agent-runner-claude-agent-service:0.2.11",
				State:     "running",
			},
		},
	}
	h := s.Handler()

	reqBody := []byte(`{"ref":"docker.io/gravtice/agent-runner-claude-agent-service:0.2.11"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/delete", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 409 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out apiErrorResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Code != "IMAGE_IN_USE" {
		t.Fatalf("code=%q body=%s", out.Error.Code, rec.Body.String())
	}
}
