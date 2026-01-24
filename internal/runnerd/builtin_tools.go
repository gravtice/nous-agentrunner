package runnerd

import (
	"net/http"
	"strings"
)

var builtinToolsByServiceType = map[string][]string{
	"claude": {
		"AskUserQuestion",
		"Bash",
		"Edit",
		"Glob",
		"Grep",
		"MultiEdit",
		"Read",
		"WebFetch",
		"Write",
	},
}

func (s *Server) handleServiceTypeBuiltinTools(w http.ResponseWriter, r *http.Request) {
	serviceType := strings.TrimSpace(r.PathValue("service_type"))
	if serviceType == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "service_type is required", nil)
		return
	}
	tools, ok := builtinToolsByServiceType[serviceType]
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "service type not found", map[string]any{"type": serviceType})
		return
	}
	writeJSON(w, 200, map[string]any{"type": serviceType, "builtin_tools": tools})
}
