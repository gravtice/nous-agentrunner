package runnerd

import (
	"net/http"
)

func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	vmState := s.getVMState(r)

	s.mu.Lock()
	servicesRunning := 0
	for _, svc := range s.services {
		if svc.State == "running" {
			servicesRunning++
		}
	}
	restartRequired := s.vmRestartRequired
	s.mu.Unlock()

	writeJSON(w, 200, map[string]any{
		"version": "0.1.0",
		"vm": map[string]any{
			"state":               vmState,
			"restart_required":    restartRequired,
			"backend":             "lima",
			"guest_runnerd_port":  s.cfg.GuestRunnerPort,
			"lima_instance_name":  s.cfg.LimaInstanceName,
			"lima_home_directory": s.cfg.LimaHome,
		},
		"services_running": servicesRunning,
	})
}

func (s *Server) handleSystemPaths(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"default_temp_dir": s.cfg.Paths.DefaultSharedTmpDir,
	})
}

func (s *Server) getVMState(r *http.Request) string {
	state, err := s.limaInstanceState(r.Context())
	if err != nil {
		return "unknown"
	}
	return state
}
