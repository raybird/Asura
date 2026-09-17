package cron

import (
	"context"
	"fmt"
	"sync"
	"time"

	"asura/internal/auth"
	"asura/internal/storage"
)

// TaskDispatcher 定義將排程觸發任務提交至執行中樞之回呼介面（由 Daemon 接入專案鎖與容量管制）
type TaskDispatcher func(ctx context.Context, cronID, workspace, prompt string) (string, error)

// Scheduler 管理定時排程生命週期、時鐘輪詢、重疊略過與授權防護
type Scheduler struct {
	store       *storage.SQLiteStore
	policy      *auth.Policy
	dispatcher  TaskDispatcher
	mu          sync.Mutex
	runningRuns map[string]string // cronID -> active taskID
}

// NewScheduler 建立新的排程調度器實例
func NewScheduler(store *storage.SQLiteStore, policy *auth.Policy, dispatcher TaskDispatcher) *Scheduler {
	return &Scheduler{
		store:       store,
		policy:      policy,
		dispatcher:  dispatcher,
		runningRuns: make(map[string]string),
	}
}

// AlignSchedules 在服務啟動或重啟時對齊排程，過期者重算未來時刻，絕不補跑歷史
// 遵循 CRON-004: 漏跑不補 (No Catchup)
func (s *Scheduler) AlignSchedules(ctx context.Context, now time.Time) error {
	schedules, err := s.store.ListCronSchedules(ctx)
	if err != nil {
		return fmt.Errorf("list schedules during alignment: %w", err)
	}

	for _, sched := range schedules {
		if sched.Status != storage.CronStatusActive {
			continue
		}

		// 若未指定下次執行時間或已過期，重算下次時間自 now 起算
		if sched.NextRunAt == nil || sched.NextRunAt.Before(now) {
			parsed, err := Parse(sched.Schedule)
			if err != nil {
				continue
			}
			next := parsed.Next(now)
			if next.IsZero() {
				_ = s.store.UpdateCronScheduleStatus(ctx, sched.ID, storage.CronStatusPaused, "unreachable schedule date")
				continue
			}
			if err := s.store.UpdateCronScheduleNextRun(ctx, sched.ID, next, sched.LastRunAt); err != nil {
				return fmt.Errorf("update schedule %s next run at: %w", sched.ID, err)
			}
		}
	}

	return nil
}

// Trigger 依排程 ID 觸發一次執行
// 遵循 CRON-004: 重疊略過 (Skip)
// 遵循 CRON-005: 未授權目錄自動拒絕並設為 paused
func (s *Scheduler) Trigger(ctx context.Context, cronID string, now time.Time) error {
	sched, err := s.store.GetCronSchedule(ctx, cronID)
	if err != nil {
		return fmt.Errorf("get schedule: %w", err)
	}

	runID := fmt.Sprintf("run-%s-%d", cronID, now.Unix())

	// 1. 授權邊界檢查 (CRON-005)
	if s.policy != nil {
		if err := s.policy.AuthorizeWorkspace(sched.Workspace); err != nil {
			// 記錄 unauthorized 失敗歷史
			unauthRun := storage.CronRun{
				ID:            runID,
				CronID:        cronID,
				ScheduledTime: now,
				TriggeredTime: now,
				Status:        storage.CronRunUnauthorized,
				Reason:        err.Error(),
				CreatedAt:     now,
			}
			_ = s.store.RecordCronRun(ctx, unauthRun)

			// 自動將排程標記為 paused (CRON-005)
			_ = s.store.UpdateCronScheduleStatus(ctx, cronID, storage.CronStatusPaused, "unauthorized workspace: "+err.Error())

			return fmt.Errorf("unauthorized workspace: %w", err)
		}
	}

	// 2. 檢查重疊執行 (CRON-004)
	s.mu.Lock()
	activeTaskID, isRunning := s.runningRuns[cronID]
	s.mu.Unlock()

	if isRunning && activeTaskID != "" {
		// 檢查前次任務在儲存層之最新狀態
		task, err := s.store.GetTask(ctx, activeTaskID)
		if err == nil && task != nil {
			switch task.Status {
			case storage.StatusPending,
				storage.StatusQueued,
				storage.StatusRunning,
				storage.StatusCancelling:
				// 前次任務尚未結束或仍在取消中，依 CRON-004 直接略過 (Skip)
				skippedRun := storage.CronRun{
					ID:            runID,
					CronID:        cronID,
					ScheduledTime: now,
					TriggeredTime: now,
					TaskID:        activeTaskID,
					Status:        storage.CronRunSkipped,
					Reason:        "previous task still active",
					CreatedAt:     now,
				}
				_ = s.store.RecordCronRun(ctx, skippedRun)

				// 重算下次執行時間
				if parsed, err := Parse(sched.Schedule); err == nil {
					next := parsed.Next(now)
					_ = s.store.UpdateCronScheduleNextRun(ctx, cronID, next, sched.LastRunAt)
				}
				return nil
			}
		}
	}

	// 計算下次執行時間（遵循至多一次語意，無論派工是否成功均推進）
	var nextRun time.Time
	if parsed, err := Parse(sched.Schedule); err == nil {
		nextRun = parsed.Next(now)
	}

	// 3. 正常觸發執行：透過 dispatcher 提交任務（受專案鎖與容量調度管制）
	taskID, dispatchErr := s.dispatcher(ctx, cronID, sched.Workspace, sched.Prompt)
	if dispatchErr != nil {
		// 派工失敗（例如容量滿等），記錄失敗並推進 NextRunAt，杜絕每秒重試雪崩 (MUST FIX 6)
		failedRun := storage.CronRun{
			ID:            runID,
			CronID:        cronID,
			ScheduledTime: now,
			TriggeredTime: now,
			Status:        storage.CronRunFailed,
			Reason:        dispatchErr.Error(),
			CreatedAt:     now,
		}
		_ = s.store.RecordCronRun(ctx, failedRun)

		if nextRun.IsZero() {
			_ = s.store.UpdateCronScheduleStatus(ctx, cronID, storage.CronStatusPaused, "unreachable schedule date")
		} else {
			_ = s.store.UpdateCronScheduleNextRun(ctx, cronID, nextRun, &now)
		}
		return fmt.Errorf("dispatch cron task: %w", dispatchErr)
	}

	s.mu.Lock()
	s.runningRuns[cronID] = taskID
	s.mu.Unlock()

	triggeredRun := storage.CronRun{
		ID:            runID,
		CronID:        cronID,
		ScheduledTime: now,
		TriggeredTime: now,
		TaskID:        taskID,
		Status:        storage.CronRunTriggered,
		CreatedAt:     now,
	}
	if err := s.store.RecordCronRun(ctx, triggeredRun); err != nil {
		// 即使記錄失敗亦保留 runningRuns 以維護重疊防護 (SHOULD FIX 8)
		if !nextRun.IsZero() {
			_ = s.store.UpdateCronScheduleNextRun(ctx, cronID, nextRun, &now)
		}
		return fmt.Errorf("record triggered run: %w", err)
	}

	// 更新下次執行時間（記錄 last_run 為 now）
	if nextRun.IsZero() {
		_ = s.store.UpdateCronScheduleStatus(ctx, cronID, storage.CronStatusPaused, "unreachable schedule date")
	} else {
		_ = s.store.UpdateCronScheduleNextRun(ctx, cronID, nextRun, &now)
	}

	return nil
}
