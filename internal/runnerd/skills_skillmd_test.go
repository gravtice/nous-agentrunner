package runnerd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSkillMDMeta_YAMLFoldedDescription(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	content := `---
name: gravtice-genai-skill
description: >
  Use gravtice-genai as an end user (not a contributor): run ` + "`genai`" + ` CLI for text/image/audio/video/embedding
  across providers (OpenAI/Gemini/Claude/DashScope/Doubao/Tuzi).
  中文: 用 gravtice-genai 调用多家大模型 + 启动 MCP 服务 + 排错。
---
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	meta := readSkillMDMeta(path)
	if meta.Name != "gravtice-genai-skill" {
		t.Fatalf("name=%q", meta.Name)
	}
	if strings.TrimSpace(meta.Description) == ">" {
		t.Fatalf("description parsed as scalar marker only: %q", meta.Description)
	}
	if !strings.Contains(meta.Description, "Use gravtice-genai as an end user") {
		t.Fatalf("missing english description: %q", meta.Description)
	}
	if !strings.Contains(meta.Description, "中文: 用 gravtice-genai 调用多家大模型") {
		t.Fatalf("missing chinese description: %q", meta.Description)
	}
}

func TestParseYAMLFrontmatter_YAMLLiteralDescription(t *testing.T) {
	input := `---
name: sample
description: |
  line one
  line two
---
`
	meta, ok := parseYAMLFrontmatter(input)
	if !ok {
		t.Fatalf("expected frontmatter")
	}
	if meta.Name != "sample" {
		t.Fatalf("name=%q", meta.Name)
	}
	if meta.Description != "line one\nline two" {
		t.Fatalf("description=%q", meta.Description)
	}
}
