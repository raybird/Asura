package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"asura/internal/auth"
	"asura/internal/engine"
	"asura/internal/storage"
)

var (
	// ErrDaemonAlreadyRunning 代表同一個 Socket 已有活躍的 Daemon 守護進程
	ErrDaemonAlreadyRunning = errors.New("daemon already running")
)

// Config 定義 Daemon 運行參數
type Config struct {
	SocketPath string
	MaxWorkers int
	MaxQueue   int
}

// Daemon 實作單一守護進程中樞
type Daemon struct {
	cfg       Config
	store     *storage.SQLiteStore
	policy    *auth.Policy
	engine    *engine.Engine
	pool      *WorkerPool
	listener  net.Listener
	closed    chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// DaemonOption 用於自訂 Daemon 設定
type DaemonOption func(*Daemon)

// WithEngine 注入既有的 Engine 實例
func WithEngine(eng *engine.Engine) DaemonOption {
	return func(d *Daemon) {
		d.engine = eng
	}
}

// NewDaemon 建立新的 Daemon 實例
func NewDaemon(cfg Config, store *storage.SQLiteStore, policy *auth.Policy, opts ...DaemonOption) *Daemon {
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 2
	}
	if cfg.MaxQueue < 0 {
		cfg.MaxQueue = 10
	}

	d := &Daemon{
		cfg:    cfg,
		store:  store,
		policy: policy,
		pool:   NewWorkerPool(cfg.MaxWorkers, cfg.MaxQueue),
		closed: make(chan struct{}),
	}

	for _, opt := range opts {
		opt(d)
	}

	if d.engine == nil {
		d.engine = engine.NewEngine(store, policy)
	}

	return d
}

// Start 競爭取得 Unix Domain Socket 擁有權並開始監聽
// 遵循 SCN-003：競爭啟動無雙重擁有者，只有一個進程成為 Daemon，其餘收到 ErrDaemonAlreadyRunning
func (d *Daemon) Start() error {
	// 1. 探測既有 Socket 是否有存活 Daemon
	if conn, err := net.DialTimeout("unix", d.cfg.SocketPath, 100*time.Millisecond); err == nil {
		_ = conn.Close()
		return ErrDaemonAlreadyRunning
	}

	// 2. 清理過期殘留 socket 檔案
	_ = os.Remove(d.cfg.SocketPath)

	// 3. 綁定監聽
	l, err := net.Listen("unix", d.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen unix socket: %w", err)
	}
	d.listener = l

	// 4. 執行重啟恢復：將前次未完成的殘留任務安全轉移為 failed，揭露中斷原因（SCN-006）
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
	Workspace string `json:"workspace"`
	Prompt    string `json:"prompt"`
}

// IPCResponse 定義傳回用戶端之結果
type IPCResponse struct {
	Task  *storage.Task `json:"task,omitempty"`
	Error string        `json:"error,omitempty"`
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	var req IPCRequest
	if err := decoder.Decode(&req); err != nil {
		_ = encoder.Encode(IPCResponse{Error: "invalid request payload"})
		return
	}

	var task *storage.Task
	var submitErr error

	// 透過 WorkerPool 限流調度
	err := d.pool.Submit(context.Background(), func() error {
		task, submitErr = d.engine.SubmitTask(context.Background(), engine.SubmitRequest{
			Workspace: req.Workspace,
			Prompt:    req.Prompt,
		})
		return submitErr
	})

	if errors.Is(err, ErrCapacityExceeded) {
		_ = encoder.Encode(IPCResponse{Error: ErrCapacityExceeded.Error()})
		return
	}

	if submitErr != nil {
		_ = encoder.Encode(IPCResponse{Task: task, Error: submitErr.Error()})
		return
	}

	_ = encoder.Encode(IPCResponse{Task: task})
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
		d.pool.Close()
		d.wg.Wait()
	})
	return err
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
			return nil, ErrCapacityExceeded
		}
		return nil, errors.New(resp.Error)
	}

	if resp.Task == nil {
		return nil, errors.New("empty task in response")
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
