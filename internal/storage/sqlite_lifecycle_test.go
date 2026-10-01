package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLifecycleLegacyMetadata(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateMemory(ctx, Memory{ID: "a", Workspace: "/a", Content: "old"}); err != nil {
		t.Fatal(err)
	}
	m, err := s.GetMemory(ctx, "/a", "a")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(m)
	var got map[string]any
	json.Unmarshal(b, &got)
	if got["lifecycle"] != "active" || got["is_durable"] != false {
		t.Fatalf("MLIFE-001 legacy metadata: %s", b)
	}
}

func TestLifecycleAtomicChainRetry(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	mustCreate := func(id, ws string) {
		t.Helper()
		if err := s.CreateMemory(ctx, Memory{ID: id, Workspace: ws, Content: "sqlite"}); err != nil {
			t.Fatal(err)
		}
	}
	mustCreate("a", "/a")
	mustCreate("foreign", "/b")
	if _, err = s.MarkMemoryDurable(ctx, "/a", "a"); err != nil {
		t.Fatal(err)
	}
	options := CreateMemoryOptions{Supersedes: "a", OperationID: "op", Reason: "decision"}
	b, err := s.CreateMemoryWithOptions(ctx, Memory{ID: "b", Workspace: "/a", Content: "new sqlite"}, options)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.CreateMemoryWithOptions(ctx, Memory{ID: "ignored", Workspace: "/a", Content: "new sqlite"}, options)
	if err != nil || again.ID != "b" {
		t.Fatalf("MLIFE-002 retry %+v %v", again, err)
	}
	if _, err = s.CreateMemoryWithOptions(ctx, Memory{ID: "different", Workspace: "/a", Content: "changed"}, options); !errors.Is(err, ErrLifecycleConflict) {
		t.Fatalf("payload conflict: %v", err)
	}
	a, err := s.GetMemory(ctx, "/a", "a")
	if err != nil || a.Lifecycle != "superseded" || !a.IsDurable || a.SupersededBy != b.ID || a.Content != "sqlite" {
		t.Fatalf("MLIFE-008 predecessor %+v %v", a, err)
	}
	for _, pred := range []string{"foreign", "absent"} {
		_, err = s.CreateMemoryWithOptions(ctx, Memory{ID: "bad", Workspace: "/a", Content: "bad"}, CreateMemoryOptions{Supersedes: pred, OperationID: pred})
		if !errors.Is(err, ErrMemoryNotFound) {
			t.Fatalf("MLIFE-009 %s %v", pred, err)
		}
	}
	if _, err = s.GetMemory(ctx, "/a", "bad"); !errors.Is(err, ErrMemoryNotFound) {
		t.Fatal("half transaction persisted")
	}
	if _, err = s.CreateMemoryWithOptions(ctx, Memory{ID: "self", Workspace: "/a"}, CreateMemoryOptions{Supersedes: "self", OperationID: "self"}); !errors.Is(err, ErrLifecycleConflict) {
		t.Fatal("self accepted")
	}
	if _, err = s.CreateMemoryWithOptions(ctx, Memory{ID: "c", Workspace: "/a", Content: "latest sqlite"}, CreateMemoryOptions{Supersedes: "b", OperationID: "op2"}); err != nil {
		t.Fatal(err)
	}
	cand, _, err := s.RecallCandidates(ctx, "/a")
	if err != nil || len(cand) != 1 || cand[0].ID != "c" {
		t.Fatalf("MLIFE-005 %+v %v", cand, err)
	}
	for _, id := range []string{"a", "b"} {
		if _, err = s.GetMemory(ctx, "/a", id); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.DeleteMemory(ctx, "/a", "c"); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetMemoryDetail(ctx, "/a", "c")
	if err != nil || !d.Deleted || d.Predecessor != "b" {
		t.Fatalf("tombstone %+v %v", d, err)
	}
	if _, err = s.CreateMemoryWithOptions(ctx, Memory{ID: "revival", Workspace: "/a", Content: "latest sqlite"}, CreateMemoryOptions{Supersedes: "b", OperationID: "op2"}); !errors.Is(err, ErrMemoryNotFound) {
		t.Fatalf("deleted successor retry %v", err)
	}
	s.Close()
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	cand, _, err = s.RecallCandidates(ctx, "/a")
	if err != nil || len(cand) != 0 {
		t.Fatalf("MLIFE-006 resurrection %+v %v", cand, err)
	}
	if err = s.DeleteMemory(ctx, "/a", "a"); err != nil {
		t.Fatal(err)
	}
	d, err = s.GetMemoryDetail(ctx, "/a", "b")
	if err != nil || d.Predecessor != "a" || d.Successor != "c" {
		t.Fatalf("chain audit %+v %v", d, err)
	}
}

func TestLifecycleRollbackAndConcurrentCAS(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateMemory(ctx, Memory{ID: "a", Workspace: "/a", Content: "old"}); err != nil {
		t.Fatal(err)
	}
	// 2026-10-01：在 successor INSERT 後的 predecessor UPDATE 注入真實 SQL 失敗。
	if _, err = s.db.Exec(`CREATE TRIGGER fail_supersession BEFORE UPDATE OF lifecycle ON memories BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateMemoryWithOptions(ctx, Memory{ID: "b", Workspace: "/a"}, CreateMemoryOptions{Supersedes: "a", OperationID: "op"}); err == nil {
		t.Fatal("injected failure ignored")
	}
	for _, table := range []string{"memory_relations", "memory_operations"} {
		var n int
		if err = s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("rollback %s n=%d err=%v", table, n, err)
		}
	}
	if _, err = s.GetMemory(ctx, "/a", "b"); !errors.Is(err, ErrMemoryNotFound) {
		t.Fatal("rollback successor remains")
	}
	if _, err = s.db.Exec("DROP TRIGGER fail_supersession"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	success := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("b%d", i)
			if _, e := s.CreateMemoryWithOptions(ctx, Memory{ID: id, Workspace: "/a"}, CreateMemoryOptions{Supersedes: "a", OperationID: id}); e == nil {
				success <- id
			} else if !errors.Is(e, ErrLifecycleConflict) {
				t.Errorf("unexpected CAS: %v", e)
			}
		}(i)
	}
	wg.Wait()
	close(success)
	if len(success) != 1 {
		t.Fatalf("MLIFE-002 fork: successes=%d", len(success))
	}
}

func TestLifecycleCandidateLimitAndUtility(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateMemory(ctx, Memory{ID: "000", Workspace: "/a", Content: "sqlite"}); err != nil {
		t.Fatal(err)
	}
	previous := "000"
	for i := 1; i <= 300; i++ {
		id := fmt.Sprintf("%03d", i)
		if _, err = s.CreateMemoryWithOptions(ctx, Memory{ID: id, Workspace: "/a", Content: "sqlite"}, CreateMemoryOptions{Supersedes: previous, OperationID: id}); err != nil {
			t.Fatal(err)
		}
		previous = id
	}
	ms, truncated, err := s.RecallCandidates(ctx, "/a")
	if err != nil || truncated || len(ms) != 1 || ms[0].ID != "300" {
		t.Fatalf("MLIFE-003 LIMIT filtering %+v %v %v", ms, truncated, err)
	}
	raw := `{"workspace":"/a","recall":{"recall_id":"r","mode":"bounded","utility_policy":"observe","utility_version":"utility-v1","auto_ids":["000"],"selected":[{"id":"000","score":1,"weight_ppm":1000000,"final_score_scaled":1000000}]}}`
	if err = s.CreateTask(ctx, &Task{ID: "t", Workspace: "/a", Status: StatusCompleted, ResolvedContext: raw}); err != nil {
		t.Fatal(err)
	}
	if err = s.PersistUtility(ctx, "/a", "r", []UtilityAssessment{{MemoryID: "000", State: "used", Provenance: "estimated"}}); err != nil {
		t.Fatal(err)
	}
	old, err := s.GetMemoryDetail(ctx, "/a", "000")
	if err != nil || old.Utility.Observed != 1 {
		t.Fatalf("MLIFE-011 predecessor utility %+v %v", old, err)
	}
	fresh, err := s.GetMemoryDetail(ctx, "/a", "300")
	if err != nil || fresh.Utility.Observed != 0 || fresh.WeightPPM != 1000000 {
		t.Fatalf("MLIFE-011 successor neutral %+v %v", fresh, err)
	}
}

func TestLifecycleLegacyMigrationAndBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE memories(id TEXT PRIMARY KEY,workspace TEXT NOT NULL,content TEXT NOT NULL,source TEXT NOT NULL DEFAULT 'manual',created_at DATETIME NOT NULL);INSERT INTO memories VALUES('a','/a','sqlite','manual','2026-09-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	m, err := s.GetMemory(ctx, "/a", "a")
	if err != nil || m.Lifecycle != "active" || m.IsDurable || m.Content != "sqlite" || m.LifecycleChangedAt == "" {
		t.Fatalf("MLIFE-001 migration %+v %v", m, err)
	}
	if err = s.CreateMemory(ctx, Memory{ID: "huge", Workspace: "/a", Content: strings.Repeat("x", 65537)}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetMemoryDetail(ctx, "/a", "huge"); !errors.Is(err, ErrMemoryTooLarge) {
		t.Fatalf("L7 oversized detail: %v", err)
	}
	if _, err = s.CreateMemoryWithOptions(ctx, Memory{ID: "b", Workspace: "/a"}, CreateMemoryOptions{Supersedes: "a"}); err == nil {
		t.Fatal("missing retry id allowed")
	}
}

func TestLifecycleGitApprovalAtomic(t *testing.T) {
	s := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/a"
	if err := s.CreateMemory(ctx, Memory{ID: "old", Workspace: ws, Content: "sqlite"}); err != nil {
		t.Fatal(err)
	}
	cand := &MemoryCandidate{ID: "cand-life", Workspace: ws, RepoID: "repo", RefName: "main", BaseSHA: "base", HeadSHA: "head", DraftContent: "new sqlite", FactsSummary: "{}", Status: "pending", PolicyVersion: "gitmem-v1", CreatedAt: time.Now().UTC()}
	if err := s.SaveCandidate(ctx, cand); err != nil {
		t.Fatal(err)
	}
	opts := ApproveOptions{Lifecycle: CreateMemoryOptions{Supersedes: "old", Durable: true}}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_provenance BEFORE INSERT ON memory_provenance BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveCandidateTx(ctx, ws, cand.ID, opts); err == nil {
		t.Fatal("L6 partial transaction")
	}
	old, err := s.GetMemory(ctx, ws, "old")
	if err != nil || old.Lifecycle != "active" {
		t.Fatalf("L6 rollback %+v %v", old, err)
	}
	if _, err = s.db.Exec("DROP TRIGGER fail_provenance"); err != nil {
		t.Fatal(err)
	}
	id, err := s.ApproveCandidateTx(ctx, ws, cand.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.ApproveCandidateTx(ctx, ws, cand.ID, opts)
	if err != nil || again != id {
		t.Fatalf("L6 retry %s %v", again, err)
	}
	if _, err = s.ApproveCandidateTx(ctx, ws, cand.ID, ApproveOptions{}); !errors.Is(err, ErrLifecycleConflict) {
		t.Fatalf("L6 changed lifecycle ignored: %v", err)
	}
	d, err := s.GetMemoryDetail(ctx, ws, id)
	if err != nil || d.Predecessor != "old" || !d.Memory.IsDurable || !strings.Contains(string(d.Provenance), "cand-life") || d.Utility.Observed != 0 {
		t.Fatalf("L6 provenance/utility %+v %v", d, err)
	}
	if err = s.DeleteMemory(ctx, ws, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApproveCandidateTx(ctx, ws, cand.ID, opts); err == nil {
		t.Fatal("L6 revival")
	}
}

func TestLifecycleMigrationRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.db.Exec(`DELETE FROM schema_migrations WHERE name='memory-lifecycle-v1';INSERT INTO memories(id,workspace,content,source,created_at,lifecycle_changed_at) VALUES('legacy','/a','old','manual','2026-09-01 00:00:00','');CREATE TRIGGER fail_lifecycle_migration BEFORE UPDATE OF lifecycle_changed_at ON memories BEGIN SELECT RAISE(ABORT,'migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.initLifecycleSchema(); err == nil {
		t.Fatal("migration failure ignored")
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE name='memory-lifecycle-v1'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration marker count=%d err=%v", count, err)
	}
	if _, err = s.db.Exec("DROP TRIGGER fail_lifecycle_migration"); err != nil {
		t.Fatal(err)
	}
	if err = s.initLifecycleSchema(); err != nil {
		t.Fatal(err)
	}
	m, err := s.GetMemory(context.Background(), "/a", "legacy")
	if err != nil || m.LifecycleChangedAt == "" {
		t.Fatalf("migration restart %+v %v", m, err)
	}
}
