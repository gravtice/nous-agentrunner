package runnerd

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
)

type skillMDMeta struct {
	Name        string
	Description string
}

func readSkillMDMeta(path string) skillMDMeta {
	const maxBytes = 256 * 1024

	f, err := os.Open(path)
	if err != nil {
		return skillMDMeta{}
	}
	defer f.Close()

	b, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return skillMDMeta{}
	}

	// UTF-8 BOM.
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})

	// Normalize newlines.
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	if meta, ok := parseYAMLFrontmatter(s); ok {
		return meta
	}
	return parseMarkdownFallback(s)
}

func parseYAMLFrontmatter(s string) (skillMDMeta, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return skillMDMeta{}, false
	}
	if strings.TrimSpace(lines[0]) != "---" {
		return skillMDMeta{}, false
	}

	var meta skillMDMeta
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		k, v, ok := cutKeyValue(trimmed)
		if !ok {
			continue
		}
		switch k {
		case "name":
			if block, ok := parseYAMLBlockScalarHeader(v); ok {
				meta.Name, i = consumeYAMLBlockScalar(lines, i+1, block)
				i--
			} else {
				meta.Name = v
			}
		case "description":
			if block, ok := parseYAMLBlockScalarHeader(v); ok {
				meta.Description, i = consumeYAMLBlockScalar(lines, i+1, block)
				i--
			} else {
				meta.Description = v
			}
		}
	}
	if meta.Name == "" && meta.Description == "" {
		return skillMDMeta{}, false
	}
	return meta, true
}

type yamlBlockScalarHeader struct {
	style  byte // '>' folded, '|' literal
	indent int
}

func parseYAMLBlockScalarHeader(v string) (yamlBlockScalarHeader, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return yamlBlockScalarHeader{}, false
	}
	style := v[0]
	if style != '>' && style != '|' {
		return yamlBlockScalarHeader{}, false
	}
	rest := strings.TrimSpace(v[1:])
	indent := 0
	for i := 0; i < len(rest); i++ {
		ch := rest[i]
		switch {
		case ch == '+' || ch == '-':
			continue
		case ch >= '0' && ch <= '9':
			indent = indent*10 + int(ch-'0')
		default:
			return yamlBlockScalarHeader{}, false
		}
	}
	return yamlBlockScalarHeader{style: style, indent: indent}, true
}

func consumeYAMLBlockScalar(lines []string, start int, header yamlBlockScalarHeader) (string, int) {
	minIndent := header.indent
	firstContentSeen := false
	var blockLines []string
	i := start
	for ; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			// Keep blank lines inside the block scalar.
			blockLines = append(blockLines, "")
			continue
		}

		indent := leadingIndentWidth(line)
		if indent == 0 {
			// Dedented line means block scalar ended.
			break
		}
		if !firstContentSeen {
			firstContentSeen = true
			if minIndent == 0 {
				minIndent = indent
			}
		}
		if indent < minIndent {
			break
		}

		blockLines = append(blockLines, trimLeadingIndent(line, minIndent))
	}

	if len(blockLines) == 0 {
		return "", i
	}

	switch header.style {
	case '|':
		return strings.Join(blockLines, "\n"), i
	case '>':
		return foldYAMLBlockLines(blockLines), i
	default:
		return strings.Join(blockLines, "\n"), i
	}
}

func leadingIndentWidth(line string) int {
	n := 0
	for n < len(line) {
		if line[n] != ' ' && line[n] != '\t' {
			break
		}
		n++
	}
	return n
}

func trimLeadingIndent(line string, indent int) string {
	if indent <= 0 {
		return line
	}
	i := 0
	for i < len(line) && i < indent {
		if line[i] != ' ' && line[i] != '\t' {
			break
		}
		i++
	}
	return line[i:]
}

func foldYAMLBlockLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	paragraphs := make([][]string, 0, 4)
	curr := make([]string, 0, 4)
	for _, line := range lines {
		if line == "" {
			if len(curr) > 0 {
				paragraphs = append(paragraphs, curr)
				curr = make([]string, 0, 4)
			}
			paragraphs = append(paragraphs, nil)
			continue
		}
		curr = append(curr, line)
	}
	if len(curr) > 0 {
		paragraphs = append(paragraphs, curr)
	}

	var parts []string
	for _, p := range paragraphs {
		if p == nil {
			parts = append(parts, "")
			continue
		}
		parts = append(parts, strings.Join(p, " "))
	}

	return strings.Join(parts, "\n")
}

func parseMarkdownFallback(s string) skillMDMeta {
	lines := strings.Split(s, "\n")
	var name string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			name = strings.TrimSpace(strings.TrimLeft(line, "#"))
			break
		}
	}
	if name == "" {
		return skillMDMeta{}
	}

	var desc string
	foundHeader := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			foundHeader = true
			continue
		}
		if !foundHeader {
			continue
		}
		if line == "---" {
			continue
		}
		desc = line
		break
	}

	return skillMDMeta{
		Name:        truncateString(name, 256),
		Description: truncateString(desc, 2048),
	}
}

func cutKeyValue(line string) (key, value string, ok bool) {
	i := strings.IndexByte(line, ':')
	if i <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:i])
	value = strings.TrimSpace(line[i+1:])
	if key == "" || value == "" {
		return "", "", false
	}
	value = strings.TrimRight(value, " \t")
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		if value[0] == '"' {
			if unq, err := strconv.Unquote(value); err == nil {
				value = unq
			}
		} else {
			value = value[1 : len(value)-1]
		}
	}
	return strings.ToLower(key), strings.TrimSpace(value), true
}

func truncateString(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max]
}
