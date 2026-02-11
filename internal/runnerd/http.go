package runnerd

import (
	"encoding/json"
	"net/http"
)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// ASMP (v1: HTTP/JSON)
	mux.HandleFunc("GET /v1/system/status", s.withAuth(s.handleSystemStatus))
	mux.HandleFunc("GET /v1/system/paths", s.withAuth(s.handleSystemPaths))
	mux.HandleFunc("POST /v1/system/vm/restart", s.withAuth(s.handleVMRestart))
	mux.HandleFunc("POST /v1/system/diagnostics/guest_to_host_tunnel", s.withAuth(s.handleSystemDiagnosticsGuestToHostTunnel))

	mux.HandleFunc("GET /v1/shares", s.withAuth(s.handleSharesList))
	mux.HandleFunc("POST /v1/shares", s.withAuth(s.handleSharesAdd))
	mux.HandleFunc("DELETE /v1/shares/{share_id}", s.withAuth(s.handleSharesDelete))
	mux.HandleFunc("PUT /v1/shares/excludes", s.withAuth(s.handleSharesExcludesSet))

	mux.HandleFunc("POST /v1/images/pull", s.withAuth(s.handleImagesPull))
	mux.HandleFunc("POST /v1/images/import", s.withAuth(s.handleImagesImport))
	mux.HandleFunc("POST /v1/images/prune", s.withAuth(s.handleImagesPrune))
	mux.HandleFunc("POST /v1/images/delete", s.withAuth(s.handleImagesDelete))
	mux.HandleFunc("GET /v1/images", s.withAuth(s.handleImagesList))

	mux.HandleFunc("POST /v1/services", s.withAuth(s.handleServicesCreate))
	mux.HandleFunc("GET /v1/services", s.withAuth(s.handleServicesList))
	mux.HandleFunc("GET /v1/services/{service_id}", s.withAuth(s.handleServicesGet))
	mux.HandleFunc("DELETE /v1/services/{service_id}", s.withAuth(s.handleServicesDelete))
	mux.HandleFunc("POST /v1/services/{service_id}/start", s.withAuth(s.handleServicesStart))
	mux.HandleFunc("POST /v1/services/{service_id}/stop", s.withAuth(s.handleServicesStop))
	mux.HandleFunc("POST /v1/services/{service_id}/resume", s.withAuth(s.handleServicesResume))
	mux.HandleFunc("POST /v1/services/{service_id}/snapshot", s.withAuth(s.handleServicesSnapshot))
	mux.HandleFunc("GET /v1/services/types/{service_type}/builtin_tools", s.withAuth(s.handleServiceTypeBuiltinTools))

	mux.HandleFunc("POST /v1/tunnels", s.withAuth(s.handleTunnelsCreate))
	mux.HandleFunc("GET /v1/tunnels", s.withAuth(s.handleTunnelsList))
	mux.HandleFunc("GET /v1/tunnels/by_host_port/{host_port}", s.withAuth(s.handleTunnelsGetByHostPort))
	mux.HandleFunc("DELETE /v1/tunnels/{tunnel_id}", s.withAuth(s.handleTunnelsDelete))
	mux.HandleFunc("DELETE /v1/tunnels/by_host_port/{host_port}", s.withAuth(s.handleTunnelsDeleteByHostPort))

	mux.HandleFunc("POST /v1/forwards", s.withAuth(s.handleForwardsCreate))
	mux.HandleFunc("DELETE /v1/forwards/{forward_id}", s.withAuth(s.handleForwardsDelete))

	mux.HandleFunc("GET /v1/skills", s.withAuth(s.handleSkillsList))
	mux.HandleFunc("POST /v1/skills/discover", s.withAuth(s.handleSkillsDiscover))
	mux.HandleFunc("POST /v1/skills/install", s.withAuth(s.handleSkillsInstall))
	mux.HandleFunc("DELETE /v1/skills/{skill_name}", s.withAuth(s.handleSkillsDelete))

	// ASP (v1: WebSocket)
	mux.HandleFunc("GET /v1/services/{service_id}/chat", s.withAuth(s.handleServiceChatWS))

	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string, details any) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
	})
}
