package daemon

import (
	"asura/internal/auth"
	"asura/internal/backend"
	"asura/internal/engine"
	"asura/internal/storage"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// 2026-10-04：EXAUD-005/011/012/014，真 IPC 的 accepted query 與拒絕 projection。
func TestExecutionAuditIPC(t *testing.T) {
	root := t.TempDir()
	runtime := &contractRuntime{id: "codex", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite}}
	reg := backend.NewRegistry()
	reg.Register(runtime)
	_, client, s := contractDaemon(t, root, reg)
	req := engine.SubmitRequest{ID: "audit-query", Workspace: root, Prompt: "secret-sentinel", Backend: "auto"}
	task, err := client.Submit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	waitExpandedTask(t, client, task.ID)
	calls, probes := runtime.calls.Load(), runtime.versions.Load()
	r, err := client.GetTaskAudit(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	if r.Terminal == nil || r.Terminal.State != "completed" || strings.Contains(string(raw), "secret-sentinel") {
		t.Fatalf("unsafe query %s", raw)
	}
	reg.Unregister("codex")
	if _, err = client.Submit(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	again, err := client.GetTaskAudit(context.Background(), task.ID)
	if err != nil || len(again.Events) != len(r.Events) || calls != runtime.calls.Load() || probes != runtime.versions.Load() {
		t.Fatalf("query/redelivery changed execution %v", err)
	}
	if err = s.CreateTask(context.Background(), &storage.Task{ID: "blocked", Workspace: "/outside", Status: storage.StatusCompleted}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.GetTaskAudit(context.Background(), "blocked"); err == nil {
		t.Fatal("unauthorized audit leaked")
	}
	if err = json.NewEncoder(client.conn).Encode(IPCRequest{Action: "submit", RequestID: "rejected", Workspace: root, Prompt: "secret-sentinel", Backend: "codex"}); err != nil {
		t.Fatal(err)
	}
	var response IPCResponse
	if err = json.NewDecoder(client.conn).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.AdmissionRejection == nil || response.AdmissionRejection.Validate() != nil || response.Task != nil {
		t.Fatalf("missing rejection %+v", response)
	}
	raw, _ = json.Marshal(response.AdmissionRejection)
	if strings.Contains(string(raw), "secret-sentinel") {
		t.Fatal("rejection leaked prompt")
	}
	if _, err = s.GetTaskByRequestID(context.Background(), "rejected"); err == nil {
		t.Fatal("rejected Task persisted")
	}
}

// 2026-10-04：EXAUD-017，recovery audit 失敗不可忽略後開始派送。
func TestExecutionAuditRecoveryStartupFailure(t *testing.T) {
	root := t.TempDir()
	s, err := storage.NewSQLiteStore(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reg := backend.NewRegistry()
	runtime := &contractRuntime{id: "codex", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite}}
	reg.Register(runtime)
	c, err := backend.AdmitRuntime(context.Background(), reg, "direct", "codex")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(engine.ExecutionContext{Workspace: root, Backend: "codex", ExecutionContract: c})
	if err = s.CreateTask(context.Background(), &storage.Task{ID: "recovery", Workspace: root, Backend: "codex", ExecutionMode: "direct", Status: storage.StatusPending, ResolvedContext: string(raw)}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER fail_recovery BEFORE INSERT ON task_execution_audit_events WHEN json_extract(NEW.event,'$.kind')='recovery' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	p := auth.NewPolicy([]string{root})
	d := NewDaemon(Config{SocketPath: filepath.Join(root, "daemon.sock")}, s, p)
	defer d.Close()
	if err = d.Start(); err == nil || d.listener != nil || d.lockFile != nil {
		t.Fatalf("startup ignored recovery failure %v", err)
	}
	task, _ := s.GetTask(context.Background(), "recovery")
	if task.Status != storage.StatusPending || runtime.calls.Load() != 0 {
		t.Fatal("recovery partially committed or executed")
	}
	if _, err = db.Exec(`DROP TRIGGER fail_recovery`); err != nil {
		t.Fatal(err)
	}
	if err = d.Start(); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetTaskAudit(context.Background(), "recovery")
	if err != nil || r.Terminal == nil || r.Terminal.State != "failed" || r.Completeness != "partial" {
		t.Fatalf("recovery retry %+v %v", r, err)
	}
}
