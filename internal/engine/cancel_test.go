package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/backend/opencode"
	"asura/internal/storage"
)

// SCN-004: 取消任務 A 在 2 秒內停止且狀態為 cancelled，不影響其他並行任務 B（仍為 completed）
func TestEngineCancel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-engine-cancel-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "allowed")
	_ = os.MkdirAll(allowedDir, 0755)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{allowedDir})

	taskAStarted := make(chan struct{})
	taskBCompleted := make(chan struct{})

	runner := func(ctx context.Context, ws string, prompt string) (string, error) {
		if prompt == "taskA" {
			close(taskAStarted)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(5 * time.Second):
				return "taskA finished", nil
			}
		}
		if prompt == "taskB" {
			time.Sleep(100 * time.Millisecond)
			close(taskBCompleted)
			return "taskB finished", nil
		}
		return "ok", nil
	}

	eng := NewEngine(store, policy, WithRunner(runner))
	ctx := context.Background()

	var wg sync.WaitGroup

	// 啟動任務 A
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = eng.SubmitTask(ctx, SubmitRequest{
			ID:        "task-A-cancel",
			Workspace: allowedDir,
			Prompt:    "taskA",
		})
	}()

	// 啟動任務 B
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = eng.SubmitTask(ctx, SubmitRequest{
			ID:        "task-B-normal",
			Workspace: allowedDir,
			Prompt:    "taskB",
		})
	}()

	// 等候任務 A 開始運行
	<-taskAStarted

	// 發送取消任務 A
	startCancel := time.Now()
	err = eng.CancelTask("task-A-cancel")
	if err != nil {
		t.Fatalf("CancelTask failed: %v", err)
	}

	wg.Wait()
	cancelDuration := time.Since(startCancel)

	// 驗證取消在 2 秒核准期限內停止
	if cancelDuration > 2*time.Second {
		t.Errorf("expected cancellation within 2s, took %v", cancelDuration)
	}

	// 驗證任務 A 在 storage 中為 cancelled
	savedA, err := store.GetTask(ctx, "task-A-cancel")
	if err != nil {
		t.Fatalf("GetTask A failed: %v", err)
	}
	if savedA.Status != storage.StatusCancelled {
		t.Errorf("expected task A status to be cancelled, got %q", savedA.Status)
	}

	// 驗證任務 B 順利完成，不受 A 取消影響
	<-taskBCompleted
	savedB, err := store.GetTask(ctx, "task-B-normal")
	if err != nil {
		t.Fatalf("GetTask B failed: %v", err)
	}
	if savedB.Status != storage.StatusCompleted {
		t.Errorf("expected task B status to be completed, got %q", savedB.Status)
	}
	if savedB.Result != "taskB finished" {
		t.Errorf("expected task B result 'taskB finished', got %q", savedB.Result)
	}

	// 測試取消不存在之任務
	err = eng.CancelTask("task-non-existent")
	if err == nil {
		t.Errorf("expected error cancelling non-existent task, got nil")
	}

	// 測試對已取消任務再次呼叫 CancelTask 具備冪等性且返回 nil (修復 I-01)
	err = eng.CancelTask("task-A-cancel")
	if err != nil {
		t.Errorf("expected idempotent cancellation to return nil for cancelled task, got: %v", err)
	}
}

// TestEngineCancel_SynchronouslyTerminatesProcessGroup: 驗證共同取消入口在返回時，進程組必須已確實回收 (INT-005 同步契約)
func TestEngineCancel_SynchronouslyTerminatesProcessGroup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cancel-sync-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	started := make(chan struct{})
	var pgid int

	runner := func(ctx context.Context, ws string, prompt string) (string, error) {
		cmd := exec.CommandContext(ctx, "sleep", "30")
		opencode.ConfigureProcessGroup(cmd)
		if err := cmd.Start(); err != nil {
			return "", err
		}
		p, err := syscall.Getpgid(cmd.Process.Pid)
		if err != nil {
			p = cmd.Process.Pid
		}
		pgid = p
		taskID := TaskIDFromContext(ctx)
		if taskID != "" {
			_ = store.UpdateTaskPGID(context.Background(), taskID, pgid)
		}
		close(started)

		go func() {
			<-ctx.Done()
			_ = opencode.KillProcessGroup(p)
		}()

		err = cmd.Wait()
		return "partial output", err
	}

	eng := NewEngine(store, policy, WithRunner(runner))
	ctx := context.Background()

	go func() {
		_, _ = eng.SubmitTask(ctx, SubmitRequest{
			ID:        "sync-cancel-task",
			Workspace: tmpDir,
			Prompt:    "run sleep",
		})
	}()

	<-started
	// 確保 store 中已登記 PGID
	time.Sleep(20 * time.Millisecond)

	// 呼叫共同取消入口
	cancelErr := eng.CancelTask("sync-cancel-task")
	if cancelErr != nil {
		t.Fatalf("CancelTask failed: %v", cancelErr)
	}

	// 關鍵驗證：CancelTask 返回的當下，進程組必須已經死亡，不得要求呼叫端 sleep！
	if opencode.IsProcessGroupAlive(pgid) {
		t.Errorf("process group %d was still alive immediately after CancelTask returned", pgid)
	}
}

// TestEngineCancel_UnknownIdentityDoesNotMarkCancelled 驗證 process group identity 無法確認時，
// CancelTask 必須回傳 error、完好保留 PGID，且資料庫絕不得呈現 confirmed cancelled 終態。
func TestEngineCancel_UnknownIdentityDoesNotMarkCancelled(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cancel-unknown-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	eng := NewEngine(store, policy)

	taskID := "task-unknown-identity"

	// 1. 建立一個孤兒進程組：Leader 啟動 sleep 後立刻 exit 0，清除環境變數
	cmd := exec.Command("sh", "-c", "exec env -i PATH=/usr/bin:/bin sh -c '(sleep 10) & exit 0'")
	opencode.ConfigureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start command: %v", err)
	}
	leaderPID := cmd.Process.Pid
	pgid, err := syscall.Getpgid(leaderPID)
	if err != nil {
		pgid = leaderPID
	}

	// 確保測試清理
	defer func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}()

	// 等候 Leader 退出
	_ = cmd.Wait()
	for i := 0; i < 50; i++ {
		if _, statErr := os.Stat(fmt.Sprintf("/proc/%d", leaderPID)); os.IsNotExist(statErr) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 確保孤兒子進程依然存活
	if !opencode.IsProcessGroupAlive(pgid) {
		t.Fatalf("expected orphan process group %d to be alive", pgid)
	}

	// 2. 在 store 中建立任務，狀態為 running 且登記了該 pgid
	ctx := context.Background()
	now := time.Now()
	task := &storage.Task{
		ID:        taskID,
		Workspace: tmpDir,
		Prompt:    "orphan test",
		Status:    storage.StatusRunning,
		PGID:      pgid,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// 3. 呼叫 CancelTask
	cancelErr := eng.CancelTask(taskID)

	// 4. 斷言 1：CancelTask 必須失敗，且包含 unable to verify process identity
	if cancelErr == nil {
		t.Fatalf("expected CancelTask to fail due to unknown process identity, got nil")
	}
	if !strings.Contains(cancelErr.Error(), "unable to verify process identity") {
		t.Errorf("expected error to contain 'unable to verify process identity', got: %v", cancelErr)
	}

	// 5. 斷言 2：從 store 讀取任務，PGID 必須完好保留
	saved, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if saved.PGID != pgid {
		t.Errorf("expected PGID to be preserved as %d, got %d", pgid, saved.PGID)
	}

	// 6. 斷言 3：狀態絕不得呈現 confirmed cancelled 終態 (因未通過驗證，維持 cancelling)
	if saved.Status == storage.StatusCancelled {
		t.Errorf("expected task status NOT to be cancelled when identity is unverified, but got cancelled")
	}
	if saved.Status != storage.StatusCancelling {
		t.Errorf("expected task status to remain %q, got %q", storage.StatusCancelling, saved.Status)
	}
}

// TestEngineCancel_ActiveRunnerUnknownIdentityDoesNotMarkCancelled
// 驗證活躍執行中的任務被取消時，若進程組身分無法確認，CancelTask 必須報錯、PGID 完好保留且任務絕不得被標記為 cancelled
func TestEngineCancel_ActiveRunnerUnknownIdentityDoesNotMarkCancelled(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-active-unknown-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	taskID := "task-active-unknown-identity"
	var orphanPGID int
	runnerStarted := make(chan struct{})

	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		cmd := exec.Command("sh", "-c", "exec env -i PATH=/usr/bin:/bin sh -c '(sleep 10) & exit 0'")
		opencode.ConfigureProcessGroup(cmd)
		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("cmd start failed: %w", err)
		}
		leaderPID := cmd.Process.Pid
		pgid, err := syscall.Getpgid(leaderPID)
		if err != nil {
			pgid = leaderPID
		}
		orphanPGID = pgid

		_ = cmd.Wait()
		for i := 0; i < 50; i++ {
			if _, statErr := os.Stat(fmt.Sprintf("/proc/%d", leaderPID)); os.IsNotExist(statErr) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		_ = store.UpdateTaskPGID(context.Background(), taskID, orphanPGID)
		close(runnerStarted)

		<-ctx.Done()
		return "partial output before cancel", ctx.Err()
	}

	eng := NewEngine(store, policy, WithRunner(runner))

	defer func() {
		if orphanPGID > 0 {
			_ = syscall.Kill(-orphanPGID, syscall.SIGKILL)
		}
	}()

	submitErrCh := make(chan error, 1)
	go func() {
		_, sErr := eng.SubmitTask(context.Background(), SubmitRequest{
			ID:        taskID,
			Workspace: tmpDir,
			Prompt:    "active orphan prompt",
		})
		submitErrCh <- sErr
	}()

	<-runnerStarted

	if !opencode.IsProcessGroupAlive(orphanPGID) {
		t.Fatalf("expected orphan process group %d to be alive", orphanPGID)
	}

	cancelErr := eng.CancelTask(taskID)

	if cancelErr == nil {
		t.Fatalf("expected CancelTask to fail due to unknown process identity, got nil")
	}
	if !strings.Contains(cancelErr.Error(), "unable to verify process identity") {
		t.Errorf("expected error to contain 'unable to verify process identity', got: %v", cancelErr)
	}

	select {
	case <-submitErrCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for SubmitTask to finish")
	}

	saved, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if saved.PGID != orphanPGID {
		t.Errorf("expected PGID to be preserved as %d, got %d", orphanPGID, saved.PGID)
	}

	if saved.Status == storage.StatusCancelled {
		t.Fatalf("CRITICAL BUG: task status in DB was marked as cancelled despite verification failure!")
	}
	if saved.Status != storage.StatusCancelling {
		t.Errorf("expected task status to remain %q, got %q", storage.StatusCancelling, saved.Status)
	}
}

// TestEngineCancel_DoesNotOverwriteCommittedTerminalState
// 驗證當任務已在資料庫中提交為 terminal state (completed 或 failed)，但 Engine 註冊表 (e.cancels)
// 尚未 defer 清理的並發競態下，CancelTask 絕不得將其覆寫為 cancelling 或 cancelled，必須返回 ErrTaskNotRunning
func TestEngineCancel_DoesNotOverwriteCommittedTerminalState(t *testing.T) {
	testCases := []struct {
		name           string
		terminalStatus storage.TaskStatus
	}{
		{
			name:           "completed status must not be overwritten",
			terminalStatus: storage.StatusCompleted,
		},
		{
			name:           "failed status must not be overwritten",
			terminalStatus: storage.StatusFailed,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "asura-term-race-*")
			if err != nil {
				t.Fatalf("failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			dbPath := filepath.Join(tmpDir, "asura.db")
			store, err := storage.NewSQLiteStore(dbPath)
			if err != nil {
				t.Fatalf("NewSQLiteStore failed: %v", err)
			}
			defer store.Close()

			policy := auth.NewPolicy([]string{tmpDir})
			eng := NewEngine(store, policy)

			taskID := "task-term-race"
			now := time.Now().UTC()

			// 1. 在 DB 中建立已經是 terminal 狀態的任務
			task := &storage.Task{
				ID:        taskID,
				Workspace: tmpDir,
				Prompt:    "term task",
				Status:    tc.terminalStatus,
				Result:    "original terminal result",
				PGID:      0,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := store.CreateTask(context.Background(), task); err != nil {
				t.Fatalf("CreateTask failed: %v", err)
			}

			// 2. 模擬 Engine registration 尚未 defer cleanup (e.cancels 與 e.doneChs 仍存在)
			eng.cancelsMu.Lock()
			eng.cancels[taskID] = func() {}
			eng.doneChs[taskID] = make(chan struct{})
			eng.cancelsMu.Unlock()

			// 3. 呼叫 CancelTask
			cancelErr := eng.CancelTask(taskID)

			// 4. 斷言 1：CancelTask 必須失敗且為 ErrTaskNotRunning
			if cancelErr == nil {
				t.Fatalf("expected CancelTask to fail on committed terminal state, got nil")
			}
			if !errors.Is(cancelErr, ErrTaskNotRunning) {
				t.Errorf("expected error to wrap ErrTaskNotRunning, got: %v", cancelErr)
			}

			// 5. 斷言 2：DB 中的狀態嚴禁被改為 cancelling 或 cancelled
			saved, err := store.GetTask(context.Background(), taskID)
			if err != nil {
				t.Fatalf("GetTask failed: %v", err)
			}
			if saved.Status != tc.terminalStatus {
				t.Fatalf("CRITICAL BUG: committed terminal status %s was overwritten to %s!", tc.terminalStatus, saved.Status)
			}
		})
	}
}

// TestEngineCancel_StalePendingSnapshotDoesNotOverwriteTerminalState
// 驗證當 CancelTask 讀取 DB 快照時任務為 pending，但在 CancelTask 繼續進行狀態轉移之前，
// SubmitTask 或外部已搶先將任務推進至 terminal 狀態 (completed / failed)。
// CancelTask 絕不得依賴過期的 pending 快照將 DB 中的終態覆寫為 cancelled 或 cancelling。
func TestEngineCancel_StalePendingSnapshotDoesNotOverwriteTerminalState(t *testing.T) {
	testCases := []struct {
		name           string
		terminalStatus storage.TaskStatus
		withRunnerReg  bool
	}{
		{
			name:           "stale pending snapshot must not overwrite completed (no registration)",
			terminalStatus: storage.StatusCompleted,
			withRunnerReg:  false,
		},
		{
			name:           "stale pending snapshot must not overwrite failed (no registration)",
			terminalStatus: storage.StatusFailed,
			withRunnerReg:  false,
		},
		{
			name:           "stale pending snapshot must not overwrite completed (with registration)",
			terminalStatus: storage.StatusCompleted,
			withRunnerReg:  true,
		},
		{
			name:           "stale pending snapshot must not overwrite failed (with registration)",
			terminalStatus: storage.StatusFailed,
			withRunnerReg:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "asura-stale-pending-*")
			if err != nil {
				t.Fatalf("failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			dbPath := filepath.Join(tmpDir, "asura.db")
			store, err := storage.NewSQLiteStore(dbPath)
			if err != nil {
				t.Fatalf("NewSQLiteStore failed: %v", err)
			}
			defer store.Close()

			policy := auth.NewPolicy([]string{tmpDir})
			eng := NewEngine(store, policy)

			taskID := "task-stale-pending"
			now := time.Now().UTC()

			// 1. 初始化 DB 中的任務狀態為 pending
			task := &storage.Task{
				ID:        taskID,
				Workspace: tmpDir,
				Prompt:    "stale pending task",
				Status:    storage.StatusPending,
				Result:    "",
				PGID:      0,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := store.CreateTask(context.Background(), task); err != nil {
				t.Fatalf("CreateTask failed: %v", err)
			}

			// 2. 先持有 cancelsMu，使 CancelTask 在讀取 GetTask(pending) 後暫停在 cancelsMu.Lock()
			eng.cancelsMu.Lock()

			cancelDone := make(chan error, 1)
			go func() {
				// CancelTask 會先執行 store.GetTask()（讀到 pending），然後嘗試獲取 cancelsMu.Lock()
				err := eng.CancelTask(taskID)
				cancelDone <- err
			}()

			// 稍等以確保 goroutine 已進入 CancelTask 並阻塞於 cancelsMu.Lock()
			time.Sleep(30 * time.Millisecond)

			// 3. 在 CancelTask 持有過期 pending 快照且尚未繼續執行前，
			// 模擬 SubmitTask 搶先完成並將狀態推進至 terminalStatus
			if err := store.UpdateTaskStatus(context.Background(), taskID, tc.terminalStatus, "finished result", ""); err != nil {
				eng.cancelsMu.Unlock()
				t.Fatalf("UpdateTaskStatus failed: %v", err)
			}

			if tc.withRunnerReg {
				eng.cancels[taskID] = func() {}
				eng.doneChs[taskID] = make(chan struct{})
			}

			// 4. 釋放 cancelsMu，讓 CancelTask 帶著舊的 pending 快照繼續執行
			eng.cancelsMu.Unlock()

			// 5. 等待 CancelTask 完成
			var cancelErr error
			select {
			case cancelErr = <-cancelDone:
			case <-time.After(2 * time.Second):
				t.Fatalf("CancelTask timed out")
			}

			// 6. 斷言：CancelTask 必須察覺已是終態並回報 ErrTaskNotRunning
			if cancelErr == nil {
				t.Fatalf("expected CancelTask to fail on stale snapshot when DB is %s, but got nil", tc.terminalStatus)
			}
			if !errors.Is(cancelErr, ErrTaskNotRunning) {
				t.Errorf("expected error to wrap ErrTaskNotRunning, got: %v", cancelErr)
			}

			// 7. 斷言：DB 中的終態絕不可被覆寫為 cancelled 或 cancelling
			saved, err := store.GetTask(context.Background(), taskID)
			if err != nil {
				t.Fatalf("GetTask failed: %v", err)
			}
			if saved.Status != tc.terminalStatus {
				t.Fatalf("CRITICAL BUG: committed terminal status %s was overwritten to %s by stale snapshot!", tc.terminalStatus, saved.Status)
			}
		})
	}
}

// TestEngineCancel_FinalizeGetTaskErrorFailsClosed
// 驗證當進程已安全回收，但在最後確認狀態並持久化 cancelled 前，
// store.GetTask 發生錯誤時，CancelTask 絕不得靜默吞掉錯誤回傳成功 (nil)，必須 fail-closed 回報錯誤
func TestEngineCancel_FinalizeGetTaskErrorFailsClosed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-final-err-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}

	policy := auth.NewPolicy([]string{tmpDir})
	eng := NewEngine(store, policy)

	taskID := "task-final-err"
	now := time.Now().UTC()

	// 1. 初始化 DB 中的任務狀態為 running
	task := &storage.Task{
		ID:        taskID,
		Workspace: tmpDir,
		Prompt:    "final err task",
		Status:    storage.StatusRunning,
		Result:    "",
		PGID:      0,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// 2. 註冊 active runner，當 runner 收到 cancel 時關閉 store
	doneCh := make(chan struct{})
	eng.cancelsMu.Lock()
	eng.cancels[taskID] = func() {
		// 收到取消信號時關閉 store，使後續 GetTask 必定出錯，並結束 runner
		_ = store.Close()
		close(doneCh)
	}
	eng.doneChs[taskID] = doneCh
	eng.cancelsMu.Unlock()

	// 3. 呼叫 CancelTask
	cancelErr := eng.CancelTask(taskID)

	// 4. 斷言：CancelTask 絕不能吞掉 DB 錯誤而回傳 nil
	if cancelErr == nil {
		t.Fatalf("CRITICAL BUG: CancelTask returned nil (success) when final GetTask failed! Violates synchronization contract")
	}
}

// TestEngineCancel_ClearPGIDErrorFailsClosed
// 驗證當進程組已成功回收，但清除 PGID (UpdateTaskPGID) 發生錯誤時，
// CancelTask 絕不得忽略該錯誤宣告成功，必須 fail-closed 回報錯誤
func TestEngineCancel_ClearPGIDErrorFailsClosed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-clear-pgid-err-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}

	policy := auth.NewPolicy([]string{tmpDir})
	eng := NewEngine(store, policy)

	taskID := "task-pgid-err"
	now := time.Now().UTC()

	// 1. 啟動一個 dummy 進程以產生真實 PGID，並帶有任務身分環境變數
	cmd := exec.Command("sleep", "10")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), fmt.Sprintf("ASURA_TASK_ID=%s", taskID))
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start dummy command: %v", err)
	}
	go func() {
		_ = cmd.Wait()
	}()
	pgid, _ := syscall.Getpgid(cmd.Process.Pid)
	defer func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}()


	task := &storage.Task{
		ID:        taskID,
		Workspace: tmpDir,
		Prompt:    "clear pgid err task",
		Status:    storage.StatusRunning,
		Result:    "",
		PGID:      pgid,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// 2. 監聽進程組死亡時，立即關閉 store 使隨後的 UpdateTaskPGID 失敗
	go func() {
		for {
			if !opencode.IsProcessGroupAlive(pgid) {
				_ = store.Close()
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// 3. 呼叫 CancelTask
	cancelErr := eng.CancelTask(taskID)

	// 4. 斷言：CancelTask 絕不能忽略 DB 錯誤而宣告成功，必須 fail-closed
	if cancelErr == nil {
		t.Fatalf("expected CancelTask to fail closed when database error occurs during cleanup, got nil")
	}

}



