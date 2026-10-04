package daemon

import (
	"asura/internal/backend"
	"asura/internal/engine"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type failingInventoryRuntime struct {
	contractRuntime
	timeout bool
	started chan struct{}
	stopped chan struct{}
}

// 2026-10-04：RTINV-005/013/014，取消 IPC 不建立 Task；explicit 原因可讀且不洩漏錯誤。
func TestInventoryIPCRejectionAndCancellation(t *testing.T) {
	root := t.TempDir()
	good := &contractRuntime{id: "codex", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite}}
	bad := &failingInventoryRuntime{contractRuntime: contractRuntime{id: "omp", caps: good.caps}}
	reg := backend.NewRegistry()
	reg.Register(good)
	reg.Register(bad)
	_, client, store := contractDaemon(t, root, reg)
	snapshot, err := client.Inventory(context.Background())
	if err != nil || snapshot.Entries[1].AvailabilityState != backend.Unavailable {
		t.Fatalf("inventory IPC: %+v %v", snapshot, err)
	}
	if _, err = client.Submit(context.Background(), engine.SubmitRequest{ID: "reject", Workspace: root, Prompt: "probe", Backend: "omp"}); err == nil || !strings.Contains(err.Error(), "omp:probe_error") || strings.Contains(err.Error(), "secret-sentinel") {
		t.Fatalf("explicit error: %v", err)
	}
	if _, err = store.GetTaskByRequestID(context.Background(), "reject"); err == nil {
		t.Fatal("rejected Task persisted")
	}
	// 2026-10-04：透過另一個 registered handle 避免與查詢 worker 競爭。
	reg.Unregister("omp")
	started, stopped := make(chan struct{}), make(chan struct{})
	reg.Register(&failingInventoryRuntime{contractRuntime: contractRuntime{id: "omp", caps: good.caps}, timeout: true, started: started, stopped: stopped})
	conn, err := net.Dial("unix", filepath.Join(root, "daemon.sock"))
	if err != nil {
		t.Fatal(err)
	}
	json.NewEncoder(conn).Encode(IPCRequest{Action: "submit", RequestID: "cancel", Workspace: root, Prompt: "probe", Backend: "omp"})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("probe not started")
	}
	conn.Close()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("IPC cancellation did not stop probe")
	}
	if _, err = store.GetTaskByRequestID(context.Background(), "cancel"); err == nil || good.calls.Load() != 0 || bad.calls.Load() != 0 {
		t.Fatal("canceled/rejected runtime dispatched")
	}
}

func (b *failingInventoryRuntime) Version(ctx context.Context) (string, error) {
	if b.timeout {
		if b.started != nil {
			close(b.started)
		}
		<-ctx.Done()
		if b.stopped != nil {
			close(b.stopped)
		}
		return "", ctx.Err()
	}
	return "", errors.New("secret-sentinel: version failed")
}

// 2026-10-04：RTINV-003/004/008，真 IPC/SQLite；單一失敗不得污染可用候選。
func TestInventoryAdmissionFailureIsolation(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "timeout"}[timeout], func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(root, "sentinel")
			if err := os.WriteFile(marker, []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			good := &contractRuntime{id: "codex", version: "v1", caps: []backend.Capability{backend.CapWorkspaceWrite}}
			bad := &failingInventoryRuntime{contractRuntime: contractRuntime{id: "omp", caps: []backend.Capability{backend.CapWorkspaceWrite}}, timeout: timeout}
			reg := backend.NewRegistry()
			reg.Register(good)
			reg.Register(bad)
			_, c, _ := contractDaemon(t, root, reg)
			started := time.Now()
			task, err := c.Submit(context.Background(), engine.SubmitRequest{ID: "isolation", Workspace: root, Prompt: "probe", Backend: "auto", Mode: "direct"})
			if err != nil {
				t.Fatalf("available Codex blocked by OMP: %v", err)
			}
			done := waitExpandedTask(t, c, task.ID)
			if done.Status != "completed" || done.Backend != "codex" || bad.calls.Load() != 0 || good.calls.Load() != 1 {
				t.Fatalf("wrong dispatch: %+v", done)
			}
			if time.Since(started) > 4500*time.Millisecond {
				t.Fatal("per-runtime deadline not bounded at 3 seconds")
			}
			value, _ := os.ReadFile(marker)
			if string(value) != "unchanged" {
				t.Fatal("workspace changed")
			}
		})
	}
}
