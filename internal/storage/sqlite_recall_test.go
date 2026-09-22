package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 2026-09-22：使用可選介面檢查新增查詢契約，缺能力時為行為紅燈。
func TestRecallCandidates(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reader, ok := any(s).(interface {
		RecallCandidates(context.Context, string) ([]Memory, bool, error)
	})
	if !ok {
		t.Fatal("RECALL-004: bounded candidate query is unavailable")
	}
	for i := 0; i < 10000; i++ {
		content := "SQLite 隔離"
		if i == 0 {
			content = strings.Repeat("x", 1<<20)
		}
		err = s.CreateMemory(context.Background(), Memory{ID: fmt.Sprintf("%04d", i), Workspace: "/a", Content: content})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = s.CreateMemory(context.Background(), Memory{ID: "foreign", Workspace: "/b", Content: "SECRET"}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	got, truncated, err := reader.RecallCandidates(context.Background(), "/a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 256 || !truncated || len(got[0].Content) != 8193 || got[255].ID != "0255" {
		t.Fatalf("bound: n=%d truncated=%v", len(got), truncated)
	}
	t.Logf("10001 rows: returned=%d elapsed=%s", len(got), time.Since(started))
	for _, m := range got {
		if m.CreatedAt.IsZero() || m.Workspace != "/a" || strings.Contains(m.Content, "SECRET") {
			t.Fatal("scope violation")
		}
	}
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, _, err = reader.RecallCandidates(ctx, "/a"); err == nil {
		t.Fatal("waiting query ignored cancellation")
	}
	conn.Close()
	if _, _, err = reader.RecallCandidates(context.Background(), "/a"); err != nil {
		t.Fatalf("connection not released: %v", err)
	}
	rows, err := s.db.Query("EXPLAIN QUERY PLAN SELECT id FROM memories WHERE workspace=? ORDER BY id LIMIT 257", "/a")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a, b, c int
		var d string
		if err = rows.Scan(&a, &b, &c, &d); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(d, "TEMP B-TREE") || !strings.Contains(d, "idx_memories_workspace_id") {
			t.Fatal(d)
		}
	}
}

func TestRecallMetadataAndLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, m := range []Memory{
		{ID: strings.Repeat("i", 300), Workspace: "/a", Content: "SQLite", Source: strings.Repeat("s", 200)},
		{ID: "legacy", Workspace: "/a", Content: "SQLite legacy"},
	} {
		if err = s.CreateMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.db.Exec("UPDATE memories SET created_at='2026-09-22' WHERE id='legacy'"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("DROP INDEX idx_memories_workspace_id"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	started := time.Now()
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t.Logf("legacy index migration=%s", time.Since(started))
	got, _, err := s.RecallCandidates(ctx, "/a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(got[0].ID) != 257 || len(got[0].Source) != 129 || got[1].CreatedAt.IsZero() {
		t.Fatalf("metadata/legacy=%+v", got)
	}
	original, err := s.GetMemory(ctx, "/a", strings.Repeat("i", 300))
	if err != nil || len(original.ID) != 300 || len(original.Source) != 200 {
		t.Fatal("migration rewrote data")
	}
	if _, err = s.db.Exec("UPDATE memories SET created_at=? WHERE id='legacy'", strings.Repeat("x", 10000)); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.RecallCandidates(ctx, "/a")
	if err != nil || !got[1].CreatedAt.IsZero() {
		t.Fatal("invalid timestamp accepted")
	}
}
