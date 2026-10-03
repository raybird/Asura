package engine_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"asura/internal/auth"
	"asura/internal/backend"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
)

type localContractRuntime struct {
	calls   int
	prompt  string
	version string
}

func (b *localContractRuntime) ID() string                              { return "opencode" }
func (b *localContractRuntime) Version(context.Context) (string, error) { return b.version, nil }
func (b *localContractRuntime) Capabilities() []backend.Capability {
	return []backend.Capability{backend.CapWorkspaceWrite}
}
func (b *localContractRuntime) HasCapability(c backend.Capability) bool {
	return c == backend.CapWorkspaceWrite
}
func (b *localContractRuntime) Run(_ context.Context, r *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	b.calls++
	b.prompt = r.Prompt
	return &backend.ExecutionResult{Output: "done"}, nil
}

// 2026-10-03：RTCAP-001／007／016，本機 runtime engine 的新任務與 persisted context 權威性。
func TestCapabilityLocalRuntimeAndPersistedAuthority(t *testing.T) {
	root := t.TempDir()
	s, err := storage.NewSQLiteStore(filepath.Join(root, "local.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b := &localContractRuntime{version: "local-V1"}
	reg := backend.NewRegistry()
	reg.Register(b)
	o := pipeline.NewOrchestrator(nil, pipeline.NewStrikerStageWithBackend(b), nil).WithRegistry(reg)
	e := engine.NewEngine(s, auth.NewPolicy([]string{root}), engine.WithPipelineRunner(o))
	task, err := e.SubmitTask(context.Background(), engine.SubmitRequest{ID: "local", Workspace: root, Prompt: "original"})
	if err != nil {
		t.Fatal(err)
	}
	var ec engine.ExecutionContext
	if err := json.Unmarshal([]byte(task.ResolvedContext), &ec); err != nil || ec.ExecutionContract == nil || ec.ExecutionContract.Runtime.Version != "local-V1" {
		t.Fatalf("local not sealed: %v", err)
	}
	if b.calls != 1 || b.prompt != "original" {
		t.Fatal("Direct execution changed")
	}
	reg.Unregister("opencode")
	same, err := e.SubmitTask(context.Background(), engine.SubmitRequest{ID: "local", Workspace: root, Prompt: "original"})
	if err != nil || same.ResolvedContext != task.ResolvedContext || b.calls != 1 {
		t.Fatalf("local redelivery: %v", err)
	}
	reg.Register(b)
	// 保存 queued fixture，再傳入相異呼叫端 context；執行只能使用保存的 prompt 與 contract。
	queued := &storage.Task{ID: "queued", Workspace: root, Prompt: "original", Backend: "opencode", Status: storage.StatusQueued, ExecutionMode: "direct", ResolvedContext: task.ResolvedContext}
	if err := s.CreateTask(context.Background(), queued); err != nil {
		t.Fatal(err)
	}
	_, err = e.SubmitTask(context.Background(), engine.SubmitRequest{ID: "queued", Workspace: root, Prompt: "changed", ResolvedContext: &engine.ExecutionContext{OriginalPrompt: "changed", Workspace: root, Backend: "opencode"}})
	if err != nil || b.prompt != "original" {
		t.Fatalf("caller context overwrote persisted task: %v %q", err, b.prompt)
	}
}
