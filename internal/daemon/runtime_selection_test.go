package daemon

import (
	"asura/internal/backend"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 2026-10-03：RTSEL-002/005/006/009/010，真 IPC、matcher、SQLite 與執行次數。
func TestRuntimeSelectionAdmissionAndRedelivery(t *testing.T) {
	root := t.TempDir()
	a := &contractRuntime{id: "opencode", version: "v1"}
	b := &contractRuntime{id: "codex", version: "v2", caps: []backend.Capability{backend.CapWorkspaceWrite}}
	reg := backend.NewRegistry()
	reg.Register(a)
	reg.Register(b)
	_, c, s := contractDaemon(t, root, reg)
	marker := filepath.Join(root, "sentinel")
	os.WriteFile(marker, []byte("unchanged"), 0600)
	_, err := c.Submit(context.Background(), engine.SubmitRequest{ID: "explicit", Workspace: root, Prompt: "probe", Backend: "opencode"})
	if err == nil || a.calls.Load() != 0 || b.calls.Load() != 0 {
		t.Fatal("explicit fell back")
	}
	req := engine.SubmitRequest{ID: "auto-request", Workspace: root, Prompt: "probe", Backend: "auto"}
	task, err := c.Submit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if task.Backend != "codex" {
		t.Fatalf("selected=%s", task.Backend)
	}
	var sealed map[string]any
	if err = json.Unmarshal([]byte(task.ResolvedContext), &sealed); err != nil {
		t.Fatal(err)
	}
	contract := sealed["execution_contract"].(map[string]any)
	if contract["selection_decision"] == nil {
		t.Fatal("missing sealed decision")
	}
	done := waitExpandedTask(t, c, task.ID)
	if done.Status != storage.StatusCompleted || b.calls.Load() != 1 {
		t.Fatalf("task=%+v calls=%d", done, b.calls.Load())
	}
	count := b.versions.Load()
	reg.Unregister("codex")
	reg.Unregister("opencode")
	same, err := c.Submit(context.Background(), req)
	if err != nil || same.ID != task.ID || same.ResolvedContext != task.ResolvedContext || b.versions.Load() != count || b.calls.Load() != 1 {
		t.Fatalf("redelivery drift: %v", err)
	}
	req.Backend = "codex"
	if _, err = c.Submit(context.Background(), req); err == nil {
		t.Fatal("intent collision accepted")
	}
	stored, _ := s.GetTask(context.Background(), task.ID)
	if stored.ResolvedContext != task.ResolvedContext {
		t.Fatal("snapshot overwritten")
	}
	data, _ := os.ReadFile(marker)
	if string(data) != "unchanged" {
		t.Fatal("workspace changed")
	}
}

// 2026-10-03：RTSEL-006，錯誤必須提供已盤點候選及缺失能力。
func TestRuntimeSelectionNoEligible(t *testing.T) {
	root := t.TempDir()
	b := &contractRuntime{id: "omp", version: "v1"}
	reg := backend.NewRegistry()
	reg.Register(b)
	_, c, s := contractDaemon(t, root, reg)
	_, err := c.Submit(context.Background(), engine.SubmitRequest{ID: "none", Workspace: root, Prompt: "probe", Backend: "auto"})
	if err == nil || !strings.Contains(err.Error(), "omp") || !strings.Contains(err.Error(), "workspace_write") {
		t.Fatalf("missing eligibility audit: %v", err)
	}
	if b.calls.Load() != 0 {
		t.Fatal("unsafe runtime executed")
	}
	if _, err = s.GetTaskByRequestID(context.Background(), "none"); err == nil {
		t.Fatal("rejected task persisted")
	}
}

func TestRuntimeSelectionRestart(t *testing.T) {
	root := t.TempDir()
	old := &contractRuntime{id: "opencode", version: "V1", caps: []backend.Capability{backend.CapWorkspaceWrite}}
	reg := backend.NewRegistry()
	reg.Register(old)
	d, c, s := contractDaemon(t, root, reg)
	req := engine.SubmitRequest{ID: "restart", Workspace: root, Prompt: "original", Backend: "auto"}
	task, err := c.Submit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	waitExpandedTask(t, c, task.ID)
	c.Close()
	d.Close()
	s.Close()
	_, again, _ := contractDaemon(t, root, backend.NewRegistry())
	same, err := again.Submit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if same.ID != task.ID || same.ResolvedContext != task.ResolvedContext || old.calls.Load() != 1 {
		t.Fatal("restart changed execution identity")
	}
}

func TestRuntimeSelectionConcurrentWinner(t *testing.T) {
	root := t.TempDir()
	b := &contractRuntime{id: "opencode", version: "V1", caps: []backend.Capability{backend.CapWorkspaceWrite}}
	reg := backend.NewRegistry()
	reg.Register(b)
	_, c, s := contractDaemon(t, root, reg)
	const n = 6
	results := make(chan *storage.Task, n)
	errs := make(chan error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, err := NewClient(filepath.Join(root, "daemon.sock"))
			if err != nil {
				errs <- err
				return
			}
			defer client.Close()
			<-start
			task, err := client.Submit(context.Background(), engine.SubmitRequest{ID: "concurrent", Workspace: root, Prompt: "same", Backend: "auto"})
			if err != nil {
				errs <- err
				return
			}
			results <- task
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		t.Fatal(err)
	}
	var winner *storage.Task
	count := 0
	for task := range results {
		count++
		if winner == nil {
			winner = task
		}
		if task.ID != winner.ID || task.ResolvedContext != winner.ResolvedContext {
			t.Fatal("duplicate winner snapshot differs")
		}
	}
	if count != n {
		t.Fatalf("only %d responses", count)
	}
	waitExpandedTask(t, c, winner.ID)
	stored, err := s.GetTaskByRequestID(context.Background(), "concurrent")
	if err != nil || stored.ID != winner.ID || stored.ResolvedContext != winner.ResolvedContext || b.calls.Load() != 1 {
		t.Fatalf("winner changed: %v calls %d", err, b.calls.Load())
	}
}

type selectionStagesRuntime struct{ contractRuntime }

func (b *selectionStagesRuntime) Run(ctx context.Context, r *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	b.calls.Add(1)
	out := "striker-proof"
	switch r.Role {
	case backend.RoleOracle:
		out = `{"goal":"probe","steps":["execute"]}`
	case backend.RoleCritic:
		out = `{"verdict":"pass","summary":"verified"}`
	}
	return &backend.ExecutionResult{BackendID: b.id, BackendVersion: b.version, Output: out}, nil
}

// 2026-10-03：RTSEL-007/008/009，真三 stage 與 queue 安全退化。
func TestRuntimeSelectionThreeFaceAndQueueGuard(t *testing.T) {
	root := t.TempDir()
	a := &selectionStagesRuntime{contractRuntime: contractRuntime{id: "codex", version: "v1", caps: []backend.Capability{backend.CapReadOnlySandbox, backend.CapWorkspaceWrite}}}
	unsafe := &contractRuntime{id: "omp", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite}}
	reg := backend.NewRegistry()
	reg.Register(a)
	reg.Register(unsafe)
	d, c, _ := contractDaemon(t, root, reg)
	task, err := c.Submit(context.Background(), engine.SubmitRequest{ID: "three", Workspace: root, Prompt: "probe", Backend: "auto", Mode: "three-face"})
	if err != nil {
		t.Fatal(err)
	}
	done := waitExpandedTask(t, c, task.ID)
	if done.Status != storage.StatusCompleted || a.calls.Load() != 3 || unsafe.calls.Load() != 0 {
		t.Fatalf("stages=%+v calls=%d", done, a.calls.Load())
	}
	var trace pipeline.PipelineTrace
	if err = json.Unmarshal([]byte(done.PipelineTrace), &trace); err != nil {
		t.Fatal(err)
	}
	for _, stage := range trace.Stages {
		if stage.BackendID != "codex" {
			t.Fatalf("mixed runtime: %+v", stage)
		}
	}
	// 2026-10-13：issue-54，completed 寫入早於 done callback 釋放 workspace；等實際釋放後再佔用。
	deadline := time.Now().Add(3 * time.Second)
	for d.wsQueue.IsLocked(root) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if acquired, _ := d.wsQueue.TryAcquire(root, "blocker"); !acquired {
		t.Fatal("workspace still held after previous task completed")
	}
	queued, err := c.Submit(context.Background(), engine.SubmitRequest{ID: "queued-auto", Workspace: root, Prompt: "probe", Backend: "auto", Mode: "three-face"})
	if err != nil || queued.Status != storage.StatusQueued {
		t.Fatalf("queue=%+v %v", queued, err)
	}
	reg.Unregister("codex")
	fresh := &contractRuntime{id: "codex", version: "v2", caps: []backend.Capability{backend.CapWorkspaceWrite}}
	reg.Register(fresh)
	d.releaseWorkspaceAndWakeNext(root, "blocker")
	rejected := waitExpandedTask(t, c, queued.ID)
	if rejected.Status != storage.StatusFailed || fresh.calls.Load() != 0 || unsafe.calls.Load() != 0 || rejected.ResolvedContext != queued.ResolvedContext {
		t.Fatalf("guard=%+v", rejected)
	}
}
