package pipeline

import (
	"asura/internal/backend"
	"asura/internal/engine"
	"context"
	"errors"
	"testing"
)

type disappearingInventoryRuntime struct {
	mockBackendForPipeline
	gone bool
}

func (b *disappearingInventoryRuntime) Version(context.Context) (string, error) {
	if b.gone {
		return "", errors.New("executable disappeared")
	}
	return "v1", nil
}

// 2026-10-04：RTINV-010，Version failure 必須在 Run 前擋下，不使用其他 runtime。
func TestInventorySealedRuntimeDisappears(t *testing.T) {
	b := &disappearingInventoryRuntime{mockBackendForPipeline: mockBackendForPipeline{id: "opencode", caps: []backend.Capability{backend.CapWorkspaceWrite}}}
	fallback := &mockBackendForPipeline{id: "codex", version: "v1", caps: b.caps}
	reg := backend.NewRegistry()
	reg.Register(b)
	reg.Register(fallback)
	o := NewOrchestrator(NewOracleStageWithBackend(fallback), NewStrikerStageWithBackend(fallback), NewCriticStageWithBackend(fallback)).WithRegistry(reg)
	contract, err := o.Preflight(context.Background(), "direct", "opencode")
	if err != nil {
		t.Fatal(err)
	}
	b.gone = true
	_, trace, err := o.Execute(context.Background(), ModeDirect, &engine.ExecutionContext{Backend: "opencode", Workspace: t.TempDir(), ExecutionContract: contract})
	if err == nil || b.calls != 0 || fallback.calls != 0 || trace == nil || trace.ModelCalls != 0 {
		t.Fatalf("sealed unavailable runtime executed: err=%v calls=%d fallback=%d trace=%+v", err, b.calls, fallback.calls, trace)
	}
}
