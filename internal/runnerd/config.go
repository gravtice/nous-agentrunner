package runnerd

import (
	"crypto/rand"
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
)

type Config struct {
	InstanceID   string
	ListenAddr   string // 127.0.0.1
	ListenPort   int
	RegistryBase string

	LimaInstanceName string
	LimaHome         string
	LimactlPath      string
	GuestBinaryPath  string

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
			var cfg instanceConfig
			if json.Unmarshal(b, &cfg) == nil && cfg.InstanceID != "" {
				return cfg.InstanceID
			}
		}
	}
	return "default"
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

	registryBase := env["NOUS_AGENT_RUNNER_REGISTRY_BASE"]
	if registryBase == "" {
		registryBase = "registry.nous.ai/"
	}

	limactlPath := env["NOUS_AGENT_RUNNER_LIMACTL_PATH"]
	if limactlPath == "" {
		limactlPath = findBundledTool("limactl")
		if limactlPath == "" {
			limactlPath = "limactl"
		}
	}

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

	limaHome := filepath.Join(paths.AppSupportDir, "lima")
	limaInstanceName := "nous-" + instanceID

	vmCPU := mustParseInt(env["NOUS_AGENT_RUNNER_VM_CPU_CORES"], 0)
	vmMemMiB := mustParseInt(env["NOUS_AGENT_RUNNER_VM_MEMORY_MB"], 0)
	if vmMemMiB == 0 {
		vmMemMiB = 4096
	}

	return Config{
		InstanceID:       instanceID,
		ListenAddr:       "127.0.0.1",
		ListenPort:       port,
		RegistryBase:     registryBase,
		LimaInstanceName: limaInstanceName,
		LimaHome:         limaHome,
		LimactlPath:      limactlPath,
		GuestBinaryPath:  guestBinaryPath,
		GuestRunnerPort:  guestPort,
		MaxInlineBytes:   maxInlineBytes,
		Token:            token,
		Paths:            paths,
		VMCPU:            vmCPU,
		VMMemoryMiB:      vmMemMiB,
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
