package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// 2026-09-23：WEBMEM-003、010 的 SQLite 有界分頁與 detail 行為。
func TestMemoryPageBoundaries(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reader, ok := any(s).(interface {
		PageMemories(context.Context, string, int, string) (MemoryPage, error)
		GetMemoryBounded(context.Context, string, string) (*Memory, error)
	})
	if !ok {
		t.Fatal("WEBMEM-003: bounded page/detail contract unavailable")
	}
	ctx := context.Background()
	stamp := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 101; i++ {
		content := "短內容"
		if i == 0 {
			content = strings.Repeat("x", 1<<20)
		}
		if err := s.CreateMemory(ctx, Memory{ID: fmt.Sprintf("m%03d", i), Workspace: "/a", Content: content, CreatedAt: stamp}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateMemory(ctx, Memory{ID: "foreign", Workspace: "/b", Content: "secret", CreatedAt: stamp}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	cursor := ""
	for {
		page, err := reader.PageMemories(ctx, "/a", 50, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 50 {
			t.Fatalf("page size=%d", len(page.Items))
		}
		for _, item := range page.Items {
			ids = append(ids, item.ID)
			if len(item.Summary) > 512 || strings.Contains(item.Summary, "secret") {
				t.Fatalf("unbounded or foreign summary: %+v", item)
			}
		}
		if cursor == "" && (page.Items[0].ID != "m000" || len(page.Items[0].Summary) != 512 || !page.Items[0].Truncated) {
			t.Fatalf("first summary=%+v", page.Items[0])
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(ids) != 101 || ids[0] != "m000" || ids[100] != "m100" {
		t.Fatalf("page ordering: %d %+v", len(ids), ids)
	}
	if _, err := reader.PageMemories(ctx, "/b", 50, cursor); !errors.Is(err, ErrMemoryCursor) {
		t.Fatalf("cross-scope cursor=%v", err)
	}
	if _, err := reader.PageMemories(ctx, "/a", 51, ""); !errors.Is(err, ErrMemoryCursor) {
		t.Fatalf("oversize limit=%v", err)
	}
	if _, err := reader.GetMemoryBounded(ctx, "/a", "foreign"); !errors.Is(err, ErrMemoryNotFound) {
		t.Fatalf("cross-scope detail=%v", err)
	}
	if _, err := reader.GetMemoryBounded(ctx, "/a", "m000"); !errors.Is(err, ErrMemoryTooLarge) {
		t.Fatalf("oversize detail=%v", err)
	}
	got, err := reader.GetMemoryBounded(ctx, "/a", "m001")
	if err != nil || got.Content != "短內容" {
		t.Fatalf("detail=%+v, %v", got, err)
	}
}

func TestMemoryPageLargeDatasetAndLegacy(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	stamp := time.Date(2026, 9, 23, 1, 0, 0, 0, time.UTC)
	for i := 0; i < 10000; i++ {
		if err := s.CreateMemory(ctx, Memory{ID: fmt.Sprintf("m%05d", i), Workspace: "/a", Content: "x", CreatedAt: stamp}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.PageMemories(ctx, "/a", 50, "")
	if err != nil || len(page.Items) != 50 || page.Items[49].ID != "m00049" || page.NextCursor == "" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	second, err := s.PageMemories(ctx, "/a", 50, page.NextCursor)
	if err != nil || len(second.Items) != 50 || second.Items[0].ID != "m00050" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT id FROM memories WHERE workspace=? AND (created_at,id)>(?,?) ORDER BY created_at,id LIMIT 51`, "/a", page.Items[49].CreatedAt, page.Items[49].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	usedIndex := false
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "idx_memories_workspace_created_id") {
			usedIndex = true
		}
		if strings.Contains(detail, "TEMP B-TREE") {
			t.Fatal(detail)
		}
	}
	if !usedIndex {
		t.Fatal("keyset index unused")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE memories SET source=? WHERE id='m00000'`, strings.Repeat("s", 129)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PageMemories(ctx, "/a", 50, ""); !errors.Is(err, ErrMemoryMetadata) {
		t.Fatalf("legacy metadata=%v", err)
	}
	if _, err := s.GetMemoryBounded(ctx, "/a", "m00000"); !errors.Is(err, ErrMemoryMetadata) {
		t.Fatalf("legacy detail metadata=%v", err)
	}
}

func TestMemoryPageUTF8AndDetailLimit(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for _, m := range []Memory{
		{ID: "a", Workspace: "/a", Content: strings.Repeat("x", 511) + "界"},
		{ID: "b", Workspace: "/a", Content: strings.Repeat("z", 65536)},
		{ID: "c", Workspace: "/a", Content: strings.Repeat("z", 65537)},
	} {
		if err := s.CreateMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.PageMemories(ctx, "/a", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].Summary != strings.Repeat("x", 511) || !page.Items[0].Truncated {
		t.Fatalf("UTF-8 boundary=%+v", page.Items[0])
	}
	if got, err := s.GetMemoryBounded(ctx, "/a", "b"); err != nil || len(got.Content) != 65536 {
		t.Fatalf("exact bound=%v, %+v", err, got)
	}
	if _, err := s.GetMemoryBounded(ctx, "/a", "c"); !errors.Is(err, ErrMemoryTooLarge) {
		t.Fatalf("over bound=%v", err)
	}
}
