package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/cron"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/storage"
)

// waitForSocket 輪詢等待 Unix domain socket 就緒
func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if client, err := daemon.NewClient(socketPath); err == nil {
			_ = client.Close()
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for socket: %s", socketPath)
}

// =========================================================================
// CRON-001: 同專案互斥排隊與依序執行
// =========================================================================

// TestCRON001_WorkspaceMutexQueue 驗證 CRON-001: 授權專案 A 已有執行中任務 1，提交同專案任務 2 時初始狀態為 queued，任務 1 完成後自動接續啟動且不並行修改檔案
func TestCRON001_WorkspaceMutexQueue(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "daemon.sock")
	dbPath := filepath.Join(tmpDir, "asura.db")
	ws := filepath.Join(tmpDir, "project-a")
	_ = os.MkdirAll(ws, 0755)

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{ws})

	var runningCount int32
	var maxConcurrent atomic.Int32
	t1Started := make(chan struct{})
	t1Release := make(chan struct{})

	testRunner := func(ctx context.Context, dir, prompt string) (string, error) {
		cur := atomic.AddInt32(&runningCount, 1)
		for {
			oldMax := maxConcurrent.Load()
			if cur <= oldMax || maxConcurrent.CompareAndSwap(oldMax, cur) {
				break
			}
		}

		if prompt == "task-1" {
			close(t1Started)
			<-t1Release
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&runningCount, -1)
		return "done: " + prompt, nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(testRunner))
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: socketPath,
		MaxWorkers: 4,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	waitForSocket(t, socketPath)
	client, err := daemon.NewClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx := context.Background()

	// 1. 提交專案 A 的任務 1
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

	select {
	case <-t1Started:
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for task 1 to start")
	}

	// 2. 提交同專案的任務 2（以帶尾斜線路徑驗證正規化專案鎖，MUST FIX 4）
	resp2, err := client.Submit(ctx, engine.SubmitRequest{
		Workspace: ws + string(filepath.Separator),
		Prompt:    "task-2",
	})
	if err != nil {
		t.Fatalf("submit task 2 failed: %v", err)
	}
	// 驗證初始狀態為 queued 排隊等待
	if resp2.Status != storage.StatusQueued {
		t.Fatalf("expected task 2 status to be 'queued', got %q", resp2.Status)
	}

	// 3. 釋放任務 1，驗證任務 2 自動被喚醒並執行完成
	close(t1Release)

	var finalTask2 *storage.Task
	for i := 0; i < 50; i++ {
		time.Sleep(30 * time.Millisecond)
		t2, err := store.GetTask(ctx, resp2.ID)
		if err == nil && t2.Status == storage.StatusCompleted {
			finalTask2 = t2
			break
		}
	}
	if finalTask2 == nil {
		t.Fatalf("task 2 never completed after task 1 released")
	}

	// 驗證同專案的並行執行數絕對不超過 1
	if max := maxConcurrent.Load(); max > 1 {
		t.Fatalf("expected max concurrent tasks in same workspace <= 1, got %d", max)
	}
}

// =========================================================================
// CRON-002: 保留互動通道與背景容量上限
// =========================================================================

// TestCRON002_InteractiveChannelReserved 驗證 CRON-002: 背景 Cron 佔滿 Worker 達到並行上限時，人類即時互動任務仍能立即分配至保留 Worker 執行而不被阻塞
func TestCRON002_InteractiveChannelReserved(t *testing.T) {
	tmpDir := t.TempDir()
	ws1 := filepath.Join(tmpDir, "ws1")
	ws2 := filepath.Join(tmpDir, "ws2")
	ws3 := filepath.Join(tmpDir, "ws3")
	_ = os.MkdirAll(ws1, 0755)
	_ = os.MkdirAll(ws2, 0755)
	_ = os.MkdirAll(ws3, 0755)

	socketPath := filepath.Join(tmpDir, "daemon.sock")
	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{ws1, ws2, ws3})

	var runningCronCount int32
	var maxCronConcurrent int32
	cron1Started := make(chan struct{}, 2)
	cronBlocker := make(chan struct{})
	var cronBlockerOnce sync.Once
	defer cronBlockerOnce.Do(func() { close(cronBlocker) })

	interactiveStarted := make(chan struct{}, 1)
	interactiveBlocker := make(chan struct{})
	var interactiveBlockerOnce sync.Once
	defer interactiveBlockerOnce.Do(func() { close(interactiveBlocker) })

	testRunner := func(ctx context.Context, dir, prompt string) (string, error) {
		if strings.Contains(prompt, "cron") {
			cur := atomic.AddInt32(&runningCronCount, 1)
			for {
				oldMax := atomic.LoadInt32(&maxCronConcurrent)
				if cur <= oldMax || atomic.CompareAndSwapInt32(&maxCronConcurrent, oldMax, cur) {
					break
				}
			}
			cron1Started <- struct{}{}
			<-cronBlocker
			atomic.AddInt32(&runningCronCount, -1)
			return "cron done", nil
		}

		interactiveStarted <- struct{}{}
		<-interactiveBlocker
		return "interactive done", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(testRunner))
	// 配置 MaxWorkers=2, MaxCronWorkers=1
	d := daemon.NewDaemon(daemon.Config{
		SocketPath:     socketPath,
		MaxWorkers:     2,
		MaxCronWorkers: 1,
		MaxQueue:       10,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	waitForSocket(t, socketPath)
	client, err := daemon.NewClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx := context.Background()

	// 1. 新增兩個不同工作區的排程並觸發
	sched1, err := client.AddCronSchedule(ctx, daemon.CronAddRequest{
		Name:      "cron-job-1",
		Workspace: ws1,
		Schedule:  "* * * * *",
		Prompt:    "task cron 1",
	})
	if err != nil {
		t.Fatal(err)
	}

	sched2, err := client.AddCronSchedule(ctx, daemon.CronAddRequest{
		Name:      "cron-job-2",
		Workspace: ws2,
		Schedule:  "* * * * *",
		Prompt:    "task cron 2",
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	if err := d.Scheduler().Trigger(ctx, sched1.ID, now); err != nil {
		t.Fatalf("Trigger sched1 failed: %v", err)
	}
	if err := d.Scheduler().Trigger(ctx, sched2.ID, now); err != nil {
		t.Fatalf("Trigger sched2 failed: %v", err)
	}

	select {
	case <-cron1Started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for cron 1 to start")
	}

	// 驗證背景 Cron 並行數上限為 1
	time.Sleep(30 * time.Millisecond)
	concurrentCron := atomic.LoadInt32(&maxCronConcurrent)
	if concurrentCron > 1 {
		t.Fatalf("expected max cron concurrent to be 1, got %d", concurrentCron)
	}

	// 2. 人類提交即時互動任務（ws3）
	// 驗證即使背景 Cron 佔滿額度，保留通道確保互動任務立即啟動
	_, err = client.Submit(ctx, engine.SubmitRequest{
		Workspace: ws3,
		Prompt:    "interactive prompt",
	})
	if err != nil {
		t.Fatalf("Submit interactive task failed: %v", err)
	}

	select {
	case <-interactiveStarted:
		// 互動任務成功立即啟動！
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for interactive task to start (blocked by cron!)")
	}

	interactiveBlockerOnce.Do(func() { close(interactiveBlocker) })
	cronBlockerOnce.Do(func() { close(cronBlocker) })
}

// =========================================================================
// CRON-003: 動態建立與管理排程 (CLI: add, list, pause, resume, remove)
// =========================================================================

// TestCRON003_CLICronManagement 驗證 CRON-003: 透過 CLI 完整管理排程生命週期，檢查本地時區格式與不可達日期防禦
func TestCRON003_CLICronManagement(t *testing.T) {
	tmpDir := t.TempDir()
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

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	waitForSocket(t, socketPath)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")
	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-o", asuraBin, cmdDir)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", err, string(out))
	}

	runCLI := func(args ...string) (string, string, int) {
		cmd := exec.Command(asuraBin, args...)
		var out, errOut bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errOut
		err := cmd.Run()
		code := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
			} else {
				code = 1
			}
		}
		return out.String(), errOut.String(), code
	}

	// 1. 執行 asura cron add 新增排程
	stdout, stderr, code := runCLI(
		"cron", "add",
		"-socket", socketPath,
		"-name", "audit-job",
		"-workspace", tmpDir,
		"-schedule", "0 3 * * *",
		"-prompt", "run security audit",
	)
	if code != 0 {
		t.Fatalf("cron add failed with exit %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Created cron schedule") {
		t.Fatalf("expected 'Created cron schedule' in stdout, got: %s", stdout)
	}

	// 2. 執行 asura cron list 查看排程、下次觸發時刻與狀態（斷言包含本地時間格式與 active 狀態）
	stdout, stderr, code = runCLI("cron", "list", "-socket", socketPath)
	if code != 0 {
		t.Fatalf("cron list failed with exit %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "audit-job") {
		t.Fatalf("expected audit-job in list output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "active") {
		t.Fatalf("expected active status in list output, got: %s", stdout)
	}
	// 2026-09-30：每日 03:00 的下一次觸發可跨月，以獨立日曆計算驗證完整日期。
	now := time.Now().Local()
	next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
	if !now.Before(next) {
		next = next.AddDate(0, 0, 1)
	}
	expectedDate := next.Format("2006-01-02")
	if !strings.Contains(stdout, expectedDate) {
		t.Fatalf("expected next local date %q in list output, got: %s", expectedDate, stdout)
	}
	if !strings.Contains(stdout, "03:00") {
		t.Fatalf("expected local time 03:00 in list output, got: %s", stdout)
	}

	// 擷取排程 ID 供後續操作
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines in list output, got: %s", stdout)
	}
	fields := strings.Fields(lines[1])
	cronID := fields[0]

	// 3. 執行 asura cron pause 暫停排程
	_, stderr, code = runCLI("cron", "pause", "-socket", socketPath, cronID)
	if code != 0 {
		t.Fatalf("cron pause failed: %s", stderr)
	}

	// 再次 list 確認狀態為 paused
	stdout, _, _ = runCLI("cron", "list", "-socket", socketPath)
	if !strings.Contains(stdout, "paused") {
		t.Fatalf("expected paused status in list after pause, got: %s", stdout)
	}

	// 4. 執行 asura cron resume 恢復排程
	_, stderr, code = runCLI("cron", "resume", "-socket", socketPath, cronID)
	if code != 0 {
		t.Fatalf("cron resume failed: %s", stderr)
	}

	// 再次 list 確認狀態為 active
	stdout, _, _ = runCLI("cron", "list", "-socket", socketPath)
	if !strings.Contains(stdout, "active") {
		t.Fatalf("expected active status in list after resume, got: %s", stdout)
	}

	// 5. 執行 asura cron remove 刪除排程
	_, stderr, code = runCLI("cron", "remove", "-socket", socketPath, cronID)
	if code != 0 {
		t.Fatalf("cron remove failed: %s", stderr)
	}

	// 再次 list 確認無排程
	stdout, _, _ = runCLI("cron", "list", "-socket", socketPath)
	if !strings.Contains(stdout, "No scheduled tasks found") {
		t.Fatalf("expected 'No scheduled tasks found', got: %s", stdout)
	}

	// 6. 不可達日期（如 2 月 31 日）建立時被拒絕
	_, stderr, code = runCLI(
		"cron", "add",
		"-socket", socketPath,
		"-name", "feb31-job",
		"-workspace", tmpDir,
		"-schedule", "0 0 31 2 *",
		"-prompt", "echo impossible",
	)
	if code == 0 {
		t.Fatalf("expected non-zero exit code when adding unreachable Feb 31 schedule")
	}
	if !strings.Contains(stderr, "unreachable") {
		t.Fatalf("expected 'unreachable' error message, got: %s", stderr)
	}
}

// =========================================================================
// CRON-004: 排程週期觸發產生可追蹤任務（真實 cronLoop 驗證）
// =========================================================================

// TestCRON004_PeriodicTriggerCreatesTask 驗證 CRON-004: 已啟用之有效排程，預定時間點抵達時由真實 cronLoop 自動建立關聯標準 Task 實體並執行完成
func TestCRON004_PeriodicTriggerCreatesTask(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "daemon.sock")
	dbPath := filepath.Join(tmpDir, "asura.db")
	allowedRoot := filepath.Join(tmpDir, "project")
	_ = os.MkdirAll(allowedRoot, 0755)

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{allowedRoot})

	taskExecuted := make(chan struct{})
	runner := func(ctx context.Context, ws string, prompt string) (string, error) {
		if prompt == "cron periodic work" {
			select {
			case taskExecuted <- struct{}{}:
			default:
			}
		}
		return "periodic done", nil
	}
	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))

	// 預先在儲存層建立一筆排程
	now := time.Now().UTC()
	sched := storage.CronSchedule{
		ID:        "sched-cron004",
		Name:      "periodic-task",
		Workspace: allowedRoot,
		Schedule:  "* * * * *",
		Prompt:    "cron periodic work",
		Status:    storage.CronStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateCronSchedule(context.Background(), sched); err != nil {
		t.Fatal(err)
	}

	// 啟動真實 Daemon（包含背景 cronLoop 輪詢）
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: socketPath,
		MaxWorkers: 2,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// 模擬預定觸發時間點抵達（When 預定時間點抵達）
	triggerNow := time.Now().UTC()
	if err := store.UpdateCronScheduleNextRun(context.Background(), "sched-cron004", triggerNow, nil); err != nil {
		t.Fatal(err)
	}

	// 等待真實 cronLoop 輪詢到期時間點並自動建立任務執行
	select {
	case <-taskExecuted:
		// 成功由真實 cronLoop 觸發！
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: cronLoop failed to periodically trigger task upon scheduled time arrival")
	}

	waitForSocket(t, socketPath)
	client, err := daemon.NewClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// 驗證紀錄關聯該排程之觸發歷史
	runs, err := client.ListCronRuns(context.Background(), "sched-cron004", 10)
	if err != nil || len(runs) == 0 {
		t.Fatalf("expected cron run recorded by cronLoop, got: %+v", runs)
	}
	if runs[0].TaskID == "" {
		t.Fatalf("expected non-empty TaskID for triggered run")
	}

	// 使用者可查得該任務之執行狀態與結果
	var task *storage.Task
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, err = client.GetTask(context.Background(), runs[0].TaskID)
		if err == nil && task != nil && task.Status == storage.StatusCompleted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if task == nil || task.Status != storage.StatusCompleted {
		t.Fatalf("expected task status completed, got %+v", task)
	}
	if task.Result != "periodic done" {
		t.Fatalf("expected task result 'periodic done', got %q", task.Result)
	}
}

// =========================================================================
// CRON-005: 排程執行重疊時略過不重複堆疊
// =========================================================================

// TestCRON005_OverlapSkip 驗證 CRON-005: 排程上一次觸發任務仍在執行中，下一個預定時間點抵達時系統略過本次觸發且不產生新任務，記錄該次觸發因前次執行中而略過（skipped）
func TestCRON005_OverlapSkip(t *testing.T) {
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
		ID:        "sched-cron005",
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
	var taskCounter atomic.Int32
	dispatcher := func(ctx context.Context, cronID, ws, p string) (string, error) {
		taskCounter.Add(1)
		taskID := "task-overlap-1"
		task := &storage.Task{
			ID:        taskID,
			Workspace: ws,
			Prompt:    p,
			Status:    storage.StatusRunning, // 模擬前次任務仍在執行中
			CreatedAt: now,
			UpdatedAt: now,
		}
		_ = store.CreateTask(ctx, task)
		return taskID, nil
	}

	scheduler := cron.NewScheduler(store, policy, dispatcher)

	// 1. 第一次觸發（正常派出任務）
	if err := scheduler.Trigger(ctx, "sched-cron005", now); err != nil {
		t.Fatalf("first trigger failed: %v", err)
	}
	if taskCounter.Load() != 1 {
		t.Fatalf("expected 1 task dispatched, got %d", taskCounter.Load())
	}

	// 2. 第二次預定時間點抵達（前次任務仍在執行中）
	if err := scheduler.Trigger(ctx, "sched-cron005", now.Add(5*time.Minute)); err != nil {
		t.Fatalf("second trigger failed: %v", err)
	}

	// 驗證不產生新任務（taskCounter 依然為 1）
	if taskCounter.Load() != 1 {
		t.Fatalf("expected no new task generated on overlap, got count %d", taskCounter.Load())
	}

	// 3. 驗證記錄該次觸發為 skipped
	runs, err := store.ListCronRuns(ctx, "sched-cron005", 10)
	if err != nil {
		t.Fatal(err)
	}
	foundSkipped := false
	for _, r := range runs {
		if r.Status == storage.CronRunSkipped {
			foundSkipped = true
			if r.Reason == "" {
				t.Fatalf("expected non-empty reason for skipped run")
			}
			break
		}
	}
	if !foundSkipped {
		t.Fatalf("expected a skipped run record, got runs: %+v", runs)
	}
}

// =========================================================================
// CRON-006: 重啟或中斷後不補跑過期排程（真實 Daemon 重啟驗證）
// =========================================================================

// TestCRON006_RestartNoCatchup 驗證 CRON-006: 排程在離線期間錯過多次預定觸發時間點，Daemon 重新啟動時不補跑過去任務，自動以重啟時間重算下一個未來觸發點
func TestCRON006_RestartNoCatchup(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "asura.sock")
	dbPath := filepath.Join(tmpDir, "asura.db")
	allowedRoot := filepath.Join(tmpDir, "project")
	_ = os.MkdirAll(allowedRoot, 0755)

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{allowedRoot})

	var runnerInvoked atomic.Bool
	runner := func(ctx context.Context, ws string, prompt string) (string, error) {
		runnerInvoked.Store(true)
		return "should not run", nil
	}
	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	// 排程下次時間在 3 小時前（離線期間錯過多次）
	pastTime := now.Add(-3 * time.Hour)
	sched := storage.CronSchedule{
		ID:        "sched-cron006",
		Name:      "daily-task",
		Workspace: allowedRoot,
		Schedule:  "0 2 * * *",
		Prompt:    "daily cleanup",
		Status:    storage.CronStatusActive,
		CreatedAt: pastTime,
		UpdatedAt: pastTime,
		NextRunAt: &pastTime,
	}
	if err := store.CreateCronSchedule(ctx, sched); err != nil {
		t.Fatal(err)
	}

	// 啟動真實 Daemon 服務（執行 Start()，調用 AlignSchedules）
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: socketPath,
		MaxWorkers: 2,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// 1. 驗證未補跑過去錯過的任務
	if runnerInvoked.Load() {
		t.Fatalf("expected no catchup tasks to run on daemon start, but runner was invoked")
	}

	// 2. 驗證 NextRunAt 已自動以當前重啟時間計算為未來的下一個觸發點
	updated, err := store.GetCronSchedule(ctx, "sched-cron006")
	if err != nil {
		t.Fatal(err)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.After(now) {
		t.Fatalf("expected NextRunAt to be in the future (> %v), got %v", now, updated.NextRunAt)
	}
}

// =========================================================================
// CRON-007: 觸發時非授權目錄嚴格拒絕並自動暫停
// =========================================================================

// TestCRON007_UnauthorizedWorkspaceAutoPause 驗證 CRON-007: 專案目錄在觸發時不在授權清單內，系統拒絕啟動後端，紀錄 unauthorized 失敗且排程自動標記為 paused 並記錄拒絕原因
func TestCRON007_UnauthorizedWorkspaceAutoPause(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	unauthorizedDir := filepath.Join(tmpDir, "unauthorized-dir")
	_ = os.MkdirAll(unauthorizedDir, 0755)

	allowedDir := filepath.Join(tmpDir, "allowed-dir")
	_ = os.MkdirAll(allowedDir, 0755)
	policy := auth.NewPolicy([]string{allowedDir})

	sched := storage.CronSchedule{
		ID:        "sched-cron007",
		Name:      "unauthorized-task",
		Workspace: unauthorizedDir,
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

	var runnerInvoked atomic.Bool
	eng := engine.NewEngine(store, policy, engine.WithRunner(func(ctx context.Context, ws, p string) (string, error) {
		runnerInvoked.Store(true)
		return "should not run", nil
	}))

	dispatcher := func(ctx context.Context, cronID, ws, p string) (string, error) {
		taskID := engine.GenerateTaskID()
		_, err := eng.SubmitTask(ctx, engine.SubmitRequest{
			ID:        taskID,
			Workspace: ws,
			Prompt:    p,
		})
		return taskID, err
	}

	scheduler := cron.NewScheduler(store, policy, dispatcher)

	// 觸發未授權排程
	err = scheduler.Trigger(ctx, "sched-cron007", now)
	if err == nil {
		t.Fatalf("expected error triggering unauthorized workspace, got nil")
	}

	// 1. 斷言後端執行器零啟動
	if runnerInvoked.Load() {
		t.Fatalf("backend runner was invoked for unauthorized workspace!")
	}

	// 2. 斷言 cron_runs 歷史記錄狀態為 unauthorized 且記錄拒絕原因
	runs, err := store.ListCronRuns(ctx, "sched-cron007", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) == 0 {
		t.Fatalf("expected at least 1 cron run recorded")
	}
	if runs[0].Status != storage.CronRunUnauthorized {
		t.Fatalf("expected run status 'unauthorized', got %q", runs[0].Status)
	}
	if runs[0].Reason == "" {
		t.Fatalf("expected non-empty reason in cron_runs for unauthorized rejection")
	}

	// 3. 斷言排程已自動被暫停 (paused) 且記錄拒絕原因
	updatedSched, err := store.GetCronSchedule(ctx, "sched-cron007")
	if err != nil {
		t.Fatal(err)
	}
	if updatedSched.Status != storage.CronStatusPaused {
		t.Fatalf("expected schedule status to be auto-paused (%q), got %q", storage.CronStatusPaused, updatedSched.Status)
	}
	if updatedSched.PauseReason == "" {
		t.Fatalf("expected non-empty PauseReason on auto-paused schedule")
	}
}

// =========================================================================
// CRON-008: 排程與觸發紀錄於服務重啟後完整持久化
// =========================================================================

// TestCRON008_PersistenceAcrossRestart 驗證 CRON-008: 累積排程定義與歷史記錄後重啟 Daemon，所有狀態與記錄完整還原
func TestCRON008_PersistenceAcrossRestart(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "daemon.sock")
	dbPath := filepath.Join(tmpDir, "asura.db")
	allowedRoot := filepath.Join(tmpDir, "project")
	_ = os.MkdirAll(allowedRoot, 0755)

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	next := now.Add(1 * time.Hour)

	// 1. 啟動第一個 Daemon 實例並寫入排程與觸發歷史
	store1, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	cronSched := storage.CronSchedule{
		ID:        "cron-sched-008",
		Name:      "daily-health-check",
		Workspace: allowedRoot,
		Schedule:  "0 2 * * *",
		Prompt:    "檢查專案狀態",
		Status:    storage.CronStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
		NextRunAt: &next,
	}
	if err := store1.CreateCronSchedule(ctx, cronSched); err != nil {
		t.Fatal(err)
	}

	cronRun := storage.CronRun{
		ID:            "run-008-1",
		CronID:        "cron-sched-008",
		ScheduledTime: now,
		TriggeredTime: now.Add(100 * time.Millisecond),
		TaskID:        "task-persisted-123",
		Status:        storage.CronRunTriggered,
		CreatedAt:     now,
	}
	if err := store1.RecordCronRun(ctx, cronRun); err != nil {
		t.Fatal(err)
	}

	policy1 := auth.NewPolicy([]string{allowedRoot})
	d1 := daemon.NewDaemon(daemon.Config{
		SocketPath: socketPath,
	}, store1, policy1)
	if err := d1.Start(); err != nil {
		t.Fatal(err)
	}

	// 關閉第一個 Daemon（完整排水關閉與關閉 store）
	_ = d1.Close()
	_ = store1.Close()

	// 2. 模擬重啟：以同一個 DB 啟動第二個 Daemon 實例
	store2, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()

	policy2 := auth.NewPolicy([]string{allowedRoot})
	d2 := daemon.NewDaemon(daemon.Config{
		SocketPath: socketPath,
	}, store2, policy2)
	if err := d2.Start(); err != nil {
		t.Fatal(err)
	}
	defer d2.Close()

	waitForSocket(t, socketPath)
	client, err := daemon.NewClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// 3. 驗證排程定義、啟用狀態、下次觸發時間及歷史觸發記錄完整還原
	schedules, err := client.ListCronSchedules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 1 {
		t.Fatalf("expected 1 schedule after restart, got %d", len(schedules))
	}

	recovered := schedules[0]
	if recovered.ID != "cron-sched-008" || recovered.Name != "daily-health-check" || recovered.Status != storage.CronStatusActive {
		t.Fatalf("unexpected recovered schedule: %+v", recovered)
	}
	if recovered.NextRunAt == nil || !recovered.NextRunAt.Equal(next) {
		t.Fatalf("expected next_run_at %v, got %v", next, recovered.NextRunAt)
	}

	runs, err := client.ListCronRuns(ctx, "cron-sched-008", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run after restart, got %d", len(runs))
	}

	recoveredRun := runs[0]
	if recoveredRun.ID != "run-008-1" || recoveredRun.TaskID != "task-persisted-123" || recoveredRun.Status != storage.CronRunTriggered {
		t.Fatalf("unexpected recovered run: %+v", recoveredRun)
	}
}

// TestCRON001_CronAndInteractiveWorkspaceMutex 驗證排程觸發任務與人類互動任務在同專案下嚴格互斥排隊（CRON-001, MUST FIX 7）
func TestCRON001_CronAndInteractiveWorkspaceMutex(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cron-mutex-*")
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

	var runningCount int32
	var maxConcurrent int32
	runnerStarted := make(chan struct{})
	runnerBlock := make(chan struct{})

	testRunner := func(ctx context.Context, dir, prompt string) (string, error) {
		cur := atomic.AddInt32(&runningCount, 1)
		for {
			oldMax := atomic.LoadInt32(&maxConcurrent)
			if cur <= oldMax || atomic.CompareAndSwapInt32(&maxConcurrent, oldMax, cur) {
				break
			}
		}

		if prompt == "task 1 interactive" {
			close(runnerStarted)
			<-runnerBlock
		}
		atomic.AddInt32(&runningCount, -1)
		return "done", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(testRunner))
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: socketPath,
		MaxWorkers: 2,
		MaxQueue:   10,
	}, store, policy, daemon.WithEngine(eng))

	go func() {
		_ = d.Start()
	}()
	defer d.Close()

	waitForSocket(t, socketPath)
	client, err := daemon.NewClient(socketPath)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// 1. 先提交一個人類互動任務
	task1, err := client.Submit(ctx, engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "task 1 interactive",
	})
	if err != nil {
		t.Fatalf("Submit task 1 failed: %v", err)
	}

	select {
	case <-runnerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for task 1 to start")
	}

	// 2. 新增一個排程在同一個 tmpDir 並觸發
	sched, err := client.AddCronSchedule(ctx, daemon.CronAddRequest{
		Name:      "same-workspace-cron",
		Workspace: tmpDir,
		Schedule:  "* * * * *",
		Prompt:    "task 2 cron",
	})
	if err != nil {
		t.Fatalf("AddCronSchedule failed: %v", err)
	}

	scheduler := d.Scheduler()
	if scheduler == nil {
		t.Fatal("expected scheduler to be accessible")
	}
	now := time.Now()
	if err := scheduler.Trigger(ctx, sched.ID, now); err != nil {
		t.Fatalf("Trigger failed: %v", err)
	}

	// 驗證同專案下最大並行執行數嚴格為 1
	concurrent := atomic.LoadInt32(&maxConcurrent)
	if concurrent > 1 {
		t.Fatalf("expected max concurrent to be 1, got %d (violated workspace mutex!)", concurrent)
	}

	// 查詢排程觸發紀錄，應該有一個 run
	runs, err := client.ListCronRuns(ctx, sched.ID, 10)
	if err != nil || len(runs) == 0 {
		t.Fatalf("expected cron run recorded, got err: %v, runs: %+v", err, runs)
	}
	cronTaskID := runs[0].TaskID
	if cronTaskID == "" {
		t.Fatalf("expected non-empty TaskID for triggered run")
	}

	cronTask, err := client.GetTask(ctx, cronTaskID)
	if err != nil {
		t.Fatalf("GetTask for cronTask failed: %v", err)
	}
	if cronTask.Status != storage.StatusQueued {
		t.Fatalf("expected cronTask to be queued while task 1 is running, got status: %s", cronTask.Status)
	}

	// 釋放 runnerBlock 讓 task 1 結束，此時 cronTask 應自動被喚醒執行
	close(runnerBlock)

	// 等待 cronTask 完成
	for i := 0; i < 40; i++ {
		time.Sleep(50 * time.Millisecond)
		cronTask, _ = client.GetTask(ctx, cronTaskID)
		if cronTask != nil && cronTask.Status == storage.StatusCompleted {
			break
		}
	}
	if cronTask.Status != storage.StatusCompleted {
		t.Fatalf("expected cronTask completed after task 1 finished, got status: %s", cronTask.Status)
	}
	_ = task1
}

// TestCRON007_CLICronHistoryQuery 驗證 CRON-007: 透過 CLI/Client 查詢排程歷史觸發紀錄與狀態排序（MUST FIX 7）
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

	// 預置兩筆歷史觸發記錄 (一筆較早的 skipped, 一筆較晚的 triggered)
	run1 := storage.CronRun{
		ID:            "run-hist-1",
		CronID:        sched.ID,
		Status:        storage.CronRunSkipped,
		Reason:        "previous job still running",
		ScheduledTime: now.Add(-10 * time.Minute),
		TriggeredTime: now.Add(-10 * time.Minute),
	}
	if err := store.RecordCronRun(ctx, run1); err != nil {
		t.Fatal(err)
	}

	run2 := storage.CronRun{
		ID:            "run-hist-2",
		CronID:        sched.ID,
		TaskID:        "task-hist-2",
		Status:        storage.CronRunTriggered,
		ScheduledTime: now.Add(-5 * time.Minute),
		TriggeredTime: now.Add(-5 * time.Minute),
	}
	if err := store.RecordCronRun(ctx, run2); err != nil {
		t.Fatal(err)
	}

	policy := auth.NewPolicy([]string{tmpDir})
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: socketPath,
	}, store, policy)

	go func() {
		_ = d.Start()
	}()
	defer d.Close()

	waitForSocket(t, socketPath)

	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-o", asuraBin, "../../cmd/asura")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", err, string(out))
	}

	runCLI := func(args ...string) (string, string, int) {
		cmd := exec.Command(asuraBin, args...)
		var out, errOut bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errOut
		err := cmd.Run()
		code := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
			} else {
				code = 1
			}
		}
		return out.String(), errOut.String(), code
	}

	// 執行 asura cron runs 查詢該排程之歷史觸發記錄
	stdout, stderr, code := runCLI("cron", "runs", "-socket", socketPath, sched.ID)
	if code != 0 {
		t.Fatalf("cron runs failed with code %d, stderr: %s", code, stderr)
	}

	// 驗證輸出包含 triggered 與 skipped 記錄
	if !strings.Contains(stdout, "triggered") || !strings.Contains(stdout, "skipped") {
		t.Fatalf("expected both triggered and skipped in runs output, got: %s", stdout)
	}

	// 驗證時間排序（較新的 run2 在較舊的 run1 之前）
	idxRun2 := strings.Index(stdout, "run-hist-2")
	idxRun1 := strings.Index(stdout, "run-hist-1")
	if idxRun2 == -1 || idxRun1 == -1 {
		t.Fatalf("expected run-hist-1 and run-hist-2 in output, got: %s", stdout)
	}
	if idxRun2 > idxRun1 {
		t.Fatalf("expected newer run2 to appear before older run1 (ORDER BY triggered_at DESC), got: %s", stdout)
	}
}
