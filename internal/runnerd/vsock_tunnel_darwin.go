//go:build darwin

package runnerd

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

const (
	tunnelHandshakeMagic   = 0x4e54554e // "NTUN"
	tunnelHandshakeVersion = 1
)

func (s *Server) startVsockTunnelServer(ctx context.Context) error {
	if s.cfg.VsockTunnelPort <= 0 {
		return nil
	}
	if s.cfg.VsockTunnelPort > 0xffff {
		return fmt.Errorf("NOUS_AGENT_RUNNER_VSOCK_TUNNEL_PORT must be 1..65535, got %d", s.cfg.VsockTunnelPort)
	}

	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		if isVsockUnavailable(err) {
			log.Printf("vsock: unavailable (%v); guest->host tunnels disabled", err)
			s.cfg.VsockTunnelPort = 0
			return nil
		}
		return err
	}

	closeFD := func() {
		_ = unix.Close(fd)
	}

	const any = ^uint32(0)
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: any, Port: uint32(s.cfg.VsockTunnelPort)}); err != nil {
		closeFD()
		if isVsockUnavailable(err) {
			log.Printf("vsock: unavailable (%v); guest->host tunnels disabled", err)
			s.cfg.VsockTunnelPort = 0
			return nil
		}
		return err
	}
	if err := unix.Listen(fd, 128); err != nil {
		closeFD()
		if isVsockUnavailable(err) {
			log.Printf("vsock: unavailable (%v); guest->host tunnels disabled", err)
			s.cfg.VsockTunnelPort = 0
			return nil
		}
		return err
	}

	log.Printf("vsock: tunnel server listening port=%d", s.cfg.VsockTunnelPort)

	go func() {
		<-ctx.Done()
		closeFD()
	}()

	go func() {
		for {
			nfd, _, err := unix.Accept(fd)
			if err != nil {
				return
			}
			go s.handleVsockTunnelConn(ctx, nfd)
		}
	}()

	return nil
}

func (s *Server) handleVsockTunnelConn(ctx context.Context, fd int) {
	f := os.NewFile(uintptr(fd), "vsock-tunnel")
	if f == nil {
		_ = unix.Close(fd)
		return
	}
	defer f.Close()

	hostPort, err := readTunnelHandshake(f, 5*time.Second)
	if err != nil {
		log.Printf("vsock: handshake failed: %v", err)
		return
	}

	if !s.isTunnelHostPortAllowed(hostPort) {
		log.Printf("vsock: rejected host_port=%d (not allowed)", hostPort)
		return
	}

	target := fmt.Sprintf("127.0.0.1:%d", hostPort)
	tcpConn, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		log.Printf("vsock: dial host target=%s failed: %v", target, err)
		return
	}
	defer tcpConn.Close()

	proxyStream(ctx, f, tcpConn)
}

func (s *Server) isTunnelHostPortAllowed(hostPort int) bool {
	if hostPort <= 0 || hostPort > 65535 {
		return false
	}
	s.mu.Lock()
	_, ok := s.tunnelByHostPort[hostPort]
	s.mu.Unlock()
	return ok
}

func readTunnelHandshake(r io.Reader, timeout time.Duration) (int, error) {
	type result struct {
		port int
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		var buf [8]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			ch <- result{err: err}
			return
		}
		if binary.BigEndian.Uint32(buf[0:4]) != tunnelHandshakeMagic {
			ch <- result{err: errors.New("bad tunnel handshake magic")}
			return
		}
		if int(binary.BigEndian.Uint16(buf[4:6])) != tunnelHandshakeVersion {
			ch <- result{err: errors.New("unsupported tunnel handshake version")}
			return
		}
		port := int(binary.BigEndian.Uint16(buf[6:8]))
		ch <- result{port: port}
	}()

	select {
	case res := <-ch:
		return res.port, res.err
	case <-time.After(timeout):
		return 0, errors.New("tunnel handshake timeout")
	}
}

func proxyStream(ctx context.Context, a io.ReadWriteCloser, b io.ReadWriteCloser) {
	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(a, b)
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(b, a)
		errCh <- err
	}()
	select {
	case <-ctx.Done():
	case <-errCh:
	}
	_ = a.Close()
	_ = b.Close()
	<-errCh
}
