package integration

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/engine"
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

// TestCRON001_WorkspaceMutexQueue 驗證 CRON-001: 同專案互斥排隊與依序執行
func TestCRON001_WorkspaceMutexQueue(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cron001-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ws := filepath.Join(tmpDir, "project-a")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")
	socketPath := filepath.Join(tmpDir, "daemon.sock")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{ws})

	var concurrentRunning atomic.Int32
	var maxConcurrent atomic.Int32
	t1Started := make(chan struct{})
	t1Release := make(chan struct{})

	fakeRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		cur := concurrentRunning.Add(1)
		defer concurrentRunning.Add(-1)
		for {
			oldMax := maxConcurrent.Load()
			if cur <= oldMax || maxConcurrent.CompareAndSwap(oldMax, cur) {
				break
			}
		}

		if prompt == "task-1" {
			close(t1Started)
			select {
			case <-t1Release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return "ok: " + prompt, nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(fakeRunner))
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: socketPath,
		MaxWorkers: 2,
		MaxQueue:   10,
	}, store, policy, daemon.WithEngine(eng))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- d.Start()
	}()
	defer d.Close()

	waitForSocket(t, socketPath)
	client, err := daemon.NewClient(socketPath)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// 1. 提交任務 1
	resp1, err := client.Submit(ctx, engine.SubmitRequest{
		Workspace: ws,
		Prompt:    "task-1",
	})
	if err != nil {
		t.Fatalf("submit task 1 failed: %v", err)
	}
	if resp1 == nil {
		t.Fatalf("expected task 1 response")
	}

	// 等待任務 1 真正開始執行
	select {
	case <-t1Started:
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for task 1 to start")
	}

	// 2. 提交同專案的任務 2
	client2, err := daemon.NewClient(socketPath)
	if err != nil {
		t.Fatalf("failed to create client2: %v", err)
	}
	defer client2.Close()

	resp2, err := client2.Submit(ctx, engine.SubmitRequest{
		Workspace: ws,
		Prompt:    "task-2",
	})
	if err != nil {
		t.Fatalf("submit task 2 failed: %v", err)
	}
	// 驗證初始狀態為 queued 排隊等待
	if resp2.Status != storage.StatusQueued {
		t.Fatalf("expected task 2 status to be 'queued', got %q", resp2.Status)
	}

	// 3. 釋放任務 1，驗證同專案不並行執行且任務 2 接續完成
	close(t1Release)

	// 輪詢等待任務 2 達到 completed
	var finalTask2 *storage.Task
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		t2, err := store.GetTask(ctx, resp2.ID)
		if err == nil && t2.Status == storage.StatusCompleted {
			finalTask2 = t2
			break
		}
	}
	if finalTask2 == nil {
		t.Fatalf("task 2 never completed after task 1 released")
	}

	// 驗證同專案的並發執行數絕對不超過 1
	if max := maxConcurrent.Load(); max > 1 {
		t.Fatalf("expected max concurrent tasks in same workspace <= 1, got %d", max)
	}

	cancel()
	_ = <-errChan
}

// TestCRON002_InteractiveChannelReserved 驗證 CRON-002: 保留互動通道與背景容量上限
func TestCRON002_InteractiveChannelReserved(t *testing.T) {
	// WorkerPool: maxWorkers=2, maxCronWorkers=1
	pool := daemon.NewWorkerPoolWithCapacity(daemon.CapacityConfig{
		MaxWorkers:     2,
		MaxCronWorkers: 1,
		MaxQueue:       5,
	})
	defer pool.Close()

	cron1Started := make(chan struct{})
	cron1Release := make(chan struct{})

	// 1. 提交背景 Cron 任務 1（佔用唯一背景 Worker 額度）
	cronErrChan, err := pool.SubmitAsyncClassified(context.Background(), daemon.TaskClassCron, func() error {
		close(cron1Started)
		<-cron1Release
		return nil
	})
	if err != nil {
		t.Fatalf("submit cron 1 failed: %v", err)
	}

	select {
	case <-cron1Started:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for cron 1 to start")
	}

	// 2. 提交背景 Cron 任務 2（背景額度已滿，應進入佇列或無法搶佔保留 Worker）
	cron2Started := make(chan struct{})
	_, err = pool.SubmitAsyncClassified(context.Background(), daemon.TaskClassCron, func() error {
		close(cron2Started)
		return nil
	})
	if err != nil {
		t.Fatalf("submit cron 2 failed: %v", err)
	}

	// 驗證 Cron 2 目前並未執行（因為保留給互動通道）
	select {
	case <-cron2Started:
		t.Fatalf("cron 2 should not start while cron 1 is running (maxCronWorkers=1)")
	case <-time.After(100 * time.Millisecond):
		// 正常：cron 2 正在等待
	}

	// 3. 此時提交即時互動任務 3，驗證它能立即分配到保留的 Worker 啟動執行！
	interactiveStarted := make(chan struct{})
	interactiveErrChan, err := pool.SubmitAsyncClassified(context.Background(), daemon.TaskClassInteractive, func() error {
		close(interactiveStarted)
		return nil
	})
	if err != nil {
		t.Fatalf("submit interactive task failed: %v", err)
	}

	select {
	case <-interactiveStarted:
		// 成功！互動任務立即啟動
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout: interactive task was blocked by cron task!")
	}

	// 等待互動任務完成
	if err := <-interactiveErrChan; err != nil {
		t.Fatalf("interactive task failed: %v", err)
	}

	// 釋放 Cron 1，Cron 2 隨後應被喚醒執行
	close(cron1Release)
	if err := <-cronErrChan; err != nil {
		t.Fatalf("cron 1 failed: %v", err)
	}

	select {
	case <-cron2Started:
		// 成功！cron 1 結束後 cron 2 接續啟動
	case <-time.After(2 * time.Second):
		t.Fatalf("cron 2 failed to start after cron 1 completed")
	}
}

func waitForSocket(t *testing.T, socketPath string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for socket: %s", socketPath)
}

