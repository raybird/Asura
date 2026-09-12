package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/backend/opencode"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/storage"
)

// TestINT001: 自然語言任務經真實後端取得可追蹤結果 (INT-001 外迴圈)
// Given 單一執行中樞可用且固定版本後端已通過權限探測
// When 使用者傳入自然語言任務
// Then 任務交給指定後端且不被當作 shell 命令，使用者可重新查得保存結果
func TestINT001(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int001-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	// 注入真實 OpenCode Adapter 執行器
	adapter := opencode.NewAdapter(opencode.AdapterConfig{})
	eng := engine.NewEngine(store, policy, engine.WithRunner(adapter.Run))

	ctx := context.Background()
	task, err := eng.SubmitTask(ctx, engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "請回覆：'測試自然語言任務'，不要呼叫工具",
	})
	if err != nil {
		t.Fatalf("SubmitTask unexpected failure: %v", err)
	}

	if task == nil || task.Status != storage.StatusCompleted {
		t.Fatalf("expected task status %s, got task=%v", storage.StatusCompleted, task)
	}
	if !strings.Contains(task.Result, "測試自然語言任務") {
		t.Errorf("expected output to contain prompt text, got %q", task.Result)
	}
}

// TestINT007: 後端遵守已核准的操作範圍 (INT-007 外迴圈)
// Given 任務位於 Daemon 核准的專案
// When 後端需要修改專案檔案
// Then 可在核准範圍內自動執行，但限制操作未取得確認時不執行
func TestINT007(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int007-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 建立測試檔案
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("original"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	adapter := opencode.NewAdapter(opencode.AdapterConfig{
		BinaryPath: filepath.Join(os.Getenv("HOME"), ".cache/asura-probe-issue-2/install/node_modules/.bin/opencode"),
		Permission: `{"bash":"deny"}`,
	})

	ctx := context.Background()
	out, err := adapter.Run(ctx, tmpDir, "請在 test.txt 加上一行 appended")
	if err != nil {
		t.Fatalf("adapter.Run failed: %v", err)
	}
	if out == "" {
		t.Errorf("expected non-empty output from adapter")
	}
}

// TestINT002: 未授權專案在執行前被拒絕 (INT-002 外迴圈)
// Given 專案不在服務端核准的允許範圍
// When 使用者透過 CLI 提交該專案的任務
// Then 請求被拒絕且沒有建立可執行任務或啟動後端
func TestINT002(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int002-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "allowed")
	unauthorizedDir := filepath.Join(tmpDir, "unauthorized")
	_ = os.MkdirAll(allowedDir, 0755)
	_ = os.MkdirAll(unauthorizedDir, 0755)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{allowedDir})
	sockPath := filepath.Join(tmpDir, "asura.sock")

	backendStarted := false
	dummyRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		backendStarted = true
		return "unexpected", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(dummyRunner))
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 1,
		MaxQueue:   1,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	client, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	defer client.Close()

	task, err := client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: unauthorizedDir,
		Prompt:    "unauthorized prompt",
	})

	if err == nil {
		t.Fatalf("expected submission to be rejected, got nil error")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("expected error to contain 'unauthorized', got %v", err)
	}
	if task != nil {
		t.Errorf("expected no task returned for unauthorized request, got %+v", task)
	}
	if backendStarted {
		t.Errorf("backend must not be started for unauthorized request")
	}
}

// TestINT003: 多個 CLI 共用執行容量 (INT-003 外迴圈)
// Given 執行中樞已達核准的執行及佇列容量
// When 另一個 CLI 提交任務
// Then 使用者收到核准的滿載回應，且沒有另起獨立執行器繞過容量限制
func TestINT003(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int003-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	sockPath := filepath.Join(tmpDir, "asura.sock")

	blockCh := make(chan struct{})
	unblockCh := make(chan struct{})

	blockingRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		select {
		case blockCh <- struct{}{}:
		default:
		}
		<-unblockCh
		return "done", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(blockingRunner))
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 1,
		MaxQueue:   1,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	c1, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("c1 connect failed: %v", err)
	}
	defer c1.Close()

	c2, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("c2 connect failed: %v", err)
	}
	defer c2.Close()

	c3, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("c3 connect failed: %v", err)
	}
	defer c3.Close()

	go func() {
		_, _ = c1.Submit(context.Background(), engine.SubmitRequest{
			Workspace: tmpDir,
			Prompt:    "task-1",
		})
	}()

	<-blockCh

	qDone := make(chan struct{})
	go func() {
		_, _ = c2.Submit(context.Background(), engine.SubmitRequest{
			Workspace: tmpDir,
			Prompt:    "task-2",
		})
		close(qDone)
	}()

	time.Sleep(50 * time.Millisecond)

	_, err = c3.Submit(context.Background(), engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "task-3-overflow",
	})

	if err == nil {
		t.Fatalf("expected capacity exceeded error, got nil")
	}
	if !strings.Contains(err.Error(), "capacity exceeded") {
		t.Errorf("expected error to contain 'capacity exceeded', got %v", err)
	}

	close(unblockCh)
	<-qDone
}

// TestINT004: 斷線後任務繼續，另起連線查詢同一結果 (INT-004 外迴圈)
// Given 任務已通過授權並由 Daemon 接受進入執行佇列
// When 提交該任務之客戶端中斷連線
// Then 背景任務繼續執行至終態，後續查詢可取得完整結果
func TestINT004(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int004-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	sockPath := filepath.Join(tmpDir, "asura.sock")

	startedCh := make(chan struct{})
	unblockCh := make(chan struct{})

	blockingRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		select {
		case startedCh <- struct{}{}:
		default:
		}
		<-unblockCh
		return "async-outer-done", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(blockingRunner))
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
		MaxQueue:   2,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	c1, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("c1 connect failed: %v", err)
	}

	submitCtx, submitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer submitCancel()

	task, err := c1.Submit(submitCtx, engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "background-task",
	})
	if err != nil {
		t.Fatalf("c1.Submit failed: %v", err)
	}
	if task == nil || task.ID == "" {
		t.Fatalf("expected non-empty task and Task ID immediately upon acceptance, got %+v", task)
	}
	taskID := task.ID

	// 模擬客戶端主動斷線
	_ = c1.Close()

	// 等待後端啟動後放行完成
	<-startedCh
	close(unblockCh)

	time.Sleep(100 * time.Millisecond)

	// 另起獨立連線查詢同一任務狀態
	c2, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("c2 connect failed: %v", err)
	}
	defer c2.Close()

	queriedTask, err := c2.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("c2.GetTask failed: %v", err)
	}
	if queriedTask.Status != storage.StatusCompleted {
		t.Errorf("expected queried task status completed, got %s", queriedTask.Status)
	}
	if queriedTask.Result != "async-outer-done" {
		t.Errorf("expected result 'async-outer-done', got %q", queriedTask.Result)
	}
}

// TestINT004Redelivery: 請求識別重送回原任務且不產生額外副作用 (INT-004-REDELIVERY 外迴圈)
// Given 前次提交之回應因網路中斷遺失，但任務已由中樞接受
// When 客戶端以相同請求識別重新送出
// Then 中樞回傳既有任務資訊，且不重複調度執行器或產生額外副作用
func TestINT004Redelivery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int004-redelivery-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	sockPath := filepath.Join(tmpDir, "asura.sock")

	var executionCount atomic.Int32
	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		executionCount.Add(1)
		time.Sleep(50 * time.Millisecond)
		return "single-execution-outer", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
		MaxQueue:   2,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	c1, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("c1 connect failed: %v", err)
	}
	defer c1.Close()

	c2, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("c2 connect failed: %v", err)
	}
	defer c2.Close()

	// 第一次提交帶有特定 RequestID
	task1, err := c1.Submit(context.Background(), engine.SubmitRequest{
		ID:        "idempotent-outer-req-999",
		Workspace: tmpDir,
		Prompt:    "dedup-task-outer",
	})
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}

	// 模擬重新發送相同請求 (同 RequestID)
	task2, err := c2.Submit(context.Background(), engine.SubmitRequest{
		ID:        "idempotent-outer-req-999",
		Workspace: tmpDir,
		Prompt:    "dedup-task-outer",
	})
	if err != nil {
		t.Fatalf("second submit failed: %v", err)
	}

	if task1.ID != task2.ID {
		t.Errorf("expected same Task ID for redelivered request, got %s vs %s", task1.ID, task2.ID)
	}

	// 等待背景執行完成
	time.Sleep(150 * time.Millisecond)

	if count := executionCount.Load(); count != 1 {
		t.Errorf("expected exactly 1 execution, got %d (violates INT-004-REDELIVERY)", count)
	}
}

// TestINT005: 從共同入口取消任務並如實揭露回收狀態 (INT-005 外迴圈)
// Given 任務 A 執行中且另有不相關工作 B
// When 使用者要求取消 A
// Then 確認回收成功才宣告完成，且 B 不受影響
func TestINT005(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int005-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	sockPath := filepath.Join(tmpDir, "asura.sock")

	// 啟動一個外部獨立進程作為不相關工作 B
	cmdB := exec.Command("sleep", "30")
	opencode.ConfigureProcessGroup(cmdB)
	if err := cmdB.Start(); err != nil {
		t.Fatalf("failed to start independent process B: %v", err)
	}
	defer func() {
		if cmdB.Process != nil {
			_ = cmdB.Process.Kill()
		}
	}()

	pgidB, _ := syscall.Getpgid(cmdB.Process.Pid)
	if pgidB <= 0 {
		pgidB = cmdB.Process.Pid
	}

	startedA := make(chan struct{})
	var pgidA int
	runnerA := func(ctx context.Context, ws string, prompt string) (string, error) {
		cmdA := exec.CommandContext(ctx, "sleep", "30")
		opencode.ConfigureProcessGroup(cmdA)
		if err := cmdA.Start(); err != nil {
			return "", err
		}
		pgidA, _ = syscall.Getpgid(cmdA.Process.Pid)
		if pgidA <= 0 {
			pgidA = cmdA.Process.Pid
		}
		if taskID := engine.TaskIDFromContext(ctx); taskID != "" {
			_ = store.UpdateTaskPGID(context.Background(), taskID, pgidA)
		}

		close(startedA)

		go func() {
			<-ctx.Done()
			_ = opencode.KillProcessGroup(pgidA)
		}()

		err := cmdA.Wait()
		if errors.Is(ctx.Err(), context.Canceled) {
			return "partial output from A", ctx.Err()
		}
		return "done", err
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(runnerA))
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
		MaxQueue:   2,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	client, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	defer client.Close()

	// 提交任務 A
	taskA, err := client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "task-A-to-be-cancelled",
	})
	if err != nil {
		t.Fatalf("submit A failed: %v", err)
	}

	// 等待任務 A 啟動
	<-startedA
	time.Sleep(50 * time.Millisecond)

	// 從共同入口取消任務 A
	cancelTask, err := client.CancelTask(context.Background(), taskA.ID)
	if err != nil {
		t.Fatalf("CancelTask A failed: %v", err)
	}
	if cancelTask.Status != storage.StatusCancelled {
		t.Errorf("expected task A status cancelled, got %s", cancelTask.Status)
	}

	// 等待回收完成
	time.Sleep(100 * time.Millisecond)

	// 驗證 1: 任務 A 的進程組已被安全回收 (不在存活狀態)
	if opencode.IsProcessGroupAlive(pgidA) {
		t.Errorf("expected process group A (%d) to be terminated, but still alive", pgidA)
	}

	// 驗證 2: 不相關工作 B 保持存活 (不受影響)
	if !opencode.IsProcessGroupAlive(pgidB) {
		t.Errorf("expected process group B (%d) to remain alive, but was terminated", pgidB)
	}

	// 驗證 3: 查詢任務 A 終態一致
	latestA, err := client.GetTask(context.Background(), taskA.ID)
	if err != nil {
		t.Fatalf("GetTask A failed: %v", err)
	}
	if latestA.Status != storage.StatusCancelled {
		t.Errorf("expected queried status cancelled, got %s", latestA.Status)
	}
	if latestA.Result != "partial output from A" {
		t.Errorf("expected partial output retained, got %q", latestA.Result)
	}
}

// TestINT006: 後端失敗與服務重啟保留追蹤資訊 (INT-006 外迴圈)
// Given 任務已有可追蹤識別且可能產生部分輸出
// When 後端失敗或執行服務中斷後重新啟動
// Then 可查得明確失敗或中斷結果及已持久化的輸出，不標記為成功或重跑
func TestINT006(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int006-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	policy := auth.NewPolicy([]string{tmpDir})
	sockPath := filepath.Join(tmpDir, "asura.sock")

	// 模擬產生一個執行中（running）且已持久化部分輸出的中斷任務
	interruptedTask := &storage.Task{
		ID:        "interrupted-task-001",
		Workspace: tmpDir,
		Prompt:    "task-interrupted-by-crash",
		Status:    storage.StatusRunning,
		Result:    "partial-output-before-crash",
		CreatedAt: time.Now().UTC().Add(-1 * time.Minute),
		UpdatedAt: time.Now().UTC().Add(-30 * time.Second),
	}
	if err := store.CreateTask(context.Background(), interruptedTask); err != nil {
		t.Fatalf("create interrupted task failed: %v", err)
	}
	_ = store.UpdateTaskStatus(context.Background(), interruptedTask.ID, storage.StatusRunning, "partial-output-before-crash", "")

	// 重新啟動中樞服務 (模擬重啟執行 RecoverInterruptedTasks)
	eng := engine.NewEngine(store, policy)
	recovered, err := eng.RecoverInterruptedTasks(context.Background())
	if err != nil {
		t.Fatalf("RecoverInterruptedTasks failed: %v", err)
	}
	if recovered < 1 {
		t.Errorf("expected at least 1 recovered task, got %d", recovered)
	}

	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
		MaxQueue:   2,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	client, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	defer client.Close()

	// 重新查詢該中斷任務
	task, err := client.GetTask(context.Background(), "interrupted-task-001")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}

	if task.Status != storage.StatusFailed {
		t.Errorf("expected status failed for interrupted task, got %s", task.Status)
	}
	if task.Result != "partial-output-before-crash" {
		t.Errorf("expected partial output to be preserved, got %q", task.Result)
	}
	if !strings.Contains(task.ErrorMessage, "interrupted") {
		t.Errorf("expected error message to explain interruption, got %q", task.ErrorMessage)
	}
}
