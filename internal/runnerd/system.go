package runnerd

import (
	"net/http"
	"path/filepath"
)

func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	vmState := s.getVMState(r)

	s.mu.Lock()
	servicesRunning := 0
	if vmState == "running" {
		for _, svc := range s.services {
			if svc.State == "running" {
				servicesRunning++
			}
		}
	}
	restartRequired := s.vmRestartRequired
	s.mu.Unlock()

	writeJSON(w, 200, map[string]any{
		"version":      "0.2.4",
		"protocols":    map[string]any{"asmp": protocolVersionASMP, "asp": protocolVersionASP},
		"capabilities": s.protocolCapabilities(),
		"vm": map[string]any{
			"state":               vmState,
			"restart_required":    restartRequired,
			"backend":             "lima",
			"guest_runnerd_port":  s.cfg.GuestRunnerPort,
			"guest_forward_port":  s.cfg.GuestForwardPort,
			"host_tunnel_vsock":   s.cfg.VsockTunnelPort,
			"lima_instance_name":  s.cfg.LimaInstanceName,
			"lima_home_directory": s.cfg.LimaHome,
		},
		"services_running": servicesRunning,
	})
}

func (s *Server) handleSystemPaths(w http.ResponseWriter, r *http.Request) {
	limaInstanceDir := ""
	if s.cfg.LimaHome != "" && s.cfg.LimaInstanceName != "" {
		limaInstanceDir = s.limaInstanceDir()
	}
	writeJSON(w, 200, map[string]any{
		"default_temp_dir":  s.cfg.Paths.DefaultSharedTmpDir,
		"runnerd_log":       filepath.Join(s.cfg.Paths.LogsDir, "runnerd.log"),
		"lima_home_dir":     s.cfg.LimaHome,
		"lima_instance_dir": limaInstanceDir,
	})
}

func (s *Server) getVMState(r *http.Request) string {
	state, err := s.limaInstanceState(r.Context())
	if err != nil {
		return "unknown"
	}
	return state
}
