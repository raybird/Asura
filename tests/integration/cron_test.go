package integration

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/cron"
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

// TestCRON003_StandardFieldParsingAndNextRun 驗證 CRON-003: 標準 5 欄位 Cron 表示式解析與本地時區下次執行時間計算
func TestCRON003_StandardFieldParsingAndNextRun(t *testing.T) {
	// 1. 成功解析標準 5 欄位表達式
	sched, err := cron.Parse("0 15 * * *")
	if err != nil {
		t.Fatalf("Parse('0 15 * * *') failed: %v", err)
	}

	loc := time.Local
	base := time.Date(2026, 9, 13, 14, 0, 0, 0, loc)
	expected := time.Date(2026, 9, 13, 15, 0, 0, 0, loc)

	next := sched.Next(base)
	if !next.Equal(expected) {
		t.Fatalf("expected next run at %v, got %v", expected, next)
	}

	// 2. 跨日計算
	base2 := time.Date(2026, 9, 13, 16, 0, 0, 0, loc)
	expected2 := time.Date(2026, 9, 14, 15, 0, 0, 0, loc)
	next2 := sched.Next(base2)
	if !next2.Equal(expected2) {
		t.Fatalf("expected next run at %v, got %v", expected2, next2)
	}

	// 3. 非法表達式校驗
	invalidExprs := []string{
		"0 15 * *",       // 4 欄位
		"0 15 * * * *",   // 6 欄位
		"60 15 * * *",    // 分鐘超出 59
		"0 24 * * *",     // 小時超出 23
		"0 15 32 * *",    // 日超出 31
		"0 15 * 13 *",    // 月超出 12
		"0 15 * * 8",     // 週超出 7
		"invalid syntax", // 非法文字
	}
	for _, expr := range invalidExprs {
		if _, err := cron.Parse(expr); err == nil {
			t.Errorf("expected error parsing invalid expression %q, got nil", expr)
		}
	}
}

// TestCRON004_OverlapSkipAndNoCatchup 驗證 CRON-004: 前次執行未完成時略過觸發 (Skip) 且重啟漏跑不補 (No Catchup)
func TestCRON004_OverlapSkipAndNoCatchup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cron004-*")
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
	now := time.Now().Truncate(time.Second)

	// 1. 驗證漏跑不補（No Catchup）：排程下次時間為 2 小時前（過期漏跑）
	pastTime := now.Add(-2 * time.Hour)
	cronSched := storage.CronSchedule{
		ID:        "cron-catchup-test",
		Name:      "catchup-test",
		Workspace: tmpDir,
		Schedule:  "0 2 * * *",
		Prompt:    "echo catchup",
		Status:    storage.CronStatusActive,
		CreatedAt: pastTime,
		UpdatedAt: pastTime,
		NextRunAt: &pastTime,
	}
	if err := store.CreateCronSchedule(ctx, cronSched); err != nil {
		t.Fatal(err)
	}

	policy := auth.NewPolicy([]string{tmpDir})
	var runnerCalls atomic.Int32
	eng := engine.NewEngine(store, policy, engine.WithRunner(func(ctx context.Context, ws, p string) (string, error) {
		runnerCalls.Add(1)
		return "ok", nil
	}))

	scheduler := cron.NewScheduler(store, policy, eng)

	// 重啟啟動時處理過期排程：不補跑，重新計算未來時間
	if err := scheduler.AlignSchedules(ctx, now); err != nil {
		t.Fatalf("AlignSchedules failed: %v", err)
	}

	// 驗證 runner 呼叫次數為 0（不補跑）
	if calls := runnerCalls.Load(); calls != 0 {
		t.Fatalf("expected 0 calls during no-catchup alignment, got %d", calls)
	}

	// 驗證 NextRunAt 已被重算到未來
	updatedSched, err := store.GetCronSchedule(ctx, "cron-catchup-test")
	if err != nil {
		t.Fatal(err)
	}
	if updatedSched.NextRunAt == nil || !updatedSched.NextRunAt.After(now) {
		t.Fatalf("expected next run at after now, got %v", updatedSched.NextRunAt)
	}

	// 2. 驗證重疊略過（Overlap Skip）：前次任務尚未完成時，觸發時直接 Skip
	taskBlocker := make(chan struct{})
	taskStarted := make(chan struct{})
	blockingRunner := func(ctx context.Context, ws, p string) (string, error) {
		close(taskStarted)
		<-taskBlocker
		return "done", nil
	}
	blockingEng := engine.NewEngine(store, policy, engine.WithRunner(blockingRunner))
	schedWithBlocker := cron.NewScheduler(store, policy, blockingEng)

	// 第一次觸發：啟動任務
	err = schedWithBlocker.Trigger(ctx, "cron-catchup-test", now)
	if err != nil {
		t.Fatalf("first trigger failed: %v", err)
	}
	<-taskStarted // 確認任務 1 正在執行中

	// 第二次觸發：前任務仍在執行，應被自動 Skip
	err = schedWithBlocker.Trigger(ctx, "cron-catchup-test", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second trigger returned error: %v", err)
	}

	// 檢查 cron_runs 歷史：應有一筆 skipped 紀錄
	runs, err := store.ListCronRuns(ctx, "cron-catchup-test", 10)
	if err != nil {
		t.Fatal(err)
	}
	hasSkipped := false
	for _, r := range runs {
		if r.Status == storage.CronRunSkipped {
			hasSkipped = true
			break
		}
	}
	if !hasSkipped {
		t.Fatalf("expected a cron run with status 'skipped', got runs: %+v", runs)
	}

	close(taskBlocker)
}

// TestCRON005_UnauthorizedWorkspaceAutoPause 驗證 CRON-005: 專案目錄未授權時回絕啟動後端，紀錄 unauthorized 失敗並自動暫停排程
func TestCRON005_UnauthorizedWorkspaceAutoPause(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cron005-*")
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
	now := time.Now().Truncate(time.Second)

	// 建立未授權目錄
	unauthorizedDir := filepath.Join(tmpDir, "unauthorized-project")
	_ = os.MkdirAll(unauthorizedDir, 0755)

	// Policy 僅允許 /allowed/path
	allowedDir := filepath.Join(tmpDir, "allowed-project")
	_ = os.MkdirAll(allowedDir, 0755)
	policy := auth.NewPolicy([]string{allowedDir})

	cronSched := storage.CronSchedule{
		ID:        "cron-unauth-test",
		Name:      "unauth-task",
		Workspace: unauthorizedDir,
		Schedule:  "*/5 * * * *",
		Prompt:    "malicious task",
		Status:    storage.CronStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
		NextRunAt: &now,
	}
	if err := store.CreateCronSchedule(ctx, cronSched); err != nil {
		t.Fatal(err)
	}

	var runnerInvoked atomic.Bool
	eng := engine.NewEngine(store, policy, engine.WithRunner(func(ctx context.Context, ws, p string) (string, error) {
		runnerInvoked.Store(true)
		return "should not run", nil
	}))

	scheduler := cron.NewScheduler(store, policy, eng)

	// 觸發排程
	err = scheduler.Trigger(ctx, "cron-unauth-test", now)
	if err == nil {
		t.Fatalf("expected error triggering unauthorized workspace, got nil")
	}

	// 1. 斷言後端執行器零啟動
	if runnerInvoked.Load() {
		t.Fatalf("backend runner was invoked for unauthorized workspace!")
	}

	// 2. 斷言 cron_runs 歷史記錄狀態為 unauthorized
	runs, err := store.ListCronRuns(ctx, "cron-unauth-test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) == 0 {
		t.Fatalf("expected at least 1 cron run recorded")
	}
	if runs[0].Status != storage.CronRunUnauthorized {
		t.Fatalf("expected run status 'unauthorized', got %q", runs[0].Status)
	}

	// 3. 斷言排程已自動被暫停 (paused)
	updatedSched, err := store.GetCronSchedule(ctx, "cron-unauth-test")
	if err != nil {
		t.Fatal(err)
	}
	if updatedSched.Status != storage.CronStatusPaused {
		t.Fatalf("expected schedule status to be auto-paused (%q), got %q", storage.CronStatusPaused, updatedSched.Status)
	}
}

// TestCRON006_CLICronManagement 驗證 CRON-006: 透過 CLI 管理定時排程（新增、列表、暫停、恢復與刪除）
func TestCRON006_CLICronManagement(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cron006-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	socketPath := filepath.Join(tmpDir, "daemon.sock")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	eng := engine.NewEngine(store, policy)
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: socketPath,
	}, store, policy, daemon.WithEngine(eng))

	errChan := make(chan error, 1)
	go func() {
		errChan <- d.Start()
	}()
	defer d.Close()

	waitForSocket(t, socketPath)
	client, err := daemon.NewClient(socketPath)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// 1. 新增排程
	sched, err := client.AddCronSchedule(ctx, daemon.CronAddRequest{
		Name:      "test-daily-audit",
		Workspace: tmpDir,
		Schedule:  "0 3 * * *",
		Prompt:    "echo audit",
	})
	if err != nil {
		t.Fatalf("AddCronSchedule failed: %v", err)
	}
	if sched.ID == "" || sched.Name != "test-daily-audit" || sched.Status != storage.CronStatusActive {
		t.Fatalf("unexpected added schedule: %+v", sched)
	}

	// 2. 列出排程
	list, err := client.ListCronSchedules(ctx)
	if err != nil {
		t.Fatalf("ListCronSchedules failed: %v", err)
	}
	if len(list) != 1 || list[0].ID != sched.ID {
		t.Fatalf("expected 1 schedule in list, got %+v", list)
	}

	// 3. 暫停排程
	paused, err := client.PauseCronSchedule(ctx, sched.ID)
	if err != nil {
		t.Fatalf("PauseCronSchedule failed: %v", err)
	}
	if paused.Status != storage.CronStatusPaused {
		t.Fatalf("expected status paused, got %s", paused.Status)
	}

	// 4. 恢復排程
	resumed, err := client.ResumeCronSchedule(ctx, sched.ID)
	if err != nil {
		t.Fatalf("ResumeCronSchedule failed: %v", err)
	}
	if resumed.Status != storage.CronStatusActive {
		t.Fatalf("expected status active, got %s", resumed.Status)
	}

	// 5. 刪除排程
	if err := client.RemoveCronSchedule(ctx, sched.ID); err != nil {
		t.Fatalf("RemoveCronSchedule failed: %v", err)
	}

	afterRemove, err := client.ListCronSchedules(ctx)
	if err != nil {
		t.Fatalf("ListCronSchedules after remove failed: %v", err)
	}
	if len(afterRemove) != 0 {
		t.Fatalf("expected 0 schedules after remove, got %d", len(afterRemove))
	}
}

// TestCRON007_CLICronHistoryQuery 驗證 CRON-007: 透過 CLI/Client 查詢排程歷史觸發紀錄與狀態
func TestCRON007_CLICronHistoryQuery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cron007-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	socketPath := filepath.Join(tmpDir, "daemon.sock")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// 預置一筆排程
	sched := storage.CronSchedule{
		ID:        "sched-history-1",
		Name:      "history-test",
		Workspace: tmpDir,
		Schedule:  "*/15 * * * *",
		Prompt:    "echo test",
		Status:    storage.CronStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateCronSchedule(ctx, sched); err != nil {
		t.Fatal(err)
	}

	// 預置兩筆歷史觸發記錄 (一筆 triggered, 一筆 skipped)
	run1 := storage.CronRun{
		ID:            "run-hist-1",
		CronID:        "sched-history-1",
		ScheduledTime: now.Add(-30 * time.Minute),
		TriggeredTime: now.Add(-30 * time.Minute),
		TaskID:        "task-111",
		Status:        storage.CronRunTriggered,
		CreatedAt:     now.Add(-30 * time.Minute),
	}
	run2 := storage.CronRun{
		ID:            "run-hist-2",
		CronID:        "sched-history-1",
		ScheduledTime: now.Add(-15 * time.Minute),
		TriggeredTime: now.Add(-15 * time.Minute),
		TaskID:        "task-111",
		Status:        storage.CronRunSkipped,
		CreatedAt:     now.Add(-15 * time.Minute),
	}
	_ = store.RecordCronRun(ctx, run1)
	_ = store.RecordCronRun(ctx, run2)

	policy := auth.NewPolicy([]string{tmpDir})
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: socketPath,
	}, store, policy)

	errChan := make(chan error, 1)
	go func() {
		errChan <- d.Start()
	}()
	defer d.Close()

	waitForSocket(t, socketPath)
	client, err := daemon.NewClient(socketPath)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// 查詢觸發紀錄
	runs, err := client.ListCronRuns(ctx, "sched-history-1", 10)
	if err != nil {
		t.Fatalf("ListCronRuns failed: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	if runs[0].Status != storage.CronRunSkipped || runs[1].Status != storage.CronRunTriggered {
		t.Fatalf("unexpected runs order or status: %+v", runs)
	}
}



