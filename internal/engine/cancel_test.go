package engine

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/storage"
)

// SCN-004: 取消任務 A 在 2 秒內停止且狀態為 cancelled，不影響其他並行任務 B（仍為 completed）
func TestEngineCancel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-engine-cancel-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "allowed")
	_ = os.MkdirAll(allowedDir, 0755)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{allowedDir})

	taskAStarted := make(chan struct{})
	taskBCompleted := make(chan struct{})

	runner := func(ctx context.Context, ws string, prompt string) (string, error) {
		if prompt == "taskA" {
			close(taskAStarted)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(5 * time.Second):
				return "taskA finished", nil
			}
		}
		if prompt == "taskB" {
			time.Sleep(100 * time.Millisecond)
			close(taskBCompleted)
			return "taskB finished", nil
		}
		return "ok", nil
	}

	eng := NewEngine(store, policy, WithRunner(runner))
	ctx := context.Background()

	var wg sync.WaitGroup

	// 啟動任務 A
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = eng.SubmitTask(ctx, SubmitRequest{
			ID:        "task-A-cancel",
			Workspace: allowedDir,
			Prompt:    "taskA",
		})
	}()

	// 啟動任務 B
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = eng.SubmitTask(ctx, SubmitRequest{
			ID:        "task-B-normal",
			Workspace: allowedDir,
			Prompt:    "taskB",
		})
	}()

	// 等候任務 A 開始運行
	<-taskAStarted

	// 發送取消任務 A
	startCancel := time.Now()
	err = eng.CancelTask("task-A-cancel")
	if err != nil {
		t.Fatalf("CancelTask failed: %v", err)
	}

	wg.Wait()
	cancelDuration := time.Since(startCancel)

	// 驗證取消在 2 秒核准期限內停止
	if cancelDuration > 2*time.Second {
		t.Errorf("expected cancellation within 2s, took %v", cancelDuration)
	}

	// 驗證任務 A 在 storage 中為 cancelled
	savedA, err := store.GetTask(ctx, "task-A-cancel")
	if err != nil {
		t.Fatalf("GetTask A failed: %v", err)
	}
	if savedA.Status != storage.StatusCancelled {
		t.Errorf("expected task A status to be cancelled, got %q", savedA.Status)
	}

	// 驗證任務 B 順利完成，不受 A 取消影響
	<-taskBCompleted
	savedB, err := store.GetTask(ctx, "task-B-normal")
	if err != nil {
		t.Fatalf("GetTask B failed: %v", err)
	}
	if savedB.Status != storage.StatusCompleted {
		t.Errorf("expected task B status to be completed, got %q", savedB.Status)
	}
	if savedB.Result != "taskB finished" {
		t.Errorf("expected task B result 'taskB finished', got %q", savedB.Result)
	}

	// 測試取消不存在之任務
	err = eng.CancelTask("task-non-existent")
	if err == nil {
		t.Errorf("expected error cancelling non-existent task, got nil")
	}

	// 測試對已取消任務再次呼叫 CancelTask 具備冪等性且返回 nil (修復 I-01)
	err = eng.CancelTask("task-A-cancel")
	if err != nil {
		t.Errorf("expected idempotent cancellation to return nil for cancelled task, got: %v", err)
	}
}
