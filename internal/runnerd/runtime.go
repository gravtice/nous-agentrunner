package runnerd

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type runtimeInfo struct {
	Version    string `json:"version"`
	InstanceID string `json:"instance_id"`
	Pid        int    `json:"pid"`
	ListenAddr string `json:"listen_addr"`
	ListenPort int    `json:"listen_port"`
	StartedAt  string `json:"started_at"`
}

func writeRuntimeFile(cfg Config) {
	info := runtimeInfo{
		Version:    "0.2.4",
		InstanceID: cfg.InstanceID,
		Pid:        os.Getpid(),
		ListenAddr: cfg.ListenAddr,
		ListenPort: cfg.ListenPort,
		StartedAt:  nowISO8601(),
	}
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(cfg.Paths.AppSupportDir, "runtime.json")
	_ = os.WriteFile(path, append(b, '\n'), 0o600)
}
