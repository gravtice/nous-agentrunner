package runnerd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type guestToHostTunnelDiagnosticsResponse struct {
	OK            bool   `json:"ok"`
	Error         string `json:"error,omitempty"`
	TunnelID      string `json:"tunnel_id,omitempty"`
	HostPort      int    `json:"host_port,omitempty"`
	GuestPort     int    `json:"guest_port,omitempty"`
	Reply         string `json:"reply,omitempty"`
	ExpectedReply string `json:"expected_reply,omitempty"`
	ElapsedMS     int64  `json:"elapsed_ms,omitempty"`
}

func (s *Server) handleSystemDiagnosticsGuestToHostTunnel(w http.ResponseWriter, r *http.Request) {
	resp := guestToHostTunnelDiagnosticsResponse{}
	defer func() { writeJSON(w, http.StatusOK, resp) }()

	if s.cfg.VsockTunnelPort <= 0 {
		resp.OK = false
		resp.Error = "vsock tunnel is not enabled (NOUS_AGENT_RUNNER_VSOCK_TUNNEL_PORT)"
		return
	}

	var ln net.Listener
	var hostPort int
	for range 16 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			resp.OK = false
			resp.Error = err.Error()
			return
		}
		p := 0
		if tcp, ok := l.Addr().(*net.TCPAddr); ok {
			p = tcp.Port
		}

		s.mu.Lock()
		_, conflict := s.tunnelByHostPort[p]
		s.mu.Unlock()

		if p > 0 && p <= 65535 && !conflict {
			ln = l
			hostPort = p
			break
		}
		_ = l.Close()
	}
	if ln == nil {
		resp.OK = false
		resp.Error = "failed to allocate a free host port for tunnel probe"
		return
	}
	defer ln.Close()

	resp.HostPort = hostPort

	nonce, err := newID("probe_", 12)
	if err != nil {
		resp.OK = false
		resp.Error = "failed to allocate probe id"
		return
	}
	ping := "ping:" + nonce + "\n"
	pong := "pong:" + nonce + "\n"
	resp.ExpectedReply = pong

	serverDone := make(chan error, 1)
	go func() {
		if tl, ok := ln.(*net.TCPListener); ok {
			_ = tl.SetDeadline(time.Now().Add(30 * time.Second))
		}
		conn, err := ln.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

		got, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil && got == "" {
			serverDone <- err
			return
		}
		if got != ping {
			_, _ = io.WriteString(conn, "bad\n")
			serverDone <- fmt.Errorf("unexpected payload: %q", got)
			return
		}
		if _, err := io.WriteString(conn, pong); err != nil {
			serverDone <- err
			return
		}
		serverDone <- nil
	}()

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		resp.OK = false
		resp.Error = err.Error()
		return
	}

	tunnelID, err := newID("tun_", 12)
	if err != nil {
		resp.OK = false
		resp.Error = "failed to allocate tunnel_id"
		return
	}

	var tun Tunnel
	guestReq := map[string]any{
		"tunnel_id":  tunnelID,
		"host_port":  hostPort,
		"guest_port": 0,
	}
	if err := gc.postJSON(r.Context(), "/internal/tunnels", guestReq, &tun); err != nil {
		resp.OK = false
		resp.Error = err.Error()
		return
	}

	if strings.TrimSpace(tun.TunnelID) == "" || tun.TunnelID != tunnelID {
		resp.OK = false
		resp.Error = "guest returned invalid tunnel_id"
		return
	}
	if tun.HostPort != hostPort || tun.HostPort <= 0 || tun.HostPort > 65535 {
		resp.OK = false
		resp.Error = "guest returned invalid host_port"
		return
	}
	if tun.GuestPort <= 0 || tun.GuestPort > 65535 {
		resp.OK = false
		resp.Error = "guest returned invalid guest_port"
		return
	}

	resp.TunnelID = tun.TunnelID
	resp.GuestPort = tun.GuestPort

	entry := &tunnelEntry{Tunnel: tun}
	s.mu.Lock()
	if oldID, ok := s.tunnelByHostPort[hostPort]; ok && oldID != tun.TunnelID {
		delete(s.tunnels, oldID)
	}
	s.tunnels[tun.TunnelID] = entry
	s.tunnelByHostPort[hostPort] = tun.TunnelID
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.tunnels, tun.TunnelID)
		if id, ok := s.tunnelByHostPort[hostPort]; ok && id == tun.TunnelID {
			delete(s.tunnelByHostPort, hostPort)
		}
		s.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = gc.delete(ctx, "/internal/tunnels/"+tun.TunnelID)
	}()

	var probeResp struct {
		Reply     string `json:"reply"`
		ElapsedMS int64  `json:"elapsed_ms"`
	}
	if err := gc.postJSON(r.Context(), "/internal/tunnels/"+tun.TunnelID+"/probe", map[string]any{"payload": ping}, &probeResp); err != nil {
		resp.OK = false
		resp.Error = err.Error()
		return
	}

	resp.Reply = probeResp.Reply
	resp.ElapsedMS = probeResp.ElapsedMS
	if probeResp.Reply != pong {
		resp.OK = false
		resp.Error = "reply mismatch"
		return
	}

	select {
	case err := <-serverDone:
		if err != nil {
			resp.OK = false
			resp.Error = err.Error()
			return
		}
	case <-time.After(2 * time.Second):
		resp.OK = false
		resp.Error = "timeout waiting for host probe server"
		return
	}

	resp.OK = true
}
