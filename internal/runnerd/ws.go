package runnerd

import (
	"context"
	"encoding/json"
	"log"
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
	svc, ok := s.services[serviceID]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "service not found", nil)
		return
	}
	if strings.TrimSpace(strings.ToLower(svc.State)) == "stopped" {
		writeError(w, http.StatusConflict, "SERVICE_STOPPED", "service is stopped", nil)
		return
	}

	release, ok := s.tryBeginServiceChat(serviceID)
	if !ok {
		writeError(w, http.StatusConflict, "SERVICE_BUSY", "service already has an active chat connection", nil)
		return
	}

	log.Printf("ws: client connected service_id=%s", serviceID)

	clientConn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		release()
		return
	}
	defer clientConn.Close()
	defer func() {
		s.mu.Lock()
		svc, ok := s.services[serviceID]
		if ok {
			svc.LastActivityAt = nowISO8601()
			s.services[serviceID] = svc
			_ = s.saveServicesLocked()
		}
		s.mu.Unlock()
		release()
	}()
	clientConn.SetReadLimit(s.maxClientASPMessageBytes())

	sessionID := strings.TrimSpace(svc.SessionID)
	if sessionID == "" || !isUUID(sessionID) {
		sessionID, err = newUUID()
		if err != nil {
			_ = clientConn.WriteJSON(map[string]any{"type": "error", "code": "INTERNAL_ERROR", "message": "failed to allocate session", "fatal": true})
			return
		}
		svc.SessionID = sessionID
		s.mu.Lock()
		s.services[serviceID] = svc
		_ = s.saveServicesLocked()
		s.mu.Unlock()
	}
	_ = clientConn.WriteJSON(map[string]any{
		"type":         "session.started",
		"session_id":   sessionID,
		"service_id":   serviceID,
		"asp_version":  protocolVersionASP,
		"capabilities": s.protocolCapabilityFlags(),
		"limits":       s.protocolLimits(),
	})

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		_ = clientConn.WriteJSON(map[string]any{"type": "error", "code": "GUEST_UNAVAILABLE", "message": err.Error(), "fatal": true})
		return
	}

	guestWSURL := strings.Replace(gc.baseURL, "http://", "ws://", 1) + "/internal/services/" + serviceID + "/chat?session_id=" + sessionID
	guestConn, _, err := websocket.DefaultDialer.DialContext(r.Context(), guestWSURL, nil)
	if err != nil {
		log.Printf("ws: guest dial failed service_id=%s session_id=%s err=%v", serviceID, sessionID, err)
		_ = clientConn.WriteJSON(map[string]any{"type": "error", "code": "SERVICE_UNAVAILABLE", "message": err.Error(), "fatal": true})
		return
	}
	defer guestConn.Close()

	log.Printf("ws: guest connected service_id=%s session_id=%s", serviceID, sessionID)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	errCh := make(chan error, 2)

	go func() { errCh <- proxyWS(ctx, clientConn, guestConn, clientConn, s.validateClientASPMessage, nil) }()
	go func() { errCh <- proxyWS(ctx, guestConn, clientConn, nil, nil, dropSessionStarted) }()

	err = <-errCh
	cancel()
	if err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		log.Printf("ws: proxy stopped service_id=%s session_id=%s err=%v", serviceID, sessionID, err)
	}
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
						"fatal":   false,
					}))
					if isASPInputMessage(msg) {
						_ = errDst.WriteMessage(websocket.TextMessage, mustJSON(map[string]any{"type": "done"}))
					}
				}
				continue
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
	if err == errInlineBytesTooLarge {
		return "INLINE_BYTES_TOO_LARGE"
	}
	return "BAD_REQUEST"
}

func (s *Server) tryBeginServiceChat(serviceID string) (func(), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeServiceChats == nil {
		s.activeServiceChats = make(map[string]bool)
	}
	if s.activeServiceChats[serviceID] {
		return nil, false
	}
	s.activeServiceChats[serviceID] = true
	return func() {
		s.mu.Lock()
		delete(s.activeServiceChats, serviceID)
		s.mu.Unlock()
	}, true
}

func (s *Server) maxClientASPMessageBytes() int64 {
	// One `input` message may include up to MaxInlineBytes bytes, base64 encoded.
	// Keep the limit tight; large payloads should use source.type="path".
	const overhead = 512 * 1024
	if s.cfg.MaxInlineBytes <= 0 {
		return 1 * 1024 * 1024
	}
	encoded := ((s.cfg.MaxInlineBytes + 2) / 3) * 4
	if encoded < 64*1024 {
		encoded = 64 * 1024
	}
	return encoded + overhead
}

func isASPInputMessage(msg []byte) bool {
	var m aspMessage
	if json.Unmarshal(msg, &m) != nil {
		return false
	}
	return m.Type == "input"
}
