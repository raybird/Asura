package engine_test

import (
	"asura/internal/auth"
	"asura/internal/backend"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type executionAuditRuntime struct {
	id      string
	caps    []backend.Capability
	calls   atomic.Int32
	probes  atomic.Int32
	fail    bool
	invalid bool
	reject  bool
	empty   bool
}

func (b *executionAuditRuntime) ID() string { return b.id }
func (b *executionAuditRuntime) Version(context.Context) (string, error) {
	b.probes.Add(1)
	return "v1", nil
}
func (b *executionAuditRuntime) Capabilities() []backend.Capability { return b.caps }
func (b *executionAuditRuntime) HasCapability(c backend.Capability) bool {
	for _, x := range b.caps {
		if x == c {
			return true
		}
	}
	return false
}
func (b *executionAuditRuntime) Run(_ context.Context, r *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	b.calls.Add(1)
	if b.fail {
		return &backend.ExecutionResult{PartialOutput: strings.Repeat("secret-sentinel", 10000)}, errors.New(strings.Repeat("secret-sentinel", 10000))
	}
	out := "done"
	if r.Role == backend.RoleOracle {
		out = `{"goal":"g","steps":["s"]}`
	}
	if r.Role == backend.RoleCritic {
		out = `{"verdict":"pass"}`
	}
	if b.reject && r.Role == backend.RoleCritic {
		out = `{"verdict":"failed","summary":"reject"}`
	}
	if b.empty {
		out = ""
	}
	if b.invalid {
		out = "invalid-json"
	}
	return &backend.ExecutionResult{Output: out}, nil
}
func auditEngine(t *testing.T, mode string) (*storage.SQLiteStore, *engine.Engine, *backend.InMemoryRegistry, *executionAuditRuntime, *executionAuditRuntime, string) {
	t.Helper()
	root := t.TempDir()
	s, err := storage.NewSQLiteStore(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	reg := backend.NewRegistry()
	w := &executionAuditRuntime{id: "opencode", caps: []backend.Capability{backend.CapWorkspaceWrite}}
	r := &executionAuditRuntime{id: "codex", caps: []backend.Capability{backend.CapReadOnlySandbox}}
	reg.Register(w)
	reg.Register(r)
	o := pipeline.NewOrchestrator(pipeline.NewOracleStageWithBackend(r), pipeline.NewStrikerStageWithBackend(w), pipeline.NewCriticStageWithBackend(r)).WithRegistry(reg).WithRuntimePriority([]string{"codex", "opencode"})
	return s, engine.NewEngine(s, auth.NewPolicy([]string{root}), engine.WithPipelineRunner(o)), reg, w, r, root
}

// 2026-10-04：EXAUD-001/002/012/019，真 engine + pipeline + SQLite，不從 trace 猜 audit。
func TestExecutionAuditCompleted(t *testing.T) {
	for _, mode := range []string{"direct", "three-face"} {
		t.Run(mode, func(t *testing.T) {
			s, e, _, w, r, root := auditEngine(t, mode)
			task, err := e.SubmitTask(context.Background(), engine.SubmitRequest{ID: "completed", Workspace: root, Prompt: "private-prompt", Mode: mode, Backend: "auto"})
			if err != nil {
				t.Fatal(err)
			}
			calls, probes := w.calls.Load()+r.calls.Load(), w.probes.Load()+r.probes.Load()
			record, err := s.GetTaskAudit(context.Background(), task.ID)
			if err != nil {
				t.Fatalf("EXAUD-001/002 missing execution provenance: %v", err)
			}
			n := 1
			if mode == "three-face" {
				n = 3
			}
			if record.Completeness != "complete" || record.Terminal == nil || record.Terminal.State != "completed" || record.Terminal.CompletedStages != n || record.Terminal.RunIntents != n || record.Terminal.ConfirmedReturns != n || record.Terminal.ModelCalls != "unknown" {
				t.Fatalf("incorrect terminal %+v", record)
			}
			for i, st := range record.Stages {
				want := "opencode"
				if mode == "three-face" && i != 1 {
					want = "codex"
				}
				if st.SealedRuntime != want || st.ActualRuntime != want || st.Guard.State != "passed" || st.Status != "completed" {
					t.Fatalf("stage %d %+v", i, st)
				}
			}
			raw, _ := json.Marshal(record)
			if strings.Contains(string(raw), "private-prompt") {
				t.Fatal("prompt leaked")
			}
			if calls != w.calls.Load()+r.calls.Load() || probes != w.probes.Load()+r.probes.Load() {
				t.Fatal("query invoked runtime")
			}
		})
	}
}

// 2026-10-04：EXAUD-006/008/018，runtime failure 與 stage parsing failure 分開。
func TestExecutionAuditFailures(t *testing.T) {
	for _, kind := range []string{"missing", "runtime", "processing"} {
		t.Run(kind, func(t *testing.T) {
			s, e, reg, w, r, root := auditEngine(t, "three-face")
			if kind == "runtime" {
				w.fail = true
			}
			if kind == "processing" {
				r.invalid = true
			}
			req := engine.SubmitRequest{ID: "failure", Workspace: root, Prompt: "private", Mode: "three-face", Backend: "auto"}
			if kind == "missing" {
				c, err := e.Preflight(context.Background(), req.Mode, req.Backend)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(engine.ExecutionContext{Workspace: root, Backend: c.Runtime.ID, ExecutionContract: c})
				if err = s.CreateTask(context.Background(), &storage.Task{ID: req.ID, Workspace: root, Backend: c.Runtime.ID, ExecutionMode: req.Mode, Status: storage.StatusPending, ResolvedContext: string(raw)}); err != nil {
					t.Fatal(err)
				}
				reg.Unregister("opencode")
			}
			task, err := e.SubmitTask(context.Background(), req)
			if err == nil || task == nil || task.Status != storage.StatusFailed {
				t.Fatalf("missing failure %v %+v", err, task)
			}
			record, err := s.GetTaskAudit(context.Background(), req.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantLayer := "runtime"
			wantStage := backend.RoleStriker
			if kind == "missing" {
				wantLayer = "guard"
				if w.calls.Load()+r.calls.Load() != 0 {
					t.Fatal("guard failed after Run")
				}
			}
			if kind == "processing" {
				wantLayer = "processing"
				wantStage = backend.RoleOracle
			}
			if record.Terminal == nil || record.Terminal.FailureLayer != wantLayer || record.Terminal.FailingStage != wantStage {
				t.Fatalf("misclassified %+v", record.Terminal)
			}
			raw, _ := json.Marshal(record)
			if strings.Contains(string(raw), "secret-sentinel") {
				t.Fatal("runtime secret leaked")
			}
		})
	}
}

type cancellingAuditRuntime struct {
	executionAuditRuntime
	phase   string
	entered chan struct{}
	block   atomic.Bool
}

func (b *cancellingAuditRuntime) Version(ctx context.Context) (string, error) {
	if b.block.Load() && b.phase == "during_guard" {
		close(b.entered)
		<-ctx.Done()
		return "", ctx.Err()
	}
	return b.executionAuditRuntime.Version(ctx)
}
func (b *cancellingAuditRuntime) Run(ctx context.Context, r *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	b.calls.Add(1)
	close(b.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

// 2026-10-04：EXAUD-009，用 barrier 固定取消發生位置，不靠 sleep 猜競態。
func TestExecutionAuditCancellation(t *testing.T) {
	for _, phase := range []string{"before_stage", "during_guard", "during_run"} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			s, err := storage.NewSQLiteStore(filepath.Join(root, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			b := &cancellingAuditRuntime{executionAuditRuntime: executionAuditRuntime{id: "codex", caps: []backend.Capability{backend.CapWorkspaceWrite}}, phase: phase, entered: make(chan struct{})}
			reg := backend.NewRegistry()
			reg.Register(b)
			o := pipeline.NewOrchestrator(nil, pipeline.NewStrikerStageWithBackend(b), nil).WithRegistry(reg)
			e := engine.NewEngine(s, auth.NewPolicy([]string{root}), engine.WithPipelineRunner(o))
			c, err := e.Preflight(context.Background(), "direct", "codex")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(engine.ExecutionContext{Backend: "codex", Workspace: root, ExecutionContract: c})
			if err = s.CreateTask(context.Background(), &storage.Task{ID: "cancel", Workspace: root, Backend: "codex", ExecutionMode: "direct", Status: storage.StatusQueued, ResolvedContext: string(raw)}); err != nil {
				t.Fatal(err)
			}
			b.block.Store(true)
			done := make(chan error, 1)
			if phase != "before_stage" {
				go func() {
					_, err := e.SubmitTask(context.Background(), engine.SubmitRequest{ID: "cancel", Workspace: root, Backend: "codex"})
					done <- err
				}()
				select {
				case <-b.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("boundary not reached")
				}
			}
			if err = e.CancelTask("cancel"); err != nil {
				t.Fatal(err)
			}
			if phase != "before_stage" {
				select {
				case err = <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("unexpected cancellation %v", err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("runner not stopped")
				}
			}
			r, err := s.GetTaskAudit(context.Background(), "cancel")
			if err != nil {
				t.Fatal(err)
			}
			if r.Terminal == nil || r.Terminal.State != "cancelled" || r.Terminal.CancelPhase != phase || r.Terminal.CompletedStages != 0 {
				t.Fatalf("cancel phase %+v", r.Terminal)
			}
			calls := int32(0)
			if phase == "during_run" {
				calls = 1
			}
			if b.calls.Load() != calls {
				t.Fatalf("phantom calls=%d", b.calls.Load())
			}
			before := len(r.Events)
			if err = e.CancelTask("cancel"); err != nil {
				t.Fatal(err)
			}
			r, _ = s.GetTaskAudit(context.Background(), "cancel")
			if len(r.Events) != before {
				t.Fatal("duplicate terminal")
			}
		})
	}
}

// 2026-10-04：EXAUD-007，sealed identity 仍是 execution authority。
func TestExecutionAuditIdentityMismatch(t *testing.T) {
	s, e, reg, w, _, root := auditEngine(t, "direct")
	c, err := backend.AdmitRuntime(context.Background(), reg, "direct", "opencode")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(engine.ExecutionContext{Backend: "opencode", Workspace: root, ExecutionContract: c})
	if err = s.CreateTask(context.Background(), &storage.Task{ID: "identity", Workspace: root, Backend: "opencode", ExecutionMode: "direct", Status: storage.StatusPending, ResolvedContext: string(raw)}); err != nil {
		t.Fatal(err)
	}
	w.id = "wrong-identity"
	if _, err = e.SubmitTask(context.Background(), engine.SubmitRequest{ID: "identity", Workspace: root, Backend: "opencode"}); err == nil {
		t.Fatal("identity drift accepted")
	}
	r, err := s.GetTaskAudit(context.Background(), "identity")
	if err != nil {
		t.Fatal(err)
	}
	if w.calls.Load() != 0 || r.Stages[0].SealedRuntime != "opencode" || r.Stages[0].ActualRuntime != "" || r.Terminal.Reason != "identity_mismatch" {
		t.Fatalf("identity mismatch audit %+v", r)
	}
}

// 2026-10-04：EXAUD-017，真 SQLite observation fault 阻止首次或後續 Run。
func TestExecutionAuditObservationFailureStopsExecution(t *testing.T) {
	for _, boundary := range []string{"run_intent", "stage_outcome"} {
		t.Run(boundary, func(t *testing.T) {
			s, e, _, w, r, root := auditEngine(t, "three-face")
			db, err := sql.Open("sqlite", filepath.Join(root, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			_, err = db.Exec(`CREATE TRIGGER deny_observation BEFORE INSERT ON task_execution_audit_events WHEN json_extract(NEW.event,'$.kind')='` + boundary + `' BEGIN SELECT RAISE(FAIL,'injected'); END`)
			if err != nil {
				t.Fatal(err)
			}
			task, err := e.SubmitTask(context.Background(), engine.SubmitRequest{ID: "fault", Workspace: root, Prompt: "private", Mode: "three-face", Backend: "auto"})
			if err == nil || task == nil || task.Status != storage.StatusFailed {
				t.Fatalf("fault accepted: %+v %v", task, err)
			}
			want := int32(0)
			if boundary == "stage_outcome" {
				want = 1
			}
			if w.calls.Load() != 0 || r.calls.Load() != want {
				t.Fatalf("side effect after persistence failure: writer=%d reader=%d", w.calls.Load(), r.calls.Load())
			}
			record, err := s.GetTaskAudit(context.Background(), "fault")
			if err != nil {
				t.Fatal(err)
			}
			if record.Terminal == nil || record.Terminal.Reason != "audit_persistence_failed" || record.Terminal.State != "failed" {
				t.Fatalf("incorrect durable failure: %+v", record.Terminal)
			}
		})
	}
}

// 2026-10-04：EXAUD-018，stage 完成與 Task verdict/validation 互不混稱。
func TestExecutionAuditTaskTerminalLayers(t *testing.T) {
	for _, kind := range []string{"critic", "task"} {
		t.Run(kind, func(t *testing.T) {
			mode := "three-face"
			if kind == "task" {
				mode = "direct"
			}
			s, e, _, w, r, root := auditEngine(t, mode)
			r.reject = kind == "critic"
			w.empty = kind == "task"
			task, err := e.SubmitTask(context.Background(), engine.SubmitRequest{ID: "terminal", Workspace: root, Prompt: "private", Mode: mode, Backend: "auto"})
			if err == nil || task == nil || task.Status != storage.StatusFailed {
				t.Fatalf("failure absent %+v %v", task, err)
			}
			record, err := s.GetTaskAudit(context.Background(), task.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := 3
			reason := "critic_rejected"
			if kind == "task" {
				want = 1
				reason = "result_invalid"
			}
			if record.Terminal == nil || record.Terminal.FailureLayer != kind || record.Terminal.Reason != reason || record.Terminal.CompletedStages != want || record.Terminal.ConfirmedReturns != want {
				t.Fatalf("terminal conflates lifecycle %+v", record.Terminal)
			}
			for _, st := range record.Stages {
				if st.Status != "completed" {
					t.Fatalf("completed stage rewritten %+v", st)
				}
			}
		})
	}
}
