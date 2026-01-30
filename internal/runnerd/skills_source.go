package runnerd

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type parsedSkillSource struct {
	Type        string // local|github|gitlab|git
	URL         string
	Ref         string
	Subpath     string
	LocalPath   string
	SkillFilter string
}

var (
	reGithubTreeWithPath = regexp.MustCompile(`github\.com\/([^/]+)\/([^/]+)\/tree\/([^/]+)\/(.+)`)
	reGithubTree         = regexp.MustCompile(`github\.com\/([^/]+)\/([^/]+)\/tree\/([^/]+)$`)
	reGithubRepo         = regexp.MustCompile(`github\.com\/([^/]+)\/([^/]+)`)

	reGitlabTreeWithPath = regexp.MustCompile(`gitlab\.com\/([^/]+)\/([^/]+)\/-\/tree\/([^/]+)\/(.+)`)
	reGitlabTree         = regexp.MustCompile(`gitlab\.com\/([^/]+)\/([^/]+)\/-\/tree\/([^/]+)$`)
	reGitlabRepo         = regexp.MustCompile(`gitlab\.com\/([^/]+)\/([^/]+)`)

	reAtSkill   = regexp.MustCompile(`^([^/]+)\/([^/@]+)@(.+)$`)
	reShorthand = regexp.MustCompile(`^([^/]+)\/([^/]+)(?:\/(.+))?$`)
)

func parseSkillSource(input string) (parsedSkillSource, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return parsedSkillSource{}, fmt.Errorf("source is empty")
	}
	if strings.ContainsRune(input, 0) {
		return parsedSkillSource{}, fmt.Errorf("source contains NUL byte")
	}

	// Local path: absolute, relative, or current directory.
	if isLocalPath(input) {
		abs, err := filepath.Abs(input)
		if err != nil {
			return parsedSkillSource{}, fmt.Errorf("invalid local path: %w", err)
		}
		return parsedSkillSource{
			Type:      "local",
			LocalPath: filepath.Clean(abs),
		}, nil
	}

	// GitHub URL with path: https://github.com/owner/repo/tree/ref/path
	if m := reGithubTreeWithPath.FindStringSubmatch(input); len(m) == 5 {
		owner, repo, ref, subpath := m[1], m[2], m[3], m[4]
		repo = strings.TrimSuffix(repo, ".git")
		return parsedSkillSource{
			Type:    "github",
			URL:     fmt.Sprintf("https://github.com/%s/%s.git", owner, repo),
			Ref:     ref,
			Subpath: subpath,
		}, nil
	}

	// GitHub URL with branch only: https://github.com/owner/repo/tree/ref
	if m := reGithubTree.FindStringSubmatch(input); len(m) == 4 {
		owner, repo, ref := m[1], m[2], m[3]
		repo = strings.TrimSuffix(repo, ".git")
		return parsedSkillSource{
			Type: "github",
			URL:  fmt.Sprintf("https://github.com/%s/%s.git", owner, repo),
			Ref:  ref,
		}, nil
	}

	// GitHub URL: https://github.com/owner/repo
	if m := reGithubRepo.FindStringSubmatch(input); len(m) == 3 {
		owner, repo := m[1], m[2]
		repo = strings.TrimSuffix(repo, ".git")
		return parsedSkillSource{
			Type: "github",
			URL:  fmt.Sprintf("https://github.com/%s/%s.git", owner, repo),
		}, nil
	}

	// GitLab URL with path: https://gitlab.com/owner/repo/-/tree/ref/path
	if m := reGitlabTreeWithPath.FindStringSubmatch(input); len(m) == 5 {
		owner, repo, ref, subpath := m[1], m[2], m[3], m[4]
		repo = strings.TrimSuffix(repo, ".git")
		return parsedSkillSource{
			Type:    "gitlab",
			URL:     fmt.Sprintf("https://gitlab.com/%s/%s.git", owner, repo),
			Ref:     ref,
			Subpath: subpath,
		}, nil
	}

	// GitLab URL with branch only: https://gitlab.com/owner/repo/-/tree/ref
	if m := reGitlabTree.FindStringSubmatch(input); len(m) == 4 {
		owner, repo, ref := m[1], m[2], m[3]
		repo = strings.TrimSuffix(repo, ".git")
		return parsedSkillSource{
			Type: "gitlab",
			URL:  fmt.Sprintf("https://gitlab.com/%s/%s.git", owner, repo),
			Ref:  ref,
		}, nil
	}

	// GitLab URL: https://gitlab.com/owner/repo
	if m := reGitlabRepo.FindStringSubmatch(input); len(m) == 3 {
		owner, repo := m[1], m[2]
		repo = strings.TrimSuffix(repo, ".git")
		return parsedSkillSource{
			Type: "gitlab",
			URL:  fmt.Sprintf("https://gitlab.com/%s/%s.git", owner, repo),
		}, nil
	}

	// GitHub shorthand: owner/repo@skill-name
	if m := reAtSkill.FindStringSubmatch(input); len(m) == 4 && isValidShorthandSource(input) {
		owner, repo, skillFilter := m[1], m[2], m[3]
		return parsedSkillSource{
			Type:        "github",
			URL:         fmt.Sprintf("https://github.com/%s/%s.git", owner, repo),
			SkillFilter: strings.TrimSpace(skillFilter),
		}, nil
	}

	// GitHub shorthand: owner/repo or owner/repo/subpath
	if m := reShorthand.FindStringSubmatch(input); len(m) >= 3 && isValidShorthandSource(input) {
		owner, repo := m[1], m[2]
		subpath := ""
		if len(m) >= 4 {
			subpath = m[3]
		}
		return parsedSkillSource{
			Type:    "github",
			URL:     fmt.Sprintf("https://github.com/%s/%s.git", owner, repo),
			Subpath: subpath,
		}, nil
	}

	// Fallback: treat as direct git URL.
	if !looksLikeGitURL(input) {
		return parsedSkillSource{}, fmt.Errorf("unsupported source format")
	}
	return parsedSkillSource{
		Type: "git",
		URL:  input,
	}, nil
}

func isLocalPath(input string) bool {
	if filepath.IsAbs(input) {
		return true
	}
	if input == "." || input == ".." || strings.HasPrefix(input, "./") || strings.HasPrefix(input, "../") {
		return true
	}
	// Windows absolute paths like C:\ or D:/
	if len(input) >= 3 && ((input[0] >= 'a' && input[0] <= 'z') || (input[0] >= 'A' && input[0] <= 'Z')) && input[1] == ':' {
		switch input[2] {
		case '/', '\\':
			return true
		}
	}
	return false
}

func isValidShorthandSource(input string) bool {
	// Avoid matching URLs (http://) or scp-style git (git@host:repo) etc.
	if strings.Contains(input, ":") {
		return false
	}
	if strings.HasPrefix(input, ".") || strings.HasPrefix(input, "/") {
		return false
	}
	return true
}

func looksLikeGitURL(input string) bool {
	switch {
	case strings.Contains(input, "://"):
		return true
	case strings.HasPrefix(input, "git@"):
		return true
	case strings.HasSuffix(input, ".git"):
		return true
	default:
		return false
	}
}
