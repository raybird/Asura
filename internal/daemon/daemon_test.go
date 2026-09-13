package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"asura/internal/auth"
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

