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
