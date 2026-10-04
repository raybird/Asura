package storage

import (
	"asura/internal/audit"
	"asura/internal/backend"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type auditRuntime struct{}

func (*auditRuntime) ID() string                              { return "codex" }
func (*auditRuntime) Version(context.Context) (string, error) { return "v1", nil }
func (*auditRuntime) Capabilities() []backend.Capability {
	return []backend.Capability{backend.CapWorkspaceWrite, backend.CapReadOnlySandbox}
}
func (*auditRuntime) HasCapability(backend.Capability) bool { return true }
func (*auditRuntime) Run(context.Context, *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	panic("must not run")
}
func executionAuditTask(t *testing.T, id string) *Task {
	t.Helper()
	reg := backend.NewRegistry()
	reg.Register(&auditRuntime{})
	c, err := backend.AdmitRuntime(context.Background(), reg, "three-face", "auto")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(struct {
		Workspace string                     `json:"workspace"`
		Contract  *backend.ExecutionContract `json:"execution_contract"`
		Recall    *UtilityRecall             `json:"recall"`
	}{
		Workspace: "/a", Contract: c, Recall: &UtilityRecall{RecallID: id, Mode: "bounded", UtilityPolicy: "observe", UtilityVersion: UtilityVersion, AutoIDs: []string{"m"}, Selected: []UtilitySelection{{ID: "m", Score: 1, WeightPPM: 1000000, FinalScore: 1000000}}},
	})
	return &Task{ID: id, RequestID: id, Workspace: "/a", Backend: "codex", ExecutionMode: "three-face", Status: StatusPending, ResolvedContext: string(raw), CreatedAt: time.Now().UTC()}
}

// 2026-10-04：EXAUD-017，真 SQLite rollback 與對照組，非缺少 API 的編譯紅燈。
func TestExecutionAuditAdmissionAtomic(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateTask(ctx, executionAuditTask(t, "good")); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM task_execution_audits WHERE task_id='good'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("EXAUD-017 missing durable admission: count=%d err=%v", count, err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON task_execution_audits BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateTask(ctx, executionAuditTask(t, "fail")); err == nil {
		t.Fatal("audit failure accepted Task")
	}
	for _, table := range []string{"tasks", "recall_events", "task_execution_audits"} {
		if err = s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s partial commit count=%d err=%v", table, count, err)
		}
	}
}

// 2026-10-04：EXAUD-009/010/011/015/017，真交易、故障注入與 reopen。
func TestExecutionAuditTerminalAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	task := executionAuditTask(t, "crash")
	if err = s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateTaskStatusIfPendingOrQueued(ctx, task.ID, StatusRunning); err != nil {
		t.Fatal(err)
	}
	events := []audit.Event{
		{Kind: "guard_started", Stage: backend.RoleOracle, Scope: "stage_execution", State: "running", Reason: "none", Layer: "guard"},
		{Kind: "guard_result", Stage: backend.RoleOracle, Scope: "stage_execution", State: "passed", Reason: "none", Layer: "guard", RuntimeID: "codex", RuntimeVersion: "v1"},
		{Kind: "run_intent", Stage: backend.RoleOracle, State: "running", Reason: "none", Layer: "runtime", RuntimeID: "codex", RuntimeVersion: "v1"},
		{Kind: "run_returned", Stage: backend.RoleOracle, State: "completed", Reason: "none", Layer: "runtime", RuntimeID: "codex", RuntimeVersion: "v1"},
		{Kind: "stage_outcome", Stage: backend.RoleOracle, State: "completed", Reason: "none"},
		{Kind: "guard_started", Stage: backend.RoleStriker, Scope: "stage_execution", State: "running", Reason: "none", Layer: "guard"},
		{Kind: "guard_result", Stage: backend.RoleStriker, Scope: "stage_execution", State: "passed", Reason: "none", Layer: "guard", RuntimeID: "codex", RuntimeVersion: "v1"},
		{Kind: "run_intent", Stage: backend.RoleStriker, State: "running", Reason: "none", Layer: "runtime", RuntimeID: "codex", RuntimeVersion: "v1"},
	}
	for _, e := range events {
		if err = s.AppendTaskAudit(ctx, task.ID, e); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.AppendTaskAudit(ctx, task.ID, events[len(events)-1]); err != nil {
		t.Fatal(err)
	}
	bad := events[len(events)-1]
	bad.RuntimeVersion = "v2"
	if err = s.AppendTaskAudit(ctx, task.ID, bad); err != audit.ErrConflict {
		t.Fatalf("idempotent conflict %v", err)
	}
	s.Close()
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.RecoverInterruptedTasks(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetTaskAudit(ctx, task.ID)
	if err != nil {
		t.Fatalf("EXAUD-010 missing crash provenance: %v", err)
	}
	if r.Completeness != "partial" || r.Stages[0].Status != "completed" || r.Stages[1].Status != "interrupted" || r.Stages[2].Reachability != "not_reached" || r.Terminal == nil || r.Terminal.State != "failed" || r.Terminal.InvocationEvidence != "unknown" {
		t.Fatalf("fabricated crash history %+v", r)
	}
	before := len(r.Events)
	if _, err = s.RecoverInterruptedTasks(ctx); err != nil {
		t.Fatal(err)
	}
	r, _ = s.GetTaskAudit(ctx, task.ID)
	if len(r.Events) != before {
		t.Fatal("duplicate recovery")
	}
}

func TestExecutionAuditTerminalRollback(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateTask(ctx, executionAuditTask(t, "cancel")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_terminal BEFORE INSERT ON task_execution_audit_events WHEN json_extract(NEW.event,'$.kind')='terminal' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateTaskStatusIfPendingOrQueuedWithMsg(ctx, "cancel", StatusCancelled, "", "cancelled"); err == nil {
		t.Fatal("terminal failure accepted")
	}
	task, err := s.GetTask(ctx, "cancel")
	if err != nil || task.Status != StatusPending {
		t.Fatalf("partial terminal %+v %v", task, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER fail_terminal`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateTaskStatusIfPendingOrQueuedWithMsg(ctx, "cancel", StatusCancelled, "", "cancelled"); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetTaskAudit(ctx, "cancel")
	if err != nil || r.Terminal == nil || r.Terminal.State != "cancelled" || r.Terminal.RunIntents != 0 {
		t.Fatalf("cancel audit %+v %v", r, err)
	}
}

type auditInventoryRuntime struct {
	auditRuntime
	id   string
	caps []backend.Capability
	err  error
}

func (b *auditInventoryRuntime) ID() string { return b.id }
func (b *auditInventoryRuntime) Version(context.Context) (string, error) {
	if b.err != nil {
		return "", b.err
	}
	return "v1", nil
}
func (b *auditInventoryRuntime) Capabilities() []backend.Capability { return b.caps }
func TestExecutionAuditNormalizationAndTamper(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	reg := backend.NewRegistry()
	reg.Register(&auditInventoryRuntime{id: "codex", caps: []backend.Capability{backend.CapReadOnlySandbox}})
	reg.Register(&auditInventoryRuntime{id: "opencode", caps: []backend.Capability{backend.CapWorkspaceWrite}})
	reg.Register(&auditInventoryRuntime{id: "omp", err: errors.New(strings.Repeat("secret-sentinel", 10000))})
	c, err := backend.AdmitRuntime(ctx, reg, "three-face", "auto")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(struct {
		Contract *backend.ExecutionContract `json:"execution_contract"`
	}{c})
	task := &Task{ID: "normalized", RequestID: "user-request", Workspace: "/a", Backend: c.Runtime.ID, ExecutionMode: "three-face", Status: StatusPending, ResolvedContext: string(raw)}
	if err = s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetTaskAudit(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Header.Admission.Runtimes) != 3 || r.Header.Admission.Runtimes[1].ID != "omp" || r.Header.Admission.Runtimes[1].Availability != backend.Unavailable || r.Header.Admission.Runtimes[1].Reason != backend.ProbeFailed {
		t.Fatalf("availability provenance %+v", r.Header.Admission)
	}
	if r.Header.RequestDigest == "user-request" || len(r.Header.RequestDigest) != 64 {
		t.Fatal("raw request identity copied")
	}
	for i, b := range r.Header.Admission.Bindings {
		want := "codex"
		if i == 1 {
			want = "opencode"
		}
		if b.Selected != want || len(b.Candidates) != 2 || b.Policy != backend.SelectionPolicyID || b.PolicyVersion != "v1" {
			t.Fatalf("decision %d %+v", i, b)
		}
	}
	if len(r.Header.Admission.Bindings[0].Candidates[1].Missing) != 1 || r.Header.Admission.Bindings[0].Candidates[1].Missing[0] != backend.CapReadOnlySandbox {
		t.Fatal("capability rejection lost")
	}
	header, _ := json.Marshal(r.Header)
	if strings.Contains(string(header), "secret-sentinel") || strings.Contains(string(header), "selection_decision") {
		t.Fatal("secret or contract duplication")
	}
	before := audit.Digest(c)
	h, err := audit.NewHeader("clone", "", task.CreatedAt, c)
	if err != nil {
		t.Fatal(err)
	}
	h.Admission.Bindings[0].Requirements.RequiredCapabilities[0] = backend.CapWorkspaceWrite
	if audit.Digest(c) != before {
		t.Fatal("audit mutation changed execution authority")
	}
	for _, query := range []string{
		`UPDATE task_execution_audits SET digest='bad' WHERE task_id='normalized'`,
		`UPDATE task_execution_audits SET header=json_set(header,'$.admission.bindings[0].stage','striker') WHERE task_id='normalized'`,
		`UPDATE tasks SET resolved_context=json_set(resolved_context,'$.execution_contract.runtime.version','v2') WHERE id='normalized'`,
	} {
		tx, _ := s.db.Begin()
		if _, err = tx.Exec(query); err != nil {
			t.Fatal(err)
		}
		tx.Commit()
		if _, err = s.GetTaskAudit(ctx, task.ID); err != audit.ErrInvalid {
			t.Fatalf("tamper accepted: %v", err)
		}
		// Restore the known immutable fixture after each independent mutation.
		if _, err = s.db.Exec(`UPDATE tasks SET resolved_context=? WHERE id=?`, task.ResolvedContext, task.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(`UPDATE task_execution_audits SET header=?,digest=? WHERE task_id=?`, string(header), audit.Digest(r.Header), task.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExecutionAuditEventOrderAndBounds(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateTask(ctx, executionAuditTask(t, "bounds")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateTaskStatusIfPendingOrQueued(ctx, "bounds", StatusRunning); err != nil {
		t.Fatal(err)
	}
	for _, event := range []audit.Event{
		{Kind: "guard_started", Stage: backend.RoleCritic, Scope: "stage_execution", State: "running", Reason: "none", Layer: "guard"},
		{Kind: "guard_result", Stage: backend.RoleOracle, Scope: "stage_execution", State: "passed", Reason: "none", Layer: "guard", RuntimeID: "codex", RuntimeVersion: strings.Repeat("x", 257)},
		{Kind: "run_returned", Stage: backend.RoleOracle, State: "completed", Reason: "none", Layer: "runtime", RuntimeID: "codex", RuntimeVersion: "v1"},
	} {
		if err = s.AppendTaskAudit(ctx, "bounds", event); err == nil {
			t.Fatalf("invalid event accepted %+v", event)
		}
	}
	r, err := s.GetTaskAudit(ctx, "bounds")
	if err != nil || len(r.Events) != 1 {
		t.Fatalf("failed event was not atomic %+v %v", r, err)
	}
	for _, query := range []string{
		`UPDATE task_execution_audit_events SET seq=2 WHERE task_id='bounds'`,
		`UPDATE task_execution_audit_events SET event_key='foreign' WHERE task_id='bounds'`,
		`UPDATE task_execution_audit_events SET event=json_set(event,'$.kind','terminal') WHERE task_id='bounds'`,
	} {
		if _, err = s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
		if _, err = s.GetTaskAudit(ctx, "bounds"); err != audit.ErrInvalid {
			t.Fatalf("event tamper accepted %v", err)
		}
		original, _ := json.Marshal(r.Events[0])
		if _, err = s.db.Exec(`UPDATE task_execution_audit_events SET seq=1,event_key=?,event=? WHERE task_id='bounds'`, r.Events[0].Key, string(original)); err != nil {
			t.Fatal(err)
		}
	}
	// SQLite byte checks enforce the boundary even when bypassing the Go writer.
	if _, err = s.db.Exec(`UPDATE task_execution_audit_events SET event=? WHERE task_id='bounds'`, strings.Repeat("x", audit.MaxEventBytes+1)); err == nil {
		t.Fatal("DB event byte bound missing")
	}
	if _, err = s.db.Exec(`UPDATE task_execution_audits SET header=? WHERE task_id='bounds'`, strings.Repeat("x", audit.MaxHeaderBytes+1)); err == nil {
		t.Fatal("DB header byte bound missing")
	}
}

func TestExecutionAuditMissingHeaderIsNotLegacy(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateTask(ctx, executionAuditTask(t, "deleted")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DELETE FROM task_execution_audits WHERE task_id='deleted'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetTaskAudit(ctx, "deleted"); err != audit.ErrInvalid {
		t.Fatalf("missing new audit reported legacy: %v", err)
	}
	if _, err = s.UpdateTaskStatusIfPendingOrQueued(ctx, "deleted", StatusRunning); err == nil {
		t.Fatal("corrupt audit allowed execution start")
	}
	task, _ := s.GetTask(ctx, "deleted")
	if task.Status != StatusPending {
		t.Fatal("partial start commit")
	}
}

// 2026-10-04：EXAUD-013，模擬 migration 前三版 sealed Task，不回填 events。
func TestExecutionAuditLegacyContracts(t *testing.T) {
	for _, version := range []string{backend.CapabilityContractVersion, backend.StageBindingContractVersion, backend.InventoryContractVersion} {
		t.Run(version, func(t *testing.T) {
			s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			reg := backend.NewRegistry()
			reg.Register(&auditRuntime{})
			c, err := backend.AdmitRuntime(context.Background(), reg, "direct", "codex")
			if err != nil {
				t.Fatal(err)
			}
			if version != backend.InventoryContractVersion {
				c.InventorySnapshot = nil
				c.ContractVersion = backend.StageBindingContractVersion
			}
			if version == backend.CapabilityContractVersion {
				c.StageBindingPlan = nil
				c.SelectionDecision = nil
				c.ContractVersion = version
			}
			raw, _ := json.Marshal(map[string]any{"execution_contract": c})
			// 舊 schema row 的字面 fixture：不經新的 CreateTask admission writer。
			_, err = s.db.Exec(`INSERT INTO tasks(id,workspace,prompt,status,result,execution_mode,backend,resolved_context,created_at,updated_at) VALUES('legacy','/a','secret-sentinel','completed','secret-sentinel','direct','codex',?,?,?)`, string(raw), time.Now().UTC(), time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.GetTaskAudit(context.Background(), "legacy")
			if err != nil {
				t.Fatal(err)
			}
			if r.Completeness != "legacy" || r.Header.ContractVersion != version || r.Terminal != nil || len(r.Events) != 0 || len(r.Stages) != 1 || r.Stages[0].ActualRuntime != "" || r.Validate() != nil {
				t.Fatalf("invented history %+v", r)
			}
			for _, table := range []string{"task_execution_audits", "task_execution_audit_events"} {
				var count int
				if s.db.QueryRow(`SELECT count(*) FROM `+table).Scan(&count) != nil || count != 0 {
					t.Fatal("query backfilled audit")
				}
			}
		})
	}
}
