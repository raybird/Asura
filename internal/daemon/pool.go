package daemon

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrCapacityExceeded 代表達到全域並行與佇列上限
	ErrCapacityExceeded = errors.New("capacity exceeded: worker pool saturated")
	// ErrPoolClosed 代表 WorkerPool 已關閉
	ErrPoolClosed = errors.New("worker pool closed")
)

// WorkerPool 限制全域並行執行任務數量
type WorkerPool struct {
	maxWorkers int
	maxQueue   int
	running    int
	queue      chan taskItem
	mu         sync.Mutex
	closed     chan struct{}
	closeOnce  sync.Once
	wg         sync.WaitGroup
}

type taskItem struct {
	ctx    context.Context
	fn     func() error
	result chan error
}

// NewWorkerPool 初始化指定 Worker 數與佇列長度的 WorkerPool
// 遵循 R-02：以互斥鎖原子保護 Worker 額度交接與佇列檢查，徹底杜絕佇列停滯
func NewWorkerPool(maxWorkers int, maxQueue int) *WorkerPool {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	if maxQueue < 0 {
		maxQueue = 0
	}

	return &WorkerPool{
		maxWorkers: maxWorkers,
		maxQueue:   maxQueue,
		queue:      make(chan taskItem, maxQueue),
		closed:     make(chan struct{}),
	}
}

// Submit 提交任務至 WorkerPool，若容量已滿立即回傳 ErrCapacityExceeded
func (p *WorkerPool) Submit(ctx context.Context, fn func() error) error {
	select {
	case <-p.closed:
		return ErrPoolClosed
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	item := taskItem{
		ctx:    ctx,
		fn:     fn,
		result: make(chan error, 1),
	}

	p.mu.Lock()
	select {
	case <-p.closed:
		p.mu.Unlock()
		return ErrPoolClosed
	default:
	}

	if p.running < p.maxWorkers {
		p.running++
		p.wg.Add(1)
		go p.workerLoop(item)
		p.mu.Unlock()
	} else {
		// Worker 已達上限，嘗試排入佇列
		select {
		case p.queue <- item:
			p.mu.Unlock()
		default:
			p.mu.Unlock()
			return ErrCapacityExceeded
		}
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.closed:
		return ErrPoolClosed
	case err := <-item.result:
		return err
	}
}

func (p *WorkerPool) workerLoop(initialItem taskItem) {
	defer p.wg.Done()
	p.executeItem(initialItem)

	for {
		p.mu.Lock()
		select {
		case <-p.closed:
			p.running--
			p.mu.Unlock()
			return
		case item := <-p.queue:
			p.mu.Unlock()
			p.executeItem(item)
		default:
			// 佇列為空，原子釋放 Worker 額度並退出（徹底修復 R-02 佇列殘留停滯）
			p.running--
			p.mu.Unlock()
			return
		}
	}
}

func (p *WorkerPool) executeItem(item taskItem) {
	select {
	case <-p.closed:
		item.result <- ErrPoolClosed
		return
	case <-item.ctx.Done():
		item.result <- item.ctx.Err()
		return
	default:
	}

	err := item.fn()
	item.result <- err
}

// Close 關閉 WorkerPool 並等待所有正在進行的任務結束
func (p *WorkerPool) Close() {
	p.closeOnce.Do(func() {
		close(p.closed)
	})
	p.wg.Wait()
}
