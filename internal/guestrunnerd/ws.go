package guestrunnerd

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) handleServiceChatWS(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("service_id"))
	if id == "" {
		writeError(w, 400, "BAD_REQUEST", "service_id required", nil)
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))

	s.mu.Lock()
	svc, ok := s.state.Services[id]
	s.mu.Unlock()
	if !ok {
		writeError(w, 404, "NOT_FOUND", "service not found", nil)
		return
	}

	hostConn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer hostConn.Close()

	targetURL := "ws://127.0.0.1:" + strconv.Itoa(svc.Port) + "/v1/chat"
	if sessionID != "" {
		targetURL += "?session_id=" + sessionID
	}
	svcConn, _, err := websocket.DefaultDialer.DialContext(r.Context(), targetURL, nil)
	if err != nil {
		_ = hostConn.WriteJSON(map[string]any{"type": "error", "code": "SERVICE_UNAVAILABLE", "message": err.Error()})
		return
	}
	defer svcConn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	errCh := make(chan error, 2)
	go func() { errCh <- proxyWS(ctx, hostConn, svcConn) }()
	go func() { errCh <- proxyWS(ctx, svcConn, hostConn) }()
	<-errCh
}

func proxyWS(ctx context.Context, src, dst *websocket.Conn) error {
	for {
		_, msg, err := src.ReadMessage()
		if err != nil {
			return err
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
