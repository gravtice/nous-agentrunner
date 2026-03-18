package runnerd

import (
	"strings"
	"testing"
)

func TestValidateServiceEnv(t *testing.T) {
	if out, err := validateServiceEnv(nil); err != nil || out != nil {
		t.Fatalf("nil env: out=%v err=%v", out, err)
	}

	if _, err := validateServiceEnv(map[string]string{"AGENT_RUNNER_SERVICE_PORT": "1"}); err == nil {
		t.Fatalf("expected reserved key error")
	}

	if _, err := validateServiceEnv(map[string]string{"1A": "1"}); err == nil {
		t.Fatalf("expected invalid key error")
	}

	if _, err := validateServiceEnv(map[string]string{"A": "1", " A ": "2"}); err == nil {
		t.Fatalf("expected duplicate key error")
	}

	out, err := validateServiceEnv(map[string]string{"  A_B1  ": "v"})
	if err != nil {
		t.Fatalf("valid env: %v", err)
	}
	if out["A_B1"] != "v" {
		t.Fatalf("unexpected out: %#v", out)
	}

	out, err = validateServiceEnv(map[string]string{"AGENT_RUNNER_FIRST_EVENT_TIMEOUT_SECONDS": "1"})
	if err != nil {
		t.Fatalf("valid AGENT_RUNNER_* env: %v", err)
	}
	if out["AGENT_RUNNER_FIRST_EVENT_TIMEOUT_SECONDS"] != "1" {
		t.Fatalf("unexpected out: %#v", out)
	}

	if _, err := validateServiceEnv(map[string]string{"A": "x\x00y"}); err == nil {
		t.Fatalf("expected NUL error")
	}

	if _, err := validateServiceEnv(map[string]string{"A": strings.Repeat("x", 16*1024+1)}); err == nil {
		t.Fatalf("expected too large error")
	}
}
