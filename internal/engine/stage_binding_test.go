package engine_test

import (
	"asura/internal/auth"
	"asura/internal/backend"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

// 2026-10-04：STGBIND-015，Task.Backend 相容投影也須與唯一 plan 一致。
func TestStageBindingTaskProjection(t *testing.T) {
	root := t.TempDir()
	s, err := storage.NewSQLiteStore(filepath.Join(root, "task.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b := &localContractRuntime{version: "v1"}
	reg := backend.NewRegistry()
	reg.Register(b)
	c, err := backend.AdmitRuntime(context.Background(), reg, "direct", "opencode")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(engine.ExecutionContext{Backend: "opencode", Workspace: root, OriginalPrompt: "probe", ExecutionContract: c})
	task := &storage.Task{ID: "mismatch", Workspace: root, Prompt: "probe", Backend: "codex", ExecutionMode: "direct", Status: storage.StatusQueued, ResolvedContext: string(raw)}
	if err = s.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	o := pipeline.NewOrchestrator(nil, pipeline.NewStrikerStageWithBackend(b), nil).WithRegistry(reg)
	e := engine.NewEngine(s, auth.NewPolicy([]string{root}), engine.WithPipelineRunner(o))
	got, err := e.SubmitTask(context.Background(), engine.SubmitRequest{ID: task.ID, Workspace: root, Prompt: "probe", Backend: "opencode"})
	if err == nil || b.calls != 0 || got == nil || got.Status != storage.StatusFailed {
		t.Fatalf("projection inconsistency executed: task=%+v err=%v calls=%d", got, err, b.calls)
	}
}
