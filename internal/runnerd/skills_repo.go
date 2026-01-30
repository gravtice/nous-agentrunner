package runnerd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *Server) cloneSkillRepo(ctx context.Context, url, ref string) (repoDir, cleanupDir, commit string, _ error) {
	url = strings.TrimSpace(url)
	ref = strings.TrimSpace(ref)
	if url == "" {
		return "", "", "", skillBadRequest("repo url is required", nil)
	}
	if strings.ContainsRune(url, 0) || strings.ContainsAny(url, "\r\n\t") {
		return "", "", "", skillBadRequest("invalid repo url", nil)
	}

	parent := s.cfg.Paths.DefaultSharedTmpDir
	if parent == "" {
		parent = os.TempDir()
	}
	tmpParent, err := os.MkdirTemp(parent, "skills-src-")
	if err != nil {
		return "", "", "", err
	}
	cleanupDir = tmpParent
	repoDir = filepath.Join(tmpParent, "repo")

	cloneCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if err := gitClone(cloneCtx, url, repoDir, ref); err != nil {
		return "", "", "", &skillHTTPError{
			Status:  500,
			Code:    "GIT_ERROR",
			Message: "git clone failed",
			Details: map[string]any{"error": err.Error()},
		}
	}

	commit, _ = gitRevParseHEAD(cloneCtx, repoDir)
	return repoDir, cleanupDir, commit, nil
}

func gitClone(ctx context.Context, url, dst, ref string) error {
	if ref == "" {
		return runGit(ctx, "clone", "--depth", "1", "--", url, dst)
	}

	// Fast path: branch/tag clone.
	if err := runGit(ctx, "clone", "--depth", "1", "--branch", ref, "--", url, dst); err == nil {
		return nil
	}

	// Fallback: clone default branch, then fetch+checkout requested ref (commit SHA, etc).
	if err := runGit(ctx, "clone", "--depth", "1", "--", url, dst); err != nil {
		return err
	}
	if err := runGit(ctx, "-C", dst, "fetch", "--depth", "1", "origin", ref); err != nil {
		return err
	}
	return runGit(ctx, "-C", dst, "checkout", "--detach", "FETCH_HEAD")
}

func gitRevParseHEAD(ctx context.Context, repoDir string) (string, error) {
	out, err := runGitOutput(ctx, "-C", repoDir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func runGit(ctx context.Context, args ...string) error {
	_, err := runGitOutputBytes(ctx, args...)
	return err
}

func runGitOutput(ctx context.Context, args ...string) (string, error) {
	b, err := runGitOutputBytes(ctx, args...)
	return string(b), err
}

func runGitOutputBytes(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	env := os.Environ()
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false")
	cmd.Env = env
	err := cmd.Run()
	out := bytes.TrimSpace(stdout.Bytes())
	errOut := bytes.TrimSpace(stderr.Bytes())
	if err == nil {
		if len(out) > 0 {
			return append(out, '\n'), nil
		}
		return nil, nil
	}
	msg := strings.TrimSpace(string(append(out, errOut...)))
	if msg == "" {
		msg = err.Error()
	}
	return nil, errors.New(msg)
}

func discoverSkillDirs(basePath, subpath string) ([]discoveredSkill, error) {
	basePath = strings.TrimSpace(basePath)
	if basePath == "" {
		return nil, nil
	}
	root := filepath.Clean(basePath)
	searchRoot := root
	if strings.TrimSpace(subpath) != "" {
		searchRoot = filepath.Clean(filepath.Join(root, subpath))
		if !hasPathPrefix(searchRoot, root) {
			return nil, fmt.Errorf("invalid subpath")
		}
	}

	if isRegularFile(filepath.Join(searchRoot, "SKILL.md")) {
		return materializeDiscoveredSkills(root, []string{searchRoot})
	}

	priority := prioritySkillSearchDirs(searchRoot)
	var found []string
	seen := make(map[string]bool)

	for _, dir := range priority {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if name == "" || name == "__MACOSX" {
				continue
			}
			skillDir := filepath.Join(dir, name)
			if !isRegularFile(filepath.Join(skillDir, "SKILL.md")) {
				continue
			}
			installName := filepath.Base(skillDir)
			if !isSafeSkillDirName(installName) || strings.HasPrefix(installName, ".") {
				continue
			}
			key := strings.ToLower(installName)
			if seen[key] {
				continue
			}
			seen[key] = true
			found = append(found, skillDir)
		}
	}

	if len(found) == 0 {
		all, _ := findSkillDirsRecursive(searchRoot, 0, 5)
		for _, d := range all {
			installName := filepath.Base(d)
			if !isSafeSkillDirName(installName) || strings.HasPrefix(installName, ".") {
				continue
			}
			key := strings.ToLower(installName)
			if seen[key] {
				continue
			}
			seen[key] = true
			found = append(found, d)
		}
	}

	return materializeDiscoveredSkills(root, found)
}

func prioritySkillSearchDirs(searchRoot string) []string {
	// Mirrors vercel-labs/skills discoverSkills priority dirs.
	return []string{
		searchRoot,
		filepath.Join(searchRoot, "skills"),
		filepath.Join(searchRoot, "skills", ".curated"),
		filepath.Join(searchRoot, "skills", ".experimental"),
		filepath.Join(searchRoot, "skills", ".system"),
		filepath.Join(searchRoot, ".agent", "skills"),
		filepath.Join(searchRoot, ".agents", "skills"),
		filepath.Join(searchRoot, ".claude", "skills"),
		filepath.Join(searchRoot, ".cline", "skills"),
		filepath.Join(searchRoot, ".codebuddy", "skills"),
		filepath.Join(searchRoot, ".codex", "skills"),
		filepath.Join(searchRoot, ".commandcode", "skills"),
		filepath.Join(searchRoot, ".continue", "skills"),
		filepath.Join(searchRoot, ".cursor", "skills"),
		filepath.Join(searchRoot, ".github", "skills"),
		filepath.Join(searchRoot, ".goose", "skills"),
		filepath.Join(searchRoot, ".junie", "skills"),
		filepath.Join(searchRoot, ".kilocode", "skills"),
		filepath.Join(searchRoot, ".kiro", "skills"),
		filepath.Join(searchRoot, ".mux", "skills"),
		filepath.Join(searchRoot, ".neovate", "skills"),
		filepath.Join(searchRoot, ".opencode", "skills"),
		filepath.Join(searchRoot, ".openhands", "skills"),
		filepath.Join(searchRoot, ".pi", "skills"),
		filepath.Join(searchRoot, ".qoder", "skills"),
		filepath.Join(searchRoot, ".roo", "skills"),
		filepath.Join(searchRoot, ".trae", "skills"),
		filepath.Join(searchRoot, ".windsurf", "skills"),
		filepath.Join(searchRoot, ".zencoder", "skills"),
	}
}

var skillSkipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	"dist":         true,
	"build":        true,
	"__pycache__":  true,
}

func findSkillDirsRecursive(dir string, depth, maxDepth int) ([]string, error) {
	if depth > maxDepth {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}

	var out []string
	if isRegularFile(filepath.Join(dir, "SKILL.md")) {
		out = append(out, dir)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if skillSkipDirs[name] {
			continue
		}
		sub := filepath.Join(dir, name)
		subOut, _ := findSkillDirsRecursive(sub, depth+1, maxDepth)
		out = append(out, subOut...)
	}
	return out, nil
}

func materializeDiscoveredSkills(root string, dirs []string) ([]discoveredSkill, error) {
	out := make([]discoveredSkill, 0, len(dirs))
	for _, d := range dirs {
		rel, err := filepath.Rel(root, d)
		if err != nil {
			rel = d
		}
		installName := filepath.Base(d)
		if !isSafeSkillDirName(installName) || strings.HasPrefix(installName, ".") || installName == "__MACOSX" {
			continue
		}
		meta := readSkillMDMeta(filepath.Join(d, "SKILL.md"))
		out = append(out, discoveredSkill{
			InstallName: installName,
			AbsPath:     d,
			RelPath:     rel,
			Name:        meta.Name,
			Description: meta.Description,
		})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].InstallName) < strings.ToLower(out[j].InstallName) })
	return out, nil
}
