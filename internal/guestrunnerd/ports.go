package guestrunnerd

import "net/http"

func (s *Server) handleFreePort(w http.ResponseWriter, r *http.Request) {
	port, err := pickFreePort()
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	writeJSON(w, 200, map[string]any{"port": port})
}
