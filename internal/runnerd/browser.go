package runnerd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const agentBrowserVersion = "0.8.2"
const agentBrowserGuestRoot = "/var/lib/nous/agent-browser"
const agentBrowserGuestSockets = "/var/run/nous/agent-browser/sockets"
const agentBrowserPlaywrightBrowsers = "/var/lib/nous/playwright-browsers"

// Pin a Node.js version known to work with playwright-core used by agent-browser.
const agentBrowserNodeVersion = "22.22.0"
const agentBrowserGuestNodeDir = "/var/lib/nous/node"

type browserEnsureRuntimeRequest struct {
	Version string `json:"version"`
}

func (s *Server) handleBrowserRuntimeEnsure(w http.ResponseWriter, r *http.Request) {
	var req browserEnsureRuntimeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid json", nil)
		return
	}
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = agentBrowserVersion
	}
	if version != agentBrowserVersion {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "unsupported agent-browser version", map[string]any{"version": version})
		return
	}

	if _, err := s.ensureGuestReady(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}

	if err := s.ensureAgentBrowserRuntimeInGuest(r.Context(), version); err != nil {
		writeError(w, http.StatusInternalServerError, "BROWSER_RUNTIME_UNAVAILABLE", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": version})
}

type browserCommandRequest struct {
	Args       []string `json:"args"`
	StreamPort int      `json:"stream_port"`
}

func (s *Server) handleBrowserCommand(w http.ResponseWriter, r *http.Request) {
	session := strings.TrimSpace(r.PathValue("session"))
	if session == "" || !isSafeToken(session) {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid session", nil)
		return
	}

	var req browserCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid json", nil)
		return
	}
	if len(req.Args) == 0 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "args required", nil)
		return
	}
	if req.StreamPort < 0 || req.StreamPort > 65535 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "stream_port must be 0..65535", nil)
		return
	}

	cmdName := strings.TrimSpace(req.Args[0])
	if !isAllowedAgentBrowserCommand(cmdName) {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "command not allowed", map[string]any{"command": cmdName})
		return
	}

	if _, err := s.ensureGuestReady(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "GUEST_UNAVAILABLE", err.Error(), nil)
		return
	}
	if err := s.ensureAgentBrowserRuntimeInGuest(r.Context(), agentBrowserVersion); err != nil {
		writeError(w, http.StatusInternalServerError, "BROWSER_RUNTIME_UNAVAILABLE", err.Error(), nil)
		return
	}

	out, err := s.runAgentBrowserInGuest(r.Context(), session, req.Args, req.StreamPort)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "BROWSER_ERROR", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func isAllowedAgentBrowserCommand(cmd string) bool {
	switch cmd {
	case "open", "snapshot", "click", "fill", "press", "eval", "screenshot", "close", "get", "back", "forward", "reload":
		return true
	default:
		return false
	}
}

func isSafeToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
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

func bashQuote(s string) string {
	if s == "" {
		return "''"
	}
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('\'')
	for _, r := range s {
		if r == '\'' {
			b.WriteString(`'"'"'`)
			continue
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

func (s *Server) ensureAgentBrowserRuntimeInGuest(ctx context.Context, version string) error {
	root := fmt.Sprintf("%s/v%s", agentBrowserGuestRoot, version)
	ready := root + "/READY"
	readyCore := root + "/READY_CORE"

	nodeDir := fmt.Sprintf("%s/v%s", agentBrowserGuestNodeDir, agentBrowserNodeVersion)
	nodeBin := nodeDir + "/bin/node"

	packPath := ""
	if assets, err := s.prepareOfflineAssets(); err == nil && assets != nil {
		packPath = strings.TrimSpace(assets.BrowserRuntimePack)
	}

	script := strings.Join([]string{
		"set -euo pipefail",
		fmt.Sprintf("ROOT=%s", bashQuote(root)),
		fmt.Sprintf("READY=%s", bashQuote(ready)),
		fmt.Sprintf("READY_CORE=%s", bashQuote(readyCore)),
		fmt.Sprintf("NODE_DIR=%s", bashQuote(nodeDir)),
		fmt.Sprintf("NODE_BIN=%s", bashQuote(nodeBin)),
		fmt.Sprintf("SOCKET_DIR=%s", bashQuote(agentBrowserGuestSockets)),
		fmt.Sprintf("BROWSERS_DIR=%s", bashQuote(agentBrowserPlaywrightBrowsers)),
		fmt.Sprintf("RUNTIME_PACK=%s", bashQuote(packPath)),
		`if [ -f "$READY" ]; then exit 0; fi`,
		`user="$(id -un)"`,
		`group="$(id -gn)"`,
		`sudo -n mkdir -p "$ROOT" "$SOCKET_DIR" "$BROWSERS_DIR"`,
		`sudo -n chown -R "$user:$group" "$ROOT" "$SOCKET_DIR" "$BROWSERS_DIR"`,
		`sudo -n mkdir -p "$NODE_DIR"`,
		`sudo -n chown -R "$user:$group" "$NODE_DIR"`,
		// Offline runtime pack shortcut (optional).
		`if [ -n "$RUNTIME_PACK" ] && [ -f "$RUNTIME_PACK" ]; then`,
		// System deps for Chromium (still needed even with offline pack).
		`  if command -v apt-get >/dev/null 2>&1; then`,
		`    if apt-cache show libasound2t64 >/dev/null 2>&1; then asound=libasound2t64; else asound=libasound2; fi`,
		`    sudo -n apt-get update`,
		`    sudo -n apt-get install -y --no-install-recommends ` +
			`libxcb-shm0 libx11-xcb1 libx11-6 libxcb1 libxext6 libxrandr2 libxcomposite1 libxcursor1 ` +
			`libxdamage1 libxfixes3 libxi6 libgtk-3-0 libpangocairo-1.0-0 libpango-1.0-0 libatk1.0-0 ` +
			`libcairo-gobject2 libcairo2 libgdk-pixbuf-2.0-0 libxrender1 "$asound" libfreetype6 ` +
			`libfontconfig1 libdbus-1-3 libnss3 libnspr4 libatk-bridge2.0-0 libdrm2 libxkbcommon0 ` +
			`libatspi2.0-0 libcups2 libxshmfence1 libgbm1`,
		`  else`,
		`    echo "unsupported guest: apt-get not found" >&2; exit 1`,
		`  fi`,
		`  tmpdir="$(mktemp -d)"`,
		`  tar -xzf "$RUNTIME_PACK" -C "$tmpdir"`,
		`  stage="$tmpdir/agent-browser"`,
		`  test -d "$stage"`,
		`  test -f "$stage/node_modules/agent-browser/dist/daemon.js"`,
		`  touch "$stage/READY_CORE"`,
		`  touch "$stage/READY"`,
		`  if [ -d "$tmpdir/node" ]; then`,
		`    test -x "$tmpdir/node/bin/node"`,
		`    sudo -n rm -rf "$NODE_DIR.tmp"`,
		`    sudo -n mkdir -p "$NODE_DIR.tmp"`,
		`    sudo -n chown -R "$user:$group" "$NODE_DIR.tmp"`,
		`    cp -a "$tmpdir/node/." "$NODE_DIR.tmp/"`,
		`    sudo -n rm -rf "$NODE_DIR"`,
		`    sudo -n mv "$NODE_DIR.tmp" "$NODE_DIR"`,
		`  fi`,
		`  if [ -d "$tmpdir/playwright-browsers" ]; then`,
		`    sudo -n rm -rf "$BROWSERS_DIR.tmp"`,
		`    sudo -n mkdir -p "$BROWSERS_DIR.tmp"`,
		`    sudo -n chown -R "$user:$group" "$BROWSERS_DIR.tmp"`,
		`    cp -a "$tmpdir/playwright-browsers/." "$BROWSERS_DIR.tmp/"`,
		`    sudo -n rm -rf "$BROWSERS_DIR"`,
		`    sudo -n mv "$BROWSERS_DIR.tmp" "$BROWSERS_DIR"`,
		`  fi`,
		`  sudo -n rm -rf "$ROOT"`,
		`  sudo -n mv "$stage" "$ROOT"`,
		`  sudo -n chown -R "$user:$group" "$ROOT"`,
		`  rm -rf "$tmpdir"`,
		`  exit 0`,
		`fi`,
		`if [ ! -x "$NODE_BIN" ]; then`,
		`  if ! command -v curl >/dev/null 2>&1; then sudo -n apt-get update && sudo -n apt-get install -y curl ca-certificates; fi`,
		`  if ! command -v xz >/dev/null 2>&1; then sudo -n apt-get update && sudo -n apt-get install -y xz-utils; fi`,
		`  arch="$(uname -m)"`,
		`  case "$arch" in`,
		`    aarch64|arm64) node_arch="arm64" ;;`,
		`    x86_64|amd64) node_arch="x64" ;;`,
		`    *) echo "unsupported arch: $arch" >&2; exit 1 ;;`,
		`  esac`,
		fmt.Sprintf(`  node_ver=%s`, bashQuote("v"+agentBrowserNodeVersion)),
		`  tb="node-${node_ver}-linux-${node_arch}.tar.xz"`,
		`  url="https://nodejs.org/dist/${node_ver}/${tb}"`,
		`  tmpdir="$(mktemp -d)"`,
		`  curl -fsSL "$url" -o "$tmpdir/node.tar.xz"`,
		`  tar -xJf "$tmpdir/node.tar.xz" -C "$tmpdir"`,
		`  extracted="$tmpdir/node-${node_ver}-linux-${node_arch}"`,
		`  test -d "$extracted"`,
		`  sudo -n rm -rf "$NODE_DIR.tmp"`,
		`  sudo -n mkdir -p "$NODE_DIR.tmp"`,
		`  sudo -n chown -R "$user:$group" "$NODE_DIR.tmp"`,
		`  cp -a "$extracted/." "$NODE_DIR.tmp/"`,
		`  sudo -n mv "$NODE_DIR.tmp" "$NODE_DIR"`,
		`  rm -rf "$tmpdir"`,
		`fi`,
		`export PATH="$NODE_DIR/bin:$PATH"`,
		`if ! command -v node >/dev/null 2>&1; then echo "node not available" >&2; exit 1; fi`,
		`if ! command -v npm >/dev/null 2>&1; then echo "npm not available" >&2; exit 1; fi`,
		`if ! command -v npx >/dev/null 2>&1; then echo "npx not available" >&2; exit 1; fi`,
		// Install agent-browser core into a staging dir then atomically swap (so retries don't redo npm install).
		`if [ ! -f "$READY_CORE" ]; then`,
		// Install minimal system deps for Chromium on Debian/Ubuntu (once).
		`  if command -v apt-get >/dev/null 2>&1; then`,
		`    if apt-cache show libasound2t64 >/dev/null 2>&1; then asound=libasound2t64; else asound=libasound2; fi`,
		`    sudo -n apt-get update`,
		`    sudo -n apt-get install -y --no-install-recommends ` +
			`libxcb-shm0 libx11-xcb1 libx11-6 libxcb1 libxext6 libxrandr2 libxcomposite1 libxcursor1 ` +
			`libxdamage1 libxfixes3 libxi6 libgtk-3-0 libpangocairo-1.0-0 libpango-1.0-0 libatk1.0-0 ` +
			`libcairo-gobject2 libcairo2 libgdk-pixbuf-2.0-0 libxrender1 "$asound" libfreetype6 ` +
			`libfontconfig1 libdbus-1-3 libnss3 libnspr4 libatk-bridge2.0-0 libdrm2 libxkbcommon0 ` +
			`libatspi2.0-0 libcups2 libxshmfence1 libgbm1`,
		`  else`,
		`    echo "unsupported guest: apt-get not found" >&2; exit 1`,
		`  fi`,
		`  stage="$(mktemp -d)"`,
		`  cd "$stage"`,
		fmt.Sprintf("  npm init -y >/dev/null 2>&1 || true"),
		fmt.Sprintf("  npm install --omit=dev agent-browser@%s", bashQuote(agentBrowserVersion)),
		fmt.Sprintf("  test -f %s", bashQuote("node_modules/agent-browser/dist/daemon.js")),
		`  touch "$stage/READY_CORE"`,
		`  cd /`,
		`  if [ -d "$ROOT" ] && [ -f "$READY_CORE" ]; then`,
		`    rm -rf "$stage"`,
		`  else`,
		`    sudo -n rm -rf "$ROOT"`,
		`    sudo -n mv "$stage" "$ROOT"`,
		`    sudo -n chown -R "$user:$group" "$ROOT"`,
		`  fi`,
		`fi`,
		// Install Chromium (Playwright) separately so a failed download can be retried without reinstalling npm deps.
		`if [ ! -f "$READY" ]; then`,
		fmt.Sprintf("  export PLAYWRIGHT_BROWSERS_PATH=%s", bashQuote(agentBrowserPlaywrightBrowsers)),
		`  cd "$ROOT"`,
		`  "./node_modules/.bin/agent-browser" install`,
		`  touch "$READY"`,
		`fi`,
	}, "\n")

	_, err := s.runInGuestOutput(ctx, script)
	return err
}

func (s *Server) runAgentBrowserInGuest(ctx context.Context, session string, args []string, streamPort int) (map[string]any, error) {
	root := fmt.Sprintf("%s/v%s", agentBrowserGuestRoot, agentBrowserVersion)
	nodeDir := fmt.Sprintf("%s/v%s", agentBrowserGuestNodeDir, agentBrowserNodeVersion)

	quotedArgs := make([]string, 0, len(args)+4)
	for _, a := range args {
		quotedArgs = append(quotedArgs, bashQuote(a))
	}

	exe := root + "/node_modules/.bin/agent-browser"
	cmd := strings.Join([]string{
		"set -euo pipefail",
		fmt.Sprintf("cd %s", bashQuote(root)),
		fmt.Sprintf("export PATH=%s\"$PATH\"", bashQuote(nodeDir+"/bin:")),
		fmt.Sprintf("export PLAYWRIGHT_BROWSERS_PATH=%s", bashQuote(agentBrowserPlaywrightBrowsers)),
		fmt.Sprintf("export AGENT_BROWSER_SOCKET_DIR=%s", bashQuote(agentBrowserGuestSockets)),
		fmt.Sprintf("export AGENT_BROWSER_STREAM_PORT=%s", bashQuote(strconv.Itoa(streamPort))),
		fmt.Sprintf("%s %s --session %s --json", bashQuote(exe), strings.Join(quotedArgs, " "), bashQuote(session)),
	}, "\n")

	out, err := s.runInGuestOutput(ctx, cmd)
	if err != nil {
		return nil, err
	}

	raw := strings.TrimSpace(out)
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("invalid agent-browser json: %w", err)
	}
	return v, nil
}
