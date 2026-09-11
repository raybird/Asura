package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"asura/internal/auth"
	"asura/internal/storage"
)

// SCN-001: 授權使用者提交任務取得結果，並可再次查得相同結果
// SCN-002: 拒絕超出授權範圍的執行，沒有啟動執行後端或造成資料庫寫入
func TestEngineExecute(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-engine-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "allowed-workspace")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{allowedDir})

	var backendCallCount int32
	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		atomic.AddInt32(&backendCallCount, 1)
		return "execution output: " + prompt, nil
	}

	eng := NewEngine(store, policy, WithRunner(runner))
	ctx := context.Background()

	t.Run("SCN-002: 拒絕超出授權範圍的執行", func(t *testing.T) {
		backendCallCount = 0
		req := SubmitRequest{
			Workspace: "/etc",
			Prompt:    "cat /etc/passwd",
		}

		_, err := eng.SubmitTask(ctx, req)
		if err == nil {
			t.Fatalf("expected error for unauthorized workspace, got nil")
		}
		if !errors.Is(err, auth.ErrDisallowedWorkspace) {
			t.Errorf("expected ErrDisallowedWorkspace, got: %v", err)
		}

		// 驗證 0 後端子進程呼叫
		if count := atomic.LoadInt32(&backendCallCount); count != 0 {
			t.Errorf("expected 0 backend calls on denied workspace, got %d", count)
		}
	})

	t.Run("SCN-001: 授權使用者取得單次任務結果並可重查", func(t *testing.T) {
		backendCallCount = 0
		req := SubmitRequest{
			Workspace: allowedDir,
			Prompt:    "echo 'hello from engine'",
		}

		task, err := eng.SubmitTask(ctx, req)
		if err != nil {
			t.Fatalf("SubmitTask failed on allowed workspace: %v", err)
		}

		if task.ID == "" {
			t.Errorf("expected non-empty task ID")
		}
		if task.Status != storage.StatusCompleted {
			t.Errorf("expected status %q, got %q", storage.StatusCompleted, task.Status)
		}
		expectedOutput := "execution output: echo 'hello from engine'"
		if task.Result != expectedOutput {
			t.Errorf("expected result %q, got %q", expectedOutput, task.Result)
		}

		// 驗證後端確實執行一次
		if count := atomic.LoadInt32(&backendCallCount); count != 1 {
			t.Errorf("expected 1 backend call, got %d", count)
		}

		// 驗證從儲存層再次查得相同結果
		saved, err := store.GetTask(ctx, task.ID)
		if err != nil {
			t.Fatalf("GetTask failed: %v", err)
		}
		if saved.Status != storage.StatusCompleted {
			t.Errorf("expected saved status %q, got %q", storage.StatusCompleted, saved.Status)
		}
		if saved.Result != expectedOutput {
			t.Errorf("expected saved result %q, got %q", expectedOutput, saved.Result)
		}
	})
}

// 內迴圈單元測試：驗證後端執行錯誤時狀態轉為 StatusFailed 且記錄錯誤訊息
func TestEngine_FailedExecution(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-engine-fail-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "allowed-workspace")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{allowedDir})
	failErr := errors.New("command execution failed: exit code 2")
	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		return "partial stderr output", failErr
	}

	eng := NewEngine(store, policy, WithRunner(runner))
	ctx := context.Background()

	task, err := eng.SubmitTask(ctx, SubmitRequest{
		Workspace: allowedDir,
		Prompt:    "exit 2",
	})
	if err == nil {
		t.Fatalf("expected error from failed runner, got nil")
	}
	if task == nil {
		t.Fatalf("expected non-nil task on failure, got nil")
	}
	if task.Status != storage.StatusFailed {
		t.Errorf("expected StatusFailed, got %q", task.Status)
	}
	if task.ErrorMessage != failErr.Error() {
		t.Errorf("expected error message %q, got %q", failErr.Error(), task.ErrorMessage)
	}

	// 驗證儲存層同樣更新為失敗狀態
	saved, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if saved.Status != storage.StatusFailed {
		t.Errorf("expected saved status StatusFailed, got %q", saved.Status)
	}
	if saved.ErrorMessage != failErr.Error() {
		t.Errorf("expected saved error message %q, got %q", failErr.Error(), saved.ErrorMessage)
	}
}

