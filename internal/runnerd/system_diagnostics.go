package runnerd

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
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

	gc, err := s.ensureGuestReady(r.Context())
	if err != nil {
		resp.OK = false
		resp.Error = err.Error()
		return
	}

	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptResp, err := s.runGuestToHostTunnelDiagnosticsAttempt(r, gc)
		resp = attemptResp
		if err == nil {
			return
		}
		resp.OK = false
		resp.Error = err.Error()
		if attempt == maxAttempts {
			return
		}
		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
	}
}

func (s *Server) runGuestToHostTunnelDiagnosticsAttempt(r *http.Request, gc *guestClient) (guestToHostTunnelDiagnosticsResponse, error) {
	resp := guestToHostTunnelDiagnosticsResponse{}

	var ln net.Listener
	var hostPort int
	for range 16 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return resp, err
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
		return resp, fmt.Errorf("failed to allocate a free host port for tunnel probe")
	}
	defer ln.Close()

	resp.HostPort = hostPort

	nonce, err := newID("probe_", 12)
	if err != nil {
		return resp, fmt.Errorf("failed to allocate probe id")
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

	var portResp struct {
		Port int `json:"port"`
	}
	if err := gc.getJSON(r.Context(), "/internal/ports/free", &portResp); err != nil {
		return resp, err
	}

	guestPort := portResp.Port
	if guestPort <= 0 || guestPort > 65535 {
		return resp, fmt.Errorf("guest returned invalid free port")
	}

	cancel, _, err := s.startReverseSSHTunnel(r.Context(), hostPort, guestPort)
	if err != nil {
		return resp, err
	}
	defer cancel()

	resp.TunnelID = "diag"
	resp.GuestPort = guestPort

	var probeResp struct {
		Reply     string `json:"reply"`
		ElapsedMS int64  `json:"elapsed_ms"`
	}
	if err := gc.postJSON(r.Context(), "/internal/diagnostics/tcp_probe", map[string]any{"port": guestPort, "payload": ping}, &probeResp); err != nil {
		return resp, err
	}

	resp.Reply = probeResp.Reply
	resp.ElapsedMS = probeResp.ElapsedMS
	if probeResp.Reply != pong {
		return resp, fmt.Errorf("reply mismatch")
	}

	select {
	case err := <-serverDone:
		if err != nil {
			return resp, err
		}
	case <-time.After(2 * time.Second):
		return resp, fmt.Errorf("timeout waiting for host probe server")
	}

	resp.OK = true
	return resp, nil
}
