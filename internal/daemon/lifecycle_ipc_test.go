package daemon_test

import (
	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/storage"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestLifecycleIPCAtomicRecall(t *testing.T) {
	ws := t.TempDir()
	other := t.TempDir()
	s, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateMemory(context.Background(), storage.Memory{ID: "a", Workspace: ws, Content: "sqlite"}); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "d.sock")
	d := daemon.NewDaemon(daemon.Config{SocketPath: sock}, s, auth.NewPolicy([]string{ws, other}))
	if err = d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	call := func(req map[string]any) map[string]json.RawMessage {
		t.Helper()
		c, e := net.Dial("unix", sock)
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close()
		if e = json.NewEncoder(c).Encode(req); e != nil {
			t.Fatal(e)
		}
		var out map[string]json.RawMessage
		if e = json.NewDecoder(c).Decode(&out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	req := map[string]any{"action": "memory_add", "workspace": ws, "content": "sqlite new", "supersedes": "a", "operation_id": "op", "is_durable": true}
	first := call(req)
	if len(first["error"]) > 0 {
		t.Fatalf("MLIFE-002 add: %s", first)
	}
	second := call(req)
	if string(first["memory"]) != string(second["memory"]) {
		t.Fatalf("MLIFE-002 retry created another successor: %s / %s", first, second)
	}
	detail := call(map[string]any{"action": "memory_detail", "workspace": ws, "memory_id": "a"})
	if len(detail["error"]) > 0 {
		t.Fatalf("L7 detail: %s", detail)
	}
	var x struct {
		Memory struct {
			Lifecycle    string `json:"lifecycle"`
			SupersededBy string `json:"superseded_by"`
		} `json:"memory"`
	}
	if err = json.Unmarshal(detail["memory_detail"], &x); err != nil || x.Memory.Lifecycle != "superseded" || x.Memory.SupersededBy == "" {
		t.Fatalf("MLIFE-002 missing lifecycle: %s", detail)
	}
	foreign := call(map[string]any{"action": "memory_detail", "workspace": other, "memory_id": "a"})
	missing := call(map[string]any{"action": "memory_detail", "workspace": other, "memory_id": "missing"})
	if len(foreign["error"]) == 0 || string(foreign["error"]) != string(missing["error"]) {
		t.Fatal("MLIFE-009 existence leak")
	}
	candidates, _, err := s.RecallCandidates(context.Background(), ws)
	if err != nil || len(candidates) != 1 || candidates[0].ID == "a" {
		t.Fatalf("MLIFE-003 candidates=%+v err=%v", candidates, err)
	}
}

func TestLifecycleSealedTaskAndExplicitAudit(t *testing.T) {
	ws := t.TempDir()
	s, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateMemory(ctx, storage.Memory{ID: "a", Workspace: ws, Content: "sqlite old"}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	policy := auth.NewPolicy([]string{ws})
	eng := engine.NewEngine(s, policy, engine.WithRunner(func(context.Context, string, string) (string, error) { calls.Add(1); return "done", nil }))
	sock := filepath.Join(t.TempDir(), "d.sock")
	start := func() *daemon.Daemon {
		d := daemon.NewDaemon(daemon.Config{SocketPath: sock}, s, policy, daemon.WithEngine(eng))
		if err = d.Start(); err != nil {
			t.Fatal(err)
		}
		return d
	}
	d := start()
	client, err := daemon.NewClient(sock)
	if err != nil {
		t.Fatal(err)
	}
	req := engine.SubmitRequest{ID: "before", Workspace: ws, Prompt: "sqlite", MemoryIDs: []string{"a"}, MemoryRecall: "bounded"}
	task, err := client.Submit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	sealed := task.ResolvedContext
	wait := func(id string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			task, err := client.GetTask(ctx, id)
			if err == nil && task.Status == storage.StatusCompleted && task.UtilityStatus == "complete" {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("task did not complete")
	}
	wait(task.ID)
	b, err := client.AddMemoryWithOptions(ctx, ws, "sqlite new", storage.CreateMemoryOptions{Supersedes: "a", OperationID: "replace"})
	if err != nil {
		t.Fatal(err)
	}
	historical, err := client.Submit(ctx, engine.SubmitRequest{ID: "historical", Workspace: ws, Prompt: "sqlite", MemoryIDs: []string{"a"}, MemoryRecall: "bounded"})
	if err != nil {
		t.Fatal(err)
	}
	wait(historical.ID)
	var ec engine.ExecutionContext
	if err = json.Unmarshal([]byte(historical.ResolvedContext), &ec); err != nil {
		t.Fatal(err)
	}
	if len(ec.ProjectMemories) != 2 || ec.ProjectMemories[0].Lifecycle != "superseded" || ec.ProjectMemories[0].SupersededBy != b.ID || ec.ProjectMemories[0].Content != "sqlite old" {
		t.Fatalf("MLIFE-004 explicit snapshot %+v", ec.ProjectMemories)
	}
	if len(ec.Recall.LifecycleAudit) != 2 || ec.Recall.LifecycleAudit[0].Source != "explicit" || ec.Recall.LifecycleAudit[0].Lifecycle != "superseded" || ec.Recall.LifecycleAudit[0].SupersededBy != b.ID {
		t.Fatalf("MLIFE-004 audit %+v", ec.Recall)
	}
	if err = client.DeleteMemory(ctx, ws, b.ID); err != nil {
		t.Fatal(err)
	}
	saved, err := client.GetTask(ctx, task.ID)
	if err != nil || saved.ResolvedContext != sealed {
		t.Fatalf("MLIFE-010 status changed %v", err)
	}
	client.Close()
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	d = start()
	defer d.Close()
	client, err = daemon.NewClient(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	replay, err := client.Submit(ctx, req)
	if err != nil || replay.ID != task.ID || replay.ResolvedContext != sealed || calls.Load() != 2 {
		t.Fatalf("MLIFE-010 redelivery %+v %v calls=%d", replay, err, calls.Load())
	}
	ec = engine.ExecutionContext{}
	if err = json.Unmarshal([]byte(replay.ResolvedContext), &ec); err != nil || ec.ProjectMemories[0].Lifecycle != "active" || ec.ProjectMemories[0].SupersededBy != "" {
		t.Fatalf("MLIFE-010 snapshot rewritten %+v %v", ec, err)
	}
}
