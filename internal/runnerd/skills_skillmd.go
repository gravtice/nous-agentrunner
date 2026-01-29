package runnerd

import (
	"bufio"
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
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 0, 1024), 512*1024)

	if !sc.Scan() {
		return skillMDMeta{}, false
	}
	if strings.TrimSpace(sc.Text()) != "---" {
		return skillMDMeta{}, false
	}

	var meta skillMDMeta
	for sc.Scan() {
		line := sc.Text()
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
			meta.Name = v
		case "description":
			meta.Description = v
		}
	}
	if meta.Name == "" && meta.Description == "" {
		return skillMDMeta{}, false
	}
	return meta, true
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
