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

// GenerateTaskID 生成唯一任務識別碼 (INT-004)
func GenerateTaskID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("task-%d-%s", time.Now().Unix(), hex.EncodeToString(b))
}

type taskIDKey struct{}

// ContextWithTaskID 將 taskID 注入 context
func ContextWithTaskID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, taskIDKey{}, id)
}

// TaskIDFromContext 自 context 萃取 taskID
func TaskIDFromContext(ctx context.Context) string {
	if val, ok := ctx.Value(taskIDKey{}).(string); ok {
		return val
	}
	return ""
}

func generateTaskID() string {
	return GenerateTaskID()
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
			if evt.Type == "error" || evt.IsRateLimit || evt.ToolStatus == "error" {
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

	alreadyCreated := false
	if req.ID != "" {
		existing, err := e.store.GetTask(ctx, req.ID)
		if err == nil {
			if existing.Status == storage.StatusCancelled {
				return existing, context.Canceled
			}
			if existing.Status != storage.StatusPending {
				return existing, nil
			}
			task = existing
			alreadyCreated = true
		}
	}

	if !alreadyCreated {
		if err := e.store.CreateTask(ctx, task); err != nil {
			return nil, fmt.Errorf("create task in storage: %w", err)
		}
	}

	// 3. 條件式轉移狀態至 running (修復 F-02)
	updated, err := e.store.UpdateTaskStatusIfPending(ctx, task.ID, storage.StatusRunning)
	if err != nil {
		return nil, fmt.Errorf("update task to running: %w", err)
	}
	if !updated {
		latest, getErr := e.store.GetTask(ctx, task.ID)
		if getErr == nil && latest.Status == storage.StatusCancelled {
			task.Status = storage.StatusCancelled
			task.ErrorMessage = latest.ErrorMessage
			return task, context.Canceled
		}
		return nil, fmt.Errorf("task %s status changed unexpectedly before running", task.ID)
	}
	task.Status = storage.StatusRunning

	// 4. 配置獨立 Cancel Context 並登記
	taskCtx, cancel := context.WithCancel(ContextWithTaskID(ctx, task.ID))
	e.cancelsMu.Lock()
	e.cancels[task.ID] = cancel
	e.cancelsMu.Unlock()

	defer func() {
		e.cancelsMu.Lock()
		delete(e.cancels, task.ID)
		e.cancelsMu.Unlock()
		cancel()
	}()

	// 進入 runner 前查核儲存層與 context 最新狀態，若已被外部取消則直接退出，絕不呼叫 runner (修復 F-02)
	latestCheck, checkErr := e.store.GetTask(ctx, task.ID)
	if checkErr == nil && latestCheck.Status == storage.StatusCancelled {
		task.Status = storage.StatusCancelled
		task.ErrorMessage = latestCheck.ErrorMessage
		return task, context.Canceled
	}
	if taskCtx.Err() == context.Canceled {
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer persistCancel()

		task.Status = storage.StatusCancelled
		task.ErrorMessage = "task cancelled by user"
		if updateErr := e.store.UpdateTaskStatus(persistCtx, task.ID, storage.StatusCancelled, "", "task cancelled by user"); updateErr != nil {
			return task, fmt.Errorf("task cancelled before runner: persist status failed: %w", updateErr)
		}
		return task, taskCtx.Err()
	}

	// 5. 調用後端執行
	output, runErr := e.runner(taskCtx, req.Workspace, req.Prompt)

	// 使用未取消且具有超時的獨立 Context 進行終態持久化，杜絕父 Context 取消時狀態遺失（修復 R-05）
	persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer persistCancel()

	// 檢查儲存層是否已被外部標記為取消（修復 R-06）
	latestTask, fetchErr := e.store.GetTask(persistCtx, task.ID)
	if fetchErr == nil && latestTask.Status == storage.StatusCancelled {
		if output != "" {
			if updateErr := e.store.UpdateTaskResult(persistCtx, task.ID, output); updateErr != nil {
				return task, fmt.Errorf("task cancelled: persist output failed: %w", updateErr)
			}
		}
		task.Status = storage.StatusCancelled
		task.Result = output
		task.ErrorMessage = latestTask.ErrorMessage
		return task, context.Canceled
	}

	// 檢查內部 taskCtx 是否已被取消
	if taskCtx.Err() == context.Canceled {
		task.Status = storage.StatusCancelled
		task.Result = output
		task.ErrorMessage = "task cancelled by user"
		if updateErr := e.store.UpdateTaskStatus(persistCtx, task.ID, storage.StatusCancelled, output, "task cancelled by user"); updateErr != nil {
			return task, fmt.Errorf("task cancelled: persist status failed: %w", updateErr)
		}
		return task, taskCtx.Err()
	}

	if runErr != nil {
		task.Status = storage.StatusFailed
		task.Result = output
		task.ErrorMessage = runErr.Error()
		if persistErr := e.store.UpdateTaskStatus(persistCtx, task.ID, storage.StatusFailed, output, runErr.Error()); persistErr != nil {
			return task, fmt.Errorf("%w (persist task status failed: %v)", runErr, persistErr)
		}
		return task, runErr
	}

	// 6. 空輸出與未解決結構化錯誤防禦檢查（遵循 SCN-007，修復 R-09 與 F-04）
	if isErr, errMsg := hasUnresolvedErrorOrEmpty(output); isErr {
		task.Status = storage.StatusFailed
		task.Result = output
		task.ErrorMessage = errMsg
		if persistErr := e.store.UpdateTaskStatus(persistCtx, task.ID, storage.StatusFailed, output, errMsg); persistErr != nil {
			return task, fmt.Errorf("%w (persist task status failed: %v)", ErrEmptyOutput, persistErr)
		}
		return task, ErrEmptyOutput
	}

	// 7. 條件式記錄成功結果（修復 R-10）：僅在狀態仍為 running 時才標記為 completed，防止覆寫已被外部標記之 cancelled 狀態
	updated, err = e.store.UpdateTaskStatusIfRunning(persistCtx, task.ID, storage.StatusCompleted, output, "")
	if err != nil {
		return nil, fmt.Errorf("update task to completed: %w", err)
	}
	if !updated {
		latest, getErr := e.store.GetTask(persistCtx, task.ID)
		if getErr == nil {
			if latest.Status == storage.StatusCancelled && output != "" {
				if updateErr := e.store.UpdateTaskResult(persistCtx, task.ID, output); updateErr != nil {
					return task, fmt.Errorf("task cancelled: persist output failed: %w", updateErr)
				}
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
