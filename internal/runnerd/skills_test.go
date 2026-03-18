package runnerd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gravtice/agent-runner/internal/platformpaths"
)

func TestParseSkillSource_GitHubShorthand(t *testing.T) {
	got, err := parseSkillSource("remotion-dev/skills")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Type != "github" {
		t.Fatalf("type=%q", got.Type)
	}
	if got.URL != "https://github.com/remotion-dev/skills.git" {
		t.Fatalf("url=%q", got.URL)
	}
	if got.Subpath != "" || got.Ref != "" {
		t.Fatalf("subpath=%q ref=%q", got.Subpath, got.Ref)
	}
}

func TestParseSkillSource_GitHubAtSkill(t *testing.T) {
	got, err := parseSkillSource("vercel-labs/agent-skills@frontend-design")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Type != "github" || got.SkillFilter != "frontend-design" {
		t.Fatalf("type=%q skillFilter=%q", got.Type, got.SkillFilter)
	}
}

func TestParseSkillSource_RejectsGarbage(t *testing.T) {
	if _, err := parseSkillSource("not-a-source"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestDiscoverSkillDirs_PriorityAndFallback(t *testing.T) {
	t.Run("priority only", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFile(t, filepath.Join(root, "skills", "a", "SKILL.md"), "a\n")
		mustWriteFile(t, filepath.Join(root, ".codex", "skills", "b", "SKILL.md"), "b\n")
		mustWriteFile(t, filepath.Join(root, "random", "c", "SKILL.md"), "c\n")

		got, err := discoverSkillDirs(root, "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		names := discoveredInstallNames(got)
		if len(names) != 2 || names[0] != "a" || names[1] != "b" {
			t.Fatalf("names=%v", names)
		}
	})

	t.Run("fallback recursion", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFile(t, filepath.Join(root, "random", "c", "SKILL.md"), "c\n")
		got, err := discoverSkillDirs(root, "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		names := discoveredInstallNames(got)
		if len(names) != 1 || names[0] != "c" {
			t.Fatalf("names=%v", names)
		}
	})
}

func TestSkillsInstall_List_Delete(t *testing.T) {
	appSupport := t.TempDir()
	source := t.TempDir()
	mustWriteFile(t, filepath.Join(source, "skills", "foo", "SKILL.md"), "---\nname: foo-skill\ndescription: Foo desc\n---\n\n# Foo\n")

	canon, err := canonicalizeExistingPath(source)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}

	s := &Server{
		cfg: Config{
			Token: "tok",
			Paths: platformpaths.Paths{
				AppSupportDir:       appSupport,
				DefaultSharedTmpDir: t.TempDir(),
			},
		},
		services: make(map[string]Service),
		shares: []shareEntry{
			{
				Share:             Share{ShareID: makeShareID(canon), HostPath: source},
				CanonicalHostPath: canon,
			},
		},
	}
	h := s.Handler()

	t.Run("discover includes metadata", func(t *testing.T) {
		body := mustMarshalJSON(t, map[string]any{"source": source})
		req := httptest.NewRequest(http.MethodPost, "/v1/skills/discover", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out struct {
			Skills []struct {
				InstallName string `json:"install_name"`
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"skills"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
		}
		if len(out.Skills) != 1 {
			t.Fatalf("skills=%v", out.Skills)
		}
		if out.Skills[0].InstallName != "foo" {
			t.Fatalf("install_name=%q", out.Skills[0].InstallName)
		}
		if out.Skills[0].Name != "foo-skill" || out.Skills[0].Description != "Foo desc" {
			t.Fatalf("name=%q description=%q", out.Skills[0].Name, out.Skills[0].Description)
		}
	})

	t.Run("install ok", func(t *testing.T) {
		body := mustMarshalJSON(t, map[string]any{"source": source, "skills": []string{"foo"}})
		req := httptest.NewRequest(http.MethodPost, "/v1/skills/install", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(appSupport, "skills", "foo", "SKILL.md")); err != nil {
			t.Fatalf("installed skill missing: %v", err)
		}
		if _, err := os.Stat(filepath.Join(appSupport, "skills", "foo", ".agent-runner-source.json")); err != nil {
			t.Fatalf("source metadata missing: %v", err)
		}
	})

	t.Run("install by skill name rejected", func(t *testing.T) {
		body := mustMarshalJSON(t, map[string]any{"source": source, "skills": []string{"foo-skill"}})
		req := httptest.NewRequest(http.MethodPost, "/v1/skills/install", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("install conflict", func(t *testing.T) {
		body := mustMarshalJSON(t, map[string]any{"source": source, "skills": []string{"foo"}})
		req := httptest.NewRequest(http.MethodPost, "/v1/skills/install", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("list includes skill", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/skills", nil)
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out struct {
			Skills []skillListItem `json:"skills"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
		}
		if len(out.Skills) != 1 || out.Skills[0].Name != "foo" {
			t.Fatalf("skills=%v", out.Skills)
		}
	})

	t.Run("delete ok", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/skills/foo", nil)
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(appSupport, "skills", "foo")); !os.IsNotExist(err) {
			t.Fatalf("expected skill removed, stat err=%v", err)
		}
	})
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func mustMarshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	return b
}
