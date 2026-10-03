package daemon

import (
	"asura/internal/backend"
	"asura/internal/engine"
	"asura/internal/storage"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// 2026-10-04：STGBIND-004/006，真 IPC/SQLite，拒絕不得建立 Task 或 Run。
func TestStageBindingAtomicReject(t *testing.T) {
	root := t.TempDir()
	write := &contractRuntime{id: "omp", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite}}
	reg := backend.NewRegistry()
	reg.Register(write)
	_, c, s := contractDaemon(t, root, reg)
	marker := filepath.Join(root, "sentinel")
	os.WriteFile(marker, []byte("unchanged"), 0600)
	for _, intent := range []string{"auto", "omp"} {
		_, err := c.Submit(context.Background(), engine.SubmitRequest{ID: intent, Workspace: root, Prompt: "probe", Backend: intent, Mode: "three-face"})
		if err == nil {
			t.Fatal("unsafe task accepted")
		}
		if _, err = s.GetTaskByRequestID(context.Background(), intent); err == nil {
			t.Fatal("rejected task persisted")
		}
	}
	if write.calls.Load() != 0 {
		t.Fatal("runtime invoked")
	}
	data, _ := os.ReadFile(marker)
	if string(data) != "unchanged" {
		t.Fatal("workspace mutated")
	}
}

// 2026-10-04：STGBIND-002/012，已接受 Task 必須保留完整 plan，真三 stage 不共用 task runtime。
func TestStageBindingDaemonMixed(t *testing.T) {
	root := t.TempDir()
	write := &selectionStagesRuntime{contractRuntime: contractRuntime{id: "opencode", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite}}}
	read := &selectionStagesRuntime{contractRuntime: contractRuntime{id: "codex", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite, backend.CapReadOnlySandbox}}}
	reg := backend.NewRegistry()
	reg.Register(write)
	reg.Register(read)
	_, c, _ := contractDaemon(t, root, reg)
	req := engine.SubmitRequest{ID: "mixed", Workspace: root, Prompt: "probe", Backend: "auto", Mode: "three-face"}
	task, err := c.Submit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Contract map[string]json.RawMessage `json:"execution_contract"`
	}
	json.Unmarshal([]byte(task.ResolvedContext), &wire)
	if len(wire.Contract["stage_binding_plan"]) == 0 {
		t.Fatal("missing sealed stage plan")
	}
	done := waitExpandedTask(t, c, task.ID)
	if done.Status != storage.StatusCompleted || write.calls.Load() != 1 || read.calls.Load() != 2 {
		t.Fatalf("mixed execution failed: %s write=%d read=%d", done.ErrorMessage, write.calls.Load(), read.calls.Load())
	}
	versions := write.versions.Load() + read.versions.Load()
	reg.Unregister("codex")
	reg.Unregister("opencode")
	again, err := c.Submit(context.Background(), req)
	if err != nil || again.ID != task.ID || again.ResolvedContext != task.ResolvedContext || write.versions.Load()+read.versions.Load() != versions {
		t.Fatalf("redelivery changed binding: %v", err)
	}
	req.Backend = "opencode"
	if _, err = c.Submit(context.Background(), req); err == nil {
		t.Fatal("intent collision accepted")
	}
}

// 2026-10-04：STGBIND-009/010/012，queue 喚醒只執行原 plan，移除 runtime 不重選。
func TestStageBindingQueue(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(fmt.Sprint(remove), func(t *testing.T) {
			root := t.TempDir()
			write := &selectionStagesRuntime{contractRuntime: contractRuntime{id: "opencode", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite}}}
			read := &selectionStagesRuntime{contractRuntime: contractRuntime{id: "codex", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite, backend.CapReadOnlySandbox}}}
			reg := backend.NewRegistry()
			reg.Register(write)
			reg.Register(read)
			d, c, _ := contractDaemon(t, root, reg)
			d.wsQueue.TryAcquire(root, "blocker")
			req := engine.SubmitRequest{ID: "queued-mixed", Workspace: root, Prompt: "probe", Backend: "auto", Mode: "three-face"}
			task, err := c.Submit(context.Background(), req)
			if err != nil || task.Status != storage.StatusQueued {
				t.Fatalf("queue %v %v", task, err)
			}
			versionCalls := write.versions.Load() + read.versions.Load()
			same, err := c.Submit(context.Background(), req)
			if err != nil || same.ResolvedContext != task.ResolvedContext || versionCalls != write.versions.Load()+read.versions.Load() {
				t.Fatal("queue redelivery reselected")
			}
			if remove {
				reg.Unregister("opencode")
			} else {
				reg.Register(&contractRuntime{id: "antigravity", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite, backend.CapReadOnlySandbox}})
			}
			d.releaseWorkspaceAndWakeNext(root, "blocker")
			done := waitExpandedTask(t, c, task.ID)
			if done.ResolvedContext != task.ResolvedContext {
				t.Fatal("queue rewrote plan")
			}
			if remove {
				if done.Status != storage.StatusFailed || write.calls.Load() != 0 || read.calls.Load() != 0 {
					t.Fatal("missing Striker runtime fell back")
				}
			} else if done.Status != storage.StatusCompleted || write.calls.Load() != 1 || read.calls.Load() != 2 {
				t.Fatal("queue drift")
			}
		})
	}
}

// 2026-10-04：STGBIND-013，SQLite reopen 加 daemon recovery，不將保留快照冒稱 resume。
func TestStageBindingRestartRecovery(t *testing.T) {
	root := t.TempDir()
	write := &contractRuntime{id: "omp", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite}}
	read := &contractRuntime{id: "codex", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite, backend.CapReadOnlySandbox}}
	reg := backend.NewRegistry()
	reg.Register(write)
	reg.Register(read)
	contract, err := backend.AdmitRuntime(context.Background(), reg, "three-face", "auto", []string{"omp", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	ec := engine.ExecutionContext{OriginalPrompt: "probe", Workspace: root, Backend: "omp", ExecutionContract: contract}
	raw, _ := json.Marshal(ec)
	s, err := storage.NewSQLiteStore(filepath.Join(root, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []storage.TaskStatus{storage.StatusPending, storage.StatusQueued, storage.StatusRunning} {
		task := &storage.Task{ID: string(status), RequestID: string(status), Fingerprint: calculateTaskFingerprint(root, "probe", "three-face", nil, "auto"), Workspace: root, Prompt: "probe", Backend: "omp", ExecutionMode: "three-face", Status: status, ResolvedContext: string(raw)}
		if err = s.CreateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	versions := read.versions.Load() + write.versions.Load()
	_, c, again := contractDaemon(t, root, backend.NewRegistry())
	for _, id := range []string{"pending", "queued", "running"} {
		task, err := again.GetTask(context.Background(), id)
		if err != nil || task.Status != storage.StatusFailed || task.ResolvedContext != string(raw) {
			t.Fatalf("recovery drift %v %v", task, err)
		}
		same, err := c.Submit(context.Background(), engine.SubmitRequest{ID: id, Workspace: root, Prompt: "probe", Backend: "auto", Mode: "three-face"})
		if err != nil || same.ID != id || same.ResolvedContext != string(raw) {
			t.Fatalf("restart redelivery %v", err)
		}
	}
	if read.calls.Load()+write.calls.Load() != 0 || read.versions.Load()+write.versions.Load() != versions {
		t.Fatal("restart reselected or ran runtime")
	}
}
