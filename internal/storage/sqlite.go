package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// TaskStatus 代表任務的生命週期狀態
type TaskStatus string

const (
	StatusPending   TaskStatus = "pending"
	StatusRunning   TaskStatus = "running"
	StatusCompleted TaskStatus = "completed"
	StatusFailed    TaskStatus = "failed"
	StatusCancelled TaskStatus = "cancelled"
)

var (
	// ErrTaskNotFound 代表查詢的任務不存在
	ErrTaskNotFound = errors.New("task not found")
)

// Task 代表本機執行的任務實體
type Task struct {
	ID           string     `json:"id"`
	Prompt       string     `json:"prompt"`
	Workspace    string     `json:"workspace"`
	Status       TaskStatus `json:"status"`
	Result       string     `json:"result"`
	ErrorMessage string     `json:"error_message"`
	PGID         int        `json:"pgid"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// SQLiteStore 管理任務資料庫與單一連線池
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore 初始化 SQLite 資料庫並配置 WAL 模式與單一連線
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	// 限制單一連線池，杜絕跨連線寫入鎖死
	db.SetMaxOpenConns(1)

	// 配置 busy_timeout 與 WAL 模式（busy_timeout 必須最先執行以防止並行開庫鎖死）
	pragmas := []string{
		"PRAGMA busy_timeout=5000;",
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("exec pragma %s: %w", pragma, err)
		}
	}

	store := &SQLiteStore{db: db}
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		prompt TEXT NOT NULL,
		workspace TEXT NOT NULL,
		status TEXT NOT NULL,
		result TEXT NOT NULL DEFAULT '',
		error_message TEXT NOT NULL DEFAULT '',
		pgid INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
	`
	_, err := s.db.Exec(schema)
	return err
}

// CreateTask 將新任務寫入資料庫
func (s *SQLiteStore) CreateTask(ctx context.Context, task *Task) error {
	if task == nil {
		return errors.New("task cannot be nil")
	}
	query := `
	INSERT INTO tasks (id, prompt, workspace, status, result, error_message, pgid, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	now := time.Now().UTC()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = now
	}

	_, err := s.db.ExecContext(ctx, query,
		task.ID,
		task.Prompt,
		task.Workspace,
		string(task.Status),
		task.Result,
		task.ErrorMessage,
		task.PGID,
		task.CreatedAt,
		task.UpdatedAt,
	)
	return err
}

// GetTask 依任務 ID 查詢任務狀態與結果
func (s *SQLiteStore) GetTask(ctx context.Context, id string) (*Task, error) {
	query := `
	SELECT id, prompt, workspace, status, result, error_message, pgid, created_at, updated_at
	FROM tasks
	WHERE id = ?
	`
	row := s.db.QueryRowContext(ctx, query, id)

	var task Task
	var statusStr string
	err := row.Scan(
		&task.ID,
		&task.Prompt,
		&task.Workspace,
		&statusStr,
		&task.Result,
		&task.ErrorMessage,
		&task.PGID,
		&task.CreatedAt,
		&task.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query task: %w", err)
	}

	task.Status = TaskStatus(statusStr)
	return &task, nil
}

// UpdateTaskStatus 更新指定任務的狀態、輸出結果或錯誤訊息
func (s *SQLiteStore) UpdateTaskStatus(ctx context.Context, id string, status TaskStatus, result string, errMsg string) error {
	query := `
	UPDATE tasks
	SET status = ?, result = ?, error_message = ?, updated_at = ?
	WHERE id = ?
	`
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, query, string(status), result, errMsg, now, id)
	if err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// UpdateTaskPGID 更新任務關聯的進程組 ID
func (s *SQLiteStore) UpdateTaskPGID(ctx context.Context, id string, pgid int) error {
	query := `
	UPDATE tasks
	SET pgid = ?, updated_at = ?
	WHERE id = ?
	`
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, query, pgid, now, id)
	if err != nil {
		return fmt.Errorf("update task pgid: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// Close 關閉資料庫連線
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
