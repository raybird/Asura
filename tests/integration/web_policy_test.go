package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/storage"
)

// 2026-09-13 WEB-002：歷史任務不因知道 Task ID 就能繞過服務端清單。
func TestWEB002Daemon(t *testing.T) {
	ctx := context.Background()
	root, outside := t.TempDir(), t.TempDir()
	store, err := storage.NewSQLiteStore(filepath.Join(root, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	d := daemon.NewDaemon(daemon.Config{SocketPath: filepath.Join(root, "d.sock")}, store, auth.NewPolicy([]string{root}))
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, task := range []*storage.Task{
		{ID: "allowed", Workspace: root, Status: storage.StatusPending, Result: "allowed result", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "revoked", RequestID: "revoked-request", Workspace: outside, Status: storage.StatusPending, Result: "PRIVATE-RESULT", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	} {
		if err := store.CreateTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	c, err := daemon.NewClient(filepath.Join(root, "d.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if task, err := c.GetTask(ctx, "allowed"); err != nil || task.Result != "allowed result" {
		t.Fatalf("positive read: %v %v", task, err)
	}
	if _, err := c.CancelTask(ctx, "allowed"); err != nil {
		t.Fatalf("positive cancel: %v", err)
	}
	if task, err := c.Submit(ctx, engine.SubmitRequest{ID: "revoked-request", Workspace: root, Prompt: "retry"}); err == nil || task != nil {
		t.Errorf("unauthorized redelivery: %v %v", task, err)
	}
	for _, id := range []string{"revoked", "revoked-request"} {
		if task, err := c.GetTask(ctx, id); err == nil || task != nil {
			t.Errorf("unauthorized read: %v %v", task, err)
		}
		if _, err := c.CancelTask(ctx, id); err == nil {
			t.Errorf("unauthorized cancel: %s", id)
		}
	}
	task, err := store.GetTask(ctx, "revoked")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != storage.StatusPending || task.Result != "PRIVATE-RESULT" {
		t.Errorf("revoked task changed: %+v", task)
	}
}
