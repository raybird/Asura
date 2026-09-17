package cron

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/storage"
)

func TestScheduler_AlignSchedules_NoCatchup(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	past := now.Add(-3 * time.Hour)

	// 建立一筆過期的排程
	sched := storage.CronSchedule{
		ID:        "sched-expired-1",
		Name:      "daily-task",
		Workspace: tmpDir,
		Schedule:  "0 2 * * *",
		Prompt:    "daily cleanup",
		Status:    storage.CronStatusActive,
		CreatedAt: past,
		UpdatedAt: past,
		NextRunAt: &past,
	}
	if err := store.CreateCronSchedule(ctx, sched); err != nil {
		t.Fatal(err)
	}

	policy := auth.NewPolicy([]string{tmpDir})
	var dispatcherCalled atomic.Bool
	dispatcher := func(ctx context.Context, cronID, ws, p string) (string, error) {
		dispatcherCalled.Store(true)
		return "ok", nil
	}

	scheduler := NewScheduler(store, policy, dispatcher)

	// 執行重啟對齊
	if err := scheduler.AlignSchedules(ctx, now); err != nil {
		t.Fatalf("AlignSchedules failed: %v", err)
	}

	// 驗證未補跑任何歷史任務 (CRON-004)
	if dispatcherCalled.Load() {
		t.Fatalf("runner was called during alignment, expected no catchup")
	}

	// 驗證 NextRunAt 已更新為未來時間
	updated, err := store.GetCronSchedule(ctx, "sched-expired-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.After(now) {
		t.Fatalf("expected NextRunAt to be in the future, got %v", updated.NextRunAt)
	}
}

func TestScheduler_Trigger_OverlapSkip(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	sched := storage.CronSchedule{
		ID:        "sched-overlap-1",
		Name:      "overlap-task",
		Workspace: tmpDir,
		Schedule:  "*/5 * * * *",
		Prompt:    "heavy work",
		Status:    storage.CronStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
		NextRunAt: &now,
	}
	if err := store.CreateCronSchedule(ctx, sched); err != nil {
		t.Fatal(err)
	}

	policy := auth.NewPolicy([]string{tmpDir})
	dispatcher := func(ctx context.Context, cronID, ws, p string) (string, error) {
		taskID := "task-overlap-1"
		task := &storage.Task{
			ID:        taskID,
			Workspace: ws,
			Prompt:    p,
			Status:    storage.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		}
		_ = store.CreateTask(ctx, task)
		return taskID, nil
	}

	scheduler := NewScheduler(store, policy, dispatcher)

	// 1. 第一次觸發
	if err := scheduler.Trigger(ctx, "sched-overlap-1", now); err != nil {
		t.Fatalf("first trigger failed: %v", err)
	}

	// 2. 第二次觸發（前次任務尚未完成）
	if err := scheduler.Trigger(ctx, "sched-overlap-1", now.Add(time.Minute)); err != nil {
		t.Fatalf("second trigger failed: %v", err)
	}

	// 3. 驗證 cron_runs 紀錄為 skipped
	runs, err := store.ListCronRuns(ctx, "sched-overlap-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	foundSkipped := false
	for _, r := range runs {
		if r.Status == storage.CronRunSkipped {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Fatalf("expected a skipped run record, got runs: %+v", runs)
	}
}

// TestScheduler_Trigger_OverlapSkip_Cancelling 驗證當前次任務處於 StatusCancelling 時，依 CRON-004 略過本次觸發
func TestScheduler_Trigger_OverlapSkip_Cancelling(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cron-cancelling-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	policy := auth.NewPolicy([]string{tmpDir})

	now := time.Now().UTC().Truncate(time.Second)
	sched := storage.CronSchedule{
		ID:        "sched-cancelling-1",
		Name:      "test-cancelling-overlap",
		Workspace: tmpDir,
		Schedule:  "* * * * *",
		Prompt:    "echo test",
		Status:    storage.CronStatusActive,
		NextRunAt: &now,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateCronSchedule(ctx, sched); err != nil {
		t.Fatal(err)
	}

	callCount := 0
	dispatcher := func(ctx context.Context, cronID, workspace, prompt string) (string, error) {
		callCount++
		taskID := "task-cancelling-1"
		task := &storage.Task{
			ID:        taskID,
			Workspace: workspace,
			Prompt:    prompt,
			Status:    storage.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		}
		_ = store.CreateTask(ctx, task)
		return taskID, nil
	}

	scheduler := NewScheduler(store, policy, dispatcher)

	// 1. 第一次觸發
	if err := scheduler.Trigger(ctx, "sched-cancelling-1", now); err != nil {
		t.Fatalf("first trigger failed: %v", err)
	}

	// 將前次任務狀態更新為 StatusCancelling（模擬正在取消中）
	_ = store.UpdateTaskStatus(ctx, "task-cancelling-1", storage.StatusCancelling, "", "cancellation requested")

	// 2. 第二次觸發：前次任務處於 cancelling，必須略過
	if err := scheduler.Trigger(ctx, "sched-cancelling-1", now.Add(time.Minute)); err != nil {
		t.Fatalf("second trigger failed: %v", err)
	}

	// 驗證 dispatcher 沒有被第二次呼叫
	if callCount != 1 {
		t.Fatalf("expected dispatcher callCount to be 1, got %d", callCount)
	}

	// 3. 驗證記錄為 skipped
	runs, err := store.ListCronRuns(ctx, "sched-cancelling-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	foundSkipped := false
	for _, r := range runs {
		if r.Status == storage.CronRunSkipped {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Fatalf("expected a skipped run record when task is cancelling, got runs: %+v", runs)
	}
}

func TestScheduler_Trigger_UnauthorizedAutoPause(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	unauthorizedWs := filepath.Join(tmpDir, "unauth-dir")
	_ = os.MkdirAll(unauthorizedWs, 0755)

	allowedWs := filepath.Join(tmpDir, "allowed-dir")
	_ = os.MkdirAll(allowedWs, 0755)
	policy := auth.NewPolicy([]string{allowedWs})

	sched := storage.CronSchedule{
		ID:        "sched-unauth-1",
		Name:      "unauthorized-task",
		Workspace: unauthorizedWs,
		Schedule:  "0 12 * * *",
		Prompt:    "should fail",
		Status:    storage.CronStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
		NextRunAt: &now,
	}
	if err := store.CreateCronSchedule(ctx, sched); err != nil {
		t.Fatal(err)
	}

	var dispatcherCalled atomic.Bool
	dispatcher := func(ctx context.Context, cronID, ws, p string) (string, error) {
		dispatcherCalled.Store(true)
		return "ok", nil
	}

	scheduler := NewScheduler(store, policy, dispatcher)

	// 觸發未授權排程
	err = scheduler.Trigger(ctx, "sched-unauth-1", now)
	if err == nil {
		t.Fatalf("expected error triggering unauthorized workspace, got nil")
	}

	// 驗證後端未執行
	if dispatcherCalled.Load() {
		t.Fatalf("runner was invoked for unauthorized workspace")
	}

	// 驗證排程狀態自動轉為 paused (CRON-005)
	updated, err := store.GetCronSchedule(ctx, "sched-unauth-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != storage.CronStatusPaused {
		t.Fatalf("expected schedule to be paused, got %s", updated.Status)
	}

	// 驗證 cron_runs 紀錄為 unauthorized
	runs, err := store.ListCronRuns(ctx, "sched-unauth-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) == 0 || runs[0].Status != storage.CronRunUnauthorized {
		t.Fatalf("expected run with status unauthorized, got %+v", runs)
	}
}

func TestScheduler_Trigger_DispatchFailureAdvancesNextRunAt(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	sched := storage.CronSchedule{
		ID:        "sched-dispatch-fail-1",
		Name:      "fail-task",
		Workspace: tmpDir,
		Schedule:  "0 12 * * *",
		Prompt:    "should fail dispatch",
		Status:    storage.CronStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
		NextRunAt: &now,
	}
	if err := store.CreateCronSchedule(ctx, sched); err != nil {
		t.Fatal(err)
	}

	policy := auth.NewPolicy([]string{tmpDir})
	dispatcher := func(ctx context.Context, cronID, ws, p string) (string, error) {
		return "", errors.New("capacity exceeded: worker pool saturated")
	}

	scheduler := NewScheduler(store, policy, dispatcher)

	// 觸發排程（dispatcher 失敗）
	err = scheduler.Trigger(ctx, "sched-dispatch-fail-1", now)
	if err == nil {
		t.Fatalf("expected error from trigger when dispatcher fails")
	}

	// 1. MUST FIX 6: 驗證 NextRunAt 必須被推進到未來時間點
	updated, err := store.GetCronSchedule(ctx, "sched-dispatch-fail-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.After(now) {
		t.Fatalf("expected NextRunAt to be advanced to future even on dispatch failure, got %v", updated.NextRunAt)
	}

	// 2. 驗證 cron_runs 記錄了 CronRunFailed 且包含 Reason
	runs, err := store.ListCronRuns(ctx, "sched-dispatch-fail-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) == 0 {
		t.Fatalf("expected a cron_run record on dispatch failure, got none")
	}
	if runs[0].Status != storage.CronRunFailed {
		t.Fatalf("expected status %s, got %s", storage.CronRunFailed, runs[0].Status)
	}
	if runs[0].Reason == "" {
		t.Fatalf("expected non-empty Reason in cron_runs for dispatch failure")
	}
}

