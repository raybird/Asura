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
	sem        chan struct{}
	queue      chan taskItem
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
func NewWorkerPool(maxWorkers int, maxQueue int) *WorkerPool {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	if maxQueue < 0 {
		maxQueue = 0
	}

	return &WorkerPool{
		maxWorkers: maxWorkers,
		sem:        make(chan struct{}, maxWorkers),
		queue:      make(chan taskItem, maxQueue),
		closed:     make(chan struct{}),
	}
}

// Submit 提交任務至 WorkerPool，若容量已滿立即回傳 ErrCapacityExceeded
func (p *WorkerPool) Submit(ctx context.Context, fn func() error) error {
	select {
	case <-p.closed:
		return ErrPoolClosed
	default:
	}

	item := taskItem{
		ctx:    ctx,
		fn:     fn,
		result: make(chan error, 1),
	}

	// 1. 嘗試直接取得 worker 執行許可
	select {
	case p.sem <- struct{}{}:
		p.wg.Add(1)
		go p.runWorker(item)
	default:
		// 2. 若 worker 已滿，嘗試排入佇列
		select {
		case p.queue <- item:
			// 成功排入佇列
		default:
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

func (p *WorkerPool) runWorker(initialItem taskItem) {
	defer p.wg.Done()
	defer func() { <-p.sem }()

	p.executeItem(initialItem)

	// 接續處理排隊任務，直到佇列為空
	for {
		select {
		case <-p.closed:
			return
		case item := <-p.queue:
			p.executeItem(item)
		default:
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
