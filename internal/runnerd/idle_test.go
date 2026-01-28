package runnerd

import (
	"testing"
	"time"
)

func TestShouldIdleStop(t *testing.T) {
	now := time.Date(2026, 1, 26, 0, 0, 0, 0, time.UTC)

	t.Run("active chat", func(t *testing.T) {
		svc := Service{
			State:              "running",
			IdleTimeoutSeconds: 10,
			LastActivityAt:     now.Add(-30 * time.Second).Format(time.RFC3339Nano),
		}
		if shouldIdleStop(svc, now, true) {
			t.Fatalf("expected no idle stop when chat active")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		svc := Service{
			State:              "running",
			IdleTimeoutSeconds: 0,
			LastActivityAt:     now.Add(-30 * time.Second).Format(time.RFC3339Nano),
		}
		if shouldIdleStop(svc, now, false) {
			t.Fatalf("expected idle stop disabled when timeout=0")
		}
	})

	t.Run("not running", func(t *testing.T) {
		svc := Service{
			State:              "stopped",
			IdleTimeoutSeconds: 10,
			LastActivityAt:     now.Add(-30 * time.Second).Format(time.RFC3339Nano),
		}
		if shouldIdleStop(svc, now, false) {
			t.Fatalf("expected no idle stop when service not running")
		}
	})

	t.Run("uses last_activity_at", func(t *testing.T) {
		svc := Service{
			State:              "running",
			IdleTimeoutSeconds: 10,
			LastActivityAt:     now.Add(-11 * time.Second).Format(time.RFC3339Nano),
		}
		if !shouldIdleStop(svc, now, false) {
			t.Fatalf("expected idle stop when last_activity expired")
		}
	})

	t.Run("falls back to created_at", func(t *testing.T) {
		svc := Service{
			State:              "running",
			IdleTimeoutSeconds: 10,
			CreatedAt:          now.Add(-11 * time.Second).Format(time.RFC3339Nano),
		}
		if !shouldIdleStop(svc, now, false) {
			t.Fatalf("expected idle stop when created_at expired")
		}
	})

	t.Run("invalid timestamps", func(t *testing.T) {
		svc := Service{
			State:              "running",
			IdleTimeoutSeconds: 10,
			LastActivityAt:     "nope",
			CreatedAt:          "nope",
		}
		if shouldIdleStop(svc, now, false) {
			t.Fatalf("expected no idle stop when timestamps invalid")
		}
	})
}
