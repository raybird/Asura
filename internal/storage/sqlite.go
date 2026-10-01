package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// TaskStatus 代表任務的生命週期狀態
type TaskStatus string

const (
	StatusPending    TaskStatus = "pending"
	StatusQueued     TaskStatus = "queued"
	StatusRunning    TaskStatus = "running"
	StatusCancelling TaskStatus = "cancelling"
	StatusCompleted  TaskStatus = "completed"
	StatusFailed     TaskStatus = "failed"
	StatusCancelled  TaskStatus = "cancelled"
)

var (
	// ErrTaskNotFound 代表查詢的任務不存在
	ErrTaskNotFound = errors.New("task not found")
	// ErrDuplicateRequestID 代表相同 request_id 的任務已存在
	ErrDuplicateRequestID = errors.New("duplicate request_id")
	// ErrCronNotFound 代表查詢的排程不存在
	ErrCronNotFound = errors.New("cron schedule not found")
)

// CronStatus 代表排程的啟用狀態
type CronStatus string

const (
	CronStatusActive CronStatus = "active"
	CronStatusPaused CronStatus = "paused"
)

// CronRunStatus 代表單次排程觸發的執行狀態
type CronRunStatus string

const (
	CronRunTriggered    CronRunStatus = "triggered"
	CronRunSkipped      CronRunStatus = "skipped"
	CronRunUnauthorized CronRunStatus = "unauthorized"
	CronRunFailed       CronRunStatus = "failed"
)

// CronSchedule 代表定時排程的定義實體
type CronSchedule struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Workspace   string     `json:"workspace"`
	Schedule    string     `json:"schedule"`
	Prompt      string     `json:"prompt"`
	Status      CronStatus `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastRunAt   *time.Time `json:"last_run_at,omitempty"`
	NextRunAt   *time.Time `json:"next_run_at,omitempty"`
	PauseReason string     `json:"pause_reason,omitempty"`
}

// CronRun 代表一次定時排程的觸發紀錄
type CronRun struct {
	ID            string        `json:"id"`
	CronID        string        `json:"cron_id"`
	ScheduledTime time.Time     `json:"scheduled_time"`
	TriggeredTime time.Time     `json:"triggered_time"`
	TaskID        string        `json:"task_id,omitempty"`
	Status        CronRunStatus `json:"status"`
	Reason        string        `json:"reason,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
}

// Task 代表本機執行的任務實體
type Task struct {
	UtilityStatus   string     `json:"utility_status,omitempty"`
	UtilityError    string     `json:"utility_error,omitempty"`
	ID              string     `json:"id"`
	RequestID       string     `json:"request_id,omitempty"`
	Fingerprint     string     `json:"fingerprint,omitempty"`
	MemoryRefs      []string   `json:"memory_refs,omitempty"`
	ResolvedContext string     `json:"resolved_context,omitempty"`
	Prompt          string     `json:"prompt"`
	Workspace       string     `json:"workspace"`
	Status          TaskStatus `json:"status"`
	Result          string     `json:"result"`
	ErrorMessage    string     `json:"error_message"`
	PGID            int        `json:"pgid"`
	Class           string     `json:"class,omitempty"`
	ExecutionMode   string     `json:"execution_mode,omitempty"`
	Backend         string     `json:"backend,omitempty"`
	PipelineTrace   string     `json:"pipeline_trace,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
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

	// 配置 busy_timeout 與 WAL 模式（若遇到並發開庫鎖定則短暫重試）
	pragmas := []string{
		"PRAGMA busy_timeout=10000;",
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA foreign_keys=ON;",
	}
	for _, pragma := range pragmas {
		var execErr error
		for attempt := 0; attempt < 20; attempt++ {
			if _, execErr = db.Exec(pragma); execErr == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if execErr != nil {
			_ = db.Close()
			return nil, fmt.Errorf("exec pragma %s: %w", pragma, execErr)
		}
	}

	store := &SQLiteStore{db: db}
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	if err := store.initUtilitySchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init utility schema: %w", err)
	}
	return store, nil
}

func (s *SQLiteStore) initSchema() error {
	createTable := `
	CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		request_id TEXT NOT NULL DEFAULT '',
		fingerprint TEXT NOT NULL DEFAULT '',
		memory_refs TEXT NOT NULL DEFAULT '',
		resolved_context TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL,
		workspace TEXT NOT NULL,
		status TEXT NOT NULL,
		result TEXT NOT NULL DEFAULT '',
		error_message TEXT NOT NULL DEFAULT '',
		pgid INTEGER NOT NULL DEFAULT 0,
		class TEXT NOT NULL DEFAULT 'interactive',
		execution_mode TEXT NOT NULL DEFAULT 'direct',
		backend TEXT NOT NULL DEFAULT 'opencode',
		pipeline_trace TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	`
	if _, err := s.db.Exec(createTable); err != nil {
		return fmt.Errorf("create table: %w", err)
	}

	// 容錯升級既有表：先確保 request_id、fingerprint、memory_refs、resolved_context、class、execution_mode、backend 與 pipeline_trace 欄位存在再建立索引
	cols, err := s.getTableColumns("tasks")
	if err != nil {
		return fmt.Errorf("get table columns: %w", err)
	}
	if !cols["request_id"] {
		if _, err := s.db.Exec("ALTER TABLE tasks ADD COLUMN request_id TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add column request_id: %w", err)
		}
	}
	if !cols["fingerprint"] {
		if _, err := s.db.Exec("ALTER TABLE tasks ADD COLUMN fingerprint TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add column fingerprint: %w", err)
		}
	}
	if !cols["memory_refs"] {
		if _, err := s.db.Exec("ALTER TABLE tasks ADD COLUMN memory_refs TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add column memory_refs: %w", err)
		}
	}
	if !cols["resolved_context"] {
		if _, err := s.db.Exec("ALTER TABLE tasks ADD COLUMN resolved_context TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add column resolved_context: %w", err)
		}
	}
	if !cols["class"] {
		if _, err := s.db.Exec("ALTER TABLE tasks ADD COLUMN class TEXT NOT NULL DEFAULT 'interactive'"); err != nil {
			return fmt.Errorf("add column class: %w", err)
		}
	}
	if !cols["execution_mode"] {
		if _, err := s.db.Exec("ALTER TABLE tasks ADD COLUMN execution_mode TEXT NOT NULL DEFAULT 'direct'"); err != nil {
			return fmt.Errorf("add column execution_mode: %w", err)
		}
	}
	if !cols["backend"] {
		if _, err := s.db.Exec("ALTER TABLE tasks ADD COLUMN backend TEXT NOT NULL DEFAULT 'opencode'"); err != nil {
			return fmt.Errorf("add column backend: %w", err)
		}
	}
	if !cols["pipeline_trace"] {
		if _, err := s.db.Exec("ALTER TABLE tasks ADD COLUMN pipeline_trace TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add column pipeline_trace: %w", err)
		}
	}

	// 針對既有資料庫中可能已存在的重複 request_id，執行歷史保留之去重遷移：
	// 保留 created_at 最早（同時間則 id 字典序較小）的任務之 request_id，其餘重複任務之 request_id 降級為 ''
	// 完整保留所有歷史任務本體、執行結果與狀態，同時消除建立部分唯一索引之衝突。
	dedupQuery := `
	UPDATE tasks
	SET request_id = ''
	WHERE request_id != ''
	  AND id NOT IN (
	    SELECT id FROM (
	      SELECT id, ROW_NUMBER() OVER (
	        PARTITION BY request_id 
	        ORDER BY created_at ASC, id ASC
	      ) AS rn
	      FROM tasks
	      WHERE request_id != ''
	    ) WHERE rn = 1
	  );
	`
	if _, err := s.db.Exec(dedupQuery); err != nil {
		return fmt.Errorf("deduplicate existing request_ids: %w", err)
	}

	indices := `
	CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_request_id_unique ON tasks(request_id) WHERE request_id != '';

	CREATE TABLE IF NOT EXISTS cron_schedules (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		workspace TEXT NOT NULL,
		schedule TEXT NOT NULL,
		prompt TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		last_run_at DATETIME,
		next_run_at DATETIME,
		pause_reason TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS cron_runs (
		id TEXT PRIMARY KEY,
		cron_id TEXT NOT NULL,
		scheduled_time DATETIME NOT NULL,
		triggered_time DATETIME NOT NULL,
		task_id TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		FOREIGN KEY (cron_id) REFERENCES cron_schedules(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_cron_schedules_status ON cron_schedules(status);
	CREATE INDEX IF NOT EXISTS idx_cron_runs_cron_id ON cron_runs(cron_id);
	CREATE INDEX IF NOT EXISTS idx_cron_runs_scheduled_time ON cron_runs(scheduled_time);

	CREATE TABLE IF NOT EXISTS memories (
		id TEXT PRIMARY KEY,
		workspace TEXT NOT NULL,
		content TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT 'manual',
		created_at DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_memories_workspace ON memories(workspace);
	CREATE INDEX IF NOT EXISTS idx_memories_workspace_id ON memories(workspace, id);
	CREATE INDEX IF NOT EXISTS idx_memories_workspace_created_id ON memories(workspace, created_at, id);

	CREATE TABLE IF NOT EXISTS memory_candidates (
		id TEXT PRIMARY KEY,
		workspace TEXT NOT NULL,
		repo_id TEXT NOT NULL,
		ref_name TEXT NOT NULL,
		base_sha TEXT NOT NULL,
		head_sha TEXT NOT NULL,
		commit_count INTEGER NOT NULL,
		facts_summary TEXT NOT NULL,
		draft_content TEXT NOT NULL,
		status TEXT NOT NULL,
		policy_version TEXT NOT NULL DEFAULT 'gitmem-v1',
		approved_memory_id TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		approved_at DATETIME,
		is_truncated INTEGER NOT NULL DEFAULT 0,
		truncated_reason TEXT NOT NULL DEFAULT '',
		is_user_edited INTEGER NOT NULL DEFAULT 0,
		UNIQUE(workspace, repo_id, ref_name, base_sha, head_sha, policy_version)
	);

	CREATE INDEX IF NOT EXISTS idx_candidates_workspace_status ON memory_candidates(workspace, status);

	CREATE TABLE IF NOT EXISTS git_checkpoints (
		workspace TEXT NOT NULL,
		repo_id TEXT NOT NULL,
		ref_name TEXT NOT NULL,
		last_captured_sha TEXT NOT NULL,
		updated_at DATETIME NOT NULL,
		PRIMARY KEY (workspace, repo_id, ref_name)
	);

	CREATE TABLE IF NOT EXISTS memory_provenance (
		memory_id TEXT PRIMARY KEY,
		candidate_id TEXT NOT NULL,
		workspace TEXT NOT NULL,
		repo_id TEXT NOT NULL,
		ref_name TEXT NOT NULL,
		base_sha TEXT NOT NULL,
		head_sha TEXT NOT NULL,
		commit_count INTEGER NOT NULL,
		commit_shas_json TEXT NOT NULL DEFAULT '[]',
		facts_summary TEXT NOT NULL DEFAULT '',
		is_user_edited INTEGER NOT NULL DEFAULT 0,
		approved_at DATETIME NOT NULL,
		FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_provenance_workspace ON memory_provenance(workspace);
	CREATE INDEX IF NOT EXISTS idx_provenance_candidate ON memory_provenance(candidate_id);

	CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL
	);
	`
	if _, err := s.db.Exec(indices); err != nil {
		return fmt.Errorf("create indices, cron, and memory tables: %w", err)
	}

	candCols, err := s.getTableColumns("memory_candidates")
	if err != nil {
		return fmt.Errorf("check memory_candidates columns: %w", err)
	}
	if len(candCols) > 0 {
		if !candCols["is_truncated"] {
			if _, err := s.db.Exec("ALTER TABLE memory_candidates ADD COLUMN is_truncated INTEGER NOT NULL DEFAULT 0"); err != nil {
				return fmt.Errorf("add column is_truncated to memory_candidates: %w", err)
			}
		}
		if !candCols["truncated_reason"] {
			if _, err := s.db.Exec("ALTER TABLE memory_candidates ADD COLUMN truncated_reason TEXT NOT NULL DEFAULT ''"); err != nil {
				return fmt.Errorf("add column truncated_reason to memory_candidates: %w", err)
			}
		}
		if !candCols["is_user_edited"] {
			if _, err := s.db.Exec("ALTER TABLE memory_candidates ADD COLUMN is_user_edited INTEGER NOT NULL DEFAULT 0"); err != nil {
				return fmt.Errorf("add column is_user_edited to memory_candidates: %w", err)
			}
		}
		// clear-candidates 會清空 candidate facts，因此舊資料也需從 provenance 回填 Partial 狀態。
		backfillQuery := `
		UPDATE memory_candidates
		SET is_truncated = 1
		WHERE is_truncated = 0 AND (
			facts_summary LIKE '%"truncated":true%'
			OR id IN (
				SELECT candidate_id FROM memory_provenance WHERE facts_summary LIKE '%"truncated":true%'
			)
		);
		`
		if _, err := s.db.Exec(backfillQuery); err != nil {
			return fmt.Errorf("backfill is_truncated on memory_candidates: %w", err)
		}

		// 新規則不允許 Partial 核准。將舊版 approved + partial 隔離到舊 policy，
		// 保留原審核記錄，並讓下次 gitmem-v1 完整擷取可建立新候選。
		quarantineLegacyApprovedPartial := `
		UPDATE memory_candidates
		SET policy_version = 'gitmem-v1-legacy-approved-partial'
		WHERE status = 'approved'
		  AND is_truncated = 1
		  AND policy_version = 'gitmem-v1';
		`
		if _, err := s.db.Exec(quarantineLegacyApprovedPartial); err != nil {
			return fmt.Errorf("quarantine legacy approved partial candidates: %w", err)
		}

		quarantineLegacyPartialMemories := `
		UPDATE memories
		SET source = 'git-approved-legacy-partial'
		WHERE source = 'git-approved'
		  AND id IN (
			SELECT p.memory_id
			FROM memory_provenance p
			JOIN memory_candidates c ON c.id = p.candidate_id
			WHERE c.policy_version = 'gitmem-v1-legacy-approved-partial'
		  );
		`
		if _, err := s.db.Exec(quarantineLegacyPartialMemories); err != nil {
			return fmt.Errorf("quarantine legacy approved partial memories: %w", err)
		}

		// 以持久化 migration marker 與隔離更新的同一交易保證 crash-safety。
		// 若舊版在 ALTER TABLE 後崩潰，marker 仍不存在，下次啟動會重新執行隔離。
		const legacyCompletenessMigration = "gitmem-legacy-completeness-v1"
		var migrationApplied int
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = ?)`, legacyCompletenessMigration).Scan(&migrationApplied); err != nil {
			return fmt.Errorf("check legacy completeness migration: %w", err)
		}
		if migrationApplied == 0 {
			tx, err := s.db.Begin()
			if err != nil {
				return fmt.Errorf("begin legacy completeness migration: %w", err)
			}
			defer tx.Rollback()

			quarantineLegacyUnknown := `
			UPDATE memory_candidates
			SET policy_version = 'gitmem-v1-legacy-unknown'
			WHERE status = 'approved'
			  AND facts_summary = '{}'
			  AND policy_version = 'gitmem-v1'
			  AND NOT EXISTS (
				SELECT 1 FROM memory_provenance p
				WHERE p.candidate_id = memory_candidates.id
			  )
			  AND NOT EXISTS (
				SELECT 1 FROM memory_candidates legacy
				WHERE legacy.workspace = memory_candidates.workspace
				  AND legacy.repo_id = memory_candidates.repo_id
				  AND legacy.ref_name = memory_candidates.ref_name
				  AND legacy.base_sha = memory_candidates.base_sha
				  AND legacy.head_sha = memory_candidates.head_sha
				  AND legacy.policy_version = 'gitmem-v1-legacy-unknown'
			  );
			`
			if _, err := tx.Exec(quarantineLegacyUnknown); err != nil {
				return fmt.Errorf("quarantine legacy candidates with unknown completeness: %w", err)
			}

			quarantineLegacyUnknownMemories := `
			UPDATE memories
			SET source = 'git-approved-legacy-unknown'
			WHERE source = 'git-approved'
			  AND id IN (
				SELECT approved_memory_id
				FROM memory_candidates
				WHERE policy_version = 'gitmem-v1-legacy-unknown'
			  );
			`
			if _, err := tx.Exec(quarantineLegacyUnknownMemories); err != nil {
				return fmt.Errorf("quarantine legacy approved memories with unknown completeness: %w", err)
			}
			if _, err := tx.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`, legacyCompletenessMigration, time.Now().UTC()); err != nil {
				return fmt.Errorf("record legacy completeness migration: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit legacy completeness migration: %w", err)
			}
		}
	}
	return nil
}

func (s *SQLiteStore) getTableColumns(tableName string) (map[string]bool, error) {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// CreateTask 將新任務寫入資料庫
func (s *SQLiteStore) createTask(ctx context.Context, task *Task, executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) error {
	if task == nil {
		return errors.New("task cannot be nil")
	}

	memRefsJSON := ""
	if len(task.MemoryRefs) > 0 {
		b, _ := json.Marshal(task.MemoryRefs)
		memRefsJSON = string(b)
	}

	query := `
	INSERT INTO tasks (id, request_id, fingerprint, memory_refs, resolved_context, prompt, workspace, status, result, error_message, pgid, class, execution_mode, backend, pipeline_trace, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	now := time.Now().UTC()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = now
	}
	taskClass := task.Class
	if taskClass == "" {
		taskClass = "interactive"
	}
	mode := task.ExecutionMode
	if mode == "" {
		mode = "direct"
	}
	backendID := task.Backend
	if backendID == "" {
		backendID = "opencode"
	}

	_, err := executor.ExecContext(ctx, query,
		task.ID,
		task.RequestID,
		task.Fingerprint,
		memRefsJSON,
		task.ResolvedContext,
		task.Prompt,
		task.Workspace,
		string(task.Status),
		task.Result,
		task.ErrorMessage,
		task.PGID,
		taskClass,
		mode,
		backendID,
		task.PipelineTrace,
		task.CreatedAt,
		task.UpdatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") && strings.Contains(err.Error(), "request_id") {
			return ErrDuplicateRequestID
		}
		return err
	}
	return nil
}

// GetTask 依任務 ID 查詢任務狀態與結果
func (s *SQLiteStore) GetTask(ctx context.Context, id string) (*Task, error) {
	query := `
	SELECT id, request_id, fingerprint, memory_refs, resolved_context, prompt, workspace, status, result, error_message, pgid, class, execution_mode, backend, pipeline_trace, created_at, updated_at
	FROM tasks
	WHERE id = ?
	`
	row := s.db.QueryRowContext(ctx, query, id)

	var task Task
	var statusStr string
	var memRefsRaw string
	err := row.Scan(
		&task.ID,
		&task.RequestID,
		&task.Fingerprint,
		&memRefsRaw,
		&task.ResolvedContext,
		&task.Prompt,
		&task.Workspace,
		&statusStr,
		&task.Result,
		&task.ErrorMessage,
		&task.PGID,
		&task.Class,
		&task.ExecutionMode,
		&task.Backend,
		&task.PipelineTrace,
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
	if task.ExecutionMode == "" {
		task.ExecutionMode = "direct"
	}
	if task.Backend == "" {
		task.Backend = "opencode"
	}
	if memRefsRaw != "" {
		_ = json.Unmarshal([]byte(memRefsRaw), &task.MemoryRefs)
	}
	if task.MemoryRefs == nil {
		task.MemoryRefs = []string{}
	}
	return &task, nil
}

// GetTaskByRequestID 依用戶端 Request ID 查詢最新任務
func (s *SQLiteStore) GetTaskByRequestID(ctx context.Context, requestID string) (*Task, error) {
	if requestID == "" {
		return nil, ErrTaskNotFound
	}
	query := `
	SELECT id, request_id, fingerprint, memory_refs, resolved_context, prompt, workspace, status, result, error_message, pgid, class, execution_mode, backend, pipeline_trace, created_at, updated_at
	FROM tasks
	WHERE request_id = ?
	ORDER BY created_at DESC
	LIMIT 1
	`
	row := s.db.QueryRowContext(ctx, query, requestID)

	var task Task
	var statusStr string
	var memRefsRaw string
	err := row.Scan(
		&task.ID,
		&task.RequestID,
		&task.Fingerprint,
		&memRefsRaw,
		&task.ResolvedContext,
		&task.Prompt,
		&task.Workspace,
		&statusStr,
		&task.Result,
		&task.ErrorMessage,
		&task.PGID,
		&task.Class,
		&task.ExecutionMode,
		&task.Backend,
		&task.PipelineTrace,
		&task.CreatedAt,
		&task.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query task by request_id: %w", err)
	}

	task.Status = TaskStatus(statusStr)
	if task.ExecutionMode == "" {
		task.ExecutionMode = "direct"
	}
	if task.Backend == "" {
		task.Backend = "opencode"
	}
	if memRefsRaw != "" {
		_ = json.Unmarshal([]byte(memRefsRaw), &task.MemoryRefs)
	}
	if task.MemoryRefs == nil {
		task.MemoryRefs = []string{}
	}
	return &task, nil
}

// UpdateTaskPipelineTrace 更新指定任務的管線追蹤審計紀錄
func (s *SQLiteStore) UpdateTaskPipelineTrace(ctx context.Context, id string, trace string) error {
	query := `
	UPDATE tasks
	SET pipeline_trace = ?, updated_at = ?
	WHERE id = ?
	`
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, query, trace, now, id)
	if err != nil {
		return fmt.Errorf("update task pipeline trace: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rows == 0 {
		return ErrTaskNotFound
	}
	return nil
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

// UpdateTaskStatusIfPending 僅在任務處於 pending 狀態時才轉移為新狀態，防止覆寫已被外部取消的狀態 (修復 F-02)
func (s *SQLiteStore) UpdateTaskStatusIfPending(ctx context.Context, id string, status TaskStatus) (bool, error) {
	query := `
	UPDATE tasks
	SET status = ?, updated_at = ?
	WHERE id = ? AND status = ?
	`
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, query, string(status), now, id, string(StatusPending))
	if err != nil {
		return false, fmt.Errorf("update task status if pending: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check rows affected: %w", err)
	}
	return rowsAffected > 0, nil
}

// UpdateTaskStatusIfPendingOrQueued 僅在任務處於 pending 或 queued 狀態時才轉移為新狀態
func (s *SQLiteStore) UpdateTaskStatusIfPendingOrQueued(ctx context.Context, id string, status TaskStatus) (bool, error) {
	query := `
	UPDATE tasks
	SET status = ?, updated_at = ?
	WHERE id = ? AND (status = ? OR status = ?)
	`
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, query, string(status), now, id, string(StatusPending), string(StatusQueued))
	if err != nil {
		return false, fmt.Errorf("update task status if pending or queued: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check rows affected: %w", err)
	}
	return rowsAffected > 0, nil
}

// UpdateTaskStatusIfRunning 僅在任務處於 running 狀態時才轉移狀態，防止覆寫已被外部取消的狀態 (修復 R-10)
func (s *SQLiteStore) UpdateTaskStatusIfRunning(ctx context.Context, id string, status TaskStatus, result string, errMsg string) (bool, error) {
	query := `
	UPDATE tasks
	SET status = ?, result = ?, error_message = ?, updated_at = ?
	WHERE id = ? AND status = ?
	`
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, query, string(status), result, errMsg, now, id, string(StatusRunning))
	if err != nil {
		return false, fmt.Errorf("update task status if running: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check rows affected: %w", err)
	}
	return rowsAffected > 0, nil
}

// UpdateTaskStatusIfPendingOrQueuedWithMsg 僅在任務處於 pending 或 queued 狀態時才轉移為新狀態，並更新 result 與 error_message
func (s *SQLiteStore) UpdateTaskStatusIfPendingOrQueuedWithMsg(ctx context.Context, id string, status TaskStatus, result string, errMsg string) (bool, error) {
	query := `
	UPDATE tasks
	SET status = ?, result = ?, error_message = ?, updated_at = ?
	WHERE id = ? AND (status = ? OR status = ?)
	`
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, query, string(status), result, errMsg, now, id, string(StatusPending), string(StatusQueued))
	if err != nil {
		return false, fmt.Errorf("update task status if pending or queued: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check rows affected: %w", err)
	}
	return rowsAffected > 0, nil
}

// UpdateTaskStatusIfCancelling 僅在任務處於 cancelling 狀態時才轉移狀態，防止覆寫終態
func (s *SQLiteStore) UpdateTaskStatusIfCancelling(ctx context.Context, id string, status TaskStatus, result string, errMsg string) (bool, error) {
	query := `
	UPDATE tasks
	SET status = ?, result = ?, error_message = ?, updated_at = ?
	WHERE id = ? AND status = ?
	`
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, query, string(status), result, errMsg, now, id, string(StatusCancelling))
	if err != nil {
		return false, fmt.Errorf("update task status if cancelling: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check rows affected: %w", err)
	}
	return rowsAffected > 0, nil
}


// UpdateTaskResult 僅更新任務的輸出結果，保留既有狀態與錯誤訊息 (修復 R-06)
func (s *SQLiteStore) UpdateTaskResult(ctx context.Context, id string, result string) error {
	query := `
	UPDATE tasks
	SET result = ?, updated_at = ?
	WHERE id = ?
	`
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, query, result, now, id)
	if err != nil {
		return fmt.Errorf("update task result: %w", err)
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

// RecoverInterruptedTasks 掃描並將所有處於 running, pending, queued 或 cancelling 狀態的孤兒任務標記為 failed
// 遵循 SCN-006 與 MUST FIX 5：守護程序重啟時保留已完成結果，揭露中斷工作，杜絕假成功、自動重新執行或排隊殘留
func (s *SQLiteStore) RecoverInterruptedTasks(ctx context.Context) (int64, error) {
	query := `
	UPDATE tasks
	SET status = ?, error_message = ?, updated_at = ?
	WHERE status IN (?, ?, ?, ?)
	`
	now := time.Now().UTC()
	msg := "task interrupted: system restarted or terminated unexpectedly"
	res, err := s.db.ExecContext(ctx, query, StatusFailed, msg, now, StatusRunning, StatusPending, StatusQueued, StatusCancelling)
	if err != nil {
		return 0, fmt.Errorf("recover interrupted tasks: %w", err)
	}
	return res.RowsAffected()
}

// DeleteTask 依 ID 刪除指定任務
func (s *SQLiteStore) DeleteTask(ctx context.Context, id string) error {
	query := `DELETE FROM tasks WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	return nil
}

// Close 關閉資料庫連線
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// CreateCronSchedule 建立新的定時排程定義
func (s *SQLiteStore) CreateCronSchedule(ctx context.Context, schedule CronSchedule) error {
	query := `
	INSERT INTO cron_schedules (
		id, name, workspace, schedule, prompt, status, created_at, updated_at, last_run_at, next_run_at, pause_reason
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	var lastRunVal, nextRunVal any
	if schedule.LastRunAt != nil {
		lastRunVal = schedule.LastRunAt.UTC()
	}
	if schedule.NextRunAt != nil {
		nextRunVal = schedule.NextRunAt.UTC()
	}
	_, err := s.db.ExecContext(ctx, query,
		schedule.ID,
		schedule.Name,
		schedule.Workspace,
		schedule.Schedule,
		schedule.Prompt,
		schedule.Status,
		schedule.CreatedAt.UTC(),
		schedule.UpdatedAt.UTC(),
		lastRunVal,
		nextRunVal,
		schedule.PauseReason,
	)
	if err != nil {
		return fmt.Errorf("create cron schedule: %w", err)
	}
	return nil
}

// GetCronSchedule 依 ID 取得排程實體
func (s *SQLiteStore) GetCronSchedule(ctx context.Context, id string) (*CronSchedule, error) {
	query := `
	SELECT id, name, workspace, schedule, prompt, status, created_at, updated_at, last_run_at, next_run_at, pause_reason
	FROM cron_schedules
	WHERE id = ?
	`
	row := s.db.QueryRowContext(ctx, query, id)
	var sched CronSchedule
	var lastRun, nextRun sql.NullTime
	err := row.Scan(
		&sched.ID,
		&sched.Name,
		&sched.Workspace,
		&sched.Schedule,
		&sched.Prompt,
		&sched.Status,
		&sched.CreatedAt,
		&sched.UpdatedAt,
		&lastRun,
		&nextRun,
		&sched.PauseReason,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCronNotFound
		}
		return nil, fmt.Errorf("get cron schedule: %w", err)
	}
	if lastRun.Valid {
		t := lastRun.Time
		sched.LastRunAt = &t
	}
	if nextRun.Valid {
		t := nextRun.Time
		sched.NextRunAt = &t
	}
	return &sched, nil
}

// ListCronSchedules 列出所有排程定義
func (s *SQLiteStore) ListCronSchedules(ctx context.Context) ([]CronSchedule, error) {
	query := `
	SELECT id, name, workspace, schedule, prompt, status, created_at, updated_at, last_run_at, next_run_at, pause_reason
	FROM cron_schedules
	ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list cron schedules: %w", err)
	}
	defer rows.Close()

	var list []CronSchedule
	for rows.Next() {
		var sched CronSchedule
		var lastRun, nextRun sql.NullTime
		if err := rows.Scan(
			&sched.ID,
			&sched.Name,
			&sched.Workspace,
			&sched.Schedule,
			&sched.Prompt,
			&sched.Status,
			&sched.CreatedAt,
			&sched.UpdatedAt,
			&lastRun,
			&nextRun,
			&sched.PauseReason,
		); err != nil {
			return nil, fmt.Errorf("scan cron schedule: %w", err)
		}
		if lastRun.Valid {
			t := lastRun.Time
			sched.LastRunAt = &t
		}
		if nextRun.Valid {
			t := nextRun.Time
			sched.NextRunAt = &t
		}
		list = append(list, sched)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}
	return list, nil
}

// UpdateCronScheduleStatus 更新排程啟用狀態與原因
func (s *SQLiteStore) UpdateCronScheduleStatus(ctx context.Context, id string, status CronStatus, reason string) error {
	query := `
	UPDATE cron_schedules
	SET status = ?, pause_reason = ?, updated_at = ?
	WHERE id = ?
	`
	res, err := s.db.ExecContext(ctx, query, status, reason, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update cron schedule status: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if rows == 0 {
		return ErrCronNotFound
	}
	return nil
}

// UpdateCronScheduleNextRun 更新排程的下次與上次執行時間
func (s *SQLiteStore) UpdateCronScheduleNextRun(ctx context.Context, id string, nextRun time.Time, lastRun *time.Time) error {
	query := `
	UPDATE cron_schedules
	SET next_run_at = ?, last_run_at = ?, updated_at = ?
	WHERE id = ?
	`
	var lastRunVal any
	if lastRun != nil {
		lastRunVal = lastRun.UTC()
	}
	res, err := s.db.ExecContext(ctx, query, nextRun.UTC(), lastRunVal, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update cron schedule next run: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if rows == 0 {
		return ErrCronNotFound
	}
	return nil
}

// DeleteCronSchedule 依 ID 刪除排程定義
func (s *SQLiteStore) DeleteCronSchedule(ctx context.Context, id string) error {
	query := `DELETE FROM cron_schedules WHERE id = ?`
	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete cron schedule: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if rows == 0 {
		return ErrCronNotFound
	}
	return nil
}

// RecordCronRun 記錄一次排程觸發執行紀錄
func (s *SQLiteStore) RecordCronRun(ctx context.Context, run CronRun) error {
	query := `
	INSERT INTO cron_runs (
		id, cron_id, scheduled_time, triggered_time, task_id, status, reason, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(ctx, query,
		run.ID,
		run.CronID,
		run.ScheduledTime.UTC(),
		run.TriggeredTime.UTC(),
		run.TaskID,
		run.Status,
		run.Reason,
		run.CreatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("record cron run: %w", err)
	}
	return nil
}

// ListCronRuns 依 scheduled_time 倒序列出指定排程的歷史觸發紀錄
func (s *SQLiteStore) ListCronRuns(ctx context.Context, cronID string, limit int) ([]CronRun, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `
	SELECT id, cron_id, scheduled_time, triggered_time, task_id, status, reason, created_at
	FROM cron_runs
	WHERE cron_id = ?
	ORDER BY scheduled_time DESC
	LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, query, cronID, limit)
	if err != nil {
		return nil, fmt.Errorf("list cron runs: %w", err)
	}
	defer rows.Close()

	var list []CronRun
	for rows.Next() {
		var r CronRun
		if err := rows.Scan(
			&r.ID,
			&r.CronID,
			&r.ScheduledTime,
			&r.TriggeredTime,
			&r.TaskID,
			&r.Status,
			&r.Reason,
			&r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan cron run: %w", err)
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}
	return list, nil
}
