package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
