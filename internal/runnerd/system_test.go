package runnerd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSystemStatus_IncludesProtocolsAndCapabilities(t *testing.T) {
	s := &Server{
		cfg: Config{
			Token:            "tok",
			LimaHome:         t.TempDir(),
			LimaInstanceName: "agent-test",
			MaxInlineBytes:   8 * 1024 * 1024,
		},
		services: make(map[string]Service),
	}
	h := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/v1/system/status", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var out struct {
		Protocols    map[string]string `json:"protocols"`
		Capabilities struct {
			SingleWSPerService      bool  `json:"single_ws_per_service"`
			ErrorFatalField         bool  `json:"error_fatal_field"`
			InvalidInputReturnsDone bool  `json:"invalid_input_returns_done"`
			ServiceIdleTimeout      bool  `json:"service_idle_timeout"`
			MaxInlineBytes          int64 `json:"max_inline_bytes"`
			MaxWSMessageBytes       int64 `json:"max_ws_message_bytes"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}

	if got := out.Protocols["asmp"]; got != protocolVersionASMP {
		t.Fatalf("protocols.asmp=%q, want %q", got, protocolVersionASMP)
	}
	if got := out.Protocols["asp"]; got != protocolVersionASP {
		t.Fatalf("protocols.asp=%q, want %q", got, protocolVersionASP)
	}

	if !out.Capabilities.SingleWSPerService {
		t.Fatalf("capabilities.single_ws_per_service=false")
	}
	if !out.Capabilities.ErrorFatalField {
		t.Fatalf("capabilities.error_fatal_field=false")
	}
	if !out.Capabilities.InvalidInputReturnsDone {
		t.Fatalf("capabilities.invalid_input_returns_done=false")
	}
	if !out.Capabilities.ServiceIdleTimeout {
		t.Fatalf("capabilities.service_idle_timeout=false")
	}
	if got := out.Capabilities.MaxInlineBytes; got != s.cfg.MaxInlineBytes {
		t.Fatalf("capabilities.max_inline_bytes=%d, want %d", got, s.cfg.MaxInlineBytes)
	}
	if got := out.Capabilities.MaxWSMessageBytes; got != s.maxClientASPMessageBytes() {
		t.Fatalf("capabilities.max_ws_message_bytes=%d, want %d", got, s.maxClientASPMessageBytes())
	}
}
