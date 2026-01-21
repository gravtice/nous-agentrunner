package runnerd

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) handleServiceChatWS(w http.ResponseWriter, r *http.Request) {
	serviceID := strings.TrimSpace(r.PathValue("service_id"))
	if serviceID == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "service_id is required", nil)
		return
	}

	s.mu.Lock()
	_, ok := s.services[serviceID]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "service not found", nil)
		return
	}

	clientConn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer clientConn.Close()

	sessionID, err := newID("sess_", 12)
	if err != nil {
		_ = clientConn.WriteJSON(map[string]any{"type": "error", "code": "INTERNAL_ERROR", "message": "failed to allocate session"})
		return
	}
	_ = clientConn.WriteJSON(map[string]any{"type": "session.started", "session_id": sessionID, "service_id": serviceID})

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		_ = clientConn.WriteJSON(map[string]any{"type": "error", "code": "GUEST_UNAVAILABLE", "message": err.Error()})
		return
	}

	guestWSURL := strings.Replace(gc.baseURL, "http://", "ws://", 1) + "/internal/services/" + serviceID + "/chat?session_id=" + sessionID
	guestConn, _, err := websocket.DefaultDialer.DialContext(r.Context(), guestWSURL, nil)
	if err != nil {
		_ = clientConn.WriteJSON(map[string]any{"type": "error", "code": "SERVICE_UNAVAILABLE", "message": err.Error()})
		return
	}
	defer guestConn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	errCh := make(chan error, 2)

	go func() { errCh <- proxyWS(ctx, clientConn, guestConn, clientConn, s.validateClientASPMessage, nil) }()
	go func() { errCh <- proxyWS(ctx, guestConn, clientConn, nil, nil, dropSessionStarted) }()

	<-errCh
}

func proxyWS(ctx context.Context, src, dst, errDst *websocket.Conn, validate func([]byte) error, filter func([]byte) bool) error {
	for {
		_, msg, err := src.ReadMessage()
		if err != nil {
			return err
		}
		if filter != nil && filter(msg) {
			continue
		}
		if validate != nil {
			if err := validate(msg); err != nil {
				if errDst != nil {
					_ = errDst.WriteMessage(websocket.TextMessage, mustJSON(map[string]any{
						"type":    "error",
						"code":    mapErrorCode(err),
						"message": err.Error(),
					}))
				}
				return err
			}
		}
		if err := dst.WriteMessage(websocket.TextMessage, msg); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func dropSessionStarted(msg []byte) bool {
	var m aspMessage
	if json.Unmarshal(msg, &m) == nil && m.Type == "session.started" {
		return true
	}
	return false
}

func mapErrorCode(err error) string {
	if err == errPathNotAllowed {
		return "PATH_NOT_ALLOWED"
	}
	if strings.Contains(err.Error(), "INLINE_BYTES_TOO_LARGE") {
		return "INLINE_BYTES_TOO_LARGE"
	}
	return "BAD_REQUEST"
}
