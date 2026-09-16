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

	// 若任務在 DB 中已是終態且無殘留進程組 (PGID <= 0)，即使 registration 尚未 defer 清除，亦嚴禁覆寫已提交之終態
	if task.PGID <= 0 {
		if task.Status == storage.StatusCompleted || task.Status == storage.StatusFailed {
			return fmt.Errorf("%w: task status is %s", ErrTaskNotRunning, task.Status)
		}
		if task.Status == storage.StatusCancelled {
			return nil
		}
		if !exists && (task.Status == storage.StatusPending || task.Status == storage.StatusQueued) {
			// 未啟動且無活躍 runner、無殘留進程組之排隊任務，直接取消
			if updateErr := e.store.UpdateTaskStatus(context.Background(), taskID, storage.StatusCancelled, task.Result, "task cancelled by user"); updateErr != nil {
				return fmt.Errorf("persist cancelled status failed: %w", updateErr)
			}
			return nil
		}
	}

	// 2. 狀態轉換：利用 UpdateTaskStatusIfRunning 進行原子條件式 CAS，杜絕 terminal-state race
	// running ─ CAS ─┬─> completed / failed (SubmitTask 贏)
	//                └─> cancelling (CancelTask 贏)
	if task.Status == storage.StatusRunning {
		updated, casErr := e.store.UpdateTaskStatusIfRunning(
			context.Background(),
			taskID,
			storage.StatusCancelling,
			task.Result,
			"task cancellation requested",
		)
		if casErr != nil {
			return fmt.Errorf("persist cancelling status: %w", casErr)
		}
		if !updated {
			// CAS 失敗：表示 SubmitTask 已搶先提交終態，回讀最新狀態
			latest, getErr := e.store.GetTask(context.Background(), taskID)
			if getErr != nil {
				return fmt.Errorf("task not found: %w", getErr)
			}
			switch latest.Status {
			case storage.StatusCancelling:
				// 已由並發取消或其他協調者設為 cancelling，繼續向下執行
			case storage.StatusCancelled:
				return nil
			case storage.StatusCompleted, storage.StatusFailed:
				if latest.PGID <= 0 {
					return fmt.Errorf("%w: task status is %s", ErrTaskNotRunning, latest.Status)
				}
			default:
				if latest.PGID <= 0 {
					return fmt.Errorf("%w: task status is %s", ErrTaskNotRunning, latest.Status)
				}
			}
		}
	} else if task.Status != storage.StatusCancelling && task.Status != storage.StatusCancelled {
		if task.Status == storage.StatusCompleted || task.Status == storage.StatusFailed {
			if task.PGID <= 0 {
				return fmt.Errorf("%w: task status is %s", ErrTaskNotRunning, task.Status)
			}
		} else {
			// 針對 pending/queued 但已有 active runner 或殘留 PGID，轉移為 cancelling
			if err := e.store.UpdateTaskStatus(context.Background(), taskID, storage.StatusCancelling, task.Result, "task cancellation requested"); err != nil {
				return fmt.Errorf("persist cancelling status: %w", err)
			}
		}
	}

	// 3. 觸發 context 取消 (cancelling 成功持久化後才發出停止意圖)
	if exists && cancel != nil {
		cancel()
	}

	// 4. 若有 active runner，等待其完全退出
	if doneCh != nil {
		select {
		case <-doneCh:
		case <-time.After(2 * time.Second):
		}
	}

	// 5. 取得最新 PGID
	targetPGID := task.PGID
	if latest, getErr := e.store.GetTask(context.Background(), taskID); getErr == nil && latest.PGID > 0 {
		targetPGID = latest.PGID
	}

	// 6. 實施三態進程組身分驗證與安全回收 (防禦 J-01, K-01, L-01, M-01)
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

	// 7. 確認回收成功後，最後持久化終態為 cancelled (保留既有輸出)
	existingResult := task.Result
	if latest, getErr := e.store.GetTask(context.Background(), taskID); getErr == nil && latest.Result != "" {
		existingResult = latest.Result
	}
	if updateErr := e.store.UpdateTaskStatus(context.Background(), taskID, storage.StatusCancelled, existingResult, "task cancelled by user"); updateErr != nil {
		return fmt.Errorf("persist cancelled status failed: %w", updateErr)
	}

	return nil
}

