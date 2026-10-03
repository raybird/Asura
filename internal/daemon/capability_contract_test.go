package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"asura/internal/auth"
	"asura/internal/backend"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
)

type contractRuntime struct {
	id       string
	version  string
	caps     []backend.Capability
	calls    atomic.Int32
	versions atomic.Int32
}

func (b *contractRuntime) ID() string { return b.id }
func (b *contractRuntime) Version(context.Context) (string, error) {
	b.versions.Add(1)
	return b.version, nil
}
func (b *contractRuntime) Capabilities() []backend.Capability { return b.caps }
func (b *contractRuntime) HasCapability(c backend.Capability) bool {
	for _, v := range b.caps {
		if c == v {
			return true
		}
	}
	return false
}
func (b *contractRuntime) Run(_ context.Context, r *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	b.calls.Add(1)
	return &backend.ExecutionResult{BackendID: b.id, BackendVersion: b.version, Output: "done"}, nil
}

func contractDaemon(t *testing.T, root string, reg backend.Registry) (*Daemon, *Client, *storage.SQLiteStore) {
	t.Helper()
	s, err := storage.NewSQLiteStore(filepath.Join(root, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	p := auth.NewPolicy([]string{root})
	o := pipeline.NewOrchestrator(pipeline.NewOracleStageWithBackend(nil), pipeline.NewStrikerStageWithBackend(nil), pipeline.NewCriticStageWithBackend(nil)).WithRegistry(reg)
	d := NewDaemon(Config{SocketPath: filepath.Join(root, "daemon.sock"), MaxQueue: 16}, s, p, WithEngine(engine.NewEngine(s, p, engine.WithPipelineRunner(o))))
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(filepath.Join(root, "daemon.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); d.Close(); s.Close() })
	return d, c, s
}

// 2026-10-03：RTCAP-003／006／009，真 IPC admission 與 SQLite，不 mock matcher。
func TestCapabilityContractAdmissionRejects(t *testing.T) {
	for _, id := range []string{"unsafe", "unknown"} {
		t.Run(id, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(root, "sentinel")
			os.WriteFile(marker, []byte("unchanged"), 0600)
			a := &contractRuntime{id: "unsafe", version: "v1"}
			b := &contractRuntime{id: "opencode", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite}}
			reg := backend.NewRegistry()
			reg.Register(a)
			reg.Register(b)
			_, c, s := contractDaemon(t, root, reg)
			_, err := c.Submit(context.Background(), engine.SubmitRequest{ID: "rejected", Workspace: root, Prompt: "probe", Backend: id})
			if err == nil {
				t.Fatal("missing/unknown capability accepted before persistence")
			}
			if !strings.Contains(err.Error(), id) {
				t.Fatalf("identity missing: %v", err)
			}
			if _, err = s.GetTaskByRequestID(context.Background(), "rejected"); err == nil {
				t.Fatal("rejected task persisted")
			}
			if a.calls.Load() != 0 || b.calls.Load() != 0 {
				t.Fatal("backend/fallback invoked")
			}
			data, _ := os.ReadFile(marker)
			if string(data) != "unchanged" {
				t.Fatal("workspace changed")
			}
		})
	}
}

// 2026-10-03：RTCAP-007／008／016，無記憶快照與 registry 移除後 redelivery。
func TestCapabilityContractSealedRedelivery(t *testing.T) {
	root := t.TempDir()
	b := &contractRuntime{id: "opencode", version: "V1", caps: []backend.Capability{backend.CapWorkspaceWrite}}
	reg := backend.NewRegistry()
	reg.Register(b)
	_, c, s := contractDaemon(t, root, reg)
	req := engine.SubmitRequest{ID: "sealed", Workspace: root, Prompt: "probe"}
	task, err := c.Submit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var sealed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(task.ResolvedContext), &sealed); err != nil {
		t.Fatalf("accepted task not sealed: %v", err)
	}
	if len(sealed["execution_contract"]) == 0 {
		t.Fatal("no capability contract at acceptance")
	}
	done := waitExpandedTask(t, c, task.ID)
	if done.Status != storage.StatusCompleted {
		t.Fatalf("%+v", done)
	}
	versions := b.versions.Load()
	reg.Unregister("opencode")
	same, err := c.Submit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if same.ID != task.ID || stored.ResolvedContext != task.ResolvedContext || same.ResolvedContext != task.ResolvedContext {
		t.Fatal("sealed identity changed")
	}
	if b.versions.Load() != versions || b.calls.Load() != 1 {
		t.Fatal("redelivery probed or ran runtime")
	}
}

// 2026-10-03：RTCAP-007／015／016，真佇列與 Cron 封存，worker 不覆寫接受時契約。
func TestCapabilityContractQueuedAndCron(t *testing.T) {
	for _, drift := range []bool{false, true} {
		t.Run(map[bool]string{false: "upgrade", true: "capability-removed"}[drift], func(t *testing.T) {
			root := t.TempDir()
			old := &contractRuntime{id: "opencode", version: "V1", caps: []backend.Capability{backend.CapWorkspaceWrite}}
			reg := backend.NewRegistry()
			reg.Register(old)
			d, c, s := contractDaemon(t, root, reg)
			d.wsQueue.TryAcquire(root, "blocker")
			task, err := c.Submit(context.Background(), engine.SubmitRequest{Workspace: root, Prompt: "queued"})
			if err != nil {
				t.Fatal(err)
			}
			if task.Status != storage.StatusQueued {
				t.Fatalf("not queued: %+v", task)
			}
			var ec engine.ExecutionContext
			if err := json.Unmarshal([]byte(task.ResolvedContext), &ec); err != nil || ec.ExecutionContract == nil {
				t.Fatalf("no accepted contract: %v", err)
			}
			if ec.ExecutionContract.Runtime.Version != "V1" || !ec.ExecutionContract.Match.Compatible {
				t.Fatal("wrong admission")
			}
			fresh := &contractRuntime{id: "opencode", version: "V2"}
			if !drift {
				fresh.caps = []backend.Capability{backend.CapWorkspaceWrite}
			}
			reg.Unregister("opencode")
			reg.Register(fresh)
			d.releaseWorkspaceAndWakeNext(root, "blocker")
			done := waitExpandedTask(t, c, task.ID)
			if done.ResolvedContext != task.ResolvedContext {
				t.Fatal("queue admission snapshot rewritten")
			}
			if drift {
				if done.Status != storage.StatusFailed || fresh.calls.Load() != 0 || !strings.Contains(done.PipelineTrace, "guard_error") {
					t.Fatalf("unsafe queue execution: %+v calls %d", done, fresh.calls.Load())
				}
			} else if done.Status != storage.StatusCompleted || fresh.calls.Load() != 1 {
				t.Fatalf("upgrade not executed: %+v", done)
			}
			stored, _ := s.GetTask(context.Background(), task.ID)
			if stored.ResolvedContext != task.ResolvedContext {
				t.Fatal("persisted snapshot changed")
			}
		})
	}
	root := t.TempDir()
	reg := backend.NewRegistry()
	reg.Register(&contractRuntime{id: "opencode", version: "cron-V1", caps: []backend.Capability{backend.CapWorkspaceWrite}})
	d, c, s := contractDaemon(t, root, reg)
	id, err := d.dispatchCronTask(context.Background(), "cron-fixture", root, "scheduled")
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.GetTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var ec engine.ExecutionContext
	if err := json.Unmarshal([]byte(task.ResolvedContext), &ec); err != nil || ec.ExecutionContract == nil || ec.ExecutionContract.Runtime.Version != "cron-V1" {
		t.Fatalf("Cron snapshot missing: %v %+v", err, ec)
	}
	if done := waitExpandedTask(t, c, id); done.Status != storage.StatusCompleted {
		t.Fatalf("Cron did not finish: %+v", done)
	}
}

// 2026-10-03：RTCAP-007／008，關閉 Daemon/SQLite 後換 runtime 再重送。
func TestCapabilityContractRestart(t *testing.T) {
	root := t.TempDir()
	old := &contractRuntime{id: "opencode", version: "V1", caps: []backend.Capability{backend.CapWorkspaceWrite}}
	reg := backend.NewRegistry()
	reg.Register(old)
	d, c, s := contractDaemon(t, root, reg)
	req := engine.SubmitRequest{ID: "restart", Workspace: root, Prompt: "original"}
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

// 2026-10-03：RTCAP-008，並行接受由 SQLite 唯一身份決定勝者，快照與執行不重複。
func TestCapabilityContractConcurrentWinner(t *testing.T) {
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
			task, err := client.Submit(context.Background(), engine.SubmitRequest{ID: "concurrent", Workspace: root, Prompt: "same"})
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
