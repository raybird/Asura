package daemon

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerPool(t *testing.T) {
	t.Run("並行上限控制與各執行一次", func(t *testing.T) {
		maxWorkers := 2
		pool := NewWorkerPool(maxWorkers, 10)
		defer pool.Close()

		var currentActive int32
		var maxActive int32
		var execCount int32

		var wg sync.WaitGroup
		for i := 0; i < 6; i++ {
			wg.Add(1)
			err := pool.Submit(context.Background(), func() error {
				defer wg.Done()
				active := atomic.AddInt32(&currentActive, 1)
				for {
					old := atomic.LoadInt32(&maxActive)
					if active <= old || atomic.CompareAndSwapInt32(&maxActive, old, active) {
						break
					}
				}
				time.Sleep(30 * time.Millisecond)
				atomic.AddInt32(&currentActive, -1)
				atomic.AddInt32(&execCount, 1)
				return nil
			})
			if err != nil {
				t.Fatalf("Submit failed: %v", err)
			}
		}

		wg.Wait()

		if max := atomic.LoadInt32(&maxActive); max > int32(maxWorkers) {
			t.Errorf("expected maxActive <= %d, got %d", maxWorkers, max)
		}
		if total := atomic.LoadInt32(&execCount); total != 6 {
			t.Errorf("expected 6 executions, got %d", total)
		}
	})

	t.Run("超出容量上限返回明確錯誤", func(t *testing.T) {
		pool := NewWorkerPool(1, 0) // 1 worker, 0 queue
		defer pool.Close()

		blocker := make(chan struct{})
		started := make(chan struct{})

		// 第一個任務在背景佔用唯一 worker
		go func() {
			_ = pool.Submit(context.Background(), func() error {
				close(started)
				<-blocker
				return nil
			})
		}()

		<-started

		// 第二個任務無空閒 worker 且隊列為 0，應直接回傳 ErrCapacityExceeded
		err := pool.Submit(context.Background(), func() error {
			return nil
		})
		if err == nil {
			t.Fatalf("expected error for saturated pool, got nil")
		}
		if !errors.Is(err, ErrCapacityExceeded) {
			t.Errorf("expected ErrCapacityExceeded, got %v", err)
		}

		close(blocker)
	})

	t.Run("R-02: 快速交替任務不發生佇列停滯", func(t *testing.T) {
		pool := NewWorkerPool(1, 5)
		defer pool.Close()

		for i := 0; i < 50; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			err := pool.Submit(ctx, func() error {
				time.Sleep(1 * time.Millisecond)
				return nil
			})
			cancel()
			if err != nil {
				t.Fatalf("iteration %d: Submit failed: %v", i, err)
			}
		}
	})

	t.Run("排水關閉 (Drain): 已進入佇列之任務在 Close 時必須完整執行完畢", func(t *testing.T) {
		pool := NewWorkerPool(1, 2)

		var executedA, executedB, executedC atomic.Bool
		startedA := make(chan struct{})

		// 任務 A: 佔住唯一 worker
		_, errA := pool.SubmitAsync(context.Background(), func() error {
			close(startedA)
			time.Sleep(50 * time.Millisecond)
			executedA.Store(true)
			return nil
		})
		if errA != nil {
			t.Fatalf("submit A failed: %v", errA)
		}

		<-startedA

		// 任務 B, C: 進入佇列
		_, errB := pool.SubmitAsync(context.Background(), func() error {
			time.Sleep(10 * time.Millisecond)
			executedB.Store(true)
			return nil
		})
		if errB != nil {
			t.Fatalf("submit B failed: %v", errB)
		}

		_, errC := pool.SubmitAsync(context.Background(), func() error {
			time.Sleep(10 * time.Millisecond)
			executedC.Store(true)
			return nil
		})
		if errC != nil {
			t.Fatalf("submit C failed: %v", errC)
		}

		// 呼叫 Close()，此時 pool 應停止接受新任務，但必須把 A, B, C 全部 drain 執行完畢
		pool.Close()

		if !executedA.Load() {
			t.Errorf("expected task A to be executed")
		}
		if !executedB.Load() {
			t.Errorf("expected task B to be drained and executed, but was abandoned")
		}
		if !executedC.Load() {
			t.Errorf("expected task C to be drained and executed, but was abandoned")
		}

		// 關閉後再提交應被拒絕
		errD := pool.Submit(context.Background(), func() error { return nil })
		if !errors.Is(errD, ErrPoolClosed) {
			t.Errorf("expected ErrPoolClosed after Close(), got %v", errD)
		}
	})
}

func TestWorkerPool_CapacityClassification(t *testing.T) {
	cfg := CapacityConfig{
		MaxWorkers:     2,
		MaxCronWorkers: 1,
		MaxQueue:       5,
	}
	pool := NewWorkerPoolWithCapacity(cfg)
	defer pool.Close()

	cron1Started := make(chan struct{})
	cron1Block := make(chan struct{})
	cron2Started := make(chan struct{})
	interactiveStarted := make(chan struct{})

	// 1. 提交 Cron 任務 1，應立即執行並佔用唯一背景 worker
	_, err := pool.SubmitAsyncClassified(context.Background(), TaskClassCron, func() error {
		close(cron1Started)
		<-cron1Block
		return nil
	})
	if err != nil {
		t.Fatalf("submit cron 1 failed: %v", err)
	}

	select {
	case <-cron1Started:
	case <-time.After(time.Second):
		t.Fatalf("cron 1 did not start")
	}

	// 2. 提交 Cron 任務 2，應受限於 MaxCronWorkers=1 而進入佇列等待
	_, err = pool.SubmitAsyncClassified(context.Background(), TaskClassCron, func() error {
		close(cron2Started)
		return nil
	})
	if err != nil {
		t.Fatalf("submit cron 2 failed: %v", err)
	}

	// 驗證 cron 2 未搶佔保留給人類互動通道的 worker
	select {
	case <-cron2Started:
		t.Fatalf("cron 2 started unexpectedly when MaxCronWorkers=1 is already saturated")
	case <-time.After(100 * time.Millisecond):
	}

	// 3. 提交 Interactive 任務，應立即取得保留的 worker 執行
	resChan, err := pool.SubmitAsyncClassified(context.Background(), TaskClassInteractive, func() error {
		close(interactiveStarted)
		return nil
	})
	if err != nil {
		t.Fatalf("submit interactive failed: %v", err)
	}

	select {
	case <-interactiveStarted:
	case <-time.After(time.Second):
		t.Fatalf("interactive task did not start immediately")
	}

	if err := <-resChan; err != nil {
		t.Fatalf("interactive task returned error: %v", err)
	}

	// 4. 釋放 Cron 1，Cron 2 應隨後被喚醒執行
	close(cron1Block)

	select {
	case <-cron2Started:
	case <-time.After(time.Second):
		t.Fatalf("cron 2 did not start after cron 1 finished")
	}
}

