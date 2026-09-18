package daemon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"asura/internal/auth"
	"asura/internal/backend"
	"asura/internal/backend/codex"
	"asura/internal/backend/opencode"
	"asura/internal/cron"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
)

var (
	// ErrDaemonAlreadyRunning 代表同一個 Socket 已有活躍的 Daemon 守護進程
	ErrDaemonAlreadyRunning = errors.New("daemon already running")
)

// DefaultApprovedPermissionPolicy 定義 INT-007 核准之安全操作邊界：
// 允許修改/build/test，但刪除重要資料、部署、push、對外傳送必須另行確認
const DefaultApprovedPermissionPolicy = `{"*":"ask","read":"allow","edit":"allow","build":"allow","test":"allow","bash":"ask"}`

// DefaultReadOnlyPermissionPolicy 定義唯讀角色（Oracle, Critic）之安全操作邊界：
// 顯式覆寫所有 mutating / escape capability 為 deny，並開放唯讀分析 capability（read, glob, grep, list, lsp）。
// 由於 OpenCode 以 deep merge 結合既有配置，必須顯式將 edit/write/patch/bash/task/skill 等宣告為 deny，以防外層 allow 勝出。
const DefaultReadOnlyPermissionPolicy = `{"*":"deny","read":"allow","glob":"allow","grep":"allow","list":"allow","lsp":"allow","edit":"deny","write":"deny","patch":"deny","bash":"deny","task":"deny","skill":"deny","external_directory":"deny","webfetch":"deny","websearch":"deny"}`

// OpenCodeRuntimeConfig 定義 Daemon 底層執行器配置
type OpenCodeRuntimeConfig struct {
	BinaryPath      string
	ExpectedVersion string
	ConfigDir       string
	Permission      string
	DisableAuto     bool
}

// Config 定義 Daemon 運行參數
type Config struct {
	SocketPath     string
	MaxWorkers     int
	MaxCronWorkers int
	MaxQueue       int
	OpenCodeConfig OpenCodeRuntimeConfig
}

// Daemon 實作單一守護進程中樞
type Daemon struct {
	cfg       Config
	store     *storage.SQLiteStore
	policy    *auth.Policy
	engine    *engine.Engine
	pool      *WorkerPool
	wsQueue    *WorkspaceQueue
	scheduler  *cron.Scheduler
	inFlight   int
	inFlightMu sync.Mutex
	listener   net.Listener
	lockFile              *os.File
	closed                chan struct{}
	closeOnce             sync.Once
	wg                    sync.WaitGroup
	readOnlyConfigDir     string
	readOnlyConfigCleanup func()
	initErr               error
}

func (d *Daemon) tryAcquireCapacity() bool {
	d.inFlightMu.Lock()
	defer d.inFlightMu.Unlock()
	maxCapacity := d.cfg.MaxWorkers + d.cfg.MaxQueue
	if d.inFlight >= maxCapacity {
		return false
	}
	d.inFlight++
	return true
}

func (d *Daemon) releaseCapacity() {
	d.inFlightMu.Lock()
	if d.inFlight > 0 {
		d.inFlight--
	}
	d.inFlightMu.Unlock()
}

// DaemonOption 用於自訂 Daemon 設定
type DaemonOption func(*Daemon)

// WithEngine 注入既有的 Engine 實例
func WithEngine(eng *engine.Engine) DaemonOption {
	return func(d *Daemon) {
		d.engine = eng
	}
}

// WithOpenCodeConfig 注入執行器執行設定
func WithOpenCodeConfig(cfg OpenCodeRuntimeConfig) DaemonOption {
	return func(d *Daemon) {
		d.cfg.OpenCodeConfig = cfg
	}
}

// OpenCodeConfig 回傳 Daemon 當前配置之 OpenCodeRuntimeConfig
func (d *Daemon) OpenCodeConfig() OpenCodeRuntimeConfig {
	return d.cfg.OpenCodeConfig
}

// NewDaemon 建立新的 Daemon 實例
func NewDaemon(cfg Config, store *storage.SQLiteStore, policy *auth.Policy, opts ...DaemonOption) *Daemon {
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 2
	}
	if cfg.MaxCronWorkers <= 0 {
		cfg.MaxCronWorkers = 1
	}
	if cfg.MaxQueue < 0 {
		cfg.MaxQueue = 10
	}
	if cfg.OpenCodeConfig.ExpectedVersion == "" {
		cfg.OpenCodeConfig.ExpectedVersion = "1.18.23"
	}
	if cfg.OpenCodeConfig.Permission == "" {
		cfg.OpenCodeConfig.Permission = DefaultApprovedPermissionPolicy
	}

	d := &Daemon{
		cfg:     cfg,
		store:   store,
		policy:  policy,
		pool:    NewWorkerPoolWithCapacity(CapacityConfig{
			MaxWorkers:     cfg.MaxWorkers,
			MaxCronWorkers: cfg.MaxCronWorkers,
			MaxQueue:       cfg.MaxQueue,
		}),
		wsQueue: NewWorkspaceQueue(),
		closed:  make(chan struct{}),
	}

	for _, opt := range opts {
		opt(d)
	}

	if d.engine == nil {
		readOnlyConfigDir, cleanupReadOnlyConfig, err := opencode.CreateIsolatedReadOnlyConfigDir()
		if err != nil {
			d.initErr = fmt.Errorf("create read-only runtime config failed: %w", err)
		} else {
			d.readOnlyConfigDir = readOnlyConfigDir
			d.readOnlyConfigCleanup = cleanupReadOnlyConfig
		}

		readOnlyAdapter := opencode.NewAdapter(opencode.AdapterConfig{
			BinaryPath:           d.cfg.OpenCodeConfig.BinaryPath,
			ExpectedVersion:      d.cfg.OpenCodeConfig.ExpectedVersion,
			ConfigDir:            d.readOnlyConfigDir,
			Permission:           DefaultReadOnlyPermissionPolicy,
			DisableAuto:          true, // 嚴禁自動修改、建置或測試
			Role:                 opencode.RoleOracle, // Oracle 與 Critic 共享唯讀隔離角色
			Agent:                opencode.AsuraReadOnlyAgentName,
			Pure:                 true,
			DisableProjectConfig: true,
			Isolated:             true,
			OnProcessStarted: func(ctx context.Context, pgid int) {
				if taskID := engine.TaskIDFromContext(ctx); taskID != "" {
					_ = store.UpdateTaskPGID(context.Background(), taskID, pgid)
				}
			},
			OnProcessExited: func(ctx context.Context, pgid int) {
				if taskID := engine.TaskIDFromContext(ctx); taskID != "" {
					_ = store.UpdateTaskPGID(context.Background(), taskID, 0)
				}
			},
		})
		strikerAdapter := opencode.NewAdapter(opencode.AdapterConfig{
			BinaryPath:      d.cfg.OpenCodeConfig.BinaryPath,
			ExpectedVersion: d.cfg.OpenCodeConfig.ExpectedVersion,
			ConfigDir:       d.cfg.OpenCodeConfig.ConfigDir,
			Permission:      d.cfg.OpenCodeConfig.Permission,
			DisableAuto:     d.cfg.OpenCodeConfig.DisableAuto,
			Role:            opencode.RoleStriker,
			OnProcessStarted: func(ctx context.Context, pgid int) {
				if taskID := engine.TaskIDFromContext(ctx); taskID != "" {
					_ = store.UpdateTaskPGID(context.Background(), taskID, pgid)
				}
			},
			OnProcessExited: func(ctx context.Context, pgid int) {
				if taskID := engine.TaskIDFromContext(ctx); taskID != "" {
					_ = store.UpdateTaskPGID(context.Background(), taskID, 0)
				}
			},
		})
		opencodeLogicalBackend := opencode.NewRoleRoutingBackend(readOnlyAdapter, strikerAdapter)
		reg := backend.NewRegistry()
		_ = reg.Register(opencodeLogicalBackend)
		if codexPath, err := exec.LookPath("codex"); err == nil && codexPath != "" {
			_ = reg.Register(codex.NewAdapter(codex.AdapterConfig{
				BinaryPath: codexPath,
			}))
		}

		oracleStage := pipeline.NewOracleStageWithBackend(readOnlyAdapter)
		strikerStage := pipeline.NewStrikerStageWithBackend(strikerAdapter)
		criticStage := pipeline.NewCriticStageWithBackend(readOnlyAdapter)
		orc := pipeline.NewOrchestrator(oracleStage, strikerStage, criticStage).WithRegistry(reg)

		strikerRunner := func(ctx context.Context, ws, prompt string) (string, error) {
			backendID := "opencode"
			if taskID := engine.TaskIDFromContext(ctx); taskID != "" {
				if t, err := store.GetTask(context.Background(), taskID); err == nil && t != nil && t.Backend != "" {
					backendID = t.Backend
				}
			}
			activeBackend, err := reg.Get(backendID)
			if err != nil {
				return "", fmt.Errorf("backend preflight failed: %w", err)
			}
			res, err := activeBackend.Run(ctx, &backend.ExecutionRequest{
				Role:      backend.RoleStriker,
				Workspace: ws,
				Prompt:    prompt,
			})
			if err != nil {
				if res != nil && res.PartialOutput != "" {
					return res.PartialOutput, err
				}
				return "", err
			}
			return res.Output, nil
		}

		d.engine = engine.NewEngine(store, policy,
			engine.WithRunner(strikerRunner),
			engine.WithPipelineRunner(orc),
		)
	}

	d.scheduler = cron.NewScheduler(store, policy, d.dispatchCronTask)

	return d
}

// Scheduler 回傳 Daemon 內部的 Cron Scheduler 實例
func (d *Daemon) Scheduler() *cron.Scheduler {
	return d.scheduler
}

// dispatchCronTask 由 Cron Scheduler 觸發時調用，整合專案互斥鎖與保留通道調度 (CRON-001, CRON-002)
func (d *Daemon) dispatchCronTask(ctx context.Context, cronID, workspace, prompt string) (string, error) {
	if !d.tryAcquireCapacity() {
		return "", ErrCapacityExceeded
	}

	taskID := engine.GenerateTaskID()
	now := time.Now().UTC()
	fp := fmt.Sprintf("%x", sha256.Sum256([]byte(workspace+":"+prompt)))

	initialTask := &storage.Task{
		ID:          taskID,
		Fingerprint: fp,
		Prompt:      prompt,
		Workspace:   workspace,
		Status:      storage.StatusPending,
		Class:       string(TaskClassCron),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := d.store.CreateTask(ctx, initialTask); err != nil {
		d.releaseCapacity()
		return "", fmt.Errorf("create initial cron task: %w", err)
	}

	wsKey := d.canonicalWorkspace(workspace)
	// 遵循 CRON-001：同專案嚴格互斥依序排隊
	acquired, _ := d.wsQueue.TryAcquire(wsKey, taskID)
	if !acquired {
		// 專案忙碌中，條件式更新狀態為 queued 並排入專案佇列，待前任務完成後依序喚醒（修復 MUST FIX 1）
		_, _ = d.store.UpdateTaskStatusIfPending(ctx, taskID, storage.StatusQueued)
		return taskID, nil
	}

	// 遵循 CRON-002：以 TaskClassCron 類別派工，受 MaxCronWorkers 限制並保留互動通道
	err := d.dispatchTask(taskID, workspace, prompt, TaskClassCron)
	if err != nil {
		d.releaseCapacity()
		d.releaseWorkspaceAndWakeNext(wsKey, taskID)
		_ = d.store.UpdateTaskStatus(ctx, taskID, storage.StatusFailed, "", err.Error())
		return "", err
	}

	return taskID, nil
}

// Start 競爭取得 Unix Domain Socket 擁有權並開始監聽
// 遵循 SCN-003 與 R-01：以 flock 原子排他鎖消除 TOCTOU 競爭，確保單一擁有權無雙重 Daemon
func (d *Daemon) Start() error {
	if d.initErr != nil {
		return fmt.Errorf("daemon start aborted due to initialization error: %w", d.initErr)
	}

	// 1. 先取得跨進程檔案排他鎖，保護後續探測、清理與綁定過程
	lockPath := d.cfg.SocketPath + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open daemon lock file: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return ErrDaemonAlreadyRunning
	}
	d.lockFile = f

	// 2. 探測既有 Socket 是否有存活 Daemon
	if conn, err := net.DialTimeout("unix", d.cfg.SocketPath, 100*time.Millisecond); err == nil {
		_ = conn.Close()
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		d.lockFile = nil
		return ErrDaemonAlreadyRunning
	}

	// 3. 清理過期殘留 socket 檔案
	_ = os.Remove(d.cfg.SocketPath)

	// 4. 綁定監聽
	l, err := net.Listen("unix", d.cfg.SocketPath)
	if err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		d.lockFile = nil
		return fmt.Errorf("listen unix socket: %w", err)
	}
	d.listener = l

	// 5. 執行重啟恢復：將前次未完成的殘留任務安全轉移為 failed，揭露中斷原因（SCN-006）
	if d.store != nil {
		_, _ = d.store.RecoverInterruptedTasks(context.Background())
	}

	// 6. 對齊排程時鐘：過期者重算未來觸發時間，絕不補跑歷史（CRON-004）
	if d.scheduler != nil {
		_ = d.scheduler.AlignSchedules(context.Background(), time.Now())
	}

	d.wg.Add(1)
	go d.cronLoop()

	d.wg.Add(1)
	go d.acceptLoop()

	return nil
}

func (d *Daemon) cronLoop() {
	defer d.wg.Done()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.closed:
			return
		case now := <-ticker.C:
			if d.scheduler == nil || d.store == nil {
				continue
			}
			ctx := context.Background()
			schedules, err := d.store.ListCronSchedules(ctx)
			if err != nil {
				continue
			}
			for _, sched := range schedules {
				if sched.Status == storage.CronStatusActive && sched.NextRunAt != nil && !sched.NextRunAt.IsZero() && !sched.NextRunAt.After(now) {
					_ = d.scheduler.Trigger(ctx, sched.ID, now)
				}
			}
		}
	}
}

func (d *Daemon) acceptLoop() {
	defer d.wg.Done()
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			select {
			case <-d.closed:
				return
			default:
				continue
			}
		}

		go d.handleConn(conn)
	}
}

// CronAddRequest 定義新增排程之請求參數
type CronAddRequest struct {
	Name      string `json:"name"`
	Workspace string `json:"workspace"`
	Schedule  string `json:"schedule"`
	Prompt    string `json:"prompt"`
}

// IPCRequest 定義用戶端提交之請求
type IPCRequest struct {
	Action     string          `json:"action,omitempty"`
	RequestID  string          `json:"request_id,omitempty"`
	TaskID     string          `json:"task_id,omitempty"`
	Workspace  string          `json:"workspace,omitempty"`
	Prompt     string          `json:"prompt,omitempty"`
	Class      TaskClass       `json:"class,omitempty"`
	CronID     string          `json:"cron_id,omitempty"`
	CronAddReq *CronAddRequest `json:"cron_add_req,omitempty"`
	Limit      int             `json:"limit,omitempty"`
	MemoryID   string          `json:"memory_id,omitempty"`
	Content    string          `json:"content,omitempty"`
	MemoryIDs  []string        `json:"memory_ids,omitempty"`
	Mode       string          `json:"mode,omitempty"`
	Backend    string          `json:"backend,omitempty"`
}

// IPCResponse 定義傳回用戶端之結果
type IPCResponse struct {
	Task          *storage.Task          `json:"task,omitempty"`
	Projects      []string               `json:"projects,omitempty"`
	CronSchedule  *storage.CronSchedule  `json:"cron_schedule,omitempty"`
	CronSchedules []storage.CronSchedule `json:"cron_schedules,omitempty"`
	CronRuns      []storage.CronRun      `json:"cron_runs,omitempty"`
	Memory        *storage.Memory        `json:"memory,omitempty"`
	Memories      []storage.Memory       `json:"memories,omitempty"`
	Error         string                 `json:"error,omitempty"`
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var req IPCRequest
		if err := decoder.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			_ = encoder.Encode(IPCResponse{Error: "invalid request payload"})
			return
		}

		action := req.Action
		if action == "" {
			action = "submit"
		}

		switch action {
		case "status":
			if req.TaskID == "" {
				_ = encoder.Encode(IPCResponse{Error: "task_id required for status query"})
				continue
			}
			task, err := d.store.GetTask(context.Background(), req.TaskID)
			if err != nil && req.TaskID != "" {
				task, err = d.store.GetTaskByRequestID(context.Background(), req.TaskID)
			}
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("task not found: %v", err)})
				continue
			}
			if err := d.authorizeTask(task); err != nil {
				_ = encoder.Encode(IPCResponse{Error: err.Error()})
				continue
			}
			_ = encoder.Encode(IPCResponse{Task: task})

		case "cancel":
			if req.TaskID == "" {
				_ = encoder.Encode(IPCResponse{Error: "task_id required for cancel"})
				continue
			}
			target, err := d.store.GetTask(context.Background(), req.TaskID)
			if err != nil {
				target, err = d.store.GetTaskByRequestID(context.Background(), req.TaskID)
			}
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: "task not found"})
				continue
			}
			if err := d.authorizeTask(target); err != nil {
				_ = encoder.Encode(IPCResponse{Error: err.Error()})
				continue
			}
			targetID := target.ID
			if target.Status == storage.StatusQueued {
				wsKey := d.canonicalWorkspace(target.Workspace)
				if d.wsQueue.Remove(wsKey, targetID) {
					d.releaseCapacity()
				}
			}
			err = d.engine.CancelTask(targetID)
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: err.Error()})
				continue
			}
			// CancelTask 確認安全回收完成後，釋放該任務持有的 workspace lock 並喚醒下一個排隊任務
			wsKey := d.canonicalWorkspace(target.Workspace)
			d.releaseWorkspaceAndWakeNext(wsKey, targetID)
			task, _ := d.store.GetTask(context.Background(), targetID)
			_ = encoder.Encode(IPCResponse{Task: task})

		case "projects":
			projects := []string{}
			if d.policy != nil {
				projects = d.policy.AllowedRoots()
			}
			_ = encoder.Encode(IPCResponse{Projects: projects})

		case "submit":
			// 遵循 INT-002, P3-T2 與 CTXMEM-003：同步校驗專案授權並取得 canonical workspace
			var canonicalWs string
			if d.policy != nil {
				var authErr error
				canonicalWs, authErr = d.policy.AuthorizeCanonicalWorkspace(req.Workspace)
				if authErr != nil {
					_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("unauthorized workspace: %v", authErr)})
					continue
				}
			} else {
				canonicalWs = d.canonicalWorkspace(req.Workspace)
			}

			// 記憶 ID 去重並排序，定義 Set 語意，確保 fingerprint、解析、snapshot 與 Prompt 順序 100% 一致 (MUST FIX 2)
			if len(req.MemoryIDs) > 0 {
				req.MemoryIDs = normalizeMemoryIDs(req.MemoryIDs)
			}

			reqMode := req.Mode
			if reqMode == "" {
				reqMode = "direct"
			}
			if !pipeline.IsValidExecutionMode(pipeline.ExecutionMode(reqMode)) {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("invalid execution mode: %q", reqMode)})
				continue
			}

			backendID := req.Backend
			if backendID == "" {
				backendID = "opencode"
			}

			// 計算請求 Fingerprint 確保同 Request ID 之 payload 一致性（防止撞 PK 或錯置，CTXMEM-004, TFACE-009, MBACK-004）
			// 遵循 MUST FIX 1：使用 canonicalWs 作為 fingerprint workspace
			fp := calculateTaskFingerprint(canonicalWs, req.Prompt, reqMode, req.MemoryIDs, backendID)

			// 遵循 INT-004-REDELIVERY 與 CTXMEM-004：同 RequestID 且非 Cancelled 任務時直接回傳既有密封任務，不重新查詢底層記憶
			if req.RequestID != "" {
				existing, err := d.store.GetTaskByRequestID(context.Background(), req.RequestID)
				if err != nil {
					existing, err = d.store.GetTask(context.Background(), req.RequestID)
				}
				if err == nil && existing != nil {
					if err := d.authorizeTask(existing); err != nil {
						_ = encoder.Encode(IPCResponse{Error: err.Error()})
						continue
					}
					if existing.Fingerprint != "" && existing.Fingerprint != fp {
						_ = encoder.Encode(IPCResponse{Error: "request ID reused with different payload"})
						continue
					}
					if existing.Status == storage.StatusCancelled {
						_ = encoder.Encode(IPCResponse{Error: "cannot redeliver cancelled request"})
						continue
					}
					_ = encoder.Encode(IPCResponse{Task: existing})
					continue
				}
			}

			// 遵循 CTXMEM-002, CTXMEM-005：在容量取得與任務持久化前，解析選取之記憶並嚴格隔離跨專案/不存在記憶
			var resolvedMemories []storage.Memory
			if len(req.MemoryIDs) > 0 {
				var resErr error
				resolvedMemories, resErr = d.store.ResolveMemoriesForTask(context.Background(), canonicalWs, req.MemoryIDs)
				if resErr != nil {
					_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("resolve memories: %v", resErr)})
					continue
				}
			}

			// 遵循 SCN-004 與 INT-003：檢查全域容量上限（Worker 執行槽位 + 佇列排隊槽位）
			if !d.tryAcquireCapacity() {
				_ = encoder.Encode(IPCResponse{Error: ErrCapacityExceeded.Error()})
				continue
			}

			taskID := engine.GenerateTaskID()
			now := time.Now().UTC()

			taskClass := req.Class
			if taskClass == "" {
				taskClass = TaskClassInteractive
			}

			var execCtx *engine.ExecutionContext
			var resolvedCtxJSON string
			if len(resolvedMemories) > 0 {
				snapshots := make([]engine.ProjectMemorySnapshot, 0, len(resolvedMemories))
				for _, m := range resolvedMemories {
					snapshots = append(snapshots, engine.ProjectMemorySnapshot{
						ID:        m.ID,
						Workspace: m.Workspace,
						Content:   m.Content,
						Source:    m.Source,
						CreatedAt: m.CreatedAt,
					})
				}
				execCtx = &engine.ExecutionContext{
					OriginalPrompt:  req.Prompt,
					Workspace:       canonicalWs,
					Backend:         backendID,
					ProjectMemories: snapshots,
					MemoryRefs:      req.MemoryIDs,
					CreatedAt:       now,
				}
				raw, err := json.Marshal(execCtx)
				if err != nil {
					d.releaseCapacity()
					_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("seal execution context: %v", err)})
					continue
				}
				resolvedCtxJSON = string(raw)
			} else {
				execCtx = &engine.ExecutionContext{
					OriginalPrompt: req.Prompt,
					Workspace:      canonicalWs,
					Backend:        backendID,
					CreatedAt:      now,
				}
			}

			initialTask := &storage.Task{
				ID:              taskID,
				RequestID:       req.RequestID,
				Fingerprint:     fp,
				Prompt:          req.Prompt,
				Workspace:       canonicalWs,
				Status:          storage.StatusPending,
				Class:           string(taskClass),
				ExecutionMode:   reqMode,
				Backend:         backendID,
				MemoryRefs:      req.MemoryIDs,
				ResolvedContext: resolvedCtxJSON,
				CreatedAt:       now,
				UpdatedAt:       now,
			}

			if err := d.store.CreateTask(context.Background(), initialTask); err != nil {
				d.releaseCapacity()
				if req.RequestID != "" && errors.Is(err, storage.ErrDuplicateRequestID) {
					existing, getErr := d.store.GetTaskByRequestID(context.Background(), req.RequestID)
					if getErr != nil {
						existing, getErr = d.store.GetTask(context.Background(), req.RequestID)
					}
					if getErr == nil && existing != nil {
						if err := d.authorizeTask(existing); err != nil {
							_ = encoder.Encode(IPCResponse{Error: err.Error()})
							continue
						}
						if existing.Fingerprint != "" && existing.Fingerprint != fp {
							_ = encoder.Encode(IPCResponse{Error: "request ID reused with different payload"})
							continue
						}
						if existing.Status == storage.StatusCancelled {
							_ = encoder.Encode(IPCResponse{Error: "cannot redeliver cancelled request"})
							continue
						}
						_ = encoder.Encode(IPCResponse{Task: existing})
						continue
					}
				}
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("failed to persist initial task: %v", err)})
				continue
			}

			wsKey := canonicalWs
			// 遵循 CRON-001：同專案嚴格互斥依序排隊（僅創建任務之勝者競爭專案鎖）
			acquired, _ := d.wsQueue.TryAcquire(wsKey, taskID)
			if !acquired {
				// 專案忙碌中，條件式更新狀態為 queued 並排入專案佇列，待前任務完成後依序喚醒（修復 MUST FIX 1）
				initialTask.Status = storage.StatusQueued
				_, _ = d.store.UpdateTaskStatusIfPending(context.Background(), taskID, storage.StatusQueued)
				_ = encoder.Encode(IPCResponse{Task: initialTask})
				continue
			}

			err := d.dispatchTaskWithMode(taskID, canonicalWs, req.Prompt, taskClass, reqMode, execCtx)
			if err != nil {
				d.releaseCapacity()
				d.releaseWorkspaceAndWakeNext(wsKey, taskID)
				_ = d.store.DeleteTask(context.Background(), taskID)
				_ = encoder.Encode(IPCResponse{Error: err.Error()})
				continue
			}

			// 遵循 P4-T2：容量及持久化成功後、後端完成前回傳 Task ID
			_ = encoder.Encode(IPCResponse{Task: initialTask})

		case "cron_add":
			if req.CronAddReq == nil {
				_ = encoder.Encode(IPCResponse{Error: "cron_add_req required"})
				continue
			}
			addReq := req.CronAddReq
			if addReq.Name == "" || addReq.Workspace == "" || addReq.Schedule == "" || addReq.Prompt == "" {
				_ = encoder.Encode(IPCResponse{Error: "name, workspace, schedule, and prompt are required"})
				continue
			}
			parsed, err := cron.Parse(addReq.Schedule)
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("invalid cron schedule: %v", err)})
				continue
			}
			if d.policy != nil {
				if err := d.policy.AuthorizeWorkspace(addReq.Workspace); err != nil {
					_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("unauthorized workspace: %v", err)})
					continue
				}
			}
			now := time.Now().Truncate(time.Second)
			nextRun := parsed.Next(now)
			if nextRun.IsZero() {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("cron schedule %q is unreachable (no upcoming trigger dates)", addReq.Schedule)})
				continue
			}
			cronID := fmt.Sprintf("cron-%d", time.Now().UnixNano())
			sched := storage.CronSchedule{
				ID:        cronID,
				Name:      addReq.Name,
				Workspace: addReq.Workspace,
				Schedule:  addReq.Schedule,
				Prompt:    addReq.Prompt,
				Status:    storage.CronStatusActive,
				NextRunAt: &nextRun,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := d.store.CreateCronSchedule(context.Background(), sched); err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("create cron schedule: %v", err)})
				continue
			}
			created, err := d.store.GetCronSchedule(context.Background(), cronID)
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("get created schedule: %v", err)})
				continue
			}
			_ = encoder.Encode(IPCResponse{CronSchedule: created})

		case "cron_list":
			schedules, err := d.store.ListCronSchedules(context.Background())
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("list cron schedules: %v", err)})
				continue
			}
			_ = encoder.Encode(IPCResponse{CronSchedules: schedules})

		case "cron_pause":
			if req.CronID == "" {
				_ = encoder.Encode(IPCResponse{Error: "cron_id is required"})
				continue
			}
			if err := d.store.UpdateCronScheduleStatus(context.Background(), req.CronID, storage.CronStatusPaused, "paused by user"); err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("pause cron schedule: %v", err)})
				continue
			}
			sched, err := d.store.GetCronSchedule(context.Background(), req.CronID)
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("get schedule: %v", err)})
				continue
			}
			_ = encoder.Encode(IPCResponse{CronSchedule: sched})

		case "cron_resume":
			if req.CronID == "" {
				_ = encoder.Encode(IPCResponse{Error: "cron_id is required"})
				continue
			}
			sched, err := d.store.GetCronSchedule(context.Background(), req.CronID)
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("schedule not found: %v", err)})
				continue
			}
			parsed, err := cron.Parse(sched.Schedule)
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("parse schedule: %v", err)})
				continue
			}
			now := time.Now()
			next := parsed.Next(now)
			if next.IsZero() {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("cron schedule %q is unreachable (no upcoming trigger dates)", sched.Schedule)})
				continue
			}
			_ = d.store.UpdateCronScheduleNextRun(context.Background(), req.CronID, next, sched.LastRunAt)
			if err := d.store.UpdateCronScheduleStatus(context.Background(), req.CronID, storage.CronStatusActive, ""); err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("resume cron schedule: %v", err)})
				continue
			}
			sched, _ = d.store.GetCronSchedule(context.Background(), req.CronID)
			_ = encoder.Encode(IPCResponse{CronSchedule: sched})

		case "cron_remove":
			if req.CronID == "" {
				_ = encoder.Encode(IPCResponse{Error: "cron_id is required"})
				continue
			}
			if err := d.store.DeleteCronSchedule(context.Background(), req.CronID); err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("delete cron schedule: %v", err)})
				continue
			}
			_ = encoder.Encode(IPCResponse{})

		case "cron_runs":
			if req.CronID == "" {
				_ = encoder.Encode(IPCResponse{Error: "cron_id is required"})
				continue
			}
			limit := req.Limit
			if limit <= 0 {
				limit = 20
			}
			runs, err := d.store.ListCronRuns(context.Background(), req.CronID, limit)
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("list cron runs: %v", err)})
				continue
			}
			_ = encoder.Encode(IPCResponse{CronRuns: runs})

		case "memory_add":
			if req.Workspace == "" || req.Content == "" {
				_ = encoder.Encode(IPCResponse{Error: "workspace and content are required"})
				continue
			}
			if d.policy == nil {
				_ = encoder.Encode(IPCResponse{Error: "unauthorized workspace"})
				continue
			}
			canonicalScope, err := d.policy.AuthorizeCanonicalWorkspace(req.Workspace)
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("unauthorized workspace: %v", err)})
				continue
			}

			mem := storage.Memory{
				ID:        generateMemoryID(),
				Workspace: canonicalScope,
				Content:   req.Content,
				Source:    "manual",
				CreatedAt: time.Now().UTC(),
			}
			if err := d.store.CreateMemory(context.Background(), mem); err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("create memory: %v", err)})
				continue
			}
			_ = encoder.Encode(IPCResponse{Memory: &mem})

		case "memory_list":
			if req.Workspace == "" {
				_ = encoder.Encode(IPCResponse{Error: "workspace is required"})
				continue
			}
			if d.policy == nil {
				_ = encoder.Encode(IPCResponse{Error: "unauthorized workspace"})
				continue
			}
			canonicalScope, err := d.policy.AuthorizeCanonicalWorkspace(req.Workspace)
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("unauthorized workspace: %v", err)})
				continue
			}

			memories, err := d.store.ListMemories(context.Background(), canonicalScope)
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("list memories: %v", err)})
				continue
			}
			_ = encoder.Encode(IPCResponse{Memories: memories})

		case "memory_get":
			if req.Workspace == "" || req.MemoryID == "" {
				_ = encoder.Encode(IPCResponse{Error: "workspace and memory_id are required"})
				continue
			}
			if d.policy == nil {
				_ = encoder.Encode(IPCResponse{Error: "unauthorized workspace"})
				continue
			}
			canonicalScope, err := d.policy.AuthorizeCanonicalWorkspace(req.Workspace)
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("unauthorized workspace: %v", err)})
				continue
			}

			mem, err := d.store.GetMemory(context.Background(), canonicalScope, req.MemoryID)
			if err != nil {
				if errors.Is(err, storage.ErrMemoryNotFound) {
					_ = encoder.Encode(IPCResponse{Error: "memory not found"})
				} else {
					_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("get memory: %v", err)})
				}
				continue
			}
			_ = encoder.Encode(IPCResponse{Memory: mem})

		case "memory_remove":
			if req.Workspace == "" || req.MemoryID == "" {
				_ = encoder.Encode(IPCResponse{Error: "workspace and memory_id are required"})
				continue
			}
			if d.policy == nil {
				_ = encoder.Encode(IPCResponse{Error: "unauthorized workspace"})
				continue
			}
			canonicalScope, err := d.policy.AuthorizeCanonicalWorkspace(req.Workspace)
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("unauthorized workspace: %v", err)})
				continue
			}

			err = d.store.DeleteMemory(context.Background(), canonicalScope, req.MemoryID)
			if err != nil {
				if errors.Is(err, storage.ErrMemoryNotFound) {
					_ = encoder.Encode(IPCResponse{Error: "memory not found"})
				} else {
					_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("delete memory: %v", err)})
				}
				continue
			}
			_ = encoder.Encode(IPCResponse{})

		default:
			_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("unknown action: %s", action)})
		}
	}
}

func generateMemoryID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("mem-%d-%x", time.Now().UnixNano(), time.Now().Unix())
	}
	return fmt.Sprintf("mem-%d-%s", time.Now().UnixNano(), hex.EncodeToString(b))
}

func (d *Daemon) canonicalWorkspace(ws string) string {
	if d.policy != nil {
		return d.policy.CanonicalWorkspace(ws)
	}
	abs, err := filepath.Abs(ws)
	if err == nil {
		if realPath, err := filepath.EvalSymlinks(abs); err == nil {
			return filepath.Clean(realPath)
		}
		return filepath.Clean(abs)
	}
	return filepath.Clean(ws)
}

func (d *Daemon) releaseWorkspaceAndWakeNext(wsKey, completedTaskID string) {
	// 遵循 MUST FIX 3：禁止在釋放鎖時重複解析 canonicalWorkspace，確保取鎖與釋放鎖使用嚴格相同的不可變 wsKey
	nextTaskID, hasNext := d.wsQueue.Release(wsKey, completedTaskID)
	if hasNext && nextTaskID != "" {
		d.wakeQueuedTask(nextTaskID, wsKey)
	}
}

func (d *Daemon) dispatchTask(taskID, workspace, prompt string, class TaskClass, optCtx ...*engine.ExecutionContext) error {
	return d.dispatchTaskWithMode(taskID, workspace, prompt, class, "direct", optCtx...)
}

func (d *Daemon) dispatchTaskWithMode(taskID, workspace, prompt string, class TaskClass, mode string, optCtx ...*engine.ExecutionContext) error {
	var execCtx *engine.ExecutionContext
	backendID := "opencode"
	if len(optCtx) > 0 && optCtx[0] != nil {
		execCtx = optCtx[0]
		if execCtx.Backend != "" {
			backendID = execCtx.Backend
		}
	}
	wsKey := d.canonicalWorkspace(workspace)
	_, err := d.pool.SubmitAsyncClassifiedWithDone(context.Background(), class, func() error {
		_, runErr := d.engine.SubmitTask(context.Background(), engine.SubmitRequest{
			ID:              taskID,
			Workspace:       workspace,
			Prompt:          prompt,
			Mode:            mode,
			Backend:         backendID,
			ResolvedContext: execCtx,
		})
		return runErr
	}, func() {
		// 在 WorkerPool 槽位完全釋放後執行容量歸還
		d.releaseCapacity()
		// 檢查任務最新狀態：若處於 cancelling，由 CancelTask 協調器負責安全回收與解鎖，嚴禁提前釋放
		task, err := d.store.GetTask(context.Background(), taskID)
		if err == nil && task != nil && task.Status == storage.StatusCancelling {
			return
		}
		d.releaseWorkspaceAndWakeNext(wsKey, taskID)
	})
	return err
}

func (d *Daemon) dispatchQueuedTask(taskID, workspace, prompt string, class TaskClass, mode string, wsKey string, optBackend ...string) error {
	backendID := "opencode"
	if len(optBackend) > 0 && optBackend[0] != "" {
		backendID = optBackend[0]
	}
	_, err := d.pool.SubmitEnqueuedClassifiedWithDone(context.Background(), class, func() error {
		_, runErr := d.engine.SubmitTask(context.Background(), engine.SubmitRequest{
			ID:        taskID,
			Workspace: workspace,
			Prompt:    prompt,
			Mode:      mode,
			Backend:   backendID,
		})
		return runErr
	}, func() {
		d.releaseCapacity()
		task, err := d.store.GetTask(context.Background(), taskID)
		if err == nil && task != nil && task.Status == storage.StatusCancelling {
			return
		}
		d.releaseWorkspaceAndWakeNext(wsKey, taskID)
	})
	return err
}

func (d *Daemon) wakeQueuedTask(taskID, wsKey string) {
	task, err := d.store.GetTask(context.Background(), taskID)
	if err != nil || task == nil {
		d.releaseCapacity()
		d.releaseWorkspaceAndWakeNext(wsKey, taskID)
		return
	}

	// 遵循 MUST FIX 1：接受 StatusQueued 與 StatusPending，防範 SQLite 寫入延遲導致 pending 任務被遺漏丟棄
	if task.Status != storage.StatusQueued && task.Status != storage.StatusPending {
		d.releaseCapacity()
		d.releaseWorkspaceAndWakeNext(wsKey, taskID)
		return
	}

	// 遵循 MUST FIX 2：使用任務本身持久化之 Class，杜絕被剛完成任務之 Class 覆蓋
	taskClass := TaskClass(task.Class)
	if taskClass == "" {
		taskClass = TaskClassInteractive
	}

	taskBackend := task.Backend
	if taskBackend == "" {
		taskBackend = "opencode"
	}

	// 遵循 MUST FIX 2：被喚醒任務使用 dispatchQueuedTask，不受 pool.maxQueue 二次拋棄
	err = d.dispatchQueuedTask(task.ID, task.Workspace, task.Prompt, taskClass, task.ExecutionMode, wsKey, taskBackend)
	if err != nil {
		d.releaseCapacity()
		_ = d.store.UpdateTaskStatus(context.Background(), task.ID, storage.StatusFailed, "", err.Error())
		// 派工失敗時繼續交接專案鎖給後續等待任務 (MUST FIX 8)
		d.releaseWorkspaceAndWakeNext(wsKey, taskID)
	}
}

// Close 關閉 Daemon 並釋放 Socket 與 WorkerPool 資源
func (d *Daemon) Close() error {
	var err error
	d.closeOnce.Do(func() {
		close(d.closed)
		if d.listener != nil {
			err = d.listener.Close()
		}
		_ = os.Remove(d.cfg.SocketPath)
		// 優先等待所有 Worker 任務完全排水收尾 (Drain)，在此期間持續持有排他鎖 (修復 F-01)
		d.pool.Close()
		d.wg.Wait()

		// 所有工作完全收尾後才釋放鎖；不刪除鎖檔以避免破壞其他競爭者的 inode 鎖互斥性 (修復 F-01)
		if d.lockFile != nil {
			_ = syscall.Flock(int(d.lockFile.Fd()), syscall.LOCK_UN)
			_ = d.lockFile.Close()
			d.lockFile = nil
		}

		if d.readOnlyConfigCleanup != nil {
			d.readOnlyConfigCleanup()
			d.readOnlyConfigCleanup = nil
		}
	})
	return err
}

// 2026-09-13 WEB-002：所有任務回應與取消均沿用服務端政策。
func (d *Daemon) authorizeTask(task *storage.Task) error {
	if d.policy != nil {
		if err := d.policy.AuthorizeWorkspace(task.Workspace); err != nil {
			return fmt.Errorf("unauthorized workspace: %w", err)
		}
	}
	return nil
}

// Client 用於與 Daemon 通訊
type Client struct {
	sockPath string
	conn     net.Conn
}

// NewClient 建立與指定 Socket 的連線
func NewClient(sockPath string) (*Client, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("dial unix socket: %w", err)
	}
	return &Client{
		sockPath: sockPath,
		conn:     conn,
	}, nil
}

// Submit 透過 Unix Socket 向 Daemon 提交任務
func (c *Client) Submit(ctx context.Context, req engine.SubmitRequest) (*storage.Task, error) {
	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action:    "submit",
		RequestID: req.ID,
		Workspace: req.Workspace,
		Prompt:    req.Prompt,
		Mode:      req.Mode,
		Backend:   req.Backend,
		MemoryIDs: req.MemoryIDs,
	}); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != "" {
		if resp.Error == ErrCapacityExceeded.Error() {
			return resp.Task, ErrCapacityExceeded
		}
		return resp.Task, errors.New(resp.Error)
	}

	if resp.Task == nil {
		return nil, errors.New("empty task in response")
	}

	return resp.Task, nil
}

// GetTask 向 Daemon 查詢指定任務狀態 (INT-004)
func (c *Client) GetTask(ctx context.Context, taskID string) (*storage.Task, error) {
	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action: "status",
		TaskID: taskID,
	}); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}

	if resp.Task == nil {
		return nil, errors.New("task not found")
	}

	return resp.Task, nil
}

// CancelTask 向 Daemon 發送任務取消請求 (INT-005)
func (c *Client) CancelTask(ctx context.Context, taskID string) (*storage.Task, error) {
	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action: "cancel",
		TaskID: taskID,
	}); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}

	return resp.Task, nil
}

// AddCronSchedule 透過 Daemon IPC 新增定時排程 (CRON-006)
func (c *Client) AddCronSchedule(ctx context.Context, req CronAddRequest) (*storage.CronSchedule, error) {
	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action:     "cron_add",
		CronAddReq: &req,
	}); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}

	if resp.CronSchedule == nil {
		return nil, errors.New("empty cron schedule in response")
	}

	return resp.CronSchedule, nil
}

// ListCronSchedules 透過 Daemon IPC 查詢所有排程 (CRON-006)
func (c *Client) ListCronSchedules(ctx context.Context) ([]storage.CronSchedule, error) {
	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action: "cron_list",
	}); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}

	return resp.CronSchedules, nil
}

// PauseCronSchedule 透過 Daemon IPC 暫停排程 (CRON-006)
func (c *Client) PauseCronSchedule(ctx context.Context, cronID string) (*storage.CronSchedule, error) {
	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action: "cron_pause",
		CronID: cronID,
	}); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}

	if resp.CronSchedule == nil {
		return nil, errors.New("empty cron schedule in response")
	}

	return resp.CronSchedule, nil
}

// ResumeCronSchedule 透過 Daemon IPC 恢復排程 (CRON-006)
func (c *Client) ResumeCronSchedule(ctx context.Context, cronID string) (*storage.CronSchedule, error) {
	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action: "cron_resume",
		CronID: cronID,
	}); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}

	if resp.CronSchedule == nil {
		return nil, errors.New("empty cron schedule in response")
	}

	return resp.CronSchedule, nil
}

// RemoveCronSchedule 透過 Daemon IPC 刪除排程 (CRON-006)
func (c *Client) RemoveCronSchedule(ctx context.Context, cronID string) error {
	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action: "cron_remove",
		CronID: cronID,
	}); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != "" {
		return errors.New(resp.Error)
	}

	return nil
}

// ListCronRuns 透過 Daemon IPC 查詢排程歷史觸發紀錄 (CRON-007)
func (c *Client) ListCronRuns(ctx context.Context, cronID string, limit int) ([]storage.CronRun, error) {
	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action: "cron_runs",
		CronID: cronID,
		Limit:  limit,
	}); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}

	return resp.CronRuns, nil
}

// AddMemory 透過 Daemon IPC 新增專案記憶 (MEM-001)
func (c *Client) AddMemory(ctx context.Context, workspace, content string) (*storage.Memory, error) {
	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action:    "memory_add",
		Workspace: workspace,
		Content:   content,
	}); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}

	if resp.Memory == nil {
		return nil, errors.New("empty memory in response")
	}

	return resp.Memory, nil
}

// ListMemories 透過 Daemon IPC 查詢專案記憶清單 (MEM-001, MEM-002)
func (c *Client) ListMemories(ctx context.Context, workspace string) ([]storage.Memory, error) {
	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action:    "memory_list",
		Workspace: workspace,
	}); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}

	return resp.Memories, nil
}

// GetMemory 透過 Daemon IPC 查詢指定 ID 之記憶 (MEM-001, MEM-002)
func (c *Client) GetMemory(ctx context.Context, workspace, id string) (*storage.Memory, error) {
	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action:    "memory_get",
		Workspace: workspace,
		MemoryID:  id,
	}); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}

	if resp.Memory == nil {
		return nil, errors.New("memory not found")
	}

	return resp.Memory, nil
}

// DeleteMemory 透過 Daemon IPC 刪除指定 ID 之記憶 (MEM-005, MEM-002)
func (c *Client) DeleteMemory(ctx context.Context, workspace, id string) error {
	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action:    "memory_remove",
		Workspace: workspace,
		MemoryID:  id,
	}); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != "" {
		return errors.New(resp.Error)
	}

	return nil
}

// Close 關閉用戶端連線
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// calculateTaskFingerprint 計算任務請求指紋，包含 Backend、Mode 與排序後的 MemoryIDs 以確保 payload 一致性 (CTXMEM-004, TFACE-009, MBACK-004)
func calculateTaskFingerprint(workspace, prompt, mode string, memoryIDs []string, backendID string) string {
	fpPayload := workspace + ":" + prompt
	if mode != "" && mode != "direct" {
		fpPayload += ":mode=" + mode
	}
	if backendID == "" {
		backendID = "opencode"
	}
	fpPayload += ":backend=" + backendID
	if len(memoryIDs) > 0 {
		sortedIDs := make([]string, len(memoryIDs))
		copy(sortedIDs, memoryIDs)
		sort.Strings(sortedIDs)
		fpPayload += ":" + strings.Join(sortedIDs, ",")
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fpPayload)))
}

// normalizeMemoryIDs 將 MemoryIDs 去重並依照字母順序排序，定義為 Set 語意 (CTXMEM-004)
func normalizeMemoryIDs(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	sort.Strings(result)
	return result
}


