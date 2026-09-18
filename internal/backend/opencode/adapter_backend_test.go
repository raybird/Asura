package opencode

import (
	"context"
	"testing"

	"asura/internal/backend"
)

// 確保 Adapter 實作 backend.Backend 介面
var _ backend.Backend = (*Adapter)(nil)

func TestAdapter_BackendInterface(t *testing.T) {
	adapter := NewAdapter(AdapterConfig{})

	if adapter.ID() != "opencode" {
		t.Errorf("expected ID 'opencode', got %q", adapter.ID())
	}

	expectedCaps := []backend.Capability{
		backend.CapReadOnlySandbox,
		backend.CapWorkspaceWrite,
		backend.CapStructuredEvents,
		backend.CapProcessCancellation,
	}

	for _, cap := range expectedCaps {
		if !adapter.HasCapability(cap) {
			t.Errorf("expected adapter to support capability %s", cap)
		}
	}

	if adapter.HasCapability(backend.Capability("unknown_cap")) {
		t.Errorf("expected adapter to reject unknown capability")
	}

	// 驗證 Version 方法能正常調用或返回合理字串/錯誤
	ctx := context.Background()
	ver, err := adapter.Version(ctx)
	if err == nil {
		t.Logf("opencode adapter version: %s", ver)
	}
}
