package runnerd

import (
	"context"
	"log"
	"strings"
	"time"
)

func parseServiceTimestamp(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func shouldIdleStop(svc Service, now time.Time, hasActiveChat bool) bool {
	if hasActiveChat {
		return false
	}
	if svc.IdleTimeoutSeconds <= 0 {
		return false
	}
	if strings.TrimSpace(strings.ToLower(svc.State)) != "running" {
		return false
	}

	last, ok := parseServiceTimestamp(svc.LastActivityAt)
	if !ok {
		last, ok = parseServiceTimestamp(svc.CreatedAt)
	}
	if !ok {
		return false
	}

	timeout := time.Duration(svc.IdleTimeoutSeconds) * time.Second
	if timeout/time.Second != time.Duration(svc.IdleTimeoutSeconds) {
		return false
	}
	return now.Sub(last) >= timeout
}

func (s *Server) runIdleServiceReaper(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.stopIdleServicesOnce(ctx)
		}
	}
}

func (s *Server) stopIdleServicesOnce(ctx context.Context) {
	now := time.Now().UTC()
	serviceIDs := s.lockIdleStopCandidates(now)
	if len(serviceIDs) == 0 {
		return
	}
	defer s.unlockServiceChats(serviceIDs)

	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	vmState, err := s.limaInstanceState(checkCtx)
	cancel()
	if err != nil || vmState != "running" {
		return
	}

	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	gc, err := s.ensureGuestReady(stopCtx)
	if err != nil {
		log.Printf("services.idle_stop: guest unavailable: %v", err)
		return
	}

	for _, serviceID := range serviceIDs {
		if _, err := s.stopServiceWithGuest(stopCtx, gc, serviceID, "idle_timeout"); err != nil {
			log.Printf("services.idle_stop: stop failed service_id=%s err=%v", serviceID, err)
		}
	}
}

func (s *Server) lockIdleStopCandidates(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeServiceChats == nil {
		s.activeServiceChats = make(map[string]bool)
	}

	out := make([]string, 0)
	for serviceID, svc := range s.services {
		if shouldIdleStop(svc, now, s.activeServiceChats[serviceID]) {
			s.activeServiceChats[serviceID] = true
			out = append(out, serviceID)
		}
	}
	return out
}

func (s *Server) unlockServiceChats(serviceIDs []string) {
	s.mu.Lock()
	if s.activeServiceChats != nil {
		for _, serviceID := range serviceIDs {
			delete(s.activeServiceChats, serviceID)
		}
	}
	s.mu.Unlock()
}
