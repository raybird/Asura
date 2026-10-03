package pipeline

import (
	"asura/internal/backend"
	"asura/internal/engine"
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func stageOutput(r backend.Role) string {
	switch r {
	case backend.RoleOracle:
		return `{"goal":"goal","steps":["one"]}`
	case backend.RoleCritic:
		return `{"verdict":"pass","summary":"done"}`
	}
	return "done"
}

// 2026-10-04：STGBIND-011，前一階段造成能力退化時，下一個 Run 必須被擋下。
func TestStageBindingGuardBetweenStages(t *testing.T) {
	reg := backend.NewRegistry()
	b := &mockBackendForPipeline{id: "opencode", version: "v1", caps: []backend.Capability{backend.CapReadOnlySandbox, backend.CapWorkspaceWrite}}
	b.runFn = func(_ context.Context, r *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
		if r.Role == backend.RoleOracle {
			b.caps = []backend.Capability{backend.CapReadOnlySandbox}
		}
		return &backend.ExecutionResult{BackendID: b.id, Output: stageOutput(r.Role)}, nil
	}
	reg.Register(b)
	o := NewOrchestrator(NewOracleStageWithBackend(b), NewStrikerStageWithBackend(b), NewCriticStageWithBackend(b)).WithRegistry(reg)
	c, err := o.Preflight(context.Background(), "three-face", "opencode")
	if err != nil {
		t.Fatal(err)
	}
	_, trace, err := o.Execute(context.Background(), ModeThreeFace, &engine.ExecutionContext{Backend: "opencode", Workspace: t.TempDir(), ExecutionContract: c})
	if err == nil || b.calls != 1 || trace == nil || trace.ModelCalls != 1 {
		t.Fatalf("stage capability regression escaped guard: err=%v calls=%d trace=%+v", err, b.calls, trace)
	}
}

// 2026-10-04：STGBIND-002/009/010/011，實際 Run 與封存結果一致，constructor 不得補位。
func TestStageBindingMixedExecution(t *testing.T) {
	for _, change := range []string{"priority", "missing", "regression", "upgrade"} {
		t.Run(change, func(t *testing.T) {
			reg := backend.NewRegistry()
			var calls []string
			a := &mockBackendForPipeline{id: "codex", version: "v1", caps: []backend.Capability{backend.CapReadOnlySandbox, backend.CapWorkspaceWrite}}
			b := &mockBackendForPipeline{id: "omp", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite}}
			for _, r := range []*mockBackendForPipeline{a, b} {
				r := r
				r.runFn = func(_ context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
					calls = append(calls, r.id+":"+string(req.Role))
					return &backend.ExecutionResult{BackendID: r.id, BackendVersion: r.version, Output: stageOutput(req.Role)}, nil
				}
				reg.Register(r)
			}
			fallback := &mockBackendForPipeline{id: "fallback", version: "v1", caps: a.caps}
			o := NewOrchestrator(NewOracleStageWithBackend(fallback), NewStrikerStageWithBackend(fallback), NewCriticStageWithBackend(fallback)).WithRegistry(reg).WithRuntimePriority([]string{"omp", "codex"})
			c, err := o.Preflight(context.Background(), "three-face", "auto")
			if err != nil {
				t.Fatal(err)
			}
			ec := &engine.ExecutionContext{Backend: c.Runtime.ID, Workspace: t.TempDir(), ExecutionContract: c}
			before, _ := json.Marshal(ec)
			switch change {
			case "missing":
				reg.Unregister("omp")
			case "regression":
				b.caps = nil
			case "upgrade":
				a.version = "v2"
				b.version = "v2"
			case "priority":
				o.WithRuntimePriority([]string{"codex", "omp"})
			}
			_, trace, err := o.Execute(context.Background(), ModeThreeFace, ec)
			if change == "missing" || change == "regression" {
				if err == nil || len(calls) != 0 || trace == nil || trace.ModelCalls != 0 {
					t.Fatalf("unsafe execution %v %v", calls, err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(calls, []string{"codex:oracle", "omp:striker", "codex:critic"}) || fallback.calls != 0 {
				t.Fatalf("wrong stage runtimes: %v err=%v", calls, err)
			}
			after, _ := json.Marshal(ec)
			if string(before) != string(after) {
				t.Fatal("sealed context mutated")
			}
			if trace == nil || len(trace.Stages) != 3 {
				t.Fatal("missing trace")
			}
			for i, want := range []string{"codex", "omp", "codex"} {
				if trace.Stages[i].BackendID != want {
					t.Fatal("trace misreported runtime")
				}
			}
		})
	}
}

// 2026-10-04：STGBIND-015，新契約不能繞過 orchestrator 以 constructor 補位。
func TestStageBindingNoConstructorFallback(t *testing.T) {
	b := &mockBackendForPipeline{id: "opencode", version: "v1", caps: []backend.Capability{backend.CapReadOnlySandbox, backend.CapWorkspaceWrite}}
	reg := backend.NewRegistry()
	reg.Register(b)
	c, err := backend.AdmitRuntime(context.Background(), reg, "three-face", "opencode")
	if err != nil {
		t.Fatal(err)
	}
	ec := &engine.ExecutionContext{Backend: "opencode", Workspace: t.TempDir(), ExecutionContract: c}
	o := NewOracleStageWithBackend(b)
	s := NewStrikerStageWithBackend(b)
	v := NewCriticStageWithBackend(b)
	req, _ := o.Prepare(ec)
	if _, _, err = o.ExecutePrepared(context.Background(), ec, req); err == nil {
		t.Fatal("Oracle constructor fallback")
	}
	req, _ = s.Prepare(ec, nil)
	if _, _, err = s.ExecutePrepared(context.Background(), ec, req); err == nil {
		t.Fatal("Striker constructor fallback")
	}
	req, _ = v.Prepare(ec, nil, "")
	if _, _, err = v.ExecutePrepared(context.Background(), ec, req); err == nil {
		t.Fatal("Critic constructor fallback")
	}
	if b.calls != 0 {
		t.Fatal("runtime called")
	}
}
