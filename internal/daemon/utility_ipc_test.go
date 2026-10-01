package daemon_test

import (
	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/storage"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestUtilityIPCIsolation(t *testing.T) {
	ws := t.TempDir()
	s, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw := `{"workspace":` + quoted(ws) + `,"recall":{"recall_id":"r","mode":"bounded","utility_policy":"observe","utility_version":"utility-v1","auto_ids":["m"],"selected":[{"id":"m","score":1,"weight_ppm":1000000,"final_score_scaled":1000000}]}}`
	if err = s.CreateTask(context.Background(), &storage.Task{ID: "task", Workspace: ws, Status: storage.StatusCompleted, ResolvedContext: raw}); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	d := daemon.NewDaemon(daemon.Config{SocketPath: sock}, s, auth.NewPolicy([]string{ws}))
	if err = d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	query := func(scope, id string, limit int) map[string]json.RawMessage {
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err = json.NewEncoder(c).Encode(daemon.IPCRequest{Action: "memory_utility", Workspace: scope, MemoryID: id, Limit: limit}); err != nil {
			t.Fatal(err)
		}
		var resp map[string]json.RawMessage
		if err = json.NewDecoder(c).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	good := query(ws, "m", 1)
	if len(good["utility_page"]) == 0 || len(good["error"]) > 0 {
		t.Fatalf("UFL-010 missing utility audit: %s", good)
	}
	bad := query(ws, "missing", 1)
	foreign := query(t.TempDir(), "m", 1)
	other := filepath.Join(ws, "other")
	if err = os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	if string(query(other, "m", 1)["error"]) != string(bad["error"]) {
		t.Fatal("authorized cross scope existence leak")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err = os.Symlink(ws, alias); err != nil {
		t.Fatal(err)
	}
	if len(query(alias, "m", 1)["utility_page"]) == 0 {
		t.Fatal("canonical symlink alias rejected")
	}
	if len(bad["error"]) == 0 || len(foreign["error"]) == 0 {
		t.Fatal("UFL-003 scope leak")
	}
	if len(query(ws, "m", 101)["error"]) == 0 {
		t.Fatal("UFL-010 limit not enforced")
	}
}
func quoted(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestUtilitySealedRedelivery(t *testing.T) {
	ws := t.TempDir()
	s, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateMemory(context.Background(), storage.Memory{ID: "m", Workspace: ws, Content: "sqlite", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	calls := atomic.Int64{}
	policy := auth.NewPolicy([]string{ws})
	eng := engine.NewEngine(s, policy, engine.WithRunner(func(context.Context, string, string) (string, error) { calls.Add(1); return "done", nil }))
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	start := func(mode string) *daemon.Daemon {
		d := daemon.NewDaemon(daemon.Config{SocketPath: sock, UtilityPolicy: mode}, s, policy, daemon.WithEngine(eng))
		if err = d.Start(); err != nil {
			t.Fatal(err)
		}
		return d
	}
	d := start("observe")
	client, err := daemon.NewClient(sock)
	if err != nil {
		t.Fatal(err)
	}
	req := engine.SubmitRequest{ID: "request", Workspace: ws, Prompt: "sqlite", MemoryRecall: "bounded"}
	task, err := client.Submit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		saved, err := client.GetTask(context.Background(), task.ID)
		if err == nil && saved.Status == storage.StatusCompleted && saved.UtilityStatus == "complete" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	sealed := task.ResolvedContext
	client.Close()
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteMemory(context.Background(), ws, "m"); err != nil {
		t.Fatal(err)
	}
	d = start("apply")
	defer d.Close()
	client, err = daemon.NewClient(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	replay, err := client.Submit(context.Background(), req)
	if err != nil || replay.ID != task.ID || replay.ResolvedContext != sealed || calls.Load() != 1 {
		t.Fatalf("UFL-002 redelivery %+v calls=%d err=%v", replay, calls.Load(), err)
	}
	var ec engine.ExecutionContext
	if err = json.Unmarshal([]byte(replay.ResolvedContext), &ec); err != nil || ec.Recall.UtilityPolicy != "observe" || len(ec.ProjectMemories) != 1 || ec.ProjectMemories[0].Content != "sqlite" {
		t.Fatalf("sealed policy/content changed %v %v", ec, err)
	}
	page, err := client.MemoryUtility(context.Background(), ws, "m", 20, "")
	if err != nil || page.Aggregate.Observed != 1 || page.Aggregate.Unknown != 1 {
		t.Fatalf("redelivery counted %+v %v", page, err)
	}
}
