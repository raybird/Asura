package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSQLiteStore_CronSchemaMigration 驗證排程相關資料表與索引在初始化時正確建立 (Task 1.1 內迴圈)
func TestSQLiteStore_CronSchemaMigration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cron-mig-*")
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

	// 檢查 cron_schedules 欄位
	schedCols, err := store.getTableColumns("cron_schedules")
	if err != nil {
		t.Fatalf("getTableColumns(cron_schedules) failed: %v", err)
	}
	expectedSchedCols := []string{"id", "name", "workspace", "schedule", "prompt", "status", "created_at", "updated_at", "last_run_at", "next_run_at", "pause_reason"}
	for _, col := range expectedSchedCols {
		if !schedCols[col] {
			t.Errorf("expected column %q in cron_schedules, but missing", col)
		}
	}

	// 檢查 cron_runs 欄位
	runCols, err := store.getTableColumns("cron_runs")
	if err != nil {
		t.Fatalf("getTableColumns(cron_runs) failed: %v", err)
	}
	expectedRunCols := []string{"id", "cron_id", "scheduled_time", "triggered_time", "task_id", "status", "reason", "created_at"}
	for _, col := range expectedRunCols {
		if !runCols[col] {
			t.Errorf("expected column %q in cron_runs, but missing", col)
		}
	}
}

// TestSQLiteStore_CronCRUD 驗證排程與觸發歷史之增刪查改與狀態維護 (Task 1.2 內迴圈)
func TestSQLiteStore_CronCRUD(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cron-crud-*")
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
	now := time.Now().Truncate(time.Second)
	next := now.Add(30 * time.Minute)

	// 1. 建立排程
	sched := CronSchedule{
		ID:        "cron-1",
		Name:      "test-schedule",
		Workspace: "/test/workspace",
		Schedule:  "*/10 * * * *",
		Prompt:    "執行單元測試",
		Status:    CronStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
		NextRunAt: &next,
	}
	if err := store.CreateCronSchedule(ctx, sched); err != nil {
		t.Fatalf("CreateCronSchedule failed: %v", err)
	}

	// 2. 單筆查詢
	got, err := store.GetCronSchedule(ctx, "cron-1")
	if err != nil {
		t.Fatalf("GetCronSchedule failed: %v", err)
	}
	if got.Name != "test-schedule" || got.Status != CronStatusActive || got.NextRunAt == nil || !got.NextRunAt.Equal(next) {
		t.Fatalf("unexpected got schedule: %+v", got)
	}

	// 3. 查詢不存在的排程
	if _, err := store.GetCronSchedule(ctx, "non-existent"); err != ErrCronNotFound {
		t.Fatalf("expected ErrCronNotFound, got %v", err)
	}

	// 4. 更新狀態 (Pause)
	if err := store.UpdateCronScheduleStatus(ctx, "cron-1", CronStatusPaused, "workspace missing"); err != nil {
		t.Fatalf("UpdateCronScheduleStatus failed: %v", err)
	}
	paused, err := store.GetCronSchedule(ctx, "cron-1")
	if err != nil {
		t.Fatalf("GetCronSchedule failed: %v", err)
	}
	if paused.Status != CronStatusPaused || paused.PauseReason != "workspace missing" {
		t.Fatalf("expected paused with reason, got %+v", paused)
	}

	// 5. 更新下次/上次執行時間
	lastRun := now.Add(5 * time.Minute)
	nextRun2 := now.Add(15 * time.Minute)
	if err := store.UpdateCronScheduleNextRun(ctx, "cron-1", nextRun2, &lastRun); err != nil {
		t.Fatalf("UpdateCronScheduleNextRun failed: %v", err)
	}
	updated, _ := store.GetCronSchedule(ctx, "cron-1")
	if updated.LastRunAt == nil || !updated.LastRunAt.Equal(lastRun) || updated.NextRunAt == nil || !updated.NextRunAt.Equal(nextRun2) {
		t.Fatalf("unexpected updated run times: %+v", updated)
	}

	// 6. 記錄觸發歷史
	run1 := CronRun{
		ID:            "run-1",
		CronID:        "cron-1",
		ScheduledTime: now,
		TriggeredTime: now.Add(50 * time.Millisecond),
		TaskID:        "task-abc",
		Status:        CronRunTriggered,
		CreatedAt:     now,
	}
	run2 := CronRun{
		ID:            "run-2",
		CronID:        "cron-1",
		ScheduledTime: nextRun2,
		TriggeredTime: nextRun2,
		Status:        CronRunSkipped,
		Reason:        "previous task still running",
		CreatedAt:     nextRun2,
	}
	if err := store.RecordCronRun(ctx, run1); err != nil {
		t.Fatalf("RecordCronRun run1 failed: %v", err)
	}
	if err := store.RecordCronRun(ctx, run2); err != nil {
		t.Fatalf("RecordCronRun run2 failed: %v", err)
	}

	runs, err := store.ListCronRuns(ctx, "cron-1", 10)
	if err != nil {
		t.Fatalf("ListCronRuns failed: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}

	// 7. 刪除排程與外鍵約束測試
	if err := store.DeleteCronSchedule(ctx, "cron-1"); err != nil {
		t.Fatalf("DeleteCronSchedule failed: %v", err)
	}
	if _, err := store.GetCronSchedule(ctx, "cron-1"); err != ErrCronNotFound {
		t.Fatalf("expected ErrCronNotFound after delete, got %v", err)
	}
	// 外鍵串聯刪除後，runs 也應被清空
	runsAfterDelete, err := store.ListCronRuns(ctx, "cron-1", 10)
	if err != nil {
		t.Fatalf("ListCronRuns after delete failed: %v", err)
	}
	if len(runsAfterDelete) != 0 {
		t.Fatalf("expected 0 runs after cascade delete, got %d", len(runsAfterDelete))
	}
}
