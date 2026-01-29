package guestrunnerd

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mdlayher/vsock"
)

type Tunnel struct {
	TunnelID  string `json:"tunnel_id"`
	HostPort  int    `json:"host_port"`
	GuestPort int    `json:"guest_port"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
}

type tunnelEntry struct {
	Tunnel
	ln net.Listener
}

type createTunnelReq struct {
	TunnelID  string `json:"tunnel_id"`
	HostPort  int    `json:"host_port"`
	GuestPort int    `json:"guest_port"`
}

type probeTunnelReq struct {
	Payload string `json:"payload"`
}

type probeTunnelResp struct {
	TunnelID  string `json:"tunnel_id"`
	HostPort  int    `json:"host_port"`
	GuestPort int    `json:"guest_port"`
	Reply     string `json:"reply"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

const (
	tunnelHandshakeMagic   = 0x4e54554e // "NTUN"
	tunnelHandshakeVersion = 1
)

func (s *Server) handleTunnelCreate(w http.ResponseWriter, r *http.Request) {
	if s.config.HostTunnelVsockPort <= 0 || s.config.HostTunnelVsockPort > 65535 {
		writeError(w, 500, "VSOCK_UNAVAILABLE", "host tunnel vsock port is not configured", nil)
		return
	}

	var req createTunnelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "BAD_REQUEST", "invalid json", nil)
		return
	}
	req.TunnelID = strings.TrimSpace(req.TunnelID)
	if req.TunnelID == "" {
		writeError(w, 400, "BAD_REQUEST", "tunnel_id is required", nil)
		return
	}
	if req.HostPort <= 0 || req.HostPort > 65535 {
		writeError(w, 400, "BAD_REQUEST", "host_port must be 1..65535", nil)
		return
	}
	if req.GuestPort < 0 || req.GuestPort > 65535 {
		writeError(w, 400, "BAD_REQUEST", "guest_port must be 0..65535", nil)
		return
	}

	s.mu.Lock()
	if existingID, ok := s.tunnelByHostPort[req.HostPort]; ok {
		if e, ok := s.tunnels[existingID]; ok && e.ln != nil {
			out := e.Tunnel
			s.mu.Unlock()
			writeJSON(w, 200, out)
			return
		}
	}
	s.mu.Unlock()

	guestPort := req.GuestPort
	if guestPort == 0 {
		var err error
		guestPort, err = pickFreePort()
		if err != nil {
			writeError(w, 500, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(guestPort))
	if err != nil {
		writeError(w, 500, "TUNNEL_FAILED", err.Error(), nil)
		return
	}

	entry := &tunnelEntry{
		Tunnel: Tunnel{
			TunnelID:  req.TunnelID,
			HostPort:  req.HostPort,
			GuestPort: guestPort,
			State:     "running",
			CreatedAt: nowISO8601(),
		},
		ln: ln,
	}

	s.mu.Lock()
	s.tunnels[req.TunnelID] = entry
	s.tunnelByHostPort[req.HostPort] = req.TunnelID
	s.mu.Unlock()

	go s.serveTunnel(entry)

	writeJSON(w, 200, entry.Tunnel)
}

func (s *Server) handleTunnelDelete(w http.ResponseWriter, r *http.Request) {
	tunnelID := strings.TrimSpace(r.PathValue("tunnel_id"))
	if tunnelID == "" {
		writeError(w, 400, "BAD_REQUEST", "tunnel_id is required", nil)
		return
	}

	s.mu.Lock()
	entry, ok := s.tunnels[tunnelID]
	if ok {
		delete(s.tunnels, tunnelID)
		if id, ok2 := s.tunnelByHostPort[entry.HostPort]; ok2 && id == tunnelID {
			delete(s.tunnelByHostPort, entry.HostPort)
		}
	}
	s.mu.Unlock()

	if !ok {
		writeError(w, 404, "NOT_FOUND", "tunnel not found", nil)
		return
	}
	if entry.ln != nil {
		_ = entry.ln.Close()
	}
	writeJSON(w, 200, map[string]any{"deleted": true})
}

func (s *Server) serveTunnel(entry *tunnelEntry) {
	for {
		conn, err := entry.ln.Accept()
		if err != nil {
			return
		}
		go s.proxyTunnelConn(entry.HostPort, conn)
	}
}

func (s *Server) proxyTunnelConn(hostPort int, guestConn net.Conn) {
	defer guestConn.Close()

	hostConn, err := vsock.Dial(vsock.Host, uint32(s.config.HostTunnelVsockPort), nil)
	if err != nil {
		return
	}
	defer hostConn.Close()

	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], tunnelHandshakeMagic)
	binary.BigEndian.PutUint16(hdr[4:6], tunnelHandshakeVersion)
	binary.BigEndian.PutUint16(hdr[6:8], uint16(hostPort))
	if _, err := hostConn.Write(hdr[:]); err != nil {
		return
	}

	proxyConn(guestConn, hostConn)
}

func (s *Server) handleTunnelProbe(w http.ResponseWriter, r *http.Request) {
	tunnelID := strings.TrimSpace(r.PathValue("tunnel_id"))
	if tunnelID == "" {
		writeError(w, 400, "BAD_REQUEST", "tunnel_id is required", nil)
		return
	}

	var req probeTunnelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "BAD_REQUEST", "invalid json", nil)
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

	s.mu.Lock()
	entry, ok := s.tunnels[tunnelID]
	s.mu.Unlock()

	if !ok || entry.ln == nil || entry.GuestPort <= 0 || entry.GuestPort > 65535 {
		writeError(w, 404, "NOT_FOUND", "tunnel not found", nil)
		return
	}

	start := time.Now()
	addr := fmt.Sprintf("127.0.0.1:%d", entry.GuestPort)
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

	resp := probeTunnelResp{
		TunnelID:  entry.TunnelID,
		HostPort:  entry.HostPort,
		GuestPort: entry.GuestPort,
		Reply:     reply,
		ElapsedMS: time.Since(start).Milliseconds(),
	}
	writeJSON(w, 200, resp)
}

func proxyConn(a, b net.Conn) {
	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(a, b)
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(b, a)
		errCh <- err
	}()
	<-errCh
	_ = a.Close()
	_ = b.Close()
	<-errCh
}
