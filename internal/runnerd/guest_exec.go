package runnerd

import (
	"context"
	"strings"
)

func (s *Server) runInGuestOutput(ctx context.Context, script string) (string, error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return "", nil
	}
	out, err := s.runLimactl(ctx, "shell", s.cfg.LimaInstanceName, "--", "bash", "-lc", script)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
