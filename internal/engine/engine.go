package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"asura/internal/auth"
	"asura/internal/storage"
)

// RunnerFunc 定義任務底層執行的函式簽名
type RunnerFunc func(ctx context.Context, workspace string, prompt string) (string, error)

// SubmitRequest 代表提交任務的請求參數
type SubmitRequest struct {
	ID        string `json:"id,omitempty"`
	Workspace string `json:"workspace"`
	Prompt    string `json:"prompt"`
}

// Engine 協調任務授權、持久化與後端執行
type Engine struct {
	store     *storage.SQLiteStore
	policy    *auth.Policy
	runner    RunnerFunc
	cancels   map[string]context.CancelFunc
	cancelsMu sync.Mutex
}

// Option 用於自訂 Engine 行為
type Option func(*Engine)

// WithRunner 注入自訂執行器（可用於測試或特定後端串接）
func WithRunner(runner RunnerFunc) Option {
	return func(e *Engine) {
		e.runner = runner
	}
}

// NewEngine 建立新的任務協調引擎
func NewEngine(store *storage.SQLiteStore, policy *auth.Policy, opts ...Option) *Engine {
	e := &Engine{
		store:   store,
		policy:  policy,
		cancels: make(map[string]context.CancelFunc),
	}

	for _, opt := range opts {
		opt(e)
	}

	if e.runner == nil {
		e.runner = defaultRunner
	}

	return e
}

// defaultRunner 預設執行器
func defaultRunner(ctx context.Context, workspace string, prompt string) (string, error) {
	return "", errors.New("no default runner configured")
}

func generateTaskID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("task-%d-%s", time.Now().Unix(), hex.EncodeToString(b))
}

// SubmitTask 驗證授權並調度執行任務
// 遵循 SCN-002：未授權請求在入口直接拒絕，0 資料庫寫入且 0 後端子進程啟動。
// 遵循 SCN-001：授權請求寫入儲存並執行，產出可追蹤的任務識別與完成結果。
func (e *Engine) SubmitTask(ctx context.Context, req SubmitRequest) (*storage.Task, error) {
	// 1. 授權閘門檢查
	if err := e.policy.AuthorizeWorkspace(req.Workspace); err != nil {
		return nil, fmt.Errorf("workspace authorization denied: %w", err)
	}

	// 2. 建立任務初始狀態
	taskID := req.ID
	if taskID == "" {
		taskID = generateTaskID()
	}
	now := time.Now().UTC()
	task := &storage.Task{
		ID:        taskID,
		Prompt:    req.Prompt,
		Workspace: req.Workspace,
		Status:    storage.StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := e.store.CreateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("create task in storage: %w", err)
	}

	// 3. 轉移狀態至 running
	if err := e.store.UpdateTaskStatus(ctx, task.ID, storage.StatusRunning, "", ""); err != nil {
		return nil, fmt.Errorf("update task to running: %w", err)
	}
	task.Status = storage.StatusRunning

	// 4. 配置獨立 Cancel Context 並登記
	taskCtx, cancel := context.WithCancel(ctx)
	e.cancelsMu.Lock()
	e.cancels[task.ID] = cancel
	e.cancelsMu.Unlock()

	defer func() {
		e.cancelsMu.Lock()
		delete(e.cancels, task.ID)
		e.cancelsMu.Unlock()
		cancel()
	}()

	// 5. 調用後端執行
	output, runErr := e.runner(taskCtx, req.Workspace, req.Prompt)

	// 檢查儲存層是否已被外部標記為取消
	latestTask, fetchErr := e.store.GetTask(ctx, task.ID)
	if fetchErr == nil && latestTask.Status == storage.StatusCancelled {
		task.Status = storage.StatusCancelled
		task.Result = output
		task.ErrorMessage = latestTask.ErrorMessage
		return task, context.Canceled
	}

	// 檢查是否已被取消
	if taskCtx.Err() == context.Canceled {
		_ = e.store.UpdateTaskStatus(ctx, task.ID, storage.StatusCancelled, output, "task cancelled by user")
		task.Status = storage.StatusCancelled
		task.Result = output
		task.ErrorMessage = "task cancelled by user"
		return task, taskCtx.Err()
	}

	if runErr != nil {
		_ = e.store.UpdateTaskStatus(ctx, task.ID, storage.StatusFailed, output, runErr.Error())
		task.Status = storage.StatusFailed
		task.Result = output
		task.ErrorMessage = runErr.Error()
		return task, runErr
	}

	// 6. 記錄成功結果
	if err := e.store.UpdateTaskStatus(ctx, task.ID, storage.StatusCompleted, output, ""); err != nil {
		return nil, fmt.Errorf("update task to completed: %w", err)
	}
	task.Status = storage.StatusCompleted
	task.Result = output

	return task, nil
}
