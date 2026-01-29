package guestrunnerd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

type tcpProbeRequest struct {
	Port    int    `json:"port"`
	Payload string `json:"payload"`
}

type tcpProbeResponse struct {
	Reply     string `json:"reply"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

func (s *Server) handleTCPProbe(w http.ResponseWriter, r *http.Request) {
	var req tcpProbeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "BAD_REQUEST", "invalid json", nil)
		return
	}
	if req.Port <= 0 || req.Port > 65535 {
		writeError(w, 400, "BAD_REQUEST", "port must be 1..65535", nil)
		return
	}
	if req.Payload == "" {
		writeError(w, 400, "BAD_REQUEST", "payload is required", nil)
		return
	}
	if len(req.Payload) > 1024 {
		writeError(w, 400, "BAD_REQUEST", "payload is too large", nil)
		return
	}

	start := time.Now()
	addr := fmt.Sprintf("127.0.0.1:%d", req.Port)
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(r.Context(), "tcp", addr)
	if err != nil {
		writeError(w, 500, "PROBE_FAILED", err.Error(), nil)
		return
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, req.Payload); err != nil {
		writeError(w, 500, "PROBE_FAILED", err.Error(), nil)
		return
	}
	br := bufio.NewReader(conn)
	reply, err := br.ReadString('\n')
	if err != nil && reply == "" {
		writeError(w, 500, "PROBE_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, 200, tcpProbeResponse{
		Reply:     reply,
		ElapsedMS: time.Since(start).Milliseconds(),
	})
}

