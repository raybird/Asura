package contextwin_test

import (
	"testing"

	"asura/internal/contextwin"
)

func TestLoadConfig_Defaults(t *testing.T) {
	envMap := map[string]string{}
	lookup := func(key string) (string, bool) {
		val, ok := envMap[key]
		return val, ok
	}

	cfg, err := contextwin.LoadConfigWithLookup(lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ContextLimit != 0 {
		t.Errorf("expected default ContextLimit 0, got %d", cfg.ContextLimit)
	}
	if cfg.Policy != contextwin.PolicyObserve {
		t.Errorf("expected default Policy observe, got %s", cfg.Policy)
	}
	if cfg.ReserveRatio != 0.20 {
		t.Errorf("expected default ReserveRatio 0.20, got %f", cfg.ReserveRatio)
	}
}

func TestLoadConfig_ValidValues(t *testing.T) {
	envMap := map[string]string{
		"ASURA_CONTEXT_LIMIT":  "64000",
		"ASURA_CONTEXT_POLICY": "reject",
	}
	lookup := func(key string) (string, bool) {
		val, ok := envMap[key]
		return val, ok
	}

	cfg, err := contextwin.LoadConfigWithLookup(lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ContextLimit != 64000 {
		t.Errorf("expected ContextLimit 64000, got %d", cfg.ContextLimit)
	}
	if cfg.Policy != contextwin.PolicyReject {
		t.Errorf("expected Policy reject, got %s", cfg.Policy)
	}
}

func TestLoadConfig_InvalidLimit(t *testing.T) {
	badLimits := []string{"-1", "0", "abc", "12.34", "9999999999999999999999999999999"}
	for _, bad := range badLimits {
		envMap := map[string]string{
			"ASURA_CONTEXT_LIMIT": bad,
		}
		lookup := func(key string) (string, bool) {
			val, ok := envMap[key]
			return val, ok
		}

		_, err := contextwin.LoadConfigWithLookup(lookup)
		if err == nil {
			t.Errorf("expected error for invalid limit %q, got nil", bad)
		}
	}
}

func TestLoadConfig_InvalidPolicy(t *testing.T) {
	envMap := map[string]string{
		"ASURA_CONTEXT_POLICY": "unknown_policy",
	}
	lookup := func(key string) (string, bool) {
		val, ok := envMap[key]
		return val, ok
	}

	_, err := contextwin.LoadConfigWithLookup(lookup)
	if err == nil {
		t.Errorf("expected error for invalid policy, got nil")
	}
}
