package storage

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLifecycleLargeDataProbe(t *testing.T) {
	for _, n := range []int{10000, 100000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`CREATE TABLE memories(id TEXT PRIMARY KEY,workspace TEXT NOT NULL,content TEXT NOT NULL,source TEXT NOT NULL DEFAULT 'manual',created_at DATETIME NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			stmt, err := tx.Prepare(`INSERT INTO memories VALUES(?,'/a','sqlite','manual','2026-09-01 00:00:00')`)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < n; i++ {
				if _, err = stmt.Exec(fmt.Sprintf("m%06d", i)); err != nil {
					t.Fatal(err)
				}
			}
			stmt.Close()
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			db.Close()
			before := time.Now()
			s, err := NewSQLiteStore(path)
			migration := time.Since(before)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if migration > 30*time.Second {
				t.Fatalf("migration=%s >30s", migration)
			}
			tx, err = s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			stmt, err = tx.Prepare(`INSERT INTO memory_relations VALUES('/a',?,?,'2026-10-01T00:00:00Z','probe')`)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < n; i++ {
				if _, err = stmt.Exec(fmt.Sprintf("m%06d", i), fmt.Sprintf("m%06d", i+1)); err != nil {
					t.Fatal(err)
				}
			}
			stmt.Close()
			if _, err = tx.Exec(`UPDATE memories SET lifecycle='superseded'; INSERT INTO memories(id,workspace,content,source,created_at,lifecycle_changed_at) VALUES(?,'/a','sqlite','manual','2026-10-01T00:00:00Z','2026-10-01T00:00:00Z')`, fmt.Sprintf("m%06d", n)); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM memories WHERE workspace='/a' AND lifecycle='active' ORDER BY id LIMIT 257`)
			if err != nil {
				t.Fatal(err)
			}
			var plan strings.Builder
			for rows.Next() {
				var a, b, c int
				var text string
				if err = rows.Scan(&a, &b, &c, &text); err != nil {
					t.Fatal(err)
				}
				plan.WriteString(text)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if !strings.Contains(plan.String(), "idx_memory_active") {
				t.Fatalf("query plan %s", plan.String())
			}
			for i := 0; i < 5; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				before = time.Now()
				candidates, truncated, err := s.RecallCandidates(ctx, "/a")
				recallTime := time.Since(before)
				if err != nil || truncated || len(candidates) != 1 || candidates[0].ID != fmt.Sprintf("m%06d", n) {
					t.Fatalf("probe candidates %+v %v", candidates, err)
				}
				before = time.Now()
				detail, err := s.GetMemoryDetail(ctx, "/a", "m000000")
				detailTime := time.Since(before)
				cancel()
				if err != nil || detail.Successor != "m000001" || detail.Memory.Lifecycle != "superseded" || recallTime > 2*time.Second || detailTime > 2*time.Second {
					t.Fatalf("probe detail %+v err=%v recall=%s detail=%s", detail, err, recallTime, detailTime)
				}
				t.Logf("relations=%d iteration=%d migration=%s recall=%s detail=%s plan=%s", n, i, migration, recallTime, detailTime, plan.String())
			}
		})
	}
}
