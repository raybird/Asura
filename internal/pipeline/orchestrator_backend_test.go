package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"asura/internal/backend"
	"asura/internal/engine"
)

type mockBackendForPipeline struct {
	id      string
	version string
	caps    []backend.Capability
	runFn   func(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error)
	calls   int
}

func (m *mockBackendForPipeline) ID() string { return m.id }
func (m *mockBackendForPipeline) Version(ctx context.Context) (string, error) {
	return m.version, nil
}
func (m *mockBackendForPipeline) Capabilities() []backend.Capability { return m.caps }
func (m *mockBackendForPipeline) HasCapability(cap backend.Capability) bool {
	for _, c := range m.caps {
		if c == cap {
			return true
		}
	}
	return false
}
func (m *mockBackendForPipeline) Run(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	m.calls++
	if m.runFn != nil {
		return m.runFn(ctx, req)
	}
	return &backend.ExecutionResult{
		BackendID:      m.id,
		BackendVersion: m.version,
		Output:         "mock output",
	}, nil
}

// MBACK-003: 未知後端在執行前 fail-closed 拒絕，0 進程與模型呼叫，不 fallback
func TestOrchestrator_UnknownBackend_FailClosed(t *testing.T) {
	reg := backend.NewRegistry()
	_ = reg.Register(&mockBackendForPipeline{
		id:      "opencode",
		version: "1.18.31",
		caps:    []backend.Capability{backend.CapReadOnlySandbox, backend.CapWorkspaceWrite},
	})

	orc := NewOrchestrator(
		&mockOracleStage{},
		&mockStrikerStage{},
		&mockCriticStage{},
	).WithRegistry(reg)

	ctx := context.Background()
	execCtx := &engine.ExecutionContext{
		Workspace: "/tmp",
		Backend:   "unknown-backend-id",
	}

	out, trace, err := orc.Execute(ctx, ModeThreeFace, execCtx)
	if err == nil {
		t.Fatalf("expected error for unknown backend, got nil")
	}
	if !errors.Is(err, backend.ErrBackendNotFound) && !strings.Contains(err.Error(), "unknown-backend-id") {
		t.Errorf("expected error to mention unknown backend, got: %v", err)
	}
	if trace != nil {
		t.Errorf("expected nil trace on preflight failure, got: %v", trace)
	}
	if out != "" {
		t.Errorf("expected empty output on fail-closed, got: %q", out)
	}
}

// MBACK-008: Three-Face capability preflight 檢查唯讀沙箱能力，不支援立即 fail-closed
func TestOrchestrator_ThreeFace_CapabilityPreflight_FailClosed(t *testing.T) {
	reg := backend.NewRegistry()
	unsafeBackend := &mockBackendForPipeline{
		id:      "unsafe-backend",
		version: "0.1.0",
		// 故意不包含 backend.CapReadOnlySandbox
		caps: []backend.Capability{backend.CapWorkspaceWrite},
	}
	_ = reg.Register(unsafeBackend)

	oracleStage := &mockOracleStage{}
	strikerStage := &mockStrikerStage{}
	criticStage := &mockCriticStage{}

	orc := NewOrchestrator(oracleStage, strikerStage, criticStage).WithRegistry(reg)

	ctx := context.Background()
	execCtx := &engine.ExecutionContext{
		Workspace: "/tmp",
		Backend:   "unsafe-backend",
	}

	_, trace, err := orc.Execute(ctx, ModeThreeFace, execCtx)
	if err == nil {
		t.Fatalf("expected error due to missing CapReadOnlySandbox, got nil")
	}
	if !errors.Is(err, backend.ErrUnsupportedCapability) {
		t.Errorf("expected error to wrap ErrUnsupportedCapability, got %v", err)
	}
	if trace != nil {
		t.Errorf("expected nil trace on preflight failure, got %v", trace)
	}

	// 斷言 0 stage calls
	if oracleStage.callCount != 0 || strikerStage.callCount != 0 || criticStage.callCount != 0 {
		t.Errorf("expected 0 calls across all stages, got oracle=%d, striker=%d, critic=%d",
			oracleStage.callCount, strikerStage.callCount, criticStage.callCount)
	}
	if unsafeBackend.calls != 0 {
		t.Errorf("expected 0 backend model calls, got %d", unsafeBackend.calls)
	}
}

// MBACK-009: PipelineTrace 完整記錄各 Stage 之 BackendID 與 Version
func TestOrchestrator_Trace_RecordsActualBackendAndVersion(t *testing.T) {
	reg := backend.NewRegistry()
	codexBackend := &mockBackendForPipeline{
		id:      "codex",
		version: "0.155.0",
		caps:    []backend.Capability{backend.CapReadOnlySandbox, backend.CapWorkspaceWrite},
	}
	_ = reg.Register(codexBackend)

	oracleStage := &mockOracleStage{plan: &OraclePlan{Goal: "G", Steps: []string{"S1"}}}
	strikerStage := &mockStrikerStage{output: "striker result"}
	criticStage := &mockCriticStage{review: &CriticReview{Verdict: VerdictPass}}

	orc := NewOrchestrator(oracleStage, strikerStage, criticStage).WithRegistry(reg)

	ctx := context.Background()
	execCtx := &engine.ExecutionContext{
		Workspace: "/tmp",
		Backend:   "codex",
	}

	out, trace, err := orc.Execute(ctx, ModeThreeFace, execCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "striker result" {
		t.Errorf("expected output 'striker result', got %q", out)
	}

	if trace == nil {
		t.Fatalf("expected non-nil trace")
	}
	if trace.BackendID != "codex" {
		t.Errorf("expected trace.BackendID codex, got %s", trace.BackendID)
	}

	for _, st := range trace.Stages {
		if st.BackendID != "codex" {
			t.Errorf("stage %s expected BackendID codex, got %s", st.Name, st.BackendID)
		}
		if st.BackendVersion != "0.155.0" {
			t.Errorf("stage %s expected BackendVersion 0.155.0, got %s", st.Name, st.BackendVersion)
		}
	}
}

// MBACK-010: No failover after partial execution - Striker 失敗絕無二次重跑
func TestOrchestrator_NoFailoverAfterPartialExecution(t *testing.T) {
	reg := backend.NewRegistry()
	codexBackend := &mockBackendForPipeline{
		id:      "codex",
		version: "0.155.0",
		caps:    []backend.Capability{backend.CapReadOnlySandbox, backend.CapWorkspaceWrite},
	}
	opencodeBackend := &mockBackendForPipeline{
		id:      "opencode",
		version: "1.18.31",
		caps:    []backend.Capability{backend.CapReadOnlySandbox, backend.CapWorkspaceWrite},
	}
	_ = reg.Register(codexBackend)
	_ = reg.Register(opencodeBackend)

	oracleStage := &mockOracleStage{plan: &OraclePlan{Goal: "G", Steps: []string{"S1"}}}
	strikerStage := &mockStrikerStage{err: errors.New("striker disk write failed"), output: "partial work"}
	criticStage := &mockCriticStage{}

	orc := NewOrchestrator(oracleStage, strikerStage, criticStage).WithRegistry(reg)

	ctx := context.Background()
	execCtx := &engine.ExecutionContext{
		Workspace: "/tmp",
		Backend:   "codex",
	}

	out, trace, err := orc.Execute(ctx, ModeThreeFace, execCtx)
	if err == nil {
		t.Fatalf("expected error from striker, got nil")
	}
	if out != "partial work" {
		t.Errorf("expected partial work preserved, got %q", out)
	}
	if trace == nil || len(trace.Stages) != 2 {
		t.Fatalf("expected 2 stages in trace (oracle completed, striker failed)")
	}
	if trace.Stages[1].Status != StageFailed {
		t.Errorf("expected striker stage to be Failed, got %s", trace.Stages[1].Status)
	}

	// 關鍵斷言：opencode 絕未被調用 (0 次)，無任何 failover 嘗試
	if opencodeBackend.calls != 0 {
		t.Errorf("security violation: opencode backend was called %d times during failover!", opencodeBackend.calls)
	}
	if criticStage.callCount != 0 {
		t.Errorf("expected critic not called after striker failure, got %d", criticStage.callCount)
	}
}
