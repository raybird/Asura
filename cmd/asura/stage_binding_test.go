package main

import (
	"asura/internal/backend"
	"asura/internal/engine"
	"asura/internal/storage"
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// 2026-10-04：STGBIND-014，真正 persisted mixed plan 的 CLI audit，無 runtime binaries。
func TestStageBindingStatus(t *testing.T) {
	t.Setenv("ASURA_SOCKET", "")
	reg := backend.NewRegistry()
	reg.Register(&statusStageRuntime{id: "omp", caps: []backend.Capability{backend.CapWorkspaceWrite}})
	reg.Register(&statusStageRuntime{id: "codex", caps: []backend.Capability{backend.CapWorkspaceWrite, backend.CapReadOnlySandbox}})
	c, err := backend.AdmitRuntime(context.Background(), reg, "three-face", "auto", []string{"omp", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(engine.ExecutionContext{Backend: "omp", ExecutionContract: c})
	db := filepath.Join(t.TempDir(), "status.db")
	s, err := storage.NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	task := &storage.Task{ID: "mixed", Backend: "omp", ExecutionMode: "three-face", Status: storage.StatusCompleted, ResolvedContext: string(data), PipelineTrace: `{"stages":[{"name":"oracle","backend_id":"codex","backend_version":"v2","execution_guard":{"compatible":true}},{"name":"striker","backend_id":"omp","backend_version":"v1","execution_guard":{"compatible":true}},{"name":"critic","backend_id":"codex","backend_version":"v2","execution_guard":{"compatible":true}}]}`}
	if err = s.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	reg.Unregister("omp")
	reg.Unregister("codex")
	var out, stderr bytes.Buffer
	if code := handleStatus([]string{"--db", db, task.ID}, &out, &stderr); code != 0 {
		t.Fatalf("status %s", stderr.String())
	}
	for _, want := range []string{"Stage oracle: codex v1", "Stage striker: omp v1", "Stage critic: codex v1", "Actual runtime: codex v2", "Execution guard: compatible"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s: %s", want, out.String())
		}
	}
	stored, _ := s.GetTask(context.Background(), task.ID)
	if stored.ResolvedContext != task.ResolvedContext {
		t.Fatal("audit changed plan")
	}
}

type statusStageRuntime struct {
	id   string
	caps []backend.Capability
}

func (b *statusStageRuntime) ID() string                              { return b.id }
func (b *statusStageRuntime) Version(context.Context) (string, error) { return "v1", nil }
func (b *statusStageRuntime) Capabilities() []backend.Capability      { return b.caps }
func (b *statusStageRuntime) HasCapability(c backend.Capability) bool {
	for _, v := range b.caps {
		if c == v {
			return true
		}
	}
	return false
}
func (b *statusStageRuntime) Run(context.Context, *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	panic("audit invoked runtime")
}
