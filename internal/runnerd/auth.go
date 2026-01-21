package runnerd

import (
	"net/http"
	"strings"
)

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) || strings.TrimSpace(strings.TrimPrefix(auth, prefix)) != s.cfg.Token {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid token", nil)
			return
		}
		next(w, r)
	}
}
