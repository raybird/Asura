package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// SCN-001: 授權使用者取得單次任務結果，並可再次查得相同任務的結果（外迴圈整合測試）
func TestTaskStorage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-storage-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. 建立任務
	task := &Task{
		ID:        "task-test-001",
		Prompt:    "echo 'hello world'",
		Workspace: "/tmp/allowed/project",
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// 2. 查得初始狀態
	got, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if got.ID != task.ID {
		t.Errorf("expected ID %q, got %q", task.ID, got.ID)
	}
	if got.Status != StatusPending {
		t.Errorf("expected status %q, got %q", StatusPending, got.Status)
	}
	if got.Prompt != task.Prompt {
		t.Errorf("expected prompt %q, got %q", task.Prompt, got.Prompt)
	}

	// 3. 更新為 running
	if err := store.UpdateTaskStatus(ctx, task.ID, StatusRunning, "", ""); err != nil {
		t.Fatalf("UpdateTaskStatus to running failed: %v", err)
	}

	gotRunning, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask after running failed: %v", err)
	}
	if gotRunning.Status != StatusRunning {
		t.Errorf("expected status %q, got %q", StatusRunning, gotRunning.Status)
	}

	// 4. 更新為 completed 並記錄結果
	expectedResult := "hello world\n"
	if err := store.UpdateTaskStatus(ctx, task.ID, StatusCompleted, expectedResult, ""); err != nil {
		t.Fatalf("UpdateTaskStatus to completed failed: %v", err)
	}

	// 5. 驗證可再次查得相同任務結果
	gotCompleted, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask after completed failed: %v", err)
	}
	if gotCompleted.Status != StatusCompleted {
		t.Errorf("expected status %q, got %q", StatusCompleted, gotCompleted.Status)
	}
	if gotCompleted.Result != expectedResult {
		t.Errorf("expected result %q, got %q", expectedResult, gotCompleted.Result)
	}
	if gotCompleted.ErrorMessage != "" {
		t.Errorf("expected empty error message, got %q", gotCompleted.ErrorMessage)
	}
}

// 內迴圈單元測試：驗證 Task 狀態轉換邊界、查無任務處理及 WAL 單一連線配置
func TestSQLiteStore_Unit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-storage-unit-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 查詢不存在的任務
	_, err = store.GetTask(ctx, "non-existent")
	if err != ErrTaskNotFound {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}

	// 建立任務並測試失敗狀態紀錄
	task := &Task{
		ID:        "task-fail-002",
		Prompt:    "exit 1",
		Workspace: "/tmp/allowed/project",
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	if err := store.UpdateTaskStatus(ctx, task.ID, StatusFailed, "", "command returned exit status 1"); err != nil {
		t.Fatalf("UpdateTaskStatus failed: %v", err)
	}

	failedTask, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if failedTask.Status != StatusFailed {
		t.Errorf("expected status %q, got %q", StatusFailed, failedTask.Status)
	}
	if failedTask.ErrorMessage != "command returned exit status 1" {
		t.Errorf("expected error message %q, got %q", "command returned exit status 1", failedTask.ErrorMessage)
	}

	// 測試取消狀態
	cancelTask := &Task{
		ID:        "task-cancel-003",
		Prompt:    "sleep 60",
		Workspace: "/tmp/allowed/project",
		Status:    StatusRunning,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateTask(ctx, cancelTask); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if err := store.UpdateTaskStatus(ctx, cancelTask.ID, StatusCancelled, "", "context cancelled"); err != nil {
		t.Fatalf("UpdateTaskStatus to cancelled failed: %v", err)
	}
	gotCancelled, err := store.GetTask(ctx, cancelTask.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if gotCancelled.Status != StatusCancelled {
		t.Errorf("expected status %q, got %q", StatusCancelled, gotCancelled.Status)
	}
}

// SCN-006 內迴圈單元測試：重啟恢復保留已完成任務，並將中斷之 running / pending 任務標記為 failed
func TestSQLiteStore_RecoverInterruptedTasks(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-storage-recover-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	// 1. 寫入已完成任務
	taskCompleted := &Task{
		ID:        "task-completed",
		Prompt:    "echo done",
		Workspace: "/tmp/allowed",
		Status:    StatusCompleted,
		Result:    "completed result",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, taskCompleted); err != nil {
		t.Fatalf("create completed task: %v", err)
	}

	// 2. 寫入中斷前處於 running 的任務
	taskRunning := &Task{
		ID:        "task-running",
		Prompt:    "sleep 100",
		Workspace: "/tmp/allowed",
		Status:    StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, taskRunning); err != nil {
		t.Fatalf("create running task: %v", err)
	}

	// 3. 寫入中斷前處於 pending 的任務
	taskPending := &Task{
		ID:        "task-pending",
		Prompt:    "echo pending",
		Workspace: "/tmp/allowed",
		Status:    StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, taskPending); err != nil {
		t.Fatalf("create pending task: %v", err)
	}

	// 4. 執行恢復
	recoveredCount, err := store.RecoverInterruptedTasks(ctx)
	if err != nil {
		t.Fatalf("RecoverInterruptedTasks failed: %v", err)
	}
	if recoveredCount != 2 {
		t.Errorf("expected 2 recovered tasks, got %d", recoveredCount)
	}

	// 5. 斷言已完成任務不受任何影響
	savedCompleted, err := store.GetTask(ctx, "task-completed")
	if err != nil {
		t.Fatalf("get completed task: %v", err)
	}
	if savedCompleted.Status != StatusCompleted {
		t.Errorf("expected completed task status %s, got %s", StatusCompleted, savedCompleted.Status)
	}
	if savedCompleted.Result != "completed result" {
		t.Errorf("expected completed result %q, got %q", "completed result", savedCompleted.Result)
	}

	// 6. 斷言中斷之 running 任務轉為 failed 且具有明確錯誤原因
	savedRunning, err := store.GetTask(ctx, "task-running")
	if err != nil {
		t.Fatalf("get running task: %v", err)
	}
	if savedRunning.Status != StatusFailed {
		t.Errorf("expected running task status %s, got %s", StatusFailed, savedRunning.Status)
	}
	if savedRunning.ErrorMessage == "" {
		t.Errorf("expected non-empty error message for interrupted running task")
	}

	// 7. 斷言中斷之 pending 任務轉為 failed 且具有明確錯誤原因
	savedPending, err := store.GetTask(ctx, "task-pending")
	if err != nil {
		t.Fatalf("get pending task: %v", err)
	}
	if savedPending.Status != StatusFailed {
		t.Errorf("expected pending task status %s, got %s", StatusFailed, savedPending.Status)
	}
	if savedPending.ErrorMessage == "" {
		t.Errorf("expected non-empty error message for interrupted pending task")
	}
}
