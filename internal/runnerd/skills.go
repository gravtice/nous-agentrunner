package runnerd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type skillListItem struct {
	Name       string           `json:"name"`
	HasSkillMD bool             `json:"has_skill_md"`
	Source     *skillSourceInfo `json:"source,omitempty"`
}

type skillSourceInfo struct {
	Source      string `json:"source"`
	URL         string `json:"url,omitempty"`
	Ref         string `json:"ref,omitempty"`
	Subpath     string `json:"subpath,omitempty"`
	Commit      string `json:"commit,omitempty"`
	SkillPath   string `json:"skill_path,omitempty"`
	InstalledAt string `json:"installed_at,omitempty"`
}

type skillsInstallRequest struct {
	Source  string   `json:"source"`
	Ref     string   `json:"ref,omitempty"`
	Subpath string   `json:"subpath,omitempty"`
	Skills  []string `json:"skills,omitempty"`
	Replace bool     `json:"replace,omitempty"`
}

type skillsDiscoverRequest struct {
	Source  string `json:"source"`
	Ref     string `json:"ref,omitempty"`
	Subpath string `json:"subpath,omitempty"`
}

type skillDiscoveredItem struct {
	InstallName string `json:"install_name"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	SkillPath   string `json:"skill_path,omitempty"`
}

func (s *Server) skillsDir() string {
	return filepath.Join(s.cfg.Paths.AppSupportDir, "skills")
}

func (s *Server) handleSkillsList(w http.ResponseWriter, r *http.Request) {
	skillsDir := s.skillsDir()
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create skills directory", map[string]any{"error": err.Error()})
		return
	}

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list skills directory", map[string]any{"error": err.Error()})
		return
	}

	out := make([]skillListItem, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := strings.TrimSpace(e.Name())
		if name == "" || strings.HasPrefix(name, ".") || name == "__MACOSX" {
			continue
		}
		if !isSafeSkillDirName(name) {
			continue
		}
		dir := filepath.Join(skillsDir, name)
		hasSkillMD := isRegularFile(filepath.Join(dir, "SKILL.md"))
		info := skillListItem{Name: name, HasSkillMD: hasSkillMD}
		if src, ok := readSkillSourceInfo(filepath.Join(dir, ".nous-source.json")); ok {
			info.Source = &src
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	writeJSON(w, 200, map[string]any{"skills": out})
}

func (s *Server) handleSkillsDiscover(w http.ResponseWriter, r *http.Request) {
	var req skillsDiscoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid json", nil)
		return
	}
	req.Source = strings.TrimSpace(req.Source)
	req.Ref = strings.TrimSpace(req.Ref)
	req.Subpath = strings.TrimSpace(req.Subpath)
	if req.Source == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "source is required", nil)
		return
	}

	skills, commit, err := s.discoverSkills(r.Context(), req)
	if err != nil {
		var httpErr *skillHTTPError
		if errors.As(err, &httpErr) {
			writeError(w, httpErr.Status, httpErr.Code, httpErr.Message, httpErr.Details)
			return
		}
		writeError(w, http.StatusInternalServerError, "SKILL_DISCOVER_FAILED", err.Error(), nil)
		return
	}

	out := make([]skillDiscoveredItem, 0, len(skills))
	for _, s := range skills {
		out = append(out, skillDiscoveredItem{
			InstallName: s.InstallName,
			Name:        s.Name,
			Description: s.Description,
			SkillPath:   s.RelPath,
		})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].InstallName) < strings.ToLower(out[j].InstallName) })
	writeJSON(w, 200, map[string]any{
		"skills": out,
		"commit": commit,
	})
}

func (s *Server) handleSkillsInstall(w http.ResponseWriter, r *http.Request) {
	var req skillsInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid json", nil)
		return
	}
	req.Source = strings.TrimSpace(req.Source)
	req.Ref = strings.TrimSpace(req.Ref)
	req.Subpath = strings.TrimSpace(req.Subpath)
	if req.Source == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "source is required", nil)
		return
	}
	for i := range req.Skills {
		req.Skills[i] = strings.TrimSpace(req.Skills[i])
	}

	installed, commit, err := s.installSkills(r.Context(), req)
	if err != nil {
		var httpErr *skillHTTPError
		if errors.As(err, &httpErr) {
			writeError(w, httpErr.Status, httpErr.Code, httpErr.Message, httpErr.Details)
			return
		}
		writeError(w, http.StatusInternalServerError, "SKILL_INSTALL_FAILED", err.Error(), nil)
		return
	}

	writeJSON(w, 200, map[string]any{
		"installed": installed,
		"commit":    commit,
	})
}

func (s *Server) handleSkillsDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("skill_name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "skill_name is required", nil)
		return
	}
	if !isSafeSkillDirName(name) || strings.HasPrefix(name, ".") || name == "__MACOSX" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid skill_name", nil)
		return
	}

	skillsDir := s.skillsDir()
	path := filepath.Join(skillsDir, name)
	if !hasPathPrefix(filepath.Clean(path), filepath.Clean(skillsDir)) {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid skill_name", nil)
		return
	}

	s.skillsMu.Lock()
	defer s.skillsMu.Unlock()

	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "skill not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to stat skill", map[string]any{"error": err.Error()})
		return
	}
	if err := os.RemoveAll(path); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete skill", map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true})
}

type skillHTTPError struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *skillHTTPError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func skillBadRequest(msg string, details any) error {
	return &skillHTTPError{Status: http.StatusBadRequest, Code: "BAD_REQUEST", Message: msg, Details: details}
}

func skillPathNotAllowed(msg string, details any) error {
	return &skillHTTPError{Status: http.StatusBadRequest, Code: "PATH_NOT_ALLOWED", Message: msg, Details: details}
}

type resolvedSkillSource struct {
	Parsed  parsedSkillSource
	RepoDir string
	Commit  string
	Cleanup func()
}

func (s *Server) resolveSkillSource(ctx context.Context, source, ref, subpath string) (resolvedSkillSource, error) {
	parsed, err := parseSkillSource(source)
	if err != nil {
		return resolvedSkillSource{}, skillBadRequest(err.Error(), nil)
	}
	if strings.TrimSpace(ref) != "" {
		parsed.Ref = strings.TrimSpace(ref)
	}
	if strings.TrimSpace(subpath) != "" {
		parsed.Subpath = strings.TrimSpace(subpath)
	}

	switch parsed.Type {
	case "local":
		if parsed.LocalPath == "" || !filepath.IsAbs(parsed.LocalPath) {
			return resolvedSkillSource{}, skillBadRequest("local source must be an absolute path", nil)
		}
		if _, _, ok := s.validateAllowedPath(parsed.LocalPath); !ok {
			return resolvedSkillSource{}, skillPathNotAllowed("local source is not under any shared directory", nil)
		}
		fi, err := os.Stat(parsed.LocalPath)
		if err != nil {
			return resolvedSkillSource{}, skillBadRequest("local source path not accessible", map[string]any{"error": err.Error()})
		}
		if !fi.IsDir() {
			return resolvedSkillSource{}, skillBadRequest("local source must be a directory", nil)
		}
		return resolvedSkillSource{
			Parsed:  parsed,
			RepoDir: parsed.LocalPath,
			Commit:  "",
			Cleanup: func() {},
		}, nil
	case "github", "gitlab", "git":
		repoDir, cleanupDir, commit, err := s.cloneSkillRepo(ctx, parsed.URL, parsed.Ref)
		if err != nil {
			return resolvedSkillSource{}, err
		}
		return resolvedSkillSource{
			Parsed:  parsed,
			RepoDir: repoDir,
			Commit:  commit,
			Cleanup: func() { _ = os.RemoveAll(cleanupDir) },
		}, nil
	default:
		return resolvedSkillSource{}, skillBadRequest("unsupported source type", map[string]any{"type": parsed.Type})
	}
}

func (s *Server) discoverSkills(ctx context.Context, req skillsDiscoverRequest) ([]discoveredSkill, string, error) {
	start := time.Now()
	log.Printf("skills.discover: start source=%q ref=%q subpath=%q", req.Source, req.Ref, req.Subpath)
	defer func() { log.Printf("skills.discover: done (%s)", time.Since(start).Truncate(time.Millisecond)) }()

	resolved, err := s.resolveSkillSource(ctx, req.Source, req.Ref, req.Subpath)
	if err != nil {
		return nil, "", err
	}
	defer resolved.Cleanup()

	discovered, err := discoverSkillDirs(resolved.RepoDir, resolved.Parsed.Subpath)
	if err != nil {
		return nil, resolved.Commit, skillBadRequest("failed to discover skills", map[string]any{"error": err.Error()})
	}
	if len(discovered) == 0 {
		return nil, resolved.Commit, &skillHTTPError{
			Status:  http.StatusNotFound,
			Code:    "SKILLS_NOT_FOUND",
			Message: "no skills found",
			Details: map[string]any{"source": req.Source, "subpath": resolved.Parsed.Subpath},
		}
	}
	if resolved.Parsed.SkillFilter != "" {
		selected, err := selectSkills(discovered, []string{resolved.Parsed.SkillFilter})
		if err != nil {
			return nil, resolved.Commit, skillBadRequest(err.Error(), map[string]any{"available": discoveredInstallNames(discovered)})
		}
		return selected, resolved.Commit, nil
	}

	return discovered, resolved.Commit, nil
}

func (s *Server) installSkills(ctx context.Context, req skillsInstallRequest) ([]string, string, error) {
	start := time.Now()
	log.Printf("skills.install: start source=%q ref=%q subpath=%q skills=%d replace=%v", req.Source, req.Ref, req.Subpath, len(req.Skills), req.Replace)
	defer func() { log.Printf("skills.install: done (%s)", time.Since(start).Truncate(time.Millisecond)) }()

	resolved, err := s.resolveSkillSource(ctx, req.Source, req.Ref, req.Subpath)
	if err != nil {
		return nil, "", err
	}
	defer resolved.Cleanup()

	wanted := compactStringList(req.Skills)
	if resolved.Parsed.SkillFilter != "" {
		wanted = append(wanted, resolved.Parsed.SkillFilter)
		wanted = compactStringList(wanted)
	}

	discovered, err := discoverSkillDirs(resolved.RepoDir, resolved.Parsed.Subpath)
	if err != nil {
		return nil, resolved.Commit, skillBadRequest("failed to discover skills", map[string]any{"error": err.Error()})
	}
	if len(discovered) == 0 {
		return nil, resolved.Commit, &skillHTTPError{
			Status:  http.StatusNotFound,
			Code:    "SKILLS_NOT_FOUND",
			Message: "no skills found",
			Details: map[string]any{"source": req.Source, "subpath": resolved.Parsed.Subpath},
		}
	}

	selected, err := selectSkills(discovered, wanted)
	if err != nil {
		return nil, resolved.Commit, skillBadRequest(err.Error(), map[string]any{"available": discoveredInstallNames(discovered)})
	}

	s.skillsMu.Lock()
	defer s.skillsMu.Unlock()

	skillsDir := s.skillsDir()
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		return nil, resolved.Commit, err
	}

	if !req.Replace {
		var conflicts []string
		for _, skill := range selected {
			dst := filepath.Join(skillsDir, skill.InstallName)
			_, err := os.Stat(dst)
			if err == nil {
				conflicts = append(conflicts, skill.InstallName)
				continue
			}
			if !errors.Is(err, os.ErrNotExist) {
				return nil, resolved.Commit, err
			}
		}
		if len(conflicts) > 0 {
			sort.Slice(conflicts, func(i, j int) bool { return strings.ToLower(conflicts[i]) < strings.ToLower(conflicts[j]) })
			return nil, resolved.Commit, &skillHTTPError{
				Status:  http.StatusConflict,
				Code:    "SKILL_EXISTS",
				Message: "skill already exists",
				Details: map[string]any{"skills": conflicts},
			}
		}
	}

	installed := make([]string, 0, len(selected))
	for _, skill := range selected {
		src := skill.AbsPath
		name := skill.InstallName
		dst := filepath.Join(skillsDir, name)
		if !hasPathPrefix(filepath.Clean(dst), filepath.Clean(skillsDir)) {
			return nil, resolved.Commit, fmt.Errorf("invalid install path for skill %q", name)
		}

		srcInfo := skillSourceInfo{
			Source:      req.Source,
			URL:         resolved.Parsed.URL,
			Ref:         resolved.Parsed.Ref,
			Subpath:     resolved.Parsed.Subpath,
			Commit:      resolved.Commit,
			SkillPath:   skill.RelPath,
			InstalledAt: nowISO8601(),
		}
		if err := installSkillDir(src, dst, srcInfo, req.Replace); err != nil {
			var httpErr *skillHTTPError
			if errors.As(err, &httpErr) {
				return nil, resolved.Commit, httpErr
			}
			return nil, resolved.Commit, err
		}
		installed = append(installed, name)
	}
	sort.Slice(installed, func(i, j int) bool { return strings.ToLower(installed[i]) < strings.ToLower(installed[j]) })
	return installed, resolved.Commit, nil
}

func compactStringList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

func isRegularFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

func readSkillSourceInfo(path string) (skillSourceInfo, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return skillSourceInfo{}, false
	}
	var out skillSourceInfo
	if err := json.Unmarshal(b, &out); err != nil {
		return skillSourceInfo{}, false
	}
	if strings.TrimSpace(out.Source) == "" {
		return skillSourceInfo{}, false
	}
	return out, true
}

func writeSkillSourceInfo(path string, info skillSourceInfo) {
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(b, '\n'), 0o600)
}

type discoveredSkill struct {
	InstallName string
	AbsPath     string
	RelPath     string
	Name        string
	Description string
}

func discoveredInstallNames(in []discoveredSkill) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, s.InstallName)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

func selectSkills(all []discoveredSkill, wanted []string) ([]discoveredSkill, error) {
	if len(wanted) == 0 {
		return all, nil
	}

	byInstallName := make(map[string]discoveredSkill, len(all))
	for _, s := range all {
		byInstallName[strings.ToLower(s.InstallName)] = s
	}

	var out []discoveredSkill
	var missing []string
	for _, w := range wanted {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		s, ok := byInstallName[strings.ToLower(w)]
		if !ok {
			missing = append(missing, w)
			continue
		}
		out = append(out, s)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("requested skills not found: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func installSkillDir(srcDir, dstDir string, info skillSourceInfo, replace bool) error {
	srcDir = filepath.Clean(srcDir)
	dstDir = filepath.Clean(dstDir)
	if srcDir == "" || dstDir == "" {
		return errors.New("invalid install paths")
	}

	if !isRegularFile(filepath.Join(srcDir, "SKILL.md")) {
		return &skillHTTPError{
			Status:  http.StatusBadRequest,
			Code:    "BAD_REQUEST",
			Message: "skill directory missing SKILL.md",
			Details: map[string]any{"path": srcDir},
		}
	}

	parent := filepath.Dir(dstDir)
	tmp, err := os.MkdirTemp(parent, ".install-skill-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := copyDirNoSymlinks(srcDir, tmp); err != nil {
		return err
	}
	writeSkillSourceInfo(filepath.Join(tmp, ".nous-source.json"), info)

	if err := finalizeSkillInstall(tmp, dstDir, replace); err != nil {
		return err
	}
	return nil
}

func finalizeSkillInstall(tmpDir, dstDir string, replace bool) error {
	_, err := os.Stat(dstDir)
	if err == nil {
		if !replace {
			return &skillHTTPError{
				Status:  http.StatusConflict,
				Code:    "SKILL_EXISTS",
				Message: "skill already exists",
				Details: map[string]any{"name": filepath.Base(dstDir)},
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	backup := dstDir + ".old"
	_ = os.RemoveAll(backup)
	if err == nil {
		if err := os.Rename(dstDir, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpDir, dstDir); err != nil {
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, dstDir)
		}
		return err
	}
	_ = os.RemoveAll(backup)
	return nil
}

func copyDirNoSymlinks(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		base := filepath.Base(path)
		if d.IsDir() && base == ".git" {
			return fs.SkipDir
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink not allowed in skill: %s", rel)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type in skill: %s", rel)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}

func isSafeSkillDirName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}
