package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

var (
	ErrMemoryCursor   = errors.New("invalid memory cursor")
	ErrMemoryTooLarge = errors.New("memory content too large")
	ErrMemoryMetadata = errors.New("invalid memory metadata")
)

type MemorySummary struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	CreatedAt string `json:"created_at"`
	Summary   string `json:"summary"`
	Truncated bool   `json:"truncated"`
}

type MemoryPage struct {
	Items      []MemorySummary `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

type memoryCursor struct {
	Version   int    `json:"v"`
	Workspace string `json:"w"`
	CreatedAt string `json:"t"`
	ID        string `json:"i"`
}

func memoryWorkspaceHash(workspace string) string {
	sum := sha256.Sum256([]byte(workspace))
	return hex.EncodeToString(sum[:])
}

func decodeMemoryCursor(raw, workspace string) (memoryCursor, error) {
	if len(raw) > 1024 {
		return memoryCursor{}, ErrMemoryCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) > 768 {
		return memoryCursor{}, ErrMemoryCursor
	}
	var cursor memoryCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.Version != 1 || cursor.Workspace != memoryWorkspaceHash(workspace) || cursor.ID == "" || cursor.CreatedAt == "" || len(cursor.ID) > 256 || len(cursor.CreatedAt) > 64 || !utf8.ValidString(cursor.ID) || !utf8.ValidString(cursor.CreatedAt) {
		return memoryCursor{}, ErrMemoryCursor
	}
	return cursor, nil
}

func validMemoryMetadata(id, source, created string) bool {
	return len(id) <= 256 && len(source) <= 128 && len(created) <= 64 && utf8.ValidString(id) && utf8.ValidString(source) && utf8.ValidString(created)
}

func boundedMemorySummary(id, content, source, created string) (MemorySummary, error) {
	if !validMemoryMetadata(id, source, created) || id == "" {
		return MemorySummary{}, ErrMemoryMetadata
	}
	truncated := len(content) > 512
	summary := content
	if truncated {
		summary = content[:512]
		for !utf8.ValidString(summary) && len(summary) > 508 {
			summary = summary[:len(summary)-1]
		}
	}
	if !utf8.ValidString(summary) {
		return MemorySummary{}, ErrMemoryMetadata
	}
	return MemorySummary{ID: id, Source: source, CreatedAt: created, Summary: summary, Truncated: truncated}, nil
}

// PageMemories 依建立時間及 ID 做有界 keyset 查詢；2026-09-23 WEBMEM-003。
func (s *SQLiteStore) PageMemories(ctx context.Context, workspace string, limit int, rawCursor string) (MemoryPage, error) {
	if limit < 1 || limit > 50 {
		return MemoryPage{}, ErrMemoryCursor
	}
	query := `SELECT substr(CAST(id AS BLOB),1,257), substr(CAST(content AS BLOB),1,513), substr(CAST(source AS BLOB),1,129), substr(CAST(created_at AS BLOB),1,65)
FROM memories WHERE workspace = ?`
	args := []any{workspace}
	if rawCursor != "" {
		cursor, err := decodeMemoryCursor(rawCursor, workspace)
		if err != nil {
			return MemoryPage{}, err
		}
		query += ` AND (created_at,id) > (?,?)`
		args = append(args, cursor.CreatedAt, cursor.ID)
	}
	query += ` ORDER BY created_at,id LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return MemoryPage{}, fmt.Errorf("page memories: %w", err)
	}
	defer rows.Close()
	page := MemoryPage{Items: make([]MemorySummary, 0, limit)}
	hasMore := false
	for rows.Next() {
		var id, content, source, created string
		if err := rows.Scan(&id, &content, &source, &created); err != nil {
			return MemoryPage{}, fmt.Errorf("scan memory page: %w", err)
		}
		if len(page.Items) == limit {
			hasMore = true
			break
		}
		item, err := boundedMemorySummary(id, content, source, created)
		if err != nil {
			return MemoryPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return MemoryPage{}, err
	}
	if hasMore {
		last := page.Items[limit-1]
		encoded, err := json.Marshal(memoryCursor{Version: 1, Workspace: memoryWorkspaceHash(workspace), CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			return MemoryPage{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
		if len(page.NextCursor) > 1024 {
			return MemoryPage{}, ErrMemoryCursor
		}
	}
	return page, nil
}

// GetMemoryBounded 在 SQLite 投影階段限制 Web detail；2026-09-23 WEBMEM-003。
func (s *SQLiteStore) GetMemoryBounded(ctx context.Context, workspace, id string) (*Memory, error) {
	var gotID, content, source, created string
	err := s.db.QueryRowContext(ctx, `SELECT substr(CAST(id AS BLOB),1,257), substr(CAST(content AS BLOB),1,65537), substr(CAST(source AS BLOB),1,129), substr(CAST(created_at AS BLOB),1,65)
FROM memories WHERE workspace=? AND id=?`, workspace, id).Scan(&gotID, &content, &source, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMemoryNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("bounded memory detail: %w", err)
	}
	if !validMemoryMetadata(gotID, source, created) {
		return nil, ErrMemoryMetadata
	}
	if len(content) > 65536 {
		return nil, ErrMemoryTooLarge
	}
	if !utf8.ValidString(content) {
		return nil, ErrMemoryMetadata
	}
	var createdAt time.Time
	for _, layout := range []string{"2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999", time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
		if parsed, err := time.Parse(layout, created); err == nil {
			createdAt = parsed
			break
		}
	}
	if createdAt.IsZero() {
		return nil, ErrMemoryMetadata
	}
	m := &Memory{ID: gotID, Workspace: workspace, Content: content, Source: source, CreatedAt: createdAt}
	if err := s.loadLifecycle(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}
