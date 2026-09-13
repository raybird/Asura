package cron

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/engine"
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
	var runnerCalled atomic.Bool
	eng := engine.NewEngine(store, policy, engine.WithRunner(func(ctx context.Context, ws, p string) (string, error) {
		runnerCalled.Store(true)
		return "ok", nil
	}))

	scheduler := NewScheduler(store, policy, eng)

	// 執行重啟對齊
	if err := scheduler.AlignSchedules(ctx, now); err != nil {
		t.Fatalf("AlignSchedules failed: %v", err)
	}

	// 驗證未補跑任何歷史任務 (CRON-004)
	if runnerCalled.Load() {
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
	started := make(chan struct{})
	blocker := make(chan struct{})

	eng := engine.NewEngine(store, policy, engine.WithRunner(func(ctx context.Context, ws, p string) (string, error) {
		close(started)
		<-blocker
		return "finished", nil
	}))

	scheduler := NewScheduler(store, policy, eng)

	// 1. 第一次觸發
	if err := scheduler.Trigger(ctx, "sched-overlap-1", now); err != nil {
		t.Fatalf("first trigger failed: %v", err)
	}
	<-started

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

	close(blocker)
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
		Name:      "unauth-task",
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

	var runnerCalled atomic.Bool
	eng := engine.NewEngine(store, policy, engine.WithRunner(func(ctx context.Context, ws, p string) (string, error) {
		runnerCalled.Store(true)
		return "ok", nil
	}))

	scheduler := NewScheduler(store, policy, eng)

	// 觸發未授權排程
	err = scheduler.Trigger(ctx, "sched-unauth-1", now)
	if err == nil {
		t.Fatalf("expected error triggering unauthorized workspace, got nil")
	}

	// 驗證後端未執行
	if runnerCalled.Load() {
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
