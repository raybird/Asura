package daemon

import (
	"asura/internal/auth"
	"asura/internal/engine"
	"asura/internal/storage"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func waitExpandedTask(t *testing.T, c *Client, id string) *storage.Task {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, e := c.GetTask(context.Background(), id)
		if e == nil && (task.Status == storage.StatusCompleted || task.Status == storage.StatusFailed) {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("task did not finish")
	return nil
}

// 2026-09-30：BADAPT-003／005／012，正式 composition root；假 binary 僅驗版本拒絕，不宣告能力。
func TestExpandedBackendsRegisteredFailClosed(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.Mkdir(bin, 0700)
	marker := filepath.Join(root, "model-calls")
	for _, name := range []string{"opencode", "codex", "omp", "bun", "agy"} {
		script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 1.18.23; exit 0; fi\necho " + name + " >> '" + marker + "'\nexit 1\n"
		if e := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	store, e := storage.NewSQLiteStore(filepath.Join(root, "asura.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	policy := auth.NewPolicy([]string{root})
	d := NewDaemon(Config{SocketPath: filepath.Join(root, "daemon.sock"), OpenCodeConfig: OpenCodeRuntimeConfig{BinaryPath: filepath.Join(bin, "opencode")}}, store, policy)
	if e = d.Start(); e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	c, e := NewClient(filepath.Join(root, "daemon.sock"))
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	for _, id := range []string{"omp", "antigravity", "unknown-issue25"} {
		task, e := c.Submit(context.Background(), engine.SubmitRequest{Workspace: root, Prompt: "probe", Backend: id})
		if e != nil {
			t.Fatal(e)
		}
		task = waitExpandedTask(t, c, task.ID)
		if task.Status != storage.StatusFailed || task.Backend != id {
			t.Fatalf("task %+v", task)
		}
		if id != "unknown-issue25" && (!strings.Contains(task.ErrorMessage, "backend_unavailable") || strings.Contains(task.ErrorMessage, "backend not found")) {
			t.Fatalf("not registered/fail-closed: %s", task.ErrorMessage)
		}
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("model or fallback binary invoked")
	}
}

// 2026-09-30：BADAPT-008，真正關閉並重新開啟 Daemon／SQLite，已完成 request 不重跑。
func TestExpandedIdentitySurvivesDaemonRestart(t *testing.T) {
	for _, id := range []string{"omp", "antigravity"} {
		t.Run(id, func(t *testing.T) {
			root := t.TempDir()
			db, sock := filepath.Join(root, "store.db"), filepath.Join(root, "daemon.sock")
			policy := auth.NewPolicy([]string{root})
			var calls atomic.Int32
			runner := func(context.Context, string, string) (string, error) { calls.Add(1); return "done", nil }
			store, e := storage.NewSQLiteStore(db)
			if e != nil {
				t.Fatal(e)
			}
			d := NewDaemon(Config{SocketPath: sock}, store, policy, WithEngine(engine.NewEngine(store, policy, engine.WithRunner(runner))))
			if e = d.Start(); e != nil {
				t.Fatal(e)
			}
			c, e := NewClient(sock)
			if e != nil {
				t.Fatal(e)
			}
			req := engine.SubmitRequest{ID: "persisted-issue25", Workspace: root, Prompt: "sealed prompt", Backend: id}
			task, e := c.Submit(context.Background(), req)
			if e != nil {
				t.Fatal(e)
			}
			task = waitExpandedTask(t, c, task.ID)
			if task.Status != storage.StatusCompleted {
				t.Fatalf("task %+v", task)
			}
			c.Close()
			d.Close()
			store.Close()
			reopened, e := storage.NewSQLiteStore(db)
			if e != nil {
				t.Fatal(e)
			}
			defer reopened.Close()
			d2 := NewDaemon(Config{SocketPath: sock}, reopened, policy, WithEngine(engine.NewEngine(reopened, policy, engine.WithRunner(runner))))
			if e = d2.Start(); e != nil {
				t.Fatal(e)
			}
			defer d2.Close()
			c2, e := NewClient(sock)
			if e != nil {
				t.Fatal(e)
			}
			defer c2.Close()
			same, e := c2.Submit(context.Background(), req)
			if e != nil {
				t.Fatal(e)
			}
			if same.Backend != id || same.ID != task.ID || same.RequestID != req.ID || calls.Load() != 1 {
				t.Fatalf("identity=%+v calls=%d", same, calls.Load())
			}
		})
	}
}
