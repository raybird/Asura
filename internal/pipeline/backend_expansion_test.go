package pipeline

import (
	"asura/internal/backend"
	"asura/internal/backend/antigravity"
	"asura/internal/backend/omp"
	"asura/internal/contextwin"
	"asura/internal/engine"
	"context"
	"errors"
	"strings"
	"testing"
)

type countedAdapter struct {
	backend.Backend
	calls int
}

func (c *countedAdapter) Run(ctx context.Context, r *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	c.calls++
	return c.Backend.Run(ctx, r)
}

// 2026-09-30：BADAPT-006，使用正式能力矩陣，四個 backend 均計數。
func TestNewBackendsThreeFaceStageZero(t *testing.T) {
	for _, id := range []string{"omp", "antigravity"} {
		t.Run(id, func(t *testing.T) {
			all := []*countedAdapter{{Backend: &mockBackendForPipeline{id: "opencode"}}, {Backend: &mockBackendForPipeline{id: "codex"}}, {Backend: omp.NewAdapter(omp.AdapterConfig{})}, {Backend: antigravity.NewAdapter(antigravity.AdapterConfig{})}}
			reg := backend.NewRegistry()
			for _, b := range all {
				reg.Register(b)
			}
			orc := NewOrchestrator(&mockOracleStage{}, &mockStrikerStage{}, &mockCriticStage{}).WithRegistry(reg)
			_, _, err := orc.Execute(context.Background(), ModeThreeFace, &engine.ExecutionContext{Backend: id, Workspace: t.TempDir(), OriginalPrompt: "probe"})
			if !errors.Is(err, backend.ErrUnsupportedCapability) {
				t.Fatalf("error %v", err)
			}
			for _, b := range all {
				if b.calls != 0 {
					t.Fatalf("%s called %d times", b.ID(), b.calls)
				}
			}
		})
	}
}

// 2026-09-30：BADAPT-009／011，這層驗上層 admission、sealed prompt 與一次派送，未 mock 安全能力。
func TestNewBackendContextMemoryAndFailureIsolation(t *testing.T) {
	for _, id := range []string{"omp", "antigravity"} {
		t.Run(id, func(t *testing.T) {
			reg := backend.NewRegistry()
			all := make([]*mockBackendForPipeline, 0, 4)
			for _, name := range []string{"opencode", "codex", "omp", "antigravity"} {
				b := &mockBackendForPipeline{id: name, version: "fixture-contract", caps: []backend.Capability{backend.CapWorkspaceWrite}}
				all = append(all, b)
				reg.Register(b)
			}
			var selected *mockBackendForPipeline
			for _, b := range all {
				if b.id == id {
					selected = b
				}
			}
			selected.runFn = func(c context.Context, r *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
				if !strings.Contains(r.Prompt, "SEALED_APPROVED_MEMORY") {
					t.Fatal("sealed memory absent")
				}
				return &backend.ExecutionResult{PartialOutput: "PARTIAL", FailureClass: backend.FailureExecution}, backend.NewExecutionError(id, backend.FailureExecution, errors.New("fixture failure"), "PARTIAL")
			}
			orc := NewOrchestrator(&mockOracleStage{}, NewStrikerStageWithBackend(selected), &mockCriticStage{}).WithRegistry(reg).WithContextBudget(contextwin.Config{ContextLimit: 1, Policy: contextwin.PolicyReject})
			sealed := &engine.ExecutionContext{Backend: id, Workspace: t.TempDir(), OriginalPrompt: "a long prompt for context rejection", ProjectMemories: []engine.ProjectMemorySnapshot{{ID: "approved", Content: "SEALED_APPROVED_MEMORY"}}}
			_, trace, err := orc.Execute(context.Background(), ModeDirect, sealed)
			if err == nil || trace == nil || trace.ModelCalls != 0 {
				t.Fatalf("rejection trace %+v err %v", trace, err)
			}
			for _, b := range all {
				if b.calls != 0 {
					t.Fatal("admission invoked backend")
				}
			}
			orc.WithContextBudget(contextwin.Config{Policy: contextwin.PolicyObserve})
			out, _, err := orc.Execute(context.Background(), ModeDirect, sealed)
			if err == nil || out != "PARTIAL" {
				t.Fatalf("partial %q err %v", out, err)
			}
			for _, b := range all {
				want := 0
				if b.id == id {
					want = 1
				}
				if b.calls != want {
					t.Fatalf("%s calls %d want %d", b.id, b.calls, want)
				}
			}
		})
	}
}
