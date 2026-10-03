package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"asura/internal/auth"
	"asura/internal/backend"
	"asura/internal/recall"
	"asura/internal/storage"
)

var (
	// ErrEmptyOutput 代表執行後端未產出任何有效文字或僅有未解決錯誤（SCN-007, 修復 R-09）
	ErrEmptyOutput = errors.New("execution produced empty output or unresolved error")
)

// RunnerFunc 定義任務底層執行的函式簽名
type RunnerFunc func(ctx context.Context, workspace string, prompt string) (string, error)

// ProjectMemorySnapshot 代表密封進 Context 的單筆記憶快照
type ProjectMemorySnapshot struct {
	storage.LifecycleMetadata
	ID        string    `json:"id"`
	Workspace string    `json:"workspace"`
	Content   string    `json:"content"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

// 2026-10-01：舊 sealed JSON 只在讀取時補預設，不重寫持久化內容。
func (m *ProjectMemorySnapshot) UnmarshalJSON(data []byte) error {
	type snapshot ProjectMemorySnapshot
	var decoded snapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Lifecycle == "" {
		decoded.Lifecycle = "active"
	}
	*m = ProjectMemorySnapshot(decoded)
	return nil
}

// ExecutionContext 代表任務執行時之完整密封上下文
type ExecutionContext struct {
	BoundBackend      backend.Backend            `json:"-"`
	ExecutionContract *backend.ExecutionContract `json:"execution_contract,omitempty"`
	Recall            *recall.Decision           `json:"recall,omitempty"`
	OriginalPrompt    string                     `json:"original_prompt"`
	Workspace         string                     `json:"workspace"`
	Backend           string                     `json:"backend,omitempty"`
	ProjectMemories   []ProjectMemorySnapshot    `json:"project_memories"`
	MemoryRefs        []string                   `json:"memory_refs"`
	CreatedAt         time.Time                  `json:"created_at"`
}

// 2026-10-04：stage 契約禁止缺少綁定 handle 時回到其他 runtime。
func (c *ExecutionContext) ValidateStageBackend(role backend.Role) error {
	if err := c.ExecutionContract.Validate(c.ExecutionContract.Mode, c.Backend); err != nil {
		return err
	}
	binding, err := c.ExecutionContract.StageBindingPlan.Binding(role)
	if err != nil {
		return err
	}
	if c.BoundBackend == nil || c.BoundBackend.ID() != binding.SelectionDecision.Selected.ID {
		return fmt.Errorf("%w: stage bound runtime mismatch", backend.ErrInvalidContract)
	}
	match, err := backend.MatchCapabilities([]backend.ExecutionRequirements{binding.Requirements}, backend.RuntimeDescriptor{ID: c.BoundBackend.ID(), Capabilities: c.BoundBackend.Capabilities()})
	if err != nil {
		return err
	}
	if !match.Compatible {
		return &backend.CapabilityAdmissionError{RequestedBackend: c.BoundBackend.ID(), Match: match}
	}
	return nil
}

// PipelineRunner 定義任務執行管線的抽象介面，避免 engine 與 pipeline package 產生循環依賴
type PipelineRunner interface {
	ExecutePipeline(ctx context.Context, mode string, execCtx *ExecutionContext) (output string, traceJSON string, err error)
}

// 2026-10-03：正式 runtime pipeline 提供接受前契約；runner-only seam 沿用既有行為。
func (e *Engine) Preflight(ctx context.Context, mode, requested string) (*backend.ExecutionContract, error) {
	if admission, ok := e.pipelineRunner.(interface {
		Preflight(context.Context, string, string) (*backend.ExecutionContract, error)
	}); ok {
		return admission.Preflight(ctx, mode, requested)
	}
	if requested == "auto" {
		return nil, fmt.Errorf("%w: auto requires runtime registry", backend.ErrInvalidContract)
	}
	return nil, nil
}

// SubmitRequest 代表提交任務的請求參數
type SubmitRequest struct {
	MemoryRecall    string            `json:"memory_recall,omitempty"`
	ID              string            `json:"id,omitempty"`
	Workspace       string            `json:"workspace"`
	Prompt          string            `json:"prompt"`
	Backend         string            `json:"backend,omitempty"`
	Mode            string            `json:"mode,omitempty"`
	MemoryIDs       []string          `json:"memory_ids,omitempty"`
	ResolvedContext *ExecutionContext `json:"resolved_context,omitempty"`
}

// ComposePrompt 依據 ExecutionContext 將專案記憶與原始 Prompt 進行標準組裝 (CTXMEM-001, CTXMEM-007)
func ComposePrompt(prompt string, execCtx *ExecutionContext) string {
	if execCtx == nil || len(execCtx.ProjectMemories) == 0 {
		return prompt
	}

	var sb strings.Builder
	sb.WriteString("### Project Context\n")
	for _, mem := range execCtx.ProjectMemories {
		sb.WriteString(fmt.Sprintf("- [%s]: %s\n", mem.ID, mem.Content))
	}
	sb.WriteString("\n")
	sb.WriteString(prompt)
	return sb.String()
}

// Engine 協調任務授權、持久化與後端執行
type Engine struct {
	store          *storage.SQLiteStore
	policy         *auth.Policy
	runner         RunnerFunc
	pipelineRunner PipelineRunner
	cancels        map[string]context.CancelFunc
	doneChs        map[string]chan struct{}
	cancelsMu      sync.Mutex
}

// Option 用於自訂 Engine 行為
type Option func(*Engine)

// WithRunner 注入自訂執行器（可用於測試或特定後端串接）
func WithRunner(runner RunnerFunc) Option {
	return func(e *Engine) {
		e.runner = runner
	}
}

// WithPipelineRunner 注入自訂管線執行器
func WithPipelineRunner(pr PipelineRunner) Option {
	return func(e *Engine) {
		e.pipelineRunner = pr
	}
}

// NewEngine 建立新的任務協調引擎
func NewEngine(store *storage.SQLiteStore, policy *auth.Policy, opts ...Option) *Engine {
	e := &Engine{
		store:   store,
		policy:  policy,
		cancels: make(map[string]context.CancelFunc),
		doneChs: make(map[string]chan struct{}),
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
		var raw struct {
			Type  string `json:"type"`
			Error *struct {
				Message    string `json:"message"`
				StatusCode int    `json:"statusCode"`
			} `json:"error"`
			Part *struct {
				Text  string `json:"text"`
				Tool  string `json:"tool"`
				State *struct {
					Status string `json:"status"`
				} `json:"state"`
			} `json:"part"`
		}
		if err := json.Unmarshal([]byte(l), &raw); err == nil {
			isRateLimit := false
			if raw.Error != nil {
				if raw.Error.StatusCode == 429 || strings.Contains(strings.ToLower(raw.Error.Message), "rate limit") {
					isRateLimit = true
				}
			}
			toolStatus := ""
			text := ""
			tool := ""
			if raw.Part != nil {
				text = raw.Part.Text
				tool = raw.Part.Tool
				if raw.Part.State != nil {
					toolStatus = raw.Part.State.Status
				}
			}
			if raw.Type == "error" || isRateLimit || toolStatus == "error" {
				hasErrorEvent = true
			}
			if text != "" || (tool != "" && toolStatus != "error") {
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
	backendID := req.Backend
	if backendID == "" {
		backendID = "opencode"
	}
	now := time.Now().UTC()
	mode := req.Mode
	if mode == "" {
		mode = "direct"
	}
	if mode != "direct" && mode != "three-face" {
		return nil, fmt.Errorf("invalid execution mode: %q", mode)
	}
	task := &storage.Task{
		ID:            taskID,
		Prompt:        req.Prompt,
		Workspace:     req.Workspace,
		Status:        storage.StatusPending,
		Backend:       backendID,
		ExecutionMode: mode,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if req.ResolvedContext != nil {
		if req.ResolvedContext.Backend == "" {
			req.ResolvedContext.Backend = backendID
		}
		rawCtx, err := json.Marshal(req.ResolvedContext)
		if err != nil {
			return nil, fmt.Errorf("marshal resolved context: %w", err)
		}
		task.ResolvedContext = string(rawCtx)
		if len(req.ResolvedContext.MemoryRefs) > 0 {
			task.MemoryRefs = req.ResolvedContext.MemoryRefs
		} else if len(req.MemoryIDs) > 0 {
			task.MemoryRefs = req.MemoryIDs
		}
	} else if len(req.MemoryIDs) > 0 {
		task.MemoryRefs = req.MemoryIDs
	}

	defer func() {
		utilityCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		_ = e.store.CompleteTaskUtility(utilityCtx, task.ID)
	}()
	alreadyCreated := false
	if req.ID != "" {
		existing, err := e.store.GetTask(ctx, req.ID)
		if err == nil {
			existingIntent := existing.Backend
			var storedContext ExecutionContext
			if existing.ResolvedContext != "" && json.Unmarshal([]byte(existing.ResolvedContext), &storedContext) == nil && storedContext.ExecutionContract != nil {
				existingIntent = storedContext.ExecutionContract.RequestedBackend
				if existingIntent == "" {
					existingIntent = "opencode"
				}
			}
			if existingIntent != "" && existingIntent != backendID {
				return nil, fmt.Errorf("request ID %q reused with different backend (existing: %q, requested: %q)", req.ID, existing.Backend, backendID)
			}
			if existing.Status == storage.StatusCancelled {
				return existing, context.Canceled
			}
			if existing.Status != storage.StatusPending && existing.Status != storage.StatusQueued {
				return existing, nil
			}
			backendID = existing.Backend
			task = existing
			alreadyCreated = true
		}
	}

	if !alreadyCreated {
		contract, err := e.Preflight(ctx, mode, req.Backend)
		if err != nil {
			return nil, fmt.Errorf("capability admission: %w", err)
		}
		if contract != nil {
			backendID = contract.Runtime.ID
			task.Backend = backendID
			sealed := &ExecutionContext{OriginalPrompt: req.Prompt, Workspace: req.Workspace, Backend: backendID, CreatedAt: now}
			if req.ResolvedContext != nil {
				*sealed = *req.ResolvedContext
			}
			sealed.Backend = backendID
			sealed.ExecutionContract = contract
			raw, err := json.Marshal(sealed)
			if err != nil {
				return nil, fmt.Errorf("seal execution contract: %w", err)
			}
			task.ResolvedContext = string(raw)
		}
		if err := e.store.CreateTask(ctx, task); err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				task.Status = storage.StatusCancelled
				task.ErrorMessage = "task cancelled by user"
				_ = e.store.CreateTask(context.Background(), task)
				_ = e.store.UpdateTaskStatus(context.Background(), task.ID, storage.StatusCancelled, "", "task cancelled by user")
				return task, ctx.Err()
			}
			return nil, fmt.Errorf("create task in storage: %w", err)
		}
	}

	// 3. 條件式轉移狀態至 running (支援 pending 與 queued)
	updated, err := e.store.UpdateTaskStatusIfPendingOrQueued(ctx, task.ID, storage.StatusRunning)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			task.Status = storage.StatusCancelled
			task.ErrorMessage = "task cancelled by user"
			_ = e.store.UpdateTaskStatus(context.Background(), task.ID, storage.StatusCancelled, "", "task cancelled by user")
			return task, ctx.Err()
		}
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
	doneCh := make(chan struct{})
	e.cancelsMu.Lock()
	e.cancels[task.ID] = cancel
	e.doneChs[task.ID] = doneCh
	e.cancelsMu.Unlock()

	defer func() {
		e.cancelsMu.Lock()
		delete(e.cancels, task.ID)
		delete(e.doneChs, task.ID)
		e.cancelsMu.Unlock()
		cancel()
		close(doneCh)
	}()

	// 進入 runner 前查核儲存層與 context 最新狀態，若已被外部取消或處於取消中則直接退出，絕不呼叫 runner (修復 F-02)
	latestCheck, checkErr := e.store.GetTask(ctx, task.ID)
	if checkErr == nil && (latestCheck.Status == storage.StatusCancelled || latestCheck.Status == storage.StatusCancelling) {
		task.Status = latestCheck.Status
		task.ErrorMessage = latestCheck.ErrorMessage
		return task, context.Canceled
	}
	if taskCtx.Err() == context.Canceled {
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer persistCancel()

		latestBeforeRun, _ := e.store.GetTask(persistCtx, task.ID)
		if latestBeforeRun != nil && (latestBeforeRun.Status == storage.StatusCancelling || latestBeforeRun.Status == storage.StatusCancelled) {
			task.Status = latestBeforeRun.Status
			task.ErrorMessage = latestBeforeRun.ErrorMessage
			return task, taskCtx.Err()
		}

		task.Status = storage.StatusCancelled
		task.ErrorMessage = "task cancelled by user"
		if updateErr := e.store.UpdateTaskStatus(persistCtx, task.ID, storage.StatusCancelled, "", "task cancelled by user"); updateErr != nil {
			return task, fmt.Errorf("task cancelled before runner: persist status failed: %w", updateErr)
		}
		return task, taskCtx.Err()
	}

	// 5. 調用後端或管線執行
	runnerPrompt := req.Prompt
	var execCtx *ExecutionContext
	if task.ResolvedContext != "" {
		var decoded ExecutionContext
		if err := json.Unmarshal([]byte(task.ResolvedContext), &decoded); err != nil {
			persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer persistCancel()
			errMsg := fmt.Sprintf("decode sealed context failed: %v", err)
			_ = e.store.UpdateTaskStatus(persistCtx, task.ID, storage.StatusFailed, "", errMsg)
			task.Status = storage.StatusFailed
			task.ErrorMessage = errMsg
			return task, fmt.Errorf("decode sealed context: %w", err)
		}
		// 2026-10-04：持久化 Task 的相容投影也必須與 plan 一致。
		if contract := decoded.ExecutionContract; contract != nil && (contract.ContractVersion == backend.StageBindingContractVersion || contract.StageBindingPlan != nil) {
			if err := contract.Validate(task.ExecutionMode, task.Backend); err != nil {
				persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer persistCancel()
				_ = e.store.UpdateTaskStatus(persistCtx, task.ID, storage.StatusFailed, "", err.Error())
				task.Status = storage.StatusFailed
				task.ErrorMessage = err.Error()
				return task, err
			}
		}
		execCtx = &decoded
		runnerPrompt = ComposePrompt(task.Prompt, execCtx)
	} else if req.ResolvedContext != nil && !alreadyCreated {
		execCtx = req.ResolvedContext
		runnerPrompt = ComposePrompt(req.Prompt, req.ResolvedContext)
	} else if len(task.MemoryRefs) > 0 {
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer persistCancel()
		errMsg := "sealed context missing for task with memory refs"
		_ = e.store.UpdateTaskStatus(persistCtx, task.ID, storage.StatusFailed, "", errMsg)
		task.Status = storage.StatusFailed
		task.ErrorMessage = errMsg
		return task, errors.New(errMsg)
	}

	if execCtx == nil {
		execCtx = &ExecutionContext{
			OriginalPrompt: req.Prompt,
			Workspace:      req.Workspace,
			Backend:        backendID,
			CreatedAt:      now,
		}
	} else if execCtx.Backend == "" {
		execCtx.Backend = backendID
	}

	var output string
	var runErr error
	var traceJSON string

	switch task.ExecutionMode {
	case "three-face":
		if e.pipelineRunner == nil {
			persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer persistCancel()
			errMsg := "three-face execution mode requested but no pipeline runner configured"
			_ = e.store.UpdateTaskStatus(persistCtx, task.ID, storage.StatusFailed, "", errMsg)
			task.Status = storage.StatusFailed
			task.ErrorMessage = errMsg
			return task, errors.New(errMsg)
		}
		output, traceJSON, runErr = e.pipelineRunner.ExecutePipeline(taskCtx, task.ExecutionMode, execCtx)
		task.PipelineTrace = traceJSON
	case "direct", "":
		if e.pipelineRunner != nil {
			output, traceJSON, runErr = e.pipelineRunner.ExecutePipeline(taskCtx, "direct", execCtx)
			task.PipelineTrace = traceJSON
		} else if e.runner != nil {
			output, runErr = e.runner(taskCtx, req.Workspace, runnerPrompt)
			statusStr := "completed"
			errStr := ""
			if runErr != nil {
				statusStr = "failed"
				errStr = runErr.Error()
			}
			directTraceObj := map[string]interface{}{
				"mode":        "direct",
				"model_calls": 1,
				"stages": []map[string]interface{}{
					{
						"name":   "striker",
						"status": statusStr,
						"output": output,
						"error":  errStr,
					},
				},
				"final_result": output,
			}
			if b, err := json.Marshal(directTraceObj); err == nil {
				traceJSON = string(b)
				task.PipelineTrace = traceJSON
			}
		}
	default:
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer persistCancel()
		errMsg := fmt.Sprintf("invalid execution mode: %q", task.ExecutionMode)
		_ = e.store.UpdateTaskStatus(persistCtx, task.ID, storage.StatusFailed, "", errMsg)
		task.Status = storage.StatusFailed
		task.ErrorMessage = errMsg
		return task, errors.New(errMsg)
	}

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
		if output != "" {
			_ = e.store.UpdateTaskResult(persistCtx, task.ID, output)
		}

		// 若儲存層已處於 StatusCancelling 或 StatusCancelled，嚴禁 SubmitTask 自行宣告或升級為 StatusCancelled！
		// 終態 StatusCancelled 之宣告權屬於 CancelTask 協調器，必須於進程組核驗並安全確認回收後方可持久化
		if fetchErr == nil && (latestTask.Status == storage.StatusCancelling || latestTask.Status == storage.StatusCancelled) {
			task.Status = latestTask.Status
			task.Result = output
			task.ErrorMessage = latestTask.ErrorMessage
			return task, taskCtx.Err()
		}

		task.Status = storage.StatusCancelled
		task.Result = output
		task.ErrorMessage = "task cancelled by user"
		if updateErr := e.store.UpdateTaskStatus(persistCtx, task.ID, storage.StatusCancelled, output, "task cancelled by user"); updateErr != nil {
			return task, fmt.Errorf("task cancelled: persist status failed: %w", updateErr)
		}
		return task, taskCtx.Err()
	}

	if traceJSON != "" {
		if traceErr := e.store.UpdateTaskPipelineTrace(persistCtx, task.ID, traceJSON); traceErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("persist pipeline trace failed: %w", traceErr))
		}
	}

	if runErr != nil {
		updated, persistErr := e.store.UpdateTaskStatusIfRunning(persistCtx, task.ID, storage.StatusFailed, output, runErr.Error())
		if persistErr != nil {
			return task, fmt.Errorf("%w (persist task status failed: %v)", runErr, persistErr)
		}
		if !updated {
			latest, getErr := e.store.GetTask(persistCtx, task.ID)
			if getErr == nil && (latest.Status == storage.StatusCancelled || latest.Status == storage.StatusCancelling) {
				task.Status = latest.Status
				task.Result = output
				task.ErrorMessage = latest.ErrorMessage
				return task, context.Canceled
			}
		}
		task.Status = storage.StatusFailed
		task.Result = output
		task.ErrorMessage = runErr.Error()
		return task, runErr
	}

	// 6. 空輸出與未解決結構化錯誤防禦檢查（遵循 SCN-007，修復 R-09 與 F-04）
	if isErr, errMsg := hasUnresolvedErrorOrEmpty(output); isErr {
		updated, persistErr := e.store.UpdateTaskStatusIfRunning(persistCtx, task.ID, storage.StatusFailed, output, errMsg)
		if persistErr != nil {
			return task, fmt.Errorf("%w (persist task status failed: %v)", ErrEmptyOutput, persistErr)
		}
		if !updated {
			latest, getErr := e.store.GetTask(persistCtx, task.ID)
			if getErr == nil && (latest.Status == storage.StatusCancelled || latest.Status == storage.StatusCancelling) {
				task.Status = latest.Status
				task.Result = output
				task.ErrorMessage = latest.ErrorMessage
				return task, context.Canceled
			}
		}
		task.Status = storage.StatusFailed
		task.Result = output
		task.ErrorMessage = errMsg
		return task, ErrEmptyOutput
	}

	// 7. 條件式記錄成功結果（修復 R-10）：僅在狀態仍為 running 時才標記為 completed，防止覆寫已被外部標記之 cancelled 或 cancelling 狀態
	updated, err = e.store.UpdateTaskStatusIfRunning(persistCtx, task.ID, storage.StatusCompleted, output, "")
	if err != nil {
		return nil, fmt.Errorf("update task to completed: %w", err)
	}
	if !updated {
		latest, getErr := e.store.GetTask(persistCtx, task.ID)
		if getErr == nil {
			if (latest.Status == storage.StatusCancelled || latest.Status == storage.StatusCancelling) && output != "" {
				if updateErr := e.store.UpdateTaskResult(persistCtx, task.ID, output); updateErr != nil {
					return task, fmt.Errorf("task cancelled: persist output failed: %w", updateErr)
				}
			}
			task.Status = latest.Status
			task.Result = output
			task.ErrorMessage = latest.ErrorMessage
			if latest.Status == storage.StatusCancelled || latest.Status == storage.StatusCancelling {
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
