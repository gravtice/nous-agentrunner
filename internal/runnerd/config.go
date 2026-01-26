package runnerd

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gravtice/nous-agent-runner/internal/envfile"
	"github.com/gravtice/nous-agent-runner/internal/platformpaths"
	"howett.net/plist"
)

type Config struct {
	InstanceID   string
	ListenAddr   string // 127.0.0.1
	ListenPort   int
	RegistryBase string

	LimaInstanceName  string
	LimaHome          string
	LimactlPath       string
	LimaTemplatesPath string
	LimaBaseTemplate  string
	HTTPProxy         string
	HTTPSProxy        string
	NoProxy           string
	GuestBinaryPath   string

	GuestRunnerPort int // guest-runnerd inside VM

	MaxInlineBytes int64

	Token string

	Paths platformpaths.Paths

	VMCPU       int
	VMMemoryMiB int
}

type instanceConfig struct {
	InstanceID string `json:"instance_id"`
}

func loadInstanceID() string {
	// Zero-parameter: read config file near executable or Resources.
	exe, err := os.Executable()
	if err == nil {
		for _, cand := range []string{
			filepath.Join(filepath.Dir(exe), "NousAgentRunnerConfig.json"),
			filepath.Clean(filepath.Join(filepath.Dir(exe), "..", "Resources", "NousAgentRunnerConfig.json")),
		} {
			b, err := os.ReadFile(cand)
			if err != nil {
				continue
			}
			if instanceID := loadInstanceIDFromConfigJSON(b); instanceID != "" {
				return instanceID
			}
		}

		// If no explicit config is bundled, derive a stable instance ID from the host app bundle identifier.
		if bundleID := loadBundleIdentifierNearExecutable(exe); bundleID != "" {
			if instanceID := deriveInstanceIDFromBundleID(bundleID); instanceID != "" {
				return instanceID
			}
		}
	}
	return "default"
}

func loadInstanceIDFromConfigJSON(b []byte) string {
	var cfg instanceConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return ""
	}
	instanceID := strings.TrimSpace(cfg.InstanceID)
	if !isSafeInstanceID(instanceID) {
		return ""
	}
	return instanceID
}

func loadBundleIdentifierNearExecutable(exe string) string {
	infoPlist := findInfoPlistNearExecutable(exe)
	if infoPlist == "" {
		return ""
	}
	b, err := os.ReadFile(infoPlist)
	if err != nil {
		return ""
	}
	var m map[string]any
	if _, err := plist.Unmarshal(b, &m); err != nil {
		return ""
	}
	bundleID, _ := m["CFBundleIdentifier"].(string)
	return strings.TrimSpace(bundleID)
}

func findInfoPlistNearExecutable(exe string) string {
	dir := filepath.Dir(exe)
	for i := 0; i < 10; i++ {
		if filepath.Base(dir) == "Contents" {
			cand := filepath.Join(dir, "Info.plist")
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				return cand
			}
		}
		if strings.HasSuffix(strings.ToLower(filepath.Base(dir)), ".app") {
			cand := filepath.Join(dir, "Contents", "Info.plist")
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				return cand
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func deriveInstanceIDFromBundleID(bundleID string) string {
	bundleID = strings.ToLower(strings.TrimSpace(bundleID))
	if bundleID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(bundleID))
	return hex.EncodeToString(sum[:])[:12]
}

func isSafeInstanceID(s string) bool {
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

func LoadConfig() (Config, error) {
	instanceID := loadInstanceID()
	paths, err := platformpaths.Resolve(instanceID)
	if err != nil {
		return Config{}, err
	}
	if err := platformpaths.EnsureDirs(paths); err != nil {
		return Config{}, err
	}

	loadedEnvPath, env, err := envfile.LoadFirst([]string{
		filepath.Join(paths.AppSupportDir, ".env.local"),
		filepath.Join(paths.AppSupportDir, ".env.production"),
		filepath.Join(paths.AppSupportDir, ".env.development"),
		filepath.Join(paths.AppSupportDir, ".env.test"),
	})
	if err != nil {
		return Config{}, fmt.Errorf("load env: %w", err)
	}

	_ = loadedEnvPath

	port := mustParseInt(env["NOUS_AGENT_RUNNER_PORT"], 0)
	if port == 0 {
		port, err = pickFreeLocalPort()
		if err != nil {
			return Config{}, err
		}
		if err := persistEnv(filepath.Join(paths.AppSupportDir, ".env.local"), env, map[string]string{
			"NOUS_AGENT_RUNNER_PORT": strconv.Itoa(port),
		}); err != nil {
			return Config{}, err
		}
	}

	registryBase := strings.TrimSpace(env["NOUS_AGENT_RUNNER_REGISTRY_BASE"])
	if registryBase == "" {
		// Official registry base (single source of truth).
		// Docker Hub canonical prefix: docker.io/<namespace>/
		registryBase = "docker.io/gravtice/"
	}
	// Backward-compatible migration: early versions used registry.nous.ai as default.
	// If the user still has that value in their persisted .env.local, upgrade it to Docker Hub.
	legacyBase := strings.TrimSuffix(registryBase, "/")
	if legacyBase == "registry.nous.ai" || legacyBase == "registry.nous.ai/gravtice" {
		registryBase = "docker.io/gravtice/"
		if err := persistEnv(filepath.Join(paths.AppSupportDir, ".env.local"), env, map[string]string{
			"NOUS_AGENT_RUNNER_REGISTRY_BASE": registryBase,
		}); err != nil {
			return Config{}, err
		}
	}
	if !strings.HasSuffix(registryBase, "/") {
		registryBase += "/"
	}

	limactlPath := env["NOUS_AGENT_RUNNER_LIMACTL_PATH"]
	if limactlPath == "" {
		limactlPath = findBundledTool("limactl")
		if limactlPath == "" {
			limactlPath = "limactl"
		}
	}

	limaTemplatesPath := strings.TrimSpace(env["NOUS_AGENT_RUNNER_LIMA_TEMPLATES_PATH"])
	if limaTemplatesPath == "" {
		limaTemplatesPath = findBundledDir("lima-templates")
	}

	limaBaseTemplate := strings.TrimSpace(env["NOUS_AGENT_RUNNER_LIMA_BASE_TEMPLATE"])
	if limaBaseTemplate == "" {
		// Debian is a stable default and avoids Ubuntu cloud image endpoints that may be blocked in some networks.
		limaBaseTemplate = "debian-12"
	}
	if !isSafeLimaTemplateName(limaBaseTemplate) {
		return Config{}, fmt.Errorf("invalid NOUS_AGENT_RUNNER_LIMA_BASE_TEMPLATE %q", limaBaseTemplate)
	}

	httpProxy := strings.TrimSpace(env["NOUS_AGENT_RUNNER_HTTP_PROXY"])
	httpsProxy := strings.TrimSpace(env["NOUS_AGENT_RUNNER_HTTPS_PROXY"])
	noProxy := strings.TrimSpace(env["NOUS_AGENT_RUNNER_NO_PROXY"])

	guestBinaryPath := strings.TrimSpace(env["NOUS_AGENT_RUNNER_GUEST_BINARY_PATH"])
	if guestBinaryPath == "" {
		exe, err := os.Executable()
		if err == nil {
			guestBinaryPath = filepath.Clean(filepath.Join(filepath.Dir(exe), "..", "Resources", "nous-guest-runnerd"))
		}
	}

	maxInlineBytes := int64(mustParseInt(env["NOUS_AGENT_RUNNER_MAX_INLINE_BYTES"], 0))
	if maxInlineBytes == 0 {
		maxInlineBytes = 8 * 1024 * 1024
	}

	guestPort := mustParseInt(env["NOUS_GUEST_RUNNERD_PORT"], 0)
	if guestPort == 0 {
		guestPort = 17777
	}

	token, err := loadOrCreateToken(paths.AppSupportDir)
	if err != nil {
		return Config{}, err
	}

	// Lima uses UNIX domain sockets under $LIMA_HOME/<instance>/, which must be short enough
	// for UNIX_PATH_MAX (~104 bytes). Keep LIMA_HOME shared across instances to avoid repeating
	// <instance_id> in the socket path.
	limaHome := filepath.Join(filepath.Dir(paths.CachesDir), "lima")
	limaInstanceName := "nous-" + instanceID
	if err := os.MkdirAll(limaHome, 0o700); err != nil {
		return Config{}, err
	}

	vmCPU := mustParseInt(env["NOUS_AGENT_RUNNER_VM_CPU_CORES"], 0)
	vmMemMiB := mustParseInt(env["NOUS_AGENT_RUNNER_VM_MEMORY_MB"], 0)
	if vmMemMiB == 0 {
		vmMemMiB = 4096
	}

	return Config{
		InstanceID:        instanceID,
		ListenAddr:        "127.0.0.1",
		ListenPort:        port,
		RegistryBase:      registryBase,
		LimaInstanceName:  limaInstanceName,
		LimaHome:          limaHome,
		LimactlPath:       limactlPath,
		LimaTemplatesPath: limaTemplatesPath,
		LimaBaseTemplate:  limaBaseTemplate,
		HTTPProxy:         httpProxy,
		HTTPSProxy:        httpsProxy,
		NoProxy:           noProxy,
		GuestBinaryPath:   guestBinaryPath,
		GuestRunnerPort:   guestPort,
		MaxInlineBytes:    maxInlineBytes,
		Token:             token,
		Paths:             paths,
		VMCPU:             vmCPU,
		VMMemoryMiB:       vmMemMiB,
	}, nil
}

func findBundledTool(name string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	candidates := []string{
		filepath.Join(filepath.Dir(exe), name),
		filepath.Clean(filepath.Join(filepath.Dir(exe), "..", "Resources", name)),
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

func findBundledDir(name string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	candidates := []string{
		filepath.Join(filepath.Dir(exe), name),
		filepath.Clean(filepath.Join(filepath.Dir(exe), "..", "Resources", name)),
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}

func isSafeLimaTemplateName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '/':
		default:
			return false
		}
	}
	return true
}

func mustParseInt(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func pickFreeLocalPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected addr type %T", l.Addr())
	}
	return addr.Port, nil
}

func persistEnv(path string, current map[string]string, set map[string]string) error {
	for k, v := range set {
		current[k] = v
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	keys := make([]string, 0, len(current))
	for k := range current {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := current[k]
		if _, err := fmt.Fprintf(f, "%s=%s\n", k, v); err != nil {
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadOrCreateToken(appSupportDir string) (string, error) {
	path := filepath.Join(appSupportDir, "token")
	b, err := os.ReadFile(path)
	if err == nil {
		s := strings.TrimSpace(string(b))
		if s != "" {
			return s, nil
		}
	}
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf[:])
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", err
	}
	return token, nil
}
