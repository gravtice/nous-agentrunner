package runnerd

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestM4_ValidateClientASPMessage_InputBytesAndPath(t *testing.T) {
	shareRoot := t.TempDir()
	canonShare, err := canonicalizeExistingPath(shareRoot)
	if err != nil {
		t.Fatalf("canonicalizeExistingPath(shareRoot): %v", err)
	}
	inShareFile := filepath.Join(shareRoot, "in.txt")
	if err := os.WriteFile(inShareFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("write in-share file: %v", err)
	}
	outsideRoot := t.TempDir()
	outsideFile := filepath.Join(outsideRoot, "out.txt")
	if err := os.WriteFile(outsideFile, []byte("y"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	s := &Server{
		cfg: Config{MaxInlineBytes: 3},
		shares: []shareEntry{
			{Share: Share{ShareID: makeShareID(canonShare), HostPath: shareRoot}, CanonicalHostPath: canonShare},
		},
	}

	okMsg := map[string]any{
		"type": "input",
		"contents": []any{
			map[string]any{"kind": "text", "text": "hi"},
			map[string]any{
				"kind": "image",
				"source": map[string]any{
					"type":     "bytes",
					"encoding": "base64",
					"data":     base64.StdEncoding.EncodeToString([]byte("abc")),
					"mime":     "image/png",
				},
			},
			map[string]any{
				"kind": "file",
				"source": map[string]any{
					"type": "path",
					"path": inShareFile,
					"mime": "text/plain",
				},
			},
		},
	}
	raw, _ := json.Marshal(okMsg)
	if err := s.validateClientASPMessage(raw); err != nil {
		t.Fatalf("validateClientASPMessage(ok): %v", err)
	}

	tooLarge := map[string]any{
		"type": "input",
		"contents": []any{
			map[string]any{
				"kind": "file",
				"source": map[string]any{
					"type":     "bytes",
					"encoding": "base64",
					"data":     base64.StdEncoding.EncodeToString([]byte("abcd")),
				},
			},
		},
	}
	raw, _ = json.Marshal(tooLarge)
	if err := s.validateClientASPMessage(raw); err == nil || mapErrorCode(err) != "INLINE_BYTES_TOO_LARGE" {
		t.Fatalf("expected INLINE_BYTES_TOO_LARGE, got %v (code=%q)", err, mapErrorCode(err))
	}

	notAllowed := map[string]any{
		"type": "input",
		"contents": []any{
			map[string]any{
				"kind": "file",
				"source": map[string]any{
					"type": "path",
					"path": outsideFile,
				},
			},
		},
	}
	raw, _ = json.Marshal(notAllowed)
	if err := s.validateClientASPMessage(raw); err == nil || mapErrorCode(err) != "PATH_NOT_ALLOWED" {
		t.Fatalf("expected PATH_NOT_ALLOWED, got %v (code=%q)", err, mapErrorCode(err))
	}
}

func TestM4_ValidateClientASPMessage_CancelAndInvalid(t *testing.T) {
	s := &Server{cfg: Config{MaxInlineBytes: 8}}

	if err := s.validateClientASPMessage([]byte(`{"type":"cancel"}`)); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := s.validateClientASPMessage([]byte(`{"type":"ask.answer","ask_id":"ask_x","answers":{"q":"a"}}`)); err != nil {
		t.Fatalf("ask.answer: %v", err)
	}
	if err := s.validateClientASPMessage([]byte(`{"type":"permission_mode.set","mode":"plan"}`)); err != nil {
		t.Fatalf("permission_mode.set: %v", err)
	}
	if err := s.validateClientASPMessage([]byte(`{"type":"input","contents":[]}`)); err == nil {
		t.Fatalf("expected error for empty contents")
	}
	if err := s.validateClientASPMessage([]byte(`{"type":"ask.answer","ask_id":"","answers":{}}`)); err == nil {
		t.Fatalf("expected error for missing ask_id")
	}
	if err := s.validateClientASPMessage([]byte(`{"type":"permission_mode.set","mode":""}`)); err == nil {
		t.Fatalf("expected error for missing mode")
	}
	if err := s.validateClientASPMessage([]byte(`{"type":"permission_mode.set","mode":"nope"}`)); err == nil {
		t.Fatalf("expected error for unsupported mode")
	}
	if err := s.validateClientASPMessage([]byte(`{"type":"nope"}`)); err == nil {
		t.Fatalf("expected error for unsupported message type")
	}
}
