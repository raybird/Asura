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
}
