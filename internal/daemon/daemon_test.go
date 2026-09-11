package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

	sockPath := filepath.Join(tmpDir, "asura.sock")
	dbPath := filepath.Join(tmpDir, "asura.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	// 1. 驗證競爭啟動：只有一個能成為 Daemon 擁有者，另一個被拒絕
	t.Run("競爭啟動無雙重擁有者", func(t *testing.T) {
		d1 := NewDaemon(Config{
			SocketPath: sockPath,
			MaxWorkers: 2,
		}, store, policy)

		if err := d1.Start(); err != nil {
			t.Fatalf("first daemon start failed: %v", err)
		}
		defer d1.Close()

		d2 := NewDaemon(Config{
			SocketPath: sockPath,
			MaxWorkers: 2,
		}, store, policy)

		err = d2.Start()
		if err == nil {
			_ = d2.Close()
			t.Fatalf("expected error on second daemon start with same socket, got nil")
		}
		if !errors.Is(err, ErrDaemonAlreadyRunning) {
			t.Errorf("expected ErrDaemonAlreadyRunning, got %v", err)
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
				if resp.Status != storage.StatusCompleted {
					t.Errorf("expected status completed, got %s", resp.Status)
				}
				atomic.AddInt32(&executionCounts[idx], 1)
			}()
		}
		wg.Wait()

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
}
