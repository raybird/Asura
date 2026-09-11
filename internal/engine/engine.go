package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"asura/internal/auth"
	"asura/internal/backend/opencode"
	"asura/internal/storage"
)

var (
	// ErrEmptyOutput 代表執行後端未產出任何有效文字或僅有未解決錯誤（SCN-007, 修復 R-09）
	ErrEmptyOutput = errors.New("execution produced empty output or unresolved error")
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

// hasUnresolvedErrorOrEmpty 判斷執行輸出是否為空或只回傳未解決的結構化錯誤（SCN-007, 修復 R-09）
func hasUnresolvedErrorOrEmpty(output string) (bool, string) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return true, "execution produced empty output or unresolved error"
	}

	lines := strings.Split(trimmed, "\n")
	hasValidContent := false
	hasErrorEvent := false

	for _, line := range lines {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		evt, err := opencode.ParseEventLine(l)
		if err == nil && evt != nil {
			if evt.Type == "error" || evt.IsRateLimit {
				hasErrorEvent = true
			}
			if evt.Text != "" || (evt.Tool != "" && evt.ToolStatus != "error") {
				hasValidContent = true
			}
		} else {
			// 非 JSON 行，視為一般標準輸出內容
			hasValidContent = true
		}
	}

	if !hasValidContent && hasErrorEvent {
		return true, "execution produced empty output or unresolved error"
	}
	return false, ""
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

	// 使用未取消且具有超時的獨立 Context 進行終態持久化，杜絕父 Context 取消時狀態遺失（修復 R-05）
	persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer persistCancel()

	// 檢查儲存層是否已被外部標記為取消（修復 R-06）
	latestTask, fetchErr := e.store.GetTask(persistCtx, task.ID)
	if fetchErr == nil && latestTask.Status == storage.StatusCancelled {
		if output != "" {
			_ = e.store.UpdateTaskResult(persistCtx, task.ID, output)
		}
		task.Status = storage.StatusCancelled
		task.Result = output
		task.ErrorMessage = latestTask.ErrorMessage
		return task, context.Canceled
	}

	// 檢查內部 taskCtx 是否已被取消
	if taskCtx.Err() == context.Canceled {
		_ = e.store.UpdateTaskStatus(persistCtx, task.ID, storage.StatusCancelled, output, "task cancelled by user")
		task.Status = storage.StatusCancelled
		task.Result = output
		task.ErrorMessage = "task cancelled by user"
		return task, taskCtx.Err()
	}

	if runErr != nil {
		_ = e.store.UpdateTaskStatus(persistCtx, task.ID, storage.StatusFailed, output, runErr.Error())
		task.Status = storage.StatusFailed
		task.Result = output
		task.ErrorMessage = runErr.Error()
		return task, runErr
	}

	// 6. 空輸出與未解決結構化錯誤防禦檢查（遵循 SCN-007，修復 R-09）
	if isErr, errMsg := hasUnresolvedErrorOrEmpty(output); isErr {
		_ = e.store.UpdateTaskStatus(persistCtx, task.ID, storage.StatusFailed, output, errMsg)
		task.Status = storage.StatusFailed
		task.Result = output
		task.ErrorMessage = errMsg
		return task, ErrEmptyOutput
	}

	// 7. 條件式記錄成功結果（修復 R-10）：僅在狀態仍為 running 時才標記為 completed，防止覆寫已被外部標記之 cancelled 狀態
	updated, err := e.store.UpdateTaskStatusIfRunning(persistCtx, task.ID, storage.StatusCompleted, output, "")
	if err != nil {
		return nil, fmt.Errorf("update task to completed: %w", err)
	}
	if !updated {
		latest, getErr := e.store.GetTask(persistCtx, task.ID)
		if getErr == nil {
			if latest.Status == storage.StatusCancelled && output != "" {
				_ = e.store.UpdateTaskResult(persistCtx, task.ID, output)
			}
			task.Status = latest.Status
			task.Result = output
			task.ErrorMessage = latest.ErrorMessage
			if latest.Status == storage.StatusCancelled {
				return task, context.Canceled
			}
			return task, nil
		}
	}
	task.Status = storage.StatusCompleted
	task.Result = output

	return task, nil
}

// RecoverInterruptedTasks 委派儲存層執行中斷任務的狀態恢復（遵循 SCN-006）
func (e *Engine) RecoverInterruptedTasks(ctx context.Context) (int64, error) {
	return e.store.RecoverInterruptedTasks(ctx)
}
