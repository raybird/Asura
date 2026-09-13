package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"asura/internal/auth"
	"asura/internal/backend/opencode"
	"asura/internal/engine"
	"asura/internal/storage"
)

var (
	// ErrDaemonAlreadyRunning 代表同一個 Socket 已有活躍的 Daemon 守護進程
	ErrDaemonAlreadyRunning = errors.New("daemon already running")
)

// DefaultApprovedPermissionPolicy 定義 INT-007 核准之安全操作邊界：
// 允許修改/build/test，但刪除重要資料、部署、push、對外傳送必須另行確認
const DefaultApprovedPermissionPolicy = `{"*":"ask","read":"allow","edit":"allow","build":"allow","test":"allow","bash":"ask"}`

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
	inFlight   int
	inFlightMu sync.Mutex
	listener   net.Listener
	lockFile   *os.File
	closed     chan struct{}
	closeOnce  sync.Once
	wg         sync.WaitGroup
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
		adapter := opencode.NewAdapter(opencode.AdapterConfig{
			BinaryPath:      d.cfg.OpenCodeConfig.BinaryPath,
			ExpectedVersion: d.cfg.OpenCodeConfig.ExpectedVersion,
			ConfigDir:       d.cfg.OpenCodeConfig.ConfigDir,
			Permission:      d.cfg.OpenCodeConfig.Permission,
			DisableAuto:     d.cfg.OpenCodeConfig.DisableAuto,
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
		d.engine = engine.NewEngine(store, policy, engine.WithRunner(adapter.Run))
	}

	return d
}

// Start 競爭取得 Unix Domain Socket 擁有權並開始監聽
// 遵循 SCN-003 與 R-01：以 flock 原子排他鎖消除 TOCTOU 競爭，確保單一擁有權無雙重 Daemon
func (d *Daemon) Start() error {
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

	d.wg.Add(1)
	go d.acceptLoop()

	return nil
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

// IPCRequest 定義用戶端提交之請求
type IPCRequest struct {
	Action    string    `json:"action,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	TaskID    string    `json:"task_id,omitempty"`
	Workspace string    `json:"workspace,omitempty"`
	Prompt    string    `json:"prompt,omitempty"`
	Class     TaskClass `json:"class,omitempty"`
}

// IPCResponse 定義傳回用戶端之結果
type IPCResponse struct {
	Task     *storage.Task `json:"task,omitempty"`
	Projects []string      `json:"projects,omitempty"`
	Error    string        `json:"error,omitempty"`
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
				d.wsQueue.Remove(target.Workspace, targetID)
				d.releaseCapacity()
			}
			err = d.engine.CancelTask(targetID)
			if err != nil {
				_ = encoder.Encode(IPCResponse{Error: err.Error()})
				continue
			}
			task, _ := d.store.GetTask(context.Background(), targetID)
			_ = encoder.Encode(IPCResponse{Task: task})

		case "projects":
			projects := []string{}
			if d.policy != nil {
				projects = d.policy.AllowedRoots()
			}
			_ = encoder.Encode(IPCResponse{Projects: projects})

		case "submit":
			// 遵循 INT-002 與 P3-T2：在進入 WorkerPool 與啟動後端前，Daemon 服務端直接校驗專案授權清單
			if d.policy != nil {
				if err := d.policy.AuthorizeWorkspace(req.Workspace); err != nil {
					_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("unauthorized workspace: %v", err)})
					continue
				}
			}

			// 計算請求 Fingerprint 確保同 Request ID 之 payload 一致性（防止撞 PK 或錯置）
			fp := fmt.Sprintf("%x", sha256.Sum256([]byte(req.Workspace+":"+req.Prompt)))

			// 遵循 INT-004-REDELIVERY：同 RequestID 且非 Cancelled 任務時直接回傳既有任務
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

			// 遵循 SCN-004 與 INT-003：檢查全域容量上限（Worker 執行槽位 + 佇列排隊槽位）
			if !d.tryAcquireCapacity() {
				_ = encoder.Encode(IPCResponse{Error: ErrCapacityExceeded.Error()})
				continue
			}

			taskID := engine.GenerateTaskID()
			now := time.Now().UTC()

			initialTask := &storage.Task{
				ID:          taskID,
				RequestID:   req.RequestID,
				Fingerprint: fp,
				Prompt:      req.Prompt,
				Workspace:   req.Workspace,
				Status:      storage.StatusPending,
				CreatedAt:   now,
				UpdatedAt:   now,
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

			// 遵循 CRON-001：同專案嚴格互斥依序排隊（僅創建任務之勝者競爭專案鎖）
			acquired, _ := d.wsQueue.TryAcquire(req.Workspace, taskID)
			if !acquired {
				// 專案忙碌中，更新狀態為 queued 並排入專案佇列，待前任務完成後依序喚醒
				initialTask.Status = storage.StatusQueued
				_ = d.store.UpdateTaskStatus(context.Background(), taskID, storage.StatusQueued, "", "")
				_ = encoder.Encode(IPCResponse{Task: initialTask})
				continue
			}

			taskClass := req.Class
			if taskClass == "" {
				taskClass = TaskClassInteractive
			}
			err := d.dispatchTask(taskID, req.Workspace, req.Prompt, taskClass)
			if err != nil {
				d.releaseCapacity()
				d.wsQueue.Release(req.Workspace, taskID)
				_ = d.store.DeleteTask(context.Background(), taskID)
				_ = encoder.Encode(IPCResponse{Error: err.Error()})
				continue
			}

			// 遵循 P4-T2：容量及持久化成功後、後端完成前回傳 Task ID
			_ = encoder.Encode(IPCResponse{Task: initialTask})

		default:
			_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("unknown action: %s", action)})
		}
	}
}

func (d *Daemon) dispatchTask(taskID, workspace, prompt string, class TaskClass) error {
	_, err := d.pool.SubmitAsyncClassified(context.Background(), class, func() error {
		defer func() {
			d.releaseCapacity()
			nextTaskID, hasNext := d.wsQueue.Release(workspace, taskID)
			if hasNext && nextTaskID != "" {
				d.wakeQueuedTask(nextTaskID, workspace, class)
			}
		}()

		_, runErr := d.engine.SubmitTask(context.Background(), engine.SubmitRequest{
			ID:        taskID,
			Workspace: workspace,
			Prompt:    prompt,
		})
		return runErr
	})
	return err
}

func (d *Daemon) wakeQueuedTask(taskID, workspace string, defaultClass TaskClass) {
	task, err := d.store.GetTask(context.Background(), taskID)
	if err != nil || task == nil {
		d.releaseCapacity()
		nextTaskID, hasNext := d.wsQueue.Release(workspace, taskID)
		if hasNext && nextTaskID != "" {
			d.wakeQueuedTask(nextTaskID, workspace, defaultClass)
		}
		return
	}

	if task.Status != storage.StatusQueued {
		d.releaseCapacity()
		nextTaskID, hasNext := d.wsQueue.Release(workspace, taskID)
		if hasNext && nextTaskID != "" {
			d.wakeQueuedTask(nextTaskID, workspace, defaultClass)
		}
		return
	}

	err = d.dispatchTask(task.ID, task.Workspace, task.Prompt, defaultClass)
	if err != nil {
		d.releaseCapacity()
		_ = d.store.UpdateTaskStatus(context.Background(), task.ID, storage.StatusFailed, "", err.Error())
		nextTaskID, hasNext := d.wsQueue.Release(workspace, taskID)
		if hasNext && nextTaskID != "" {
			d.wakeQueuedTask(nextTaskID, workspace, defaultClass)
		}
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

// Close 關閉用戶端連線
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
