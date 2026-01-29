//go:build !darwin

package runnerd

import "context"

func (s *Server) startVsockTunnelServer(_ context.Context) error { return nil }

