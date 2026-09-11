package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// SCN-005 內迴圈單元測試：部分執行失敗不自動重試，且保留已產生的部分輸出與失敗原因
func TestEngine_PartialFailureNoRetry(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-engine-partial-*")
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

	var backendAttempts int32
	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		atomic.AddInt32(&backendAttempts, 1)
		// 模擬產生了前段有效副作用與輸出後遭遇退出錯誤
		return "step 1: file created; step 2: database touched", errors.New("exit status 42: out of memory")
	}

	eng := NewEngine(store, policy, WithRunner(runner))
	ctx := context.Background()

	task, err := eng.SubmitTask(ctx, SubmitRequest{
		Workspace: allowedDir,
		Prompt:    "multi-step-task",
	})
	if err == nil {
		t.Fatalf("expected error for failed task, got nil")
	}

	// 1. 斷言系統嚴格執行 1 次，絕不自動重試
	if attempts := atomic.LoadInt32(&backendAttempts); attempts != 1 {
		t.Errorf("expected exactly 1 execution attempt (no auto-retry), got %d", attempts)
	}

	// 2. 斷言記憶體任務與資料庫持久化之任務狀態均為 StatusFailed
	if task.Status != storage.StatusFailed {
		t.Errorf("expected task status %s, got %s", storage.StatusFailed, task.Status)
	}
	expectedOutput := "step 1: file created; step 2: database touched"
	if task.Result != expectedOutput {
		t.Errorf("expected partial output %q, got %q", expectedOutput, task.Result)
	}

	// 3. 斷言資料庫中查得相同之部分輸出與明確錯誤訊息
	saved, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("failed to get task from store: %v", err)
	}
	if saved.Status != storage.StatusFailed {
		t.Errorf("expected store task status %s, got %s", storage.StatusFailed, saved.Status)
	}
	if saved.Result != expectedOutput {
		t.Errorf("expected store partial result %q, got %q", expectedOutput, saved.Result)
	}
	if !strings.Contains(saved.ErrorMessage, "exit status 42") {
		t.Errorf("expected error message to contain 'exit status 42', got: %s", saved.ErrorMessage)
	}
}

// SCN-007 內迴圈單元測試：空輸出或純空白文字不得判定為成功，應標記為 failed 並說明原因
func TestEngine_EmptyOutputTreatedAsFailed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-engine-empty-*")
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

	// runner 正常返回但輸出為純空白
	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		return "   \n\t  ", nil
	}

	eng := NewEngine(store, policy, WithRunner(runner))
	ctx := context.Background()

	task, err := eng.SubmitTask(ctx, SubmitRequest{
		Workspace: allowedDir,
		Prompt:    "command-producing-no-output",
	})
	if err == nil {
		t.Fatalf("expected error for empty output task, got nil")
	}
	if !errors.Is(err, ErrEmptyOutput) {
		t.Errorf("expected ErrEmptyOutput, got: %v", err)
	}

	// 斷言記憶體任務狀態為 StatusFailed
	if task.Status != storage.StatusFailed {
		t.Errorf("expected task status %s, got %s", storage.StatusFailed, task.Status)
	}
	if !strings.Contains(task.ErrorMessage, "empty output") {
		t.Errorf("expected error message to mention empty output, got: %s", task.ErrorMessage)
	}

	// 斷言資料庫中狀態亦為 StatusFailed
	saved, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("failed to get task from store: %v", err)
	}
	if saved.Status != storage.StatusFailed {
		t.Errorf("expected saved status %s, got %s", storage.StatusFailed, saved.Status)
	}
	if !strings.Contains(saved.ErrorMessage, "empty output") {
		t.Errorf("expected saved error message to mention empty output, got: %s", saved.ErrorMessage)
	}
}

