package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"asura/internal/storage"
)

// TestCRON008_PersistenceAcrossRestart 驗證 CRON-008: 排程與觸發紀錄於服務重啟後完整持久化
func TestCRON008_PersistenceAcrossRestart(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cron008-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	next := now.Add(1 * time.Hour)

	// 1. 初始化第一個 Store 實例並寫入排程與觸發歷史
	store1, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store1: %v", err)
	}

	cronSched := storage.CronSchedule{
		ID:        "cron-test-1",
		Name:      "daily-health-check",
		Workspace: tmpDir,
		Schedule:  "0 2 * * *",
		Prompt:    "檢查專案狀態",
		Status:    storage.CronStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
		NextRunAt: &next,
	}

	if err := store1.CreateCronSchedule(ctx, cronSched); err != nil {
		t.Fatalf("CreateCronSchedule failed: %v", err)
	}

	cronRun := storage.CronRun{
		ID:            "run-test-1",
		CronID:        "cron-test-1",
		ScheduledTime: now,
		TriggeredTime: now.Add(100 * time.Millisecond),
		TaskID:        "task-123",
		Status:        storage.CronRunTriggered,
		CreatedAt:     now,
	}

	if err := store1.RecordCronRun(ctx, cronRun); err != nil {
		t.Fatalf("RecordCronRun failed: %v", err)
	}

	// 關閉第一個連線
	if err := store1.Close(); err != nil {
		t.Fatalf("failed to close store1: %v", err)
	}

	// 2. 模擬重啟：以同一個 DB 檔案建立第二個 Store 實例
	store2, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store2: %v", err)
	}
	defer store2.Close()

	// 3. 驗證排程與觸發歷史完整還原
	schedules, err := store2.ListCronSchedules(ctx)
	if err != nil {
		t.Fatalf("ListCronSchedules failed: %v", err)
	}
	if len(schedules) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(schedules))
	}

	recovered := schedules[0]
	if recovered.ID != "cron-test-1" || recovered.Name != "daily-health-check" || recovered.Status != storage.CronStatusActive {
		t.Fatalf("unexpected recovered schedule: %+v", recovered)
	}
	if recovered.NextRunAt == nil || !recovered.NextRunAt.Equal(next) {
		t.Fatalf("expected next_run_at %v, got %v", next, recovered.NextRunAt)
	}

	runs, err := store2.ListCronRuns(ctx, "cron-test-1", 10)
	if err != nil {
		t.Fatalf("ListCronRuns failed: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	recoveredRun := runs[0]
	if recoveredRun.ID != "run-test-1" || recoveredRun.TaskID != "task-123" || recoveredRun.Status != storage.CronRunTriggered {
		t.Fatalf("unexpected recovered run: %+v", recoveredRun)
	}
}
