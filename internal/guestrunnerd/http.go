package guestrunnerd

import (
	"encoding/json"
	"net/http"
)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /internal/images/pull", s.handleImagePull)
	mux.HandleFunc("POST /internal/images/import", s.handleImageImport)
	mux.HandleFunc("GET /internal/images", s.handleImagesList)

	mux.HandleFunc("POST /internal/services", s.handleServiceCreate)
	mux.HandleFunc("GET /internal/services", s.handleServicesList)
	mux.HandleFunc("GET /internal/services/{service_id}", s.handleServiceGet)
	mux.HandleFunc("DELETE /internal/services/{service_id}", s.handleServiceDelete)
	mux.HandleFunc("POST /internal/services/{service_id}/start", s.handleServiceStart)
	mux.HandleFunc("POST /internal/services/{service_id}/stop", s.handleServiceStop)
	mux.HandleFunc("POST /internal/services/{service_id}/snapshot", s.handleServiceSnapshot)

	mux.HandleFunc("GET /internal/services/{service_id}/chat", s.handleServiceChatWS)

	mux.HandleFunc("GET /internal/ports/free", s.handleFreePort)

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
