package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"asura/internal/backend/opencode"
	"asura/internal/storage"
)

var (
	// ErrTaskNotRunning 代表欲取消之任務目前並不在執行中
	ErrTaskNotRunning = errors.New("task is not running")
)

// CancelTask 透過 Task ID 取消執行中之任務
// 遵循 INT-005 與同步契約：cancel -> kill/verify process group -> persist terminal state -> return success
// 確認回收成功才宣告完成；若 timeout、identity unknown 或回收失敗，回錯並保留追蹤資訊
func (e *Engine) CancelTask(taskID string) error {
	// 1. 檢查任務是否存在與當前狀態
	task, err := e.store.GetTask(context.Background(), taskID)
	if err != nil {
		return fmt.Errorf("task not found: %w", err)
	}

	e.cancelsMu.Lock()
	cancel, exists := e.cancels[taskID]
	doneCh := e.doneChs[taskID]
	e.cancelsMu.Unlock()

	// 若不在活躍清單中且已經是終態：
	// 僅當 PGID <= 0 時才視為不可取消；若仍有殘留 PGID > 0 則必須允許回收 (修復 I-01 重試取消)
	if !exists && task.PGID <= 0 {
		if task.Status == storage.StatusCompleted || task.Status == storage.StatusFailed {
			return fmt.Errorf("%w: task status is %s", ErrTaskNotRunning, task.Status)
		}
		if task.Status == storage.StatusCancelled {
			return nil
		}
	}

	// 2. 觸發 context 取消
	if exists && cancel != nil {
		cancel()
	}

	// 3. 若有 active runner，等待其完全退出
	if doneCh != nil {
		select {
		case <-doneCh:
		case <-time.After(2 * time.Second):
		}
	}

	// 4. 取得最新 PGID
	targetPGID := task.PGID
	if latest, getErr := e.store.GetTask(context.Background(), taskID); getErr == nil && latest.PGID > 0 {
		targetPGID = latest.PGID
	}

	// 5. 實施三態進程組身分驗證與安全回收 (防禦 J-01, K-01, L-01, M-01)
	if targetPGID > 0 {
		matched, verifyErr := opencode.VerifyTaskProcessGroup(targetPGID, opencode.TaskProcessMetadata{}, taskID)
		if verifyErr != nil {
			// 無法確認身分，嚴禁清空 PGID、嚴禁標記已取消或視為已回收 (防禦 M-01)
			return fmt.Errorf("unable to verify process identity: %w", verifyErr)
		}

		if matched {
			// 確認屬於此任務，發送 SIGKILL 並輪詢確認進程組死亡
			_ = opencode.KillProcessGroup(targetPGID)
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if !opencode.IsProcessGroupAlive(targetPGID) {
					break
				}
				_ = opencode.KillProcessGroup(targetPGID)
				time.Sleep(20 * time.Millisecond)
			}

			if opencode.IsProcessGroupAlive(targetPGID) {
				return fmt.Errorf("process group %d still alive after cancellation timeout", targetPGID)
			}
			_ = e.store.UpdateTaskPGID(context.Background(), taskID, 0)
		} else {
			// 進程組已完全退出或 PID 已被系統重新分配給不相關工作 (防禦 J-01 PID 重用誤殺)
			// 不發信號，清零 PGID
			_ = e.store.UpdateTaskPGID(context.Background(), taskID, 0)
		}
	}

	// 6. 確認回收成功後，最後持久化終態為 cancelled (保留既有輸出)
	existingResult := task.Result
	if latest, getErr := e.store.GetTask(context.Background(), taskID); getErr == nil && latest.Result != "" {
		existingResult = latest.Result
	}
	if updateErr := e.store.UpdateTaskStatus(context.Background(), taskID, storage.StatusCancelled, existingResult, "task cancelled by user"); updateErr != nil {
		return fmt.Errorf("persist cancelled status failed: %w", updateErr)
	}

	return nil
}

