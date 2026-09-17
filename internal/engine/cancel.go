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
	var (
		cancel context.CancelFunc
		doneCh chan struct{}
		exists bool
	)

	// 1. CAS 狀態轉換迴圈：以 SQLite 為唯一 Linearization Point，杜絕 stale snapshot 覆寫競態
	for {
		current, err := e.store.GetTask(context.Background(), taskID)
		if err != nil {
			return fmt.Errorf("task not found: %w", err)
		}

		e.cancelsMu.Lock()
		cancel, exists = e.cancels[taskID]
		doneCh = e.doneChs[taskID]
		e.cancelsMu.Unlock()

		switch current.Status {
		case storage.StatusCompleted, storage.StatusFailed:
			if current.PGID <= 0 {
				return fmt.Errorf("%w: task status is %s", ErrTaskNotRunning, current.Status)
			}
			// 殘留 PGID 清理路徑 (修復 I-01 重試取消殘留進程)
			goto reap

		case storage.StatusCancelled:
			if current.PGID <= 0 {
				return nil
			}
			goto reap

		case storage.StatusCancelling:
			goto cancelAndReap

		case storage.StatusRunning:
			updated, casErr := e.store.UpdateTaskStatusIfRunning(
				context.Background(),
				taskID,
				storage.StatusCancelling,
				current.Result,
				"task cancellation requested",
			)
			if casErr != nil {
				return fmt.Errorf("persist cancelling status: %w", casErr)
			}
			if !updated {
				// CAS 失敗，狀態已被並發修改，重試迴圈以最新 DB 狀態做決策
				continue
			}
			goto cancelAndReap

		case storage.StatusPending, storage.StatusQueued:
			if !exists && current.PGID <= 0 {
				// 未啟動且無活躍 runner、無殘留進程組之排隊任務，直接條件式取消
				updated, casErr := e.store.UpdateTaskStatusIfPendingOrQueuedWithMsg(
					context.Background(),
					taskID,
					storage.StatusCancelled,
					current.Result,
					"task cancelled by user",
				)
				if casErr != nil {
					return fmt.Errorf("persist cancelled status failed: %w", casErr)
				}
				if !updated {
					// CAS 失敗，SubmitTask 搶先啟動或狀態改變，重試迴圈
					continue
				}
				return nil
			}

			// 針對 pending/queued 但已有 active runner 或殘留 PGID，條件式轉移為 cancelling
			updated, casErr := e.store.UpdateTaskStatusIfPendingOrQueuedWithMsg(
				context.Background(),
				taskID,
				storage.StatusCancelling,
				current.Result,
				"task cancellation requested",
			)
			if casErr != nil {
				return fmt.Errorf("persist cancelling status: %w", casErr)
			}
			if !updated {
				continue
			}
			goto cancelAndReap

		default:
			if current.PGID <= 0 {
				return fmt.Errorf("%w: task status is %s", ErrTaskNotRunning, current.Status)
			}
			goto reap
		}
	}

cancelAndReap:
	// 2. 觸發 context 取消 (cancelling 成功持久化後才發出停止意圖)
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

reap:
	// 4. 取得最新 PGID (fail-closed，嚴禁吞掉查詢錯誤)
	latest, getErr := e.store.GetTask(context.Background(), taskID)
	if getErr != nil {
		return fmt.Errorf("verify task state before process verification: %w", getErr)
	}
	targetPGID := latest.PGID

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
			if err := e.store.UpdateTaskPGID(context.Background(), taskID, 0); err != nil {
				return fmt.Errorf("persist cleared process group: %w", err)
			}
		} else {
			// 進程組已完全退出或 PID 已被系統重新分配給不相關工作 (防禦 J-01 PID 重用誤殺)
			// 不發信號，清零 PGID
			if err := e.store.UpdateTaskPGID(context.Background(), taskID, 0); err != nil {
				return fmt.Errorf("persist cleared process group: %w", err)
			}
		}
	}

	// 6. 確認回收成功後，最後確認狀態並持久化終態為 cancelled (fail-closed，嚴禁吞掉錯誤)
	latest, getErr = e.store.GetTask(context.Background(), taskID)
	if getErr != nil {
		return fmt.Errorf("verify task state before finalizing cancellation: %w", getErr)
	}

	switch latest.Status {
	case storage.StatusCancelling:
		existingResult := latest.Result
		updated, updateErr := e.store.UpdateTaskStatusIfCancelling(
			context.Background(),
			taskID,
			storage.StatusCancelled,
			existingResult,
			"task cancelled by user",
		)
		if updateErr != nil {
			return fmt.Errorf("persist cancelled status failed: %w", updateErr)
		}
		if !updated {
			// CAS 失敗，重新讀取以確認獲勝狀態
			reread, rereadErr := e.store.GetTask(context.Background(), taskID)
			if rereadErr != nil {
				return fmt.Errorf("verify task state after failed cancellation CAS: %w", rereadErr)
			}
			switch reread.Status {
			case storage.StatusCancelled:
				return nil
			case storage.StatusCompleted, storage.StatusFailed:
				if reread.PGID <= 0 {
					return fmt.Errorf("%w: task status is %s", ErrTaskNotRunning, reread.Status)
				}
				return fmt.Errorf("task reached terminal state %s with residual pgid %d", reread.Status, reread.PGID)
			default:
				return fmt.Errorf("unexpected task status %s after cancellation CAS", reread.Status)
			}
		}
		return nil

	case storage.StatusCancelled:
		return nil

	case storage.StatusCompleted, storage.StatusFailed:
		if latest.PGID <= 0 {
			return fmt.Errorf("%w: task status is %s", ErrTaskNotRunning, latest.Status)
		}
		return fmt.Errorf("task reached terminal state %s with residual pgid %d", latest.Status, latest.PGID)

	default:
		return fmt.Errorf("unexpected task status %s before finalizing cancellation", latest.Status)
	}
}

