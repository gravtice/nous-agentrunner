package runnerd

import "testing"

func TestTryBeginServiceChat_Exclusive(t *testing.T) {
	s := &Server{}

	release, ok := s.tryBeginServiceChat("svc_1")
	if !ok || release == nil {
		t.Fatalf("expected first acquire to succeed")
	}

	if _, ok := s.tryBeginServiceChat("svc_1"); ok {
		t.Fatalf("expected second acquire to fail")
	}

	release()

	release2, ok := s.tryBeginServiceChat("svc_1")
	if !ok || release2 == nil {
		t.Fatalf("expected acquire after release to succeed")
	}
	release2()
}
