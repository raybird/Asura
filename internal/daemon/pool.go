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

// TaskClass 代表任務類別（人類即時互動 vs 背景定時排程）
type TaskClass string

const (
	TaskClassInteractive TaskClass = "interactive"
	TaskClassCron        TaskClass = "cron"
)

// CapacityConfig 定義容量調度器配置
// 遵循 CRON-002：隨時保留至少 1 個 Worker 給即時互動任務，背景 Cron 佔用不得超過 MaxCronWorkers
type CapacityConfig struct {
	MaxWorkers     int
	MaxCronWorkers int
	MaxQueue       int
}

// WorkerPool 限制全域並行執行任務數量並實作分類容量調度
type WorkerPool struct {
	maxWorkers       int
	maxCronWorkers   int
	maxQueue         int
	running          int
	runningCron      int
	interactiveQueue []taskItem
	cronQueue        []taskItem
	mu               sync.Mutex
	isClosing        bool
	closeOnce        sync.Once
	wg               sync.WaitGroup
}

type taskItem struct {
	ctx    context.Context
	class  TaskClass
	fn     func() error
	onDone func()
	result chan error
}

// NewWorkerPool 初始化指定 Worker 數與佇列長度的 WorkerPool（向後相容）
func NewWorkerPool(maxWorkers int, maxQueue int) *WorkerPool {
	maxCron := 1
	if maxWorkers <= 1 {
		maxCron = 1
	}
	return NewWorkerPoolWithCapacity(CapacityConfig{
		MaxWorkers:     maxWorkers,
		MaxCronWorkers: maxCron,
		MaxQueue:       maxQueue,
	})
}

// NewWorkerPoolWithCapacity 初始化具備分類容量控制的 WorkerPool
// 遵循 CRON-002：保留互動通道與背景容量上限
func NewWorkerPoolWithCapacity(cfg CapacityConfig) *WorkerPool {
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 1
	}
	if cfg.MaxCronWorkers <= 0 {
		cfg.MaxCronWorkers = 1
	}
	if cfg.MaxCronWorkers > cfg.MaxWorkers {
		cfg.MaxCronWorkers = cfg.MaxWorkers
	}
	if cfg.MaxQueue < 0 {
		cfg.MaxQueue = 0
	}

	return &WorkerPool{
		maxWorkers:       cfg.MaxWorkers,
		maxCronWorkers:   cfg.MaxCronWorkers,
		maxQueue:         cfg.MaxQueue,
		interactiveQueue: make([]taskItem, 0, cfg.MaxQueue),
		cronQueue:        make([]taskItem, 0, cfg.MaxQueue),
	}
}

// Submit 提交任務至 WorkerPool（預設為 TaskClassInteractive），若容量已滿立即回傳 ErrCapacityExceeded
func (p *WorkerPool) Submit(ctx context.Context, fn func() error) error {
	resChan, err := p.SubmitAsyncClassified(ctx, TaskClassInteractive, fn)
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-resChan:
		return err
	}
}

// SubmitAsync 異步提交任務至 WorkerPool（預設為 TaskClassInteractive）
func (p *WorkerPool) SubmitAsync(ctx context.Context, fn func() error) (<-chan error, error) {
	return p.SubmitAsyncClassified(ctx, TaskClassInteractive, fn)
}

// SubmitAsyncClassified 異步提交指定類別之任務至 WorkerPool
func (p *WorkerPool) SubmitAsyncClassified(ctx context.Context, class TaskClass, fn func() error) (<-chan error, error) {
	return p.SubmitAsyncClassifiedWithDone(ctx, class, fn, nil)
}

// SubmitAsyncClassifiedWithDone 異步提交指定類別任務至 WorkerPool，並在槽位釋放後立即回呼 onDone（解決 MUST FIX 3）
func (p *WorkerPool) SubmitAsyncClassifiedWithDone(ctx context.Context, class TaskClass, fn func() error, onDone func()) (<-chan error, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if class == "" {
		class = TaskClassInteractive
	}

	item := taskItem{
		ctx:    ctx,
		class:  class,
		fn:     fn,
		onDone: onDone,
		result: make(chan error, 1),
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isClosing {
		return nil, ErrPoolClosed
	}

	queuedCount := len(p.interactiveQueue) + len(p.cronQueue)

	canStartDirectly := false
	if class == TaskClassInteractive && p.running < p.maxWorkers {
		canStartDirectly = true
	} else if class == TaskClassCron && p.running < p.maxWorkers && p.runningCron < p.maxCronWorkers {
		canStartDirectly = true
	}

	if !canStartDirectly && queuedCount >= p.maxQueue {
		return nil, ErrCapacityExceeded
	}

	if class == TaskClassCron {
		p.cronQueue = append(p.cronQueue, item)
	} else {
		p.interactiveQueue = append(p.interactiveQueue, item)
	}

	p.maybeStartWorkersLocked()
	return item.result, nil
}

// SubmitEnqueuedClassifiedWithDone 派發已在專案佇列排隊完畢之任務，不受 maxQueue 二次拋棄（修復 MUST FIX 2）
func (p *WorkerPool) SubmitEnqueuedClassifiedWithDone(ctx context.Context, class TaskClass, fn func() error, onDone func()) (<-chan error, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if class == "" {
		class = TaskClassInteractive
	}

	item := taskItem{
		ctx:    ctx,
		class:  class,
		fn:     fn,
		onDone: onDone,
		result: make(chan error, 1),
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isClosing {
		return nil, ErrPoolClosed
	}

	if class == TaskClassCron {
		p.cronQueue = append(p.cronQueue, item)
	} else {
		p.interactiveQueue = append(p.interactiveQueue, item)
	}

	p.maybeStartWorkersLocked()
	return item.result, nil
}


func (p *WorkerPool) popNextRunnableLocked() (taskItem, bool) {
	// 優先挑選互動任務，隨時保證人類互動體驗
	if len(p.interactiveQueue) > 0 && p.running < p.maxWorkers {
		item := p.interactiveQueue[0]
		p.interactiveQueue = p.interactiveQueue[1:]
		return item, true
	}
	// 檢查 Cron 任務是否符合配額上限
	if len(p.cronQueue) > 0 && p.running < p.maxWorkers && p.runningCron < p.maxCronWorkers {
		item := p.cronQueue[0]
		p.cronQueue = p.cronQueue[1:]
		return item, true
	}
	return taskItem{}, false
}

func (p *WorkerPool) maybeStartWorkersLocked() {
	for {
		item, found := p.popNextRunnableLocked()
		if !found {
			break
		}
		p.running++
		if item.class == TaskClassCron {
			p.runningCron++
		}
		p.wg.Add(1)
		go p.workerLoop(item)
	}
}

func (p *WorkerPool) workerLoop(initialItem taskItem) {
	defer p.wg.Done()
	curItem := initialItem

	for {
		p.executeItem(curItem)

		// 優先釋放本 Worker 佔用之 running 與 runningCron 槽位 (修復 MUST FIX 3)
		p.mu.Lock()
		if curItem.class == TaskClassCron {
			p.runningCron--
		}
		p.running--
		p.mu.Unlock()

		// 槽位已完全空出後再觸發完成回呼（喚醒專案佇列之任務可安全派工）
		if curItem.onDone != nil {
			curItem.onDone()
		}

		p.mu.Lock()
		nextItem, found := p.popNextRunnableLocked()
		if !found {
			p.mu.Unlock()
			return
		}

		p.running++
		if nextItem.class == TaskClassCron {
			p.runningCron++
		}
		p.maybeStartWorkersLocked()
		p.mu.Unlock()

		curItem = nextItem
	}
}

func (p *WorkerPool) executeItem(item taskItem) {
	select {
	case <-item.ctx.Done():
		item.result <- item.ctx.Err()
		return
	default:
	}

	err := item.fn()
	item.result <- err
}

// Close 關閉 WorkerPool 並等待所有正在進行與排隊中的任務完成排水 (Drain)
func (p *WorkerPool) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.isClosing = true
		p.maybeStartWorkersLocked()
		p.mu.Unlock()
	})
	p.wg.Wait()
}
