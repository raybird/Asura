package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// R-05: 父 Context 被取消時，仍使用未取消且有界的 persistCtx 將狀態寫入資料庫
func TestEngine_R05_PersistOnCancelledParentContext(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-r05-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		// 等待 ctx 被取消
		<-ctx.Done()
		return "partial execution before timeout", ctx.Err()
	}

	eng := NewEngine(store, policy, WithRunner(runner))

	// 使用立即取消的 Context 提交任務
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	task, err := eng.SubmitTask(ctx, SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "timeout-task",
	})
	if err == nil {
		t.Fatalf("expected error from cancelled context, got nil")
	}

	// 驗證記憶體與資料庫中的狀態絕不停留在 running，而是已持久化為 cancelled
	if task.Status != storage.StatusCancelled {
		t.Errorf("expected memory status %s, got %s", storage.StatusCancelled, task.Status)
	}

	saved, err := store.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("failed to get task from store: %v", err)
	}
	if saved.Status != storage.StatusCancelled {
		t.Errorf("expected saved status in DB to be %s, but got %s (remained in running?)", storage.StatusCancelled, saved.Status)
	}
}

// R-06: 外部 CancelTask 後，執行端收集到的 partial output 仍完整持久化至資料庫
func TestEngine_R06_PreservePartialOutputOnCancel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-r06-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	started := make(chan struct{})
	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		close(started)
		<-ctx.Done()
		return "step 1 finished; step 2 partial output", ctx.Err()
	}

	eng := NewEngine(store, policy, WithRunner(runner))

	var taskID = "task-r06-test"
	go func() {
		<-started
		// 模擬外部 CancelTask
		_ = eng.CancelTask(taskID)
	}()

	task, err := eng.SubmitTask(context.Background(), SubmitRequest{
		ID:        taskID,
		Workspace: tmpDir,
		Prompt:    "cancel-test",
	})
	if err == nil {
		t.Fatalf("expected error from cancelled task, got nil")
	}

	expectedOutput := "step 1 finished; step 2 partial output"
	if task.Result != expectedOutput {
		t.Errorf("expected memory task result %q, got %q", expectedOutput, task.Result)
	}

	// 查驗資料庫中是否保存了 partial output 而非空字串
	saved, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("failed to get task from store: %v", err)
	}
	if saved.Result != expectedOutput {
		t.Errorf("expected DB task result to contain partial output %q, got empty or wrong: %q", expectedOutput, saved.Result)
	}
}

// R-09: 輸出僅包含未解決的結構化錯誤（如 429 錯誤 JSON）且無有效文字時，任務標記為 failed
func TestEngine_R09_UnresolvedStructuredError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-r09-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	// runner 退出碼為 0 但只輸出 429 rate limit 結構化錯誤事件
	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		return `{"type":"error","error":{"message":"Rate limit exceeded","statusCode":429}}`, nil
	}

	eng := NewEngine(store, policy, WithRunner(runner))

	task, err := eng.SubmitTask(context.Background(), SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "rate-limit-task",
	})
	if err == nil {
		t.Fatalf("expected task with unresolved 429 to fail, got nil")
	}

	if task.Status != storage.StatusFailed {
		t.Errorf("expected task status to be %s, got %s", storage.StatusFailed, task.Status)
	}

	saved, err := store.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("failed to get task from store: %v", err)
	}
	if saved.Status != storage.StatusFailed {
		t.Errorf("expected DB task status to be %s, got %s", storage.StatusFailed, saved.Status)
	}
	if !strings.Contains(saved.ErrorMessage, "empty output or unresolved error") {
		t.Errorf("expected error message to explain unresolved error, got: %s", saved.ErrorMessage)
	}
}

// R-10: 條件式更新防護——若任務在執行期間已被外部取消，不得被成功狀態覆寫
func TestEngine_R10_DoNotOverwriteCancelledStatus(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-r10-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	taskID := "task-r10-race"
	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		// 在 runner 內部模擬外部行程在 SQLite 中將此 task 標記為 cancelled
		_ = store.UpdateTaskStatus(context.Background(), taskID, storage.StatusCancelled, "cancelled externally", "cancelled by CLI")
		return "runner completed execution", nil
	}

	eng := NewEngine(store, policy, WithRunner(runner))

	task, err := eng.SubmitTask(context.Background(), SubmitRequest{
		ID:        taskID,
		Workspace: tmpDir,
		Prompt:    "race-cancel-complete",
	})
	if err == nil {
		t.Fatalf("expected cancelled task error, got nil")
	}

	// 斷言狀態絕未被覆寫為 completed
	if task.Status != storage.StatusCancelled {
		t.Errorf("expected task status %s, got %s (overwritten?)", storage.StatusCancelled, task.Status)
	}

	saved, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("failed to get task from store: %v", err)
	}
	if saved.Status != storage.StatusCancelled {
		t.Errorf("expected DB status to remain %s, got %s", storage.StatusCancelled, saved.Status)
	}
}

// F-02: 驗證任務在轉移至 running 前或進入 runner 前被外部取消時，runner 嚴格不被執行 (0 副作用)
func TestReviewCancelBeforeRunner(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-f02-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	var runnerCalls int32
	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		atomic.AddInt32(&runnerCalls, 1)
		return "should-not-run", nil
	}

	taskID := "task-f02-cancel-early"
	eng1 := NewEngine(store, policy, WithRunner(runner))
	eng2 := NewEngine(store, policy)

	// 預先在 DB 中將該 task 標記為 cancelled（模擬極早期或 pending 階段取消）
	now := time.Now().UTC()
	taskPre := &storage.Task{
		ID:        taskID,
		Prompt:    "early-cancelled",
		Workspace: tmpDir,
		Status:    storage.StatusCancelled,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(context.Background(), taskPre); err != nil {
		t.Fatalf("failed to seed pre-cancelled task: %v", err)
	}

	// 透過 eng2 再次確認取消狀態存在
	_ = eng2.CancelTask(taskID)

	// eng1 嘗試提交同 ID 任務
	task, err := eng1.SubmitTask(context.Background(), SubmitRequest{
		ID:        taskID,
		Workspace: tmpDir,
		Prompt:    "early-cancelled",
	})
	if err == nil {
		t.Fatalf("expected error from pre-cancelled task, got nil")
	}

	// 斷言 runner 嚴格被呼叫 0 次
	if calls := atomic.LoadInt32(&runnerCalls); calls != 0 {
		t.Errorf("expected runner to be called 0 times, but was called %d times (violates F-02)", calls)
	}

	if task != nil && task.Status != storage.StatusCancelled {
		t.Errorf("expected task status to remain %s, got %s", storage.StatusCancelled, task.Status)
	}
}

// 驗證任務在 pending 階段被 CancelTask 後，SubmitTask 條件式轉移失敗且絕不呼叫 runner
func TestReviewCancelInRunningBeforeRunner(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-f02-running-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	var runnerCalls int32
	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		atomic.AddInt32(&runnerCalls, 1)
		return "should-not-run", nil
	}

	taskID := "task-f02-running-cancel"
	eng := NewEngine(store, policy, WithRunner(runner))

	// 先建立任務為 pending
	now := time.Now().UTC()
	taskPre := &storage.Task{
		ID:        taskID,
		Prompt:    "cancel-before-runner",
		Workspace: tmpDir,
		Status:    storage.StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(context.Background(), taskPre); err != nil {
		t.Fatalf("failed to seed pending task: %v", err)
	}

	// 模擬外部在 pending 階段發出 CancelTask
	if err := eng.CancelTask(taskID); err != nil {
		t.Fatalf("CancelTask failed: %v", err)
	}

	task, err := eng.SubmitTask(context.Background(), SubmitRequest{
		ID:        taskID,
		Workspace: tmpDir,
		Prompt:    "cancel-before-runner",
	})
	if err == nil {
		t.Fatalf("expected error from cancelled task, got nil")
	}

	if calls := atomic.LoadInt32(&runnerCalls); calls != 0 {
		t.Errorf("expected runner to be called 0 times, but was called %d times (violates F-02)", calls)
	}
	if task != nil && task.Status != storage.StatusCancelled {
		t.Errorf("expected task status to be %s, got %s", storage.StatusCancelled, task.Status)
	}
}

// F-03: 驗證當儲存層持久化失敗時，SubmitTask 明確揭露持久化錯誤而非默默忽略
func TestReviewPersistError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-f03-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}

	policy := auth.NewPolicy([]string{tmpDir})

	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		// 在 runner 返回前強行關閉 store，模擬資料庫故障或連線中斷
		_ = store.Close()
		return "partial", errors.New("backend failed")
	}

	eng := NewEngine(store, policy, WithRunner(runner))

	_, err = eng.SubmitTask(context.Background(), SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "persist-fail-task",
	})
	if err == nil {
		t.Fatalf("expected error from failed task and store, got nil")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "backend failed") {
		t.Errorf("expected error to contain original backend error 'backend failed', got: %s", errStr)
	}
	if !strings.Contains(errStr, "persist task status failed") {
		t.Errorf("expected error to explicitly reveal persistence failure (F-03), got: %s", errStr)
	}
}

// F-04: 驗證當輸出僅包含工具執行失敗 (ToolStatus="error") 且無有效產出時，任務明確標記為 failed (杜絕假成功)
func TestReviewToolError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-f04-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	// runner 退出碼為 0 但只輸出 tool error 事件
	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		return `{"type":"tool_use","part":{"type":"tool","tool":"apply_patch","state":{"status":"error","output":"permission denied"}}}`, nil
	}

	eng := NewEngine(store, policy, WithRunner(runner))

	task, err := eng.SubmitTask(context.Background(), SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "tool-error-task",
	})
	if err == nil {
		t.Fatalf("expected task with tool error to fail, but got nil error (violates F-04)")
	}

	if task.Status != storage.StatusFailed {
		t.Errorf("expected task status to be %s, but got %s (false success)", storage.StatusFailed, task.Status)
	}

	saved, err := store.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("failed to query task from DB: %v", err)
	}
	if saved.Status != storage.StatusFailed {
		t.Errorf("expected DB task status to be %s, got %s", storage.StatusFailed, saved.Status)
	}
	if !strings.Contains(saved.ErrorMessage, "empty output or unresolved error") {
		t.Errorf("expected error message to mention unresolved error, got: %s", saved.ErrorMessage)
	}
}

// G-02: 驗證當父 Context 在任務已成為 running、runner 尚未開始時被取消，資料庫狀態確實持久化為 cancelled
func TestLatestReview_G02_PersistOnCancelBeforeRunner(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-g02-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	var runnerCalls int32
	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		atomic.AddInt32(&runnerCalls, 1)
		return "should-not-run", nil
	}

	eng := NewEngine(store, policy, WithRunner(runner))

	// 先鎖定 cancelsMu 暫停 SubmitTask 的取消登記與後續執行
	eng.cancelsMu.Lock()

	ctx, cancel := context.WithCancel(context.Background())
	taskID := "task-g02-test"

	submitErrCh := make(chan error, 1)
	var returnedTask *storage.Task

	go func() {
		var sErr error
		returnedTask, sErr = eng.SubmitTask(ctx, SubmitRequest{
			ID:        taskID,
			Workspace: tmpDir,
			Prompt:    "cancel-before-runner-persist",
		})
		submitErrCh <- sErr
	}()

	// 等候任務在資料庫中成功寫入且轉為 running
	for i := 0; i < 50; i++ {
		tObj, err := store.GetTask(context.Background(), taskID)
		if err == nil && tObj.Status == storage.StatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 取消父 Context
	cancel()

	// 放行 cancelsMu，讓 SubmitTask 繼續執行
	eng.cancelsMu.Unlock()

	err = <-submitErrCh
	if err == nil {
		t.Fatalf("expected error from cancelled context, got nil")
	}

	if calls := atomic.LoadInt32(&runnerCalls); calls != 0 {
		t.Errorf("expected runner to be called 0 times, but was called %d times", calls)
	}

	if returnedTask == nil || returnedTask.Status != storage.StatusCancelled {
		t.Errorf("expected returned task status to be cancelled, got: %+v", returnedTask)
	}

	// 關鍵斷言 (G-02)：資料庫中任務狀態必須確實持久化為 cancelled，絕不可停留在 running！
	saved, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("failed to query task from store: %v", err)
	}
	if saved.Status != storage.StatusCancelled {
		t.Errorf("expected DB task status to be %s, but got %s (violates G-02: remained in running)", storage.StatusCancelled, saved.Status)
	}
}






