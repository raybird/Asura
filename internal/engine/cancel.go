package engine

import (
	"context"
	"errors"
	"fmt"

	"asura/internal/storage"
)

var (
	// ErrTaskNotRunning 代表欲取消之任務目前並不在執行中
	ErrTaskNotRunning = errors.New("task is not running")
)

// CancelTask 透過 Task ID 取消執行中之任務
// 遵循 SCN-004：在核准之取消期限內停止任務，回收資源，且不影響其他工作
func (e *Engine) CancelTask(taskID string) error {
	e.cancelsMu.Lock()
	cancel, exists := e.cancels[taskID]
	e.cancelsMu.Unlock()

	if exists && cancel != nil {
		cancel()
		// 更新儲存層狀態為 cancelled
		_ = e.store.UpdateTaskStatus(context.Background(), taskID, storage.StatusCancelled, "", "task cancelled by user")
		return nil
	}

	// 若不在活躍取消清單中，檢查儲存層
	task, err := e.store.GetTask(context.Background(), taskID)
	if err != nil {
		return fmt.Errorf("task not found: %w", err)
	}

	// 若任務已被標記為 cancelled，允許冪等取消以支援握手失敗後的重試與資源回收 (修復 I-01)
	if task.Status == storage.StatusCancelled {
		return nil
	}

	if task.Status != storage.StatusRunning && task.Status != storage.StatusPending {
		return fmt.Errorf("%w: task status is %s", ErrTaskNotRunning, task.Status)
	}

	return e.store.UpdateTaskStatus(context.Background(), taskID, storage.StatusCancelled, "", "task cancelled by user")
}
