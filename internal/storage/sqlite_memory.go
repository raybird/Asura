package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrMemoryNotFound 代表查詢的記憶不存在或不屬於該專案工作區 (MEM-002, MEM-005)
	ErrMemoryNotFound = errors.New("memory not found")
)

// Memory 定義專案級基礎記憶結構 (MEM-001)
type Memory struct {
	ID        string    `json:"id"`
	Workspace string    `json:"workspace"`
	Content   string    `json:"content"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateMemory 寫入專案記憶 (MEM-001)
func (s *SQLiteStore) CreateMemory(ctx context.Context, memory Memory) error {
	if memory.ID == "" {
		return errors.New("memory id cannot be empty")
	}
	if memory.Workspace == "" {
		return errors.New("workspace cannot be empty")
	}
	if memory.Source == "" {
		memory.Source = "manual"
	}
	if memory.CreatedAt.IsZero() {
		memory.CreatedAt = time.Now().UTC()
	}

	query := `
	INSERT INTO memories (id, workspace, content, source, created_at)
	VALUES (?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(ctx, query, memory.ID, memory.Workspace, memory.Content, memory.Source, memory.CreatedAt)
	if err != nil {
		return fmt.Errorf("create memory: %w", err)
	}
	return nil
}

// ListMemories 依工作區列出記憶清單，限定單一 workspace，無全域讀取捷徑 (MEM-001, MEM-002)
func (s *SQLiteStore) ListMemories(ctx context.Context, workspace string) ([]Memory, error) {
	query := `
	SELECT id, workspace, content, source, created_at
	FROM memories
	WHERE workspace = ?
	ORDER BY created_at ASC, id ASC
	`
	rows, err := s.db.QueryContext(ctx, query, workspace)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()

	memories := make([]Memory, 0)
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Workspace, &m.Content, &m.Source, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		memories = append(memories, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}
	return memories, nil
}

// GetMemory 依工作區與記憶 ID 取得記憶，嚴格同時比對 workspace 與 id (MEM-001, MEM-002)
func (s *SQLiteStore) GetMemory(ctx context.Context, workspace, id string) (*Memory, error) {
	query := `
	SELECT id, workspace, content, source, created_at
	FROM memories
	WHERE workspace = ? AND id = ?
	`
	var m Memory
	err := s.db.QueryRowContext(ctx, query, workspace, id).Scan(&m.ID, &m.Workspace, &m.Content, &m.Source, &m.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrMemoryNotFound
		}
		return nil, fmt.Errorf("get memory: %w", err)
	}
	return &m, nil
}

// DeleteMemory 依工作區與記憶 ID 刪除記憶，嚴格同時比對 workspace 與 id 並檢查影響筆數 (MEM-002, MEM-005)
func (s *SQLiteStore) DeleteMemory(ctx context.Context, workspace, id string) error {
	query := `
	DELETE FROM memories
	WHERE workspace = ? AND id = ?
	`
	res, err := s.db.ExecContext(ctx, query, workspace, id)
	if err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rows == 0 {
		return ErrMemoryNotFound
	}
	return nil
}

// ResolveMemoriesForTask 依據 canonical workspace 解析並驗證任務所需的記憶清單 (CTXMEM-002, CTXMEM-005)
// 嚴格比對 workspace 與 id；若任一 ID 不存在、已刪除或屬於其他專案，立即回傳 ErrMemoryNotFound
func (s *SQLiteStore) ResolveMemoriesForTask(ctx context.Context, canonicalWorkspace string, memoryIDs []string) ([]Memory, error) {
	if strings.TrimSpace(canonicalWorkspace) == "" {
		return nil, errors.New("canonical workspace cannot be empty")
	}
	if len(memoryIDs) == 0 {
		return []Memory{}, nil
	}

	memories := make([]Memory, 0, len(memoryIDs))
	for _, id := range memoryIDs {
		trimmedID := strings.TrimSpace(id)
		if trimmedID == "" {
			return nil, fmt.Errorf("%w: empty memory id", ErrMemoryNotFound)
		}
		mem, err := s.GetMemory(ctx, canonicalWorkspace, trimmedID)
		if err != nil {
			if errors.Is(err, ErrMemoryNotFound) {
				return nil, fmt.Errorf("%w: %s", ErrMemoryNotFound, trimmedID)
			}
			return nil, fmt.Errorf("resolve memory %s: %w", trimmedID, err)
		}
		memories = append(memories, *mem)
	}
	return memories, nil
}

// RecallCandidates 在 2026-09-22 核准的固定邊界內讀取；超限欄位保留一個 sentinel byte 供 selector 拒絕。
func (s *SQLiteStore) RecallCandidates(ctx context.Context, workspace string) ([]Memory, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
 substr(CAST(id AS BLOB),1,257), substr(CAST(content AS BLOB),1,8193),
 substr(CAST(source AS BLOB),1,129), substr(CAST(created_at AS BLOB),1,65)
	FROM memories
	WHERE workspace = ?
	  AND source NOT IN ('git-approved-legacy-partial', 'git-approved-legacy-unknown')
	ORDER BY id LIMIT 257`, workspace)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	memories := make([]Memory, 0, 256)
	truncated := false
	for rows.Next() {
		if len(memories) == 256 {
			truncated = true
			break
		}
		var id, content, source, created string
		if err := rows.Scan(&id, &content, &source, &created); err != nil {
			return nil, false, err
		}
		m := Memory{ID: id, Workspace: workspace, Content: content, Source: source}
		if len(created) <= 64 {
			for _, layout := range []string{"2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999", time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
				if parsed, err := time.Parse(layout, created); err == nil {
					m.CreatedAt = parsed
					break
				}
			}
		}
		memories = append(memories, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return memories, truncated, nil
}
