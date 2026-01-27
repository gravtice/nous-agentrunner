package runnerd

import (
	"reflect"
	"testing"
)

func TestApplyClaudeServiceConfigDefaults_SetsSettingSourcesWhenMissing(t *testing.T) {
	cfg := map[string]any{}
	applyClaudeServiceConfigDefaults(cfg)
	got, ok := cfg["setting_sources"].([]string)
	if !ok {
		t.Fatalf("setting_sources type=%T", cfg["setting_sources"])
	}
	if !reflect.DeepEqual(got, []string{"user", "project"}) {
		t.Fatalf("setting_sources=%v", got)
	}
}

func TestApplyClaudeServiceConfigDefaults_DoesNotOverrideSettingSources(t *testing.T) {
	existing := []any{"project"}
	cfg := map[string]any{"setting_sources": existing}
	applyClaudeServiceConfigDefaults(cfg)
	got, ok := cfg["setting_sources"].([]any)
	if !ok {
		t.Fatalf("setting_sources type=%T", cfg["setting_sources"])
	}
	if &got[0] != &existing[0] {
		t.Fatalf("expected existing slice preserved")
	}
}

