package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/backend/opencode"
	"asura/internal/engine"
	"asura/internal/storage"
)

// SCN-003: 競爭啟動仍只執行一次已接受任務，無雙重擁有者且不超過全域上限
func TestDaemonConcurrency(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-daemon-test-*")
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

	// 1. 驗證競爭啟動：只有一個能成為 Daemon 擁有者，另一個被拒絕
	t.Run("競爭啟動無雙重擁有者", func(t *testing.T) {
		concurrentSock := filepath.Join(tmpDir, "concurrent.sock")
		const concurrentCount = 10
		var wg sync.WaitGroup
		var successCount int32
		var alreadyRunningCount int32
		var otherErrCount int32
		var activeDaemons []*Daemon
		var activeMu sync.Mutex

		for i := 0; i < concurrentCount; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				d := NewDaemon(Config{
					SocketPath: concurrentSock,
					MaxWorkers: 2,
				}, store, policy)
				err := d.Start()
				if err == nil {
					atomic.AddInt32(&successCount, 1)
					activeMu.Lock()
					activeDaemons = append(activeDaemons, d)
					activeMu.Unlock()
				} else if errors.Is(err, ErrDaemonAlreadyRunning) {
					atomic.AddInt32(&alreadyRunningCount, 1)
				} else {
					atomic.AddInt32(&otherErrCount, 1)
					t.Errorf("unexpected error on concurrent start: %v", err)
				}
			}()
		}
		wg.Wait()

		for _, d := range activeDaemons {
			_ = d.Close()
		}

		if success := atomic.LoadInt32(&successCount); success != 1 {
			t.Fatalf("expected exactly 1 daemon to acquire ownership, got %d", success)
		}
		if rejected := atomic.LoadInt32(&alreadyRunningCount); rejected != concurrentCount-1 {
			t.Fatalf("expected %d daemons rejected with ErrDaemonAlreadyRunning, got %d", concurrentCount-1, rejected)
		}
	})

	// 2. 驗證全域並行上限與每個任務各執行一次
	t.Run("不超過全域上限且各執行一次", func(t *testing.T) {
		sockPath2 := filepath.Join(tmpDir, "asura2.sock")

		var currentActive int32
		var maxObservedActive int32
		var executionCounts [5]int32

		runner := func(ctx context.Context, ws string, prompt string) (string, error) {
			active := atomic.AddInt32(&currentActive, 1)
			for {
				oldMax := atomic.LoadInt32(&maxObservedActive)
				if active <= oldMax || atomic.CompareAndSwapInt32(&maxObservedActive, oldMax, active) {
					break
				}
			}

			// 模擬耗時任務
			time.Sleep(50 * time.Millisecond)

			atomic.AddInt32(&currentActive, -1)
			return "done: " + prompt, nil
		}

		eng := engine.NewEngine(store, policy, engine.WithRunner(runner))
		d := NewDaemon(Config{
			SocketPath: sockPath2,
			MaxWorkers: 2, // 全域上限為 2
			MaxQueue:   10,
		}, store, policy, WithEngine(eng))

		if err := d.Start(); err != nil {
			t.Fatalf("failed to start daemon: %v", err)
		}
		defer d.Close()

		// 併發提交 5 個請求
		var wg sync.WaitGroup
		var taskIDs [5]string
		for i := 0; i < 5; i++ {
			wg.Add(1)
			idx := i
			go func() {
				defer wg.Done()
				client, err := NewClient(sockPath2)
				if err != nil {
					t.Errorf("client connect failed: %v", err)
					return
				}
				defer client.Close()

				resp, err := client.Submit(context.Background(), engine.SubmitRequest{
					Workspace: tmpDir,
					Prompt:    "task",
				})
				if err != nil {
					t.Errorf("task submit failed: %v", err)
					return
				}
				if resp == nil || resp.ID == "" {
					t.Errorf("expected non-empty task and ID, got %+v", resp)
					return
				}
				taskIDs[idx] = resp.ID
			}()
		}
		wg.Wait()

		// 等待背景任務全部非同步執行完成
		for i := 0; i < 5; i++ {
			id := taskIDs[i]
			if id == "" {
				continue
			}
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				tObj, err := store.GetTask(context.Background(), id)
				if err == nil && tObj.Status == storage.StatusCompleted {
					atomic.AddInt32(&executionCounts[i], 1)
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
		}

		// 檢查最大並行數不超過上限 2
		if maxObs := atomic.LoadInt32(&maxObservedActive); maxObs > 2 {
			t.Errorf("expected max active <= 2, got %d", maxObs)
		}

		// 檢查 5 個任務各自恰好執行一次
		for i := 0; i < 5; i++ {
			if count := atomic.LoadInt32(&executionCounts[i]); count != 1 {
				t.Errorf("expected task %d executed exactly once, got %d", i, count)
			}
		}
	})

	// 3. 未接受的請求得到明確錯誤結果（容量飽和）
	t.Run("未接受的請求得到明確結果", func(t *testing.T) {
		sockPath3 := filepath.Join(tmpDir, "asura3.sock")
		blocker := make(chan struct{})

		runner := func(ctx context.Context, ws string, prompt string) (string, error) {
			<-blocker
			return "done", nil
		}

		eng := engine.NewEngine(store, policy, engine.WithRunner(runner))
		// 容量為 1，Queue 為 0（無排隊，飽和直接拒絕）
		d := NewDaemon(Config{
			SocketPath: sockPath3,
			MaxWorkers: 1,
			MaxQueue:   0,
		}, store, policy, WithEngine(eng))

		if err := d.Start(); err != nil {
			t.Fatalf("failed to start daemon: %v", err)
		}
		defer func() {
			close(blocker)
			d.Close()
		}()

		// 啟動第一個佔滿容量
		go func() {
			client, _ := NewClient(sockPath3)
			if client != nil {
				defer client.Close()
				_, _ = client.Submit(context.Background(), engine.SubmitRequest{
					Workspace: tmpDir,
					Prompt:    "blocking task",
				})
			}
		}()

		time.Sleep(30 * time.Millisecond)

		// 第二個請求應得到明確拒絕結果
		client, err := NewClient(sockPath3)
		if err != nil {
			t.Fatalf("client connect failed: %v", err)
		}
		defer client.Close()

		_, err = client.Submit(context.Background(), engine.SubmitRequest{
			Workspace: tmpDir,
			Prompt:    "rejected task",
		})
		if err == nil {
			t.Fatalf("expected error for saturated capacity, got nil")
		}
		if !errors.Is(err, ErrCapacityExceeded) {
			t.Errorf("expected ErrCapacityExceeded, got %v", err)
		}
	})

	// 4. SCN-006: 守護程序重啟保留已完成結果並揭露中斷任務
	t.Run("SCN-006: 守護程序重啟保留已完成結果並揭露中斷任務", func(t *testing.T) {
		sockPath4 := filepath.Join(tmpDir, "asura4.sock")
		now := time.Now().UTC()

		// 預置已完成之任務 A
		taskA := &storage.Task{
			ID:        "task-A-done",
			Prompt:    "task A prompt",
			Workspace: tmpDir,
			Status:    storage.StatusCompleted,
			Result:    "task A success result",
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := store.CreateTask(context.Background(), taskA); err != nil {
			t.Fatalf("failed to seed task A: %v", err)
		}

		// 預置未確認完成之中斷任務 B (status: running)
		taskB := &storage.Task{
			ID:        "task-B-interrupted",
			Prompt:    "task B prompt",
			Workspace: tmpDir,
			Status:    storage.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := store.CreateTask(context.Background(), taskB); err != nil {
			t.Fatalf("failed to seed task B: %v", err)
		}

		// 啟動守護程序（模擬系統重啟，觸發恢復程序）
		d := NewDaemon(Config{
			SocketPath: sockPath4,
			MaxWorkers: 2,
			MaxQueue:   10,
		}, store, policy)

		if err := d.Start(); err != nil {
			t.Fatalf("daemon start failed: %v", err)
		}
		defer d.Close()

		// 斷言任務 A 結果維持 completed
		savedA, err := store.GetTask(context.Background(), taskA.ID)
		if err != nil {
			t.Fatalf("failed to query task A: %v", err)
		}
		if savedA.Status != storage.StatusCompleted {
			t.Errorf("expected task A status to remain completed, got: %s", savedA.Status)
		}
		if savedA.Result != "task A success result" {
			t.Errorf("expected task A result to be preserved, got: %s", savedA.Result)
		}

		// 斷言任務 B 狀態轉移為 failed 且記載中斷原因
		savedB, err := store.GetTask(context.Background(), taskB.ID)
		if err != nil {
			t.Fatalf("failed to query task B: %v", err)
		}
		if savedB.Status != storage.StatusFailed {
			t.Errorf("expected task B status to be failed after restart, got: %s", savedB.Status)
		}
		if savedB.ErrorMessage == "" {
			t.Errorf("expected task B to have non-empty error message explaining interruption")
		}
	})

	// 5. R-08: 執行失敗時 Client.Submit 同時回傳 Task 與 Error，保留任務識別與失敗狀態
	t.Run("R-08: 執行失敗時 Client.Submit 同時回傳 Task 與 Error", func(t *testing.T) {
		sockPath5 := filepath.Join(tmpDir, "asura5.sock")
		failedRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
			return "partial stdout before error", errors.New("command failed with exit code 42")
		}
		failEng := engine.NewEngine(store, policy, engine.WithRunner(failedRunner))

		d := NewDaemon(Config{
			SocketPath: sockPath5,
			MaxWorkers: 2,
			MaxQueue:   10,
		}, store, policy, WithEngine(failEng))

		if err := d.Start(); err != nil {
			t.Fatalf("daemon start failed: %v", err)
		}
		defer d.Close()

		client, err := NewClient(sockPath5)
		if err != nil {
			t.Fatalf("client connect failed: %v", err)
		}
		defer client.Close()

		task, err := client.Submit(context.Background(), engine.SubmitRequest{
			Workspace: tmpDir,
			Prompt:    "task-that-will-fail",
		})
		if err != nil {
			t.Fatalf("submit accepted unexpected failure: %v", err)
		}
		if task == nil || task.ID == "" {
			t.Fatalf("expected initial task and ID to be populated upon acceptance")
		}
		taskID := task.ID

		// 等待背景任務執行失敗並由儲存層記錄 (INT-006, P4-T2)
		time.Sleep(100 * time.Millisecond)

		failedTask, err := client.GetTask(context.Background(), taskID)
		if err != nil {
			t.Fatalf("client.GetTask failed: %v", err)
		}
		if failedTask == nil {
			t.Fatalf("expected task to be retained on error, got nil task (violates R-08)")
		}
		if failedTask.ID != taskID {
			t.Errorf("expected task ID %s, got %s", taskID, failedTask.ID)
		}
		if failedTask.Status != storage.StatusFailed {
			t.Errorf("expected task status to be %s, got %s", storage.StatusFailed, failedTask.Status)
		}
		if failedTask.Result != "partial stdout before error" {
			t.Errorf("expected partial result to be retained, got: %q", failedTask.Result)
		}
		if !strings.Contains(failedTask.ErrorMessage, "command failed with exit code 42") {
			t.Errorf("expected error message to be retained, got: %q", failedTask.ErrorMessage)
		}
	})
}

// F-01: 驗證 Daemon 關閉排水 (Drain) 期間，排他鎖持續生效，不允許第二個 Daemon 啟動奪取擁有權
func TestReviewOwnershipDuringDrain(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-f01-*")
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
	sockPath := filepath.Join(tmpDir, "asura-drain.sock")

	blockCh := make(chan struct{})
	unblockCh := make(chan struct{})
	blockingRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		close(blockCh)
		<-unblockCh
		return "drain-done", nil
	}

	eng1 := engine.NewEngine(store, policy, engine.WithRunner(blockingRunner))
	d1 := NewDaemon(Config{
		SocketPath: sockPath,
		MaxWorkers: 1,
		MaxQueue:   1,
	}, store, policy, WithEngine(eng1))

	if err := d1.Start(); err != nil {
		t.Fatalf("d1 start failed: %v", err)
	}

	client, err := NewClient(sockPath)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}

	submitDone := make(chan struct{})
	go func() {
		_, _ = client.Submit(context.Background(), engine.SubmitRequest{
			Workspace: tmpDir,
			Prompt:    "slow-task",
		})
		close(submitDone)
	}()

	<-blockCh // 確保任務已進入 runner 正在執行

	// 並發呼叫 d1.Close()（進入 Drain 階段）
	closeDone := make(chan struct{})
	go func() {
		_ = d1.Close()
		close(closeDone)
	}()

	// 短暫等待 d1.Close() 進入 pool.Close() 與 wg.Wait()
	time.Sleep(50 * time.Millisecond)

	// 在任務未放行前，嘗試啟動 d2
	eng2 := engine.NewEngine(store, policy)
	d2 := NewDaemon(Config{
		SocketPath: sockPath,
		MaxWorkers: 1,
		MaxQueue:   1,
	}, store, policy, WithEngine(eng2))

	err = d2.Start()
	if err == nil {
		_ = d2.Close()
		close(unblockCh)
		t.Fatalf("second daemon started while first daemon still executes a task (violates F-01)")
	}
	if !errors.Is(err, ErrDaemonAlreadyRunning) {
		t.Errorf("expected ErrDaemonAlreadyRunning for d2, got: %v", err)
	}

	// 放行任務，讓 d1 順利完成 Drain 並退出
	close(unblockCh)
	<-closeDone
	<-submitDone
	_ = client.Close()

	// d1 完全關閉後，d3 應能順利啟動
	d3 := NewDaemon(Config{
		SocketPath: sockPath,
		MaxWorkers: 1,
		MaxQueue:   1,
	}, store, policy, WithEngine(eng2))
	if err := d3.Start(); err != nil {
		t.Errorf("expected d3 to start successfully after d1 fully drained, got: %v", err)
	}
	_ = d3.Close()
}

// TestINT002Unit 驗證 Daemon 於進入 WorkerPool 與後端前直接拒絕未授權專案 (INT-002 內迴圈)
func TestINT002Unit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int002-unit-*")
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

	runnerCalled := false
	testRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		runnerCalled = true
		return "ok", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(testRunner))
	d := NewDaemon(Config{
		SocketPath: sockPath,
		MaxWorkers: 1,
		MaxQueue:   1,
	}, store, policy, WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	client, err := NewClient(sockPath)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	defer client.Close()

	// 提交未授權目錄
	task, err := client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: unauthorizedDir,
		Prompt:    "some prompt",
	})

	if err == nil {
		t.Fatalf("expected error for unauthorized workspace, got nil")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("expected error message to contain 'unauthorized', got %v", err)
	}
	if task != nil {
		t.Errorf("expected nil task for rejected submission, got %+v", task)
	}
	if runnerCalled {
		t.Errorf("runner must not be called for rejected submission")
	}
}

// TestINT003Unit 驗證多個客戶端並發提交時共用 WorkerPool 容量且超額被拒 (INT-003 內迴圈)
func TestINT003Unit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int003-unit-*")
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

	blockCh := make(chan struct{}, 1)
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
	// 容量：1 個 worker + 1 個隊列 = 容量 2
	d := NewDaemon(Config{
		SocketPath: sockPath,
		MaxWorkers: 1,
		MaxQueue:   1,
	}, store, policy, WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	client1, err := NewClient(sockPath)
	if err != nil {
		t.Fatalf("client1 connect failed: %v", err)
	}
	defer client1.Close()

	client2, err := NewClient(sockPath)
	if err != nil {
		t.Fatalf("client2 connect failed: %v", err)
	}
	defer client2.Close()

	client3, err := NewClient(sockPath)
	if err != nil {
		t.Fatalf("client3 connect failed: %v", err)
	}
	defer client3.Close()

	// 任務 1：佔用 worker
	go func() {
		_, _ = client1.Submit(context.Background(), engine.SubmitRequest{
			Workspace: tmpDir,
			Prompt:    "task-1",
		})
	}()

	<-blockCh // 確認任務 1 正在執行

	// 任務 2：佔用 queue (容量已滿)
	qDone := make(chan struct{})
	go func() {
		_, _ = client2.Submit(context.Background(), engine.SubmitRequest{
			Workspace: tmpDir,
			Prompt:    "task-2",
		})
		close(qDone)
	}()

	// 稍微等待任務 2 進入 queue
	time.Sleep(50 * time.Millisecond)

	// 任務 3：超過容量，必須收到滿載錯誤
	_, err = client3.Submit(context.Background(), engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "task-3-overflow",
	})

	if err == nil {
		t.Fatalf("expected capacity exceeded error for client3, got nil")
	}
	if !strings.Contains(err.Error(), "capacity exceeded") {
		t.Errorf("expected error to contain 'capacity exceeded', got %v", err)
	}

	// 解除阻塞並收尾
	close(unblockCh)
	<-qDone
}

// TestINT004Unit 驗證任務接受後客戶端斷線仍由背景繼續執行，並可另行查得同一結果 (INT-004 內迴圈)
func TestINT004Unit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int004-unit-*")
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

	startedCh := make(chan struct{}, 1)
	unblockCh := make(chan struct{})

	blockingRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		select {
		case startedCh <- struct{}{}:
		default:
		}
		<-unblockCh
		return "async-done-result", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(blockingRunner))
	d := NewDaemon(Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
		MaxQueue:   2,
	}, store, policy, WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	c1, err := NewClient(sockPath)
	if err != nil {
		t.Fatalf("c1 connect failed: %v", err)
	}

	// 提交任務，預期後端完成前立即回傳 Task ID (P4-T2)
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

	// 模擬客戶端斷線
	_ = c1.Close()

	// 等待後端開始並放行完成
	<-startedCh
	close(unblockCh)

	// 等待後端完成寫入
	time.Sleep(100 * time.Millisecond)

	// 另起連線重新查詢同一任務 (INT-004)
	c2, err := NewClient(sockPath)
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
	if queriedTask.Result != "async-done-result" {
		t.Errorf("expected result 'async-done-result', got %q", queriedTask.Result)
	}
}

// TestINT004RedeliveryUnit 驗證相同請求識別重送時回傳原任務且不重複執行 (INT-004-REDELIVERY 內迴圈)
func TestINT004RedeliveryUnit(t *testing.T) {
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
		return "single-execution", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))
	d := NewDaemon(Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
		MaxQueue:   2,
	}, store, policy, WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	c1, err := NewClient(sockPath)
	if err != nil {
		t.Fatalf("c1 connect failed: %v", err)
	}
	defer c1.Close()

	c2, err := NewClient(sockPath)
	if err != nil {
		t.Fatalf("c2 connect failed: %v", err)
	}
	defer c2.Close()

	// 第一次提交帶有特定 RequestID
	task1, err := c1.Submit(context.Background(), engine.SubmitRequest{
		ID:        "idempotent-req-123",
		Workspace: tmpDir,
		Prompt:    "dedup-task",
	})
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}

	// 模擬重送相同請求 (同 RequestID)
	task2, err := c2.Submit(context.Background(), engine.SubmitRequest{
		ID:        "idempotent-req-123",
		Workspace: tmpDir,
		Prompt:    "dedup-task",
	})
	if err != nil {
		t.Fatalf("second submit failed: %v", err)
	}

	if task1.ID != task2.ID {
		t.Errorf("expected same Task ID for redelivered request, got %s vs %s", task1.ID, task2.ID)
	}

	// 等待執行完成
	time.Sleep(150 * time.Millisecond)

	if count := executionCount.Load(); count != 1 {
		t.Errorf("expected exactly 1 execution, got %d (violates INT-004-REDELIVERY)", count)
	}
}

// 驗證高並發下相同 RequestID 同時提交時之原子去重性：後端僅執行一次，且所有並發連線皆回傳相同 Task ID (INT-004-REDELIVERY)
func TestDaemon_ConcurrentRedeliveryDedupAtomic(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-atomic-dedup-*")
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
		return "concurrent-dedup-success", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))
	d := NewDaemon(Config{
		SocketPath: sockPath,
		MaxWorkers: 4,
		MaxQueue:   10,
	}, store, policy, WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	const concurrentWorkers = 8
	var wg sync.WaitGroup
	results := make([]*storage.Task, concurrentWorkers)
	errs := make([]error, concurrentWorkers)
	startBarrier := make(chan struct{})

	for i := 0; i < concurrentWorkers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			client, err := NewClient(sockPath)
			if err != nil {
				errs[idx] = err
				return
			}
			defer client.Close()

			<-startBarrier // 同步起跑

			task, err := client.Submit(context.Background(), engine.SubmitRequest{
				ID:        "concurrent-atomic-req-001",
				Workspace: tmpDir,
				Prompt:    "dedup-task",
			})
			results[idx] = task
			errs[idx] = err
		}(i)
	}

	close(startBarrier)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d submit failed: %v", i, err)
		}
	}

	firstID := results[0].ID
	for i, res := range results {
		if res == nil {
			t.Fatalf("worker %d returned nil task", i)
		}
		if res.ID != firstID {
			t.Errorf("worker %d got different Task ID: %s vs %s", i, res.ID, firstID)
		}
	}

	// 等待執行完成
	time.Sleep(150 * time.Millisecond)

	if count := executionCount.Load(); count != 1 {
		t.Errorf("expected exactly 1 execution, got %d (violates INT-004-REDELIVERY concurrent atomic dedup)", count)
	}
}

// TestDaemon_INT007_PermissionPolicy 驗證 Daemon 預設持有並注入 INT-007 核准操作邊界
func TestDaemon_INT007_PermissionPolicy(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int007-perm-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, _ := storage.NewSQLiteStore(dbPath)
	defer store.Close()
	policy := auth.NewPolicy([]string{tmpDir})

	d := NewDaemon(Config{
		SocketPath: filepath.Join(tmpDir, "asura.sock"),
	}, store, policy)

	cfg := d.OpenCodeConfig()
	if cfg.ExpectedVersion != "1.18.23" {
		t.Errorf("expected OpenCode ExpectedVersion '1.18.23', got %q", cfg.ExpectedVersion)
	}
	if !strings.Contains(cfg.Permission, "edit") || !strings.Contains(cfg.Permission, "ask") {
		t.Errorf("expected DefaultApprovedPermissionPolicy in Daemon config, got %q", cfg.Permission)
	}
}

// TestDaemon_RequestID_FingerprintSafety 驗證 RequestID 重用時防範 fingerprint 不符與已取消之任務
func TestDaemon_RequestID_FingerprintSafety(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-fp-safety-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, _ := storage.NewSQLiteStore(dbPath)
	defer store.Close()
	policy := auth.NewPolicy([]string{tmpDir})
	sockPath := filepath.Join(tmpDir, "asura.sock")

	eng := engine.NewEngine(store, policy, engine.WithRunner(func(ctx context.Context, ws, p string) (string, error) {
		return "ok", nil
	}))

	d := NewDaemon(Config{SocketPath: sockPath}, store, policy, WithEngine(eng))
	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	client, err := NewClient(sockPath)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	defer client.Close()

	// 第一次提交
	_, err = client.Submit(context.Background(), engine.SubmitRequest{
		ID:        "shared-req-id",
		Workspace: tmpDir,
		Prompt:    "original prompt",
	})
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}

	// 相同 RequestID 但不同 Prompt (fingerprint 不符)，必須被拒絕
	_, err = client.Submit(context.Background(), engine.SubmitRequest{
		ID:        "shared-req-id",
		Workspace: tmpDir,
		Prompt:    "different mismatched prompt",
	})
	if err == nil {
		t.Fatalf("expected error for mismatched fingerprint on same RequestID, got nil")
	}
	if !strings.Contains(err.Error(), "different payload") {
		t.Errorf("expected error to explain payload mismatch, got: %v", err)
	}
}

func TestDaemon_CronIPC(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura.db")
	sockPath := filepath.Join(tmpDir, "asura-cron-ipc.sock")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	d := NewDaemon(Config{SocketPath: sockPath}, store, policy)
	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	client, err := NewClient(sockPath)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// 1. 新增排程
	addReq := CronAddRequest{
		Name:      "backup-job",
		Workspace: tmpDir,
		Schedule:  "*/10 * * * *",
		Prompt:    "run backup",
	}
	sched, err := client.AddCronSchedule(ctx, addReq)
	if err != nil {
		t.Fatalf("AddCronSchedule failed: %v", err)
	}
	if sched.Name != "backup-job" || sched.Status != storage.CronStatusActive {
		t.Fatalf("unexpected added schedule: %+v", sched)
	}

	// 2. 非法表達式新增應回報錯誤
	badReq := CronAddRequest{
		Name:      "bad-job",
		Workspace: tmpDir,
		Schedule:  "bad-expression",
		Prompt:    "run bad",
	}
	_, err = client.AddCronSchedule(ctx, badReq)
	if err == nil {
		t.Fatalf("expected error adding invalid cron expression, got nil")
	}

	// 3. 查詢清單
	list, err := client.ListCronSchedules(ctx)
	if err != nil {
		t.Fatalf("ListCronSchedules failed: %v", err)
	}
	if len(list) != 1 || list[0].ID != sched.ID {
		t.Fatalf("expected 1 schedule in list, got %+v", list)
	}

	// 4. 暫停與恢復
	paused, err := client.PauseCronSchedule(ctx, sched.ID)
	if err != nil {
		t.Fatalf("PauseCronSchedule failed: %v", err)
	}
	if paused.Status != storage.CronStatusPaused {
		t.Fatalf("expected paused status, got %s", paused.Status)
	}

	resumed, err := client.ResumeCronSchedule(ctx, sched.ID)
	if err != nil {
		t.Fatalf("ResumeCronSchedule failed: %v", err)
	}
	if resumed.Status != storage.CronStatusActive {
		t.Fatalf("expected active status, got %s", resumed.Status)
	}

	// 5. 刪除排程
	if err := client.RemoveCronSchedule(ctx, sched.ID); err != nil {
		t.Fatalf("RemoveCronSchedule failed: %v", err)
	}
	afterRemove, err := client.ListCronSchedules(ctx)
	if err != nil {
		t.Fatalf("ListCronSchedules failed: %v", err)
	}
	if len(afterRemove) != 0 {
		t.Fatalf("expected 0 schedules after remove, got %d", len(afterRemove))
	}
}

// TestDaemon_WorkspaceMutex_NormalizedPaths 驗證 MUST FIX 4: 帶尾斜線或子路徑均被正規化為同專案互斥排隊
func TestDaemon_WorkspaceMutex_NormalizedPaths(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "asura.sock")
	dbPath := filepath.Join(tmpDir, "asura.db")
	allowedRoot := filepath.Join(tmpDir, "project-a")
	_ = os.MkdirAll(allowedRoot, 0755)

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{allowedRoot})

	t1Started := make(chan struct{})
	t1Release := make(chan struct{})
	runner := func(ctx context.Context, ws string, prompt string) (string, error) {
		if prompt == "task-1" {
			close(t1Started)
			<-t1Release
		}
		return "done", nil
	}
	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))

	d := NewDaemon(Config{
		SocketPath: socketPath,
		MaxWorkers: 2,
		MaxQueue:   5,
	}, store, policy, WithEngine(eng))
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx := context.Background()

	// 1. 提交 /project-a 任務 1
	resp1, err := client.Submit(ctx, engine.SubmitRequest{
		Workspace: allowedRoot,
		Prompt:    "task-1",
	})
	if err != nil {
		t.Fatalf("submit task 1 failed: %v", err)
	}
	if resp1 == nil {
		t.Fatalf("expected resp1")
	}
	<-t1Started

	// 2. 提交帶尾斜線的 /project-a/ 任務 2
	resp2, err := client.Submit(ctx, engine.SubmitRequest{
		Workspace: allowedRoot + string(filepath.Separator),
		Prompt:    "task-2",
	})
	if err != nil {
		t.Fatalf("submit task 2 failed: %v", err)
	}

	// 驗證任務 2 被正確判定為同專案，進入 queued 狀態而不是並行
	if resp2.Status != storage.StatusQueued {
		t.Fatalf("expected task 2 to be queued due to normalized workspace lock, got status %s", resp2.Status)
	}

	close(t1Release)
}

// TestDaemon_QueuedTaskClassPreservedOnWake 驗證 MUST FIX 2: 排隊任務喚醒時使用本身持久化之 Class
func TestDaemon_QueuedTaskClassPreservedOnWake(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "asura.sock")
	dbPath := filepath.Join(tmpDir, "asura.db")
	allowedRoot := filepath.Join(tmpDir, "project-b")
	_ = os.MkdirAll(allowedRoot, 0755)

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{allowedRoot})

	t1Started := make(chan struct{})
	t1Release := make(chan struct{})
	var t2Started atomic.Bool

	runner := func(ctx context.Context, ws string, prompt string) (string, error) {
		if prompt == "cron-1" {
			close(t1Started)
			<-t1Release
		} else if prompt == "interactive-2" {
			t2Started.Store(true)
		}
		return "done", nil
	}
	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))

	// 容量配置: workers=2, maxCron=1, queue=2
	pool := NewWorkerPoolWithCapacity(CapacityConfig{
		MaxWorkers:     2,
		MaxCronWorkers: 1,
		MaxQueue:       2,
	})
	d := &Daemon{
		cfg: Config{
			SocketPath: socketPath,
			MaxWorkers: 2,
			MaxQueue:   2,
		},
		pool:     pool,
		wsQueue:  NewWorkspaceQueue(),
		store:    store,
		engine:   eng,
		policy:   policy,
		closed:   make(chan struct{}),
		inFlight: 0,
	}

	ctx := context.Background()

	// 1. 在專案 B 執行 Cron 任務，且佔滿唯一 cron worker
	cronTaskID, err := d.dispatchCronTask(ctx, "cron-b", allowedRoot, "cron-1")
	if err != nil {
		t.Fatalf("dispatchCronTask failed: %v", err)
	}
	<-t1Started

	// 2. 提交同專案的即時互動任務 2（此時 cron 佔滿唯一 cron worker）
	d.inFlight++
	interactiveTaskID := engine.GenerateTaskID()
	now := time.Now().UTC()
	task2 := &storage.Task{
		ID:        interactiveTaskID,
		Prompt:    "interactive-2",
		Workspace: allowedRoot,
		Status:    storage.StatusPending,
		Class:     string(TaskClassInteractive),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, task2); err != nil {
		t.Fatal(err)
	}

	wsKey := d.canonicalWorkspace(allowedRoot)
	acquired, _ := d.wsQueue.TryAcquire(wsKey, interactiveTaskID)
	if acquired {
		t.Fatalf("expected interactive task to be queued behind cron task")
	}
	_ = store.UpdateTaskStatus(ctx, interactiveTaskID, storage.StatusQueued, "", "")

	// 3. 釋放 cron 任務
	close(t1Release)

	// 等待互動任務被喚醒執行
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if t2Started.Load() {
			break
		}
	}
	if !t2Started.Load() {
		t.Fatalf("interactive task was not executed after cron task completed")
	}

	// 等待 completed 狀態
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		t2, err := store.GetTask(ctx, interactiveTaskID)
		if err == nil && t2.Status == storage.StatusCompleted {
			break
		}
	}

	// 驗證互動任務最終狀態為 completed（絕非 failed: capacity exceeded）
	t2, err := store.GetTask(ctx, interactiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if t2.Status != storage.StatusCompleted {
		t.Fatalf("expected interactive task to complete successfully, got %s (err: %s)", t2.Status, t2.ErrorMessage)
	}
	_ = cronTaskID
}

// TestDaemon_WakeQueuedTask_PoolSlotFreed 驗證 MUST FIX 3: 喚醒排隊任務時前一任務已釋放 pool 槽位
func TestDaemon_WakeQueuedTask_PoolSlotFreed(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "asura.sock")
	dbPath := filepath.Join(tmpDir, "asura.db")
	wsA := filepath.Join(tmpDir, "ws-a")
	wsB := filepath.Join(tmpDir, "ws-b")
	_ = os.MkdirAll(wsA, 0755)
	_ = os.MkdirAll(wsB, 0755)

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{wsA, wsB})

	a1Started := make(chan struct{})
	a1Release := make(chan struct{})
	var a2Executed atomic.Bool

	runner := func(ctx context.Context, ws string, prompt string) (string, error) {
		if prompt == "cron-a1" {
			close(a1Started)
			<-a1Release
		} else if prompt == "cron-a2" {
			a2Executed.Store(true)
		}
		return "done", nil
	}
	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))

	// 容量極限: MaxWorkers=2, MaxCronWorkers=1, MaxQueue=1
	pool := NewWorkerPoolWithCapacity(CapacityConfig{
		MaxWorkers:     2,
		MaxCronWorkers: 1,
		MaxQueue:       1,
	})
	d := &Daemon{
		cfg: Config{
			SocketPath: socketPath,
			MaxWorkers: 2,
			MaxQueue:   1,
		},
		pool:     pool,
		wsQueue:  NewWorkspaceQueue(),
		store:    store,
		engine:   eng,
		policy:   policy,
		closed:   make(chan struct{}),
		inFlight: 0,
	}

	ctx := context.Background()

	// 步驟:
	// 1. Cron-A1 在 wsA 執行中 (佔滿 1 個 MaxCronWorkers)
	// 2. Cron-B 在 wsB 提交 (因無 cron 槽位進入 pool 的 cronQueue)
	// 3. Cron-A2 在 wsA 提交 (因同專案互斥進入 wsQueue 專案佇列)
	// 4. A1 執行完畢釋放
	// 5. 驗證 A2 被喚醒後能成功執行完成，絕不因 A1 還佔著 pool 槽位而被標記為 failed

	_, err = d.dispatchCronTask(ctx, "c1", wsA, "cron-a1")
	if err != nil {
		t.Fatalf("dispatch a1 failed: %v", err)
	}
	<-a1Started

	// 提交 Cron-B (進入 pool 佇列)
	bStarted := make(chan struct{})
	go func() {
		_, _ = d.dispatchCronTask(ctx, "cb", wsB, "cron-b")
		close(bStarted)
	}()
	time.Sleep(50 * time.Millisecond)

	// 提交 Cron-A2 (同專案排隊)
	a2ID, err := d.dispatchCronTask(ctx, "c2", wsA, "cron-a2")
	if err != nil {
		t.Fatalf("dispatch a2 failed: %v", err)
	}

	// 釋放 A1
	close(a1Release)

	// 輪詢等待 A2 執行
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if a2Executed.Load() {
			break
		}
	}

	// 等待 completed 狀態
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		t2, err := store.GetTask(ctx, a2ID)
		if err == nil && t2.Status == storage.StatusCompleted {
			break
		}
	}

	a2Task, err := store.GetTask(ctx, a2ID)
	if err != nil {
		t.Fatal(err)
	}
	if a2Task.Status == storage.StatusFailed {
		t.Fatalf("A2 failed with capacity exceeded! Error: %s", a2Task.ErrorMessage)
	}
	if a2Task.Status != storage.StatusCompleted {
		t.Fatalf("expected A2 to complete, got %s", a2Task.Status)
	}
}

// TestDaemon_WakeQueuedTask_WhenTaskStatusIsPending 驗證 MUST FIX 1: 排隊任務在 DB 仍為 pending 時喚醒不被遺漏跳過
func TestDaemon_WakeQueuedTask_WhenTaskStatusIsPending(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura.db")
	allowedRoot := filepath.Join(tmpDir, "project-pending")
	_ = os.MkdirAll(allowedRoot, 0755)

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{allowedRoot})

	var t2Started atomic.Bool
	runner := func(ctx context.Context, ws string, prompt string) (string, error) {
		if prompt == "task-2" {
			t2Started.Store(true)
		}
		return "done", nil
	}
	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))

	pool := NewWorkerPoolWithCapacity(CapacityConfig{
		MaxWorkers:     2,
		MaxCronWorkers: 1,
		MaxQueue:       5,
	})
	d := &Daemon{
		cfg: Config{
			SocketPath: filepath.Join(tmpDir, "asura.sock"),
			MaxWorkers: 2,
			MaxQueue:   5,
		},
		pool:     pool,
		wsQueue:  NewWorkspaceQueue(),
		store:    store,
		engine:   eng,
		policy:   policy,
		closed:   make(chan struct{}),
		inFlight: 0,
	}

	ctx := context.Background()

	// 1. 任務 1 持有專案鎖
	t1ID := engine.GenerateTaskID()
	wsKey := d.canonicalWorkspace(allowedRoot)
	acquired, _ := d.wsQueue.TryAcquire(wsKey, t1ID)
	if !acquired {
		t.Fatal("expected t1 to acquire workspace lock")
	}

	// 2. 任務 2 排入專案佇列，但狀態仍在 pending（模擬 SQLite 寫入延遲）
	t2ID := engine.GenerateTaskID()
	now := time.Now().UTC()
	task2 := &storage.Task{
		ID:        t2ID,
		Prompt:    "task-2",
		Workspace: allowedRoot,
		Status:    storage.StatusPending, // 故意保持 pending
		Class:     string(TaskClassInteractive),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, task2); err != nil {
		t.Fatal(err)
	}
	d.inFlight++
	// 加入專案等待佇列
	acquired2, _ := d.wsQueue.TryAcquire(wsKey, t2ID)
	if acquired2 {
		t.Fatal("expected t2 to be queued behind t1")
	}

	// 3. 任務 1 完成並喚醒下一個任務
	d.releaseWorkspaceAndWakeNext(wsKey, t1ID)

	// 等待 t2 被喚醒並執行
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if t2Started.Load() {
			break
		}
	}

	if !t2Started.Load() {
		t.Fatalf("MUST FIX 1 regression: task 2 remained stuck because its status was pending during wake!")
	}
}

// TestDaemon_WakeQueuedTask_BypassesPoolQueueLimit 驗證 MUST FIX 2: 被喚醒的 cron 任務不被 pool 的 MaxQueue 拒絕標為 failed
func TestDaemon_WakeQueuedTask_BypassesPoolQueueLimit(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura.db")
	wsA := filepath.Join(tmpDir, "project-a")
	wsB := filepath.Join(tmpDir, "project-b")
	wsC := filepath.Join(tmpDir, "project-c")
	_ = os.MkdirAll(wsA, 0755)
	_ = os.MkdirAll(wsB, 0755)
	_ = os.MkdirAll(wsC, 0755)

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{wsA, wsB, wsC})

	cronBStarted := make(chan struct{})
	cronBBlocker := make(chan struct{})
	i1Started := make(chan struct{})
	i1Blocker := make(chan struct{})
	var cronA2Started atomic.Bool

	runner := func(ctx context.Context, ws string, prompt string) (string, error) {
		switch prompt {
		case "cron-b":
			close(cronBStarted)
			<-cronBBlocker
		case "i-1":
			close(i1Started)
			<-i1Blocker
		case "cron-a2":
			cronA2Started.Store(true)
		}
		return "done", nil
	}
	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))

	// 配置 MaxWorkers=3, MaxCronWorkers=1, MaxQueue=1
	pool := NewWorkerPoolWithCapacity(CapacityConfig{
		MaxWorkers:     3,
		MaxCronWorkers: 1,
		MaxQueue:       1,
	})
	d := &Daemon{
		cfg: Config{
			SocketPath: filepath.Join(tmpDir, "asura.sock"),
			MaxWorkers: 3,
			MaxQueue:   1,
		},
		pool:     pool,
		wsQueue:  NewWorkspaceQueue(),
		store:    store,
		engine:   eng,
		policy:   policy,
		closed:   make(chan struct{}),
		inFlight: 0,
	}

	ctx := context.Background()

	// 1. 專案 B 執行 cron-B，佔滿唯一的 cron worker (runningCron=1)
	_, err = d.dispatchCronTask(ctx, "sched-b", wsB, "cron-b")
	if err != nil {
		t.Fatalf("dispatch cron-b failed: %v", err)
	}
	<-cronBStarted

	// 2. 專案 C 提交 cron-C，進入 pool.cronQueue 排隊（使 pool.cronQueue 達到 maxQueue=1 上限）
	_, err = d.dispatchCronTask(ctx, "sched-c", wsC, "cron-c")
	if err != nil {
		t.Fatalf("dispatch cron-c failed: %v", err)
	}

	// 3. 專案 A 執行互動任務 i-1
	wsKeyA := d.canonicalWorkspace(wsA)
	i1ID := engine.GenerateTaskID()
	d.inFlight++
	tI1 := &storage.Task{
		ID:        i1ID,
		Prompt:    "i-1",
		Workspace: wsA,
		Status:    storage.StatusPending,
		Class:     string(TaskClassInteractive),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	_ = store.CreateTask(ctx, tI1)
	d.wsQueue.TryAcquire(wsKeyA, i1ID)
	_ = d.dispatchTask(i1ID, wsA, "i-1", TaskClassInteractive)
	<-i1Started

	// 4. 專案 A 觸發 cron-A2，進入專案 A 佇列等待
	cronA2ID, err := d.dispatchCronTask(ctx, "sched-a2", wsA, "cron-a2")
	if err != nil {
		t.Fatalf("dispatchCronTask a2 failed: %v", err)
	}

	// 5. 互動任務 i-1 結束，釋放專案鎖並喚醒 cron-A2
	close(i1Blocker)

	// 等待一小段時間確認 cron-A2 的狀態
	time.Sleep(100 * time.Millisecond)

	a2Task, err := store.GetTask(ctx, cronA2ID)
	if err != nil {
		t.Fatal(err)
	}

	if a2Task.Status == storage.StatusFailed {
		t.Fatalf("MUST FIX 2 regression: awakened cron-A2 was rejected by pool with error: %s", a2Task.ErrorMessage)
	}

	// 釋放 cron-B 讓 cron-A2 執行完畢
	close(cronBBlocker)

	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if cronA2Started.Load() {
			break
		}
	}
	if !cronA2Started.Load() {
		t.Fatal("expected cron-A2 to execute after cron-b released")
	}
}

// TestDaemon_ReleaseWorkspace_SymlinkTargetChanged_DoesNotLeak 驗證 MUST FIX 3: 專案目錄為 symlink 且 target 改變時，專案鎖不外洩
func TestDaemon_ReleaseWorkspace_SymlinkTargetChanged_DoesNotLeak(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura.db")
	target1 := filepath.Join(tmpDir, "target1")
	target2 := filepath.Join(tmpDir, "target2")
	linkPath := filepath.Join(tmpDir, "link-project")
	_ = os.MkdirAll(target1, 0755)
	_ = os.MkdirAll(target2, 0755)

	// 初始: link 指向 target1
	if err := os.Symlink(target1, linkPath); err != nil {
		t.Fatal(err)
	}

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	d := &Daemon{
		cfg: Config{
			SocketPath: filepath.Join(tmpDir, "asura.sock"),
			MaxWorkers: 2,
			MaxQueue:   2,
		},
		pool:     NewWorkerPool(2, 2),
		wsQueue:  NewWorkspaceQueue(),
		store:    store,
		policy:   policy,
		closed:   make(chan struct{}),
		inFlight: 0,
	}

	var t1Started atomic.Bool
	var t1Completed atomic.Bool
	runner := func(ctx context.Context, ws string, prompt string) (string, error) {
		t1Started.Store(true)
		time.Sleep(30 * time.Millisecond) // 確保在執行期間有窗口進行 symlink 變更
		t1Completed.Store(true)
		return "done", nil
	}
	d.engine = engine.NewEngine(store, policy, engine.WithRunner(runner))

	// 1. 任務 1 以 linkPath 取得專案鎖並派發（此時 canonical 為 target1）
	t1ID := "task-1"
	wsKey1 := d.canonicalWorkspace(linkPath)
	acquired, _ := d.wsQueue.TryAcquire(wsKey1, t1ID)
	if !acquired {
		t.Fatal("expected task 1 to acquire lock")
	}

	// 派發任務 1，其內部會捕獲取鎖當下的 wsKey
	err = d.dispatchTask(t1ID, linkPath, "prompt-1", TaskClassInteractive)
	if err != nil {
		t.Fatalf("dispatchTask failed: %v", err)
	}

	// 等候任務 1 真正進入執行階段
	for i := 0; i < 100; i++ {
		if t1Started.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !t1Started.Load() {
		t.Fatal("expected task 1 to start running")
	}

	// 2. 任務 1 執行期間，symlink 更改指向 target2
	_ = os.Remove(linkPath)
	if err := os.Symlink(target2, linkPath); err != nil {
		t.Fatal(err)
	}

	// 等待任務 1 完成
	for i := 0; i < 100; i++ {
		time.Sleep(10 * time.Millisecond)
		if t1Completed.Load() {
			break
		}
	}

	// 3. 驗證 target1 的鎖已經被成功釋放（不會因 symlink 更改而洩漏），新任務可以直接取得鎖
	t2ID := "task-2"
	var acquired2 bool
	for i := 0; i < 100; i++ {
		acquired2, _ = d.wsQueue.TryAcquire(target1, t2ID)
		if acquired2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !acquired2 {
		t.Fatalf("MUST FIX 3 regression: workspace lock leaked! target1 is permanently locked")
	}
}

// TestDaemon_DispatchFailure_HandsOverWorkspaceLock 驗證 MUST FIX 8: 任務在派工失敗時，正確將專案鎖交接給排隊任務
func TestDaemon_DispatchFailure_HandsOverWorkspaceLock(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura.db")
	allowedRoot := filepath.Join(tmpDir, "project-handover")
	_ = os.MkdirAll(allowedRoot, 0755)

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{allowedRoot})

	var t2Executed atomic.Bool
	runner := func(ctx context.Context, ws string, prompt string) (string, error) {
		if prompt == "task-2" {
			t2Executed.Store(true)
		}
		return "done", nil
	}
	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))

	// 特意設定 MaxQueue=0，使任務 1 派工失敗
	pool := NewWorkerPoolWithCapacity(CapacityConfig{
		MaxWorkers:     1,
		MaxCronWorkers: 1,
		MaxQueue:       0, // 無佇列
	})
	d := &Daemon{
		cfg: Config{
			SocketPath: filepath.Join(tmpDir, "asura.sock"),
			MaxWorkers: 1,
			MaxQueue:   0,
		},
		pool:     pool,
		wsQueue:  NewWorkspaceQueue(),
		store:    store,
		engine:   eng,
		policy:   policy,
		closed:   make(chan struct{}),
		inFlight: 0,
	}

	ctx := context.Background()
	wsKey := d.canonicalWorkspace(allowedRoot)

	// 先佔滿唯一的 worker
	blocker := make(chan struct{})
	_, _ = pool.SubmitAsync(ctx, func() error {
		<-blocker
		return nil
	})

	// 任務 1 取得專案鎖
	t1ID := engine.GenerateTaskID()
	acquired, _ := d.wsQueue.TryAcquire(wsKey, t1ID)
	if !acquired {
		t.Fatal("expected t1 to acquire workspace lock")
	}

	// 任務 2 排入專案佇列
	t2ID := engine.GenerateTaskID()
	acquired2, _ := d.wsQueue.TryAcquire(wsKey, t2ID)
	if acquired2 {
		t.Fatal("expected t2 to queue")
	}
	_ = store.CreateTask(ctx, &storage.Task{
		ID:        t2ID,
		Prompt:    "task-2",
		Workspace: allowedRoot,
		Status:    storage.StatusQueued,
		Class:     string(TaskClassInteractive),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
	d.inFlight++

	// 任務 1 嘗試派發，因 pool 滿（running=1, maxQueue=0）而失敗
	err = d.dispatchTask(t1ID, allowedRoot, "task-1", TaskClassInteractive)
	if err == nil {
		t.Fatal("expected dispatchTask to fail due to full pool")
	}

	// 釋放 pool 中的佔位 blocker，讓任務 2 有 worker 可用
	close(blocker)
	time.Sleep(20 * time.Millisecond)

	// 派工失敗後，處理交接
	d.releaseCapacity()
	d.releaseWorkspaceAndWakeNext(wsKey, t1ID)

	// 驗證任務 2 被成功喚醒並執行
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if t2Executed.Load() {
			break
		}
	}
	if !t2Executed.Load() {
		t.Fatalf("MUST FIX 8 regression: task 2 was not executed after task 1 dispatch failure!")
	}
}

// TestDaemon_CancellingTaskKeepsWorkspaceLockedUntilReapConfirmed
// 驗證任務處於 cancelling 且進程組未確認回收（身分未知或逾時）時，
// 嚴禁提前釋放 workspace lock，同 workspace 的下一個排隊任務絕不得開始執行
func TestDaemon_CancellingTaskKeepsWorkspaceLockedUntilReapConfirmed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-daemon-cancel-lock-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	sockPath := filepath.Join(tmpDir, "asura.sock")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	var (
		orphanPGID        int
		runnerInvocations int32
		taskARunning      = make(chan struct{})
	)

	runner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		atomic.AddInt32(&runnerInvocations, 1)
		if prompt == "prompt-task-a" {
			cmd := exec.Command("sh", "-c", "exec env -i PATH=/usr/bin:/bin sh -c '(sleep 10) & exit 0'")
			opencode.ConfigureProcessGroup(cmd)
			if err := cmd.Start(); err != nil {
				return "", err
			}
			leaderPID := cmd.Process.Pid
			pgid, pErr := syscall.Getpgid(leaderPID)
			if pErr != nil {
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

			taskID := engine.TaskIDFromContext(ctx)
			if taskID != "" {
				_ = store.UpdateTaskPGID(context.Background(), taskID, orphanPGID)
			}
			close(taskARunning)

			<-ctx.Done()
			return "task A partial output", ctx.Err()
		}

		return "task B completed", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))
	d := NewDaemon(Config{
		SocketPath: sockPath,
	}, store, policy, WithEngine(eng))
	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	defer func() {
		if orphanPGID > 0 {
			_ = syscall.Kill(-orphanPGID, syscall.SIGKILL)
		}
	}()

	client, err := NewClient(sockPath)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// 1. 提交 Task A
	taskA, err := client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "prompt-task-a",
	})
	if err != nil {
		t.Fatalf("submit task A failed: %v", err)
	}

	// 等候 Task A 啟動並登記 orphan PGID
	select {
	case <-taskARunning:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for task A to start")
	}

	// 2. 提交 Task B（相同 workspace），必然排入 queue
	taskB, err := client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "prompt-task-b",
	})
	if err != nil {
		t.Fatalf("submit task B failed: %v", err)
	}
	if taskB.Status != storage.StatusQueued {
		t.Fatalf("expected task B to be queued, got: %s", taskB.Status)
	}

	// 3. 取消 Task A：因為身分無法確認，CancelTask 必須失敗
	_, cancelErr := client.CancelTask(context.Background(), taskA.ID)
	if cancelErr == nil {
		t.Fatalf("expected cancel task A to fail due to unknown identity, got nil")
	}

	// 等候背景 SubmitTask(A) 退出
	time.Sleep(150 * time.Millisecond)

	// 4. 斷言 Task A
	savedA, err := store.GetTask(context.Background(), taskA.ID)
	if err != nil {
		t.Fatalf("failed to query task A: %v", err)
	}
	if savedA.Status != storage.StatusCancelling {
		t.Errorf("expected task A to be %s, got: %s", storage.StatusCancelling, savedA.Status)
	}
	if savedA.PGID != orphanPGID {
		t.Errorf("expected task A PGID to be preserved as %d, got: %d", orphanPGID, savedA.PGID)
	}

	// 5. 核心斷言：Task B 絕不得被喚醒或執行！
	savedB, err := store.GetTask(context.Background(), taskB.ID)
	if err != nil {
		t.Fatalf("failed to query task B: %v", err)
	}
	if savedB.Status != storage.StatusQueued {
		t.Fatalf("CRITICAL BUG: task B was transitioned to %s, workspace lock was released before reap confirmed!", savedB.Status)
	}
	if invocations := atomic.LoadInt32(&runnerInvocations); invocations != 1 {
		t.Fatalf("CRITICAL BUG: runner invocations expected 1 (only A), got: %d (task B was executed!)", invocations)
	}
}

func TestThreeFaceRedelivery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-tf-redelivery-*")
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
	sockPath := filepath.Join(tmpDir, "daemon.sock")

	var runCount int32
	mockRunner := func(ctx context.Context, ws, prompt string) (string, error) {
		atomic.AddInt32(&runCount, 1)
		return "three-face result", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(mockRunner))
	d := NewDaemon(Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
	}, store, policy, WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("failed to start daemon: %v", err)
	}
	defer d.Close()

	client, err := NewClient(sockPath)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// 1. 提交 Three-Face 任務
	res1, err := client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "execute complex feature",
		ID:        "req-tf-redelivery-001",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	if res1.ID == "" {
		t.Fatalf("expected valid task ID, got empty")
	}

	// 等候背景執行完成
	var completedTask *storage.Task
	for i := 0; i < 20; i++ {
		tQuery, err := store.GetTask(context.Background(), res1.ID)
		if err == nil && (tQuery.Status == storage.StatusCompleted || tQuery.Status == storage.StatusFailed) {
			completedTask = tQuery
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if completedTask == nil {
		t.Fatalf("task timed out waiting for completion")
	}

	initialRunCount := atomic.LoadInt32(&runCount)

	// 2. 相同 RequestID 與相同 payload 重新發送
	res2, err := client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "execute complex feature",
		ID:        "req-tf-redelivery-001",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("second submit (redelivery) failed: %v", err)
	}

	if res2.ID != res1.ID {
		t.Errorf("expected same task ID %s, got %s", res1.ID, res2.ID)
	}
	if res2.ExecutionMode != "three-face" {
		t.Errorf("expected ExecutionMode three-face, got %s", res2.ExecutionMode)
	}

	// 斷言：完全不觸發第二次執行
	if afterCount := atomic.LoadInt32(&runCount); afterCount != initialRunCount {
		t.Errorf("redelivery triggered second run! expected %d, got %d", initialRunCount, afterCount)
	}

	// 3. 相同 RequestID 但以不同 Mode (direct) 發送，必須被拒絕
	_, err = client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "execute complex feature",
		ID:        "req-tf-redelivery-001",
		Mode:      "direct",
	})
	if err == nil {
		t.Fatalf("expected different mode redelivery to fail, got nil")
	}
	if !strings.Contains(err.Error(), "request ID reused with different payload") {
		t.Errorf("expected 'request ID reused with different payload', got: %v", err)
	}
}

// TestDaemon_IPC_InvalidExecutionMode_Rejected 驗證 Daemon IPC Ingress 嚴格阻擋非法 mode
func TestDaemon_IPC_InvalidExecutionMode_Rejected(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura.db")
	sockPath := filepath.Join(tmpDir, "asura.sock")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	eng := engine.NewEngine(store, policy, engine.WithRunner(func(ctx context.Context, ws, p string) (string, error) {
		return "ok", nil
	}))

	d := NewDaemon(Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
	}, store, policy, WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	client, err := NewClient(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// 嘗試提交未定義之 mode "invalid_mode"
	_, err = client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "test",
		Mode:      "invalid_mode",
	})
	if err == nil {
		t.Fatalf("expected error for invalid execution mode at IPC ingress, got nil")
	}
	if !strings.Contains(err.Error(), "invalid execution mode") {
		t.Errorf("expected 'invalid execution mode' in error, got: %v", err)
	}
}

// TestDaemon_ThreeFace_CapabilityIsolation 驗證唯讀權限策略定義與能力邊界隔離
func TestDaemon_ThreeFace_CapabilityIsolation(t *testing.T) {
	// 驗證 DefaultReadOnlyPermissionPolicy 包含唯讀限制
	if !strings.Contains(DefaultReadOnlyPermissionPolicy, `"read":"allow"`) {
		t.Errorf("expected read:allow in DefaultReadOnlyPermissionPolicy, got %s", DefaultReadOnlyPermissionPolicy)
	}
	if !strings.Contains(DefaultReadOnlyPermissionPolicy, `"*":"deny"`) {
		t.Errorf("expected *:deny in DefaultReadOnlyPermissionPolicy, got %s", DefaultReadOnlyPermissionPolicy)
	}

	// 驗證 DefaultApprovedPermissionPolicy 允許 edit/build/test
	if !strings.Contains(DefaultApprovedPermissionPolicy, `"edit":"allow"`) {
		t.Errorf("expected edit:allow in DefaultApprovedPermissionPolicy, got %s", DefaultApprovedPermissionPolicy)
	}
}
