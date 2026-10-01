package storage

import (
	"path/filepath"
	"testing"
)

func TestLegacyMigrationConcurrentClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.db")
	seed, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.db.Exec(`DELETE FROM schema_migrations WHERE name = 'gitmem-legacy-completeness-v1'`); err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	start := make(chan struct{})
	results := make(chan error, 16)
	for i := 0; i < cap(results); i++ {
		go func() {
			<-start
			store, err := NewSQLiteStore(path)
			if err == nil {
				err = store.Close()
			}
			results <- err
		}()
	}
	close(start)
	for i := 0; i < cap(results); i++ {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	var count int
	if err := seed.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = 'gitmem-legacy-completeness-v1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("marker count = %d, want 1", count)
	}
}

func TestLegacyMigrationClaimRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.db.Exec(`
 DELETE FROM schema_migrations WHERE name = 'gitmem-legacy-completeness-v1';
 INSERT INTO memory_candidates (id, workspace, repo_id, ref_name, base_sha, head_sha, commit_count, facts_summary, draft_content, status, policy_version, created_at)
 VALUES ('legacy', '/ws', 'repo', 'main', 'base', 'head', 1, '{}', '', 'approved', 'gitmem-v1', '2026-10-01 00:00:00');
 CREATE TRIGGER reject_quarantine BEFORE UPDATE ON memory_candidates BEGIN SELECT RAISE(ABORT, 'quarantine failure'); END;
 `)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.initSchema(); err == nil {
		t.Fatal("expected quarantine failure")
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = 'gitmem-legacy-completeness-v1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed migration retained marker: %d", count)
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_quarantine`); err != nil {
		t.Fatal(err)
	}
	if err := store.initSchema(); err != nil {
		t.Fatal(err)
	}
	var policy string
	if err := store.db.QueryRow(`SELECT policy_version FROM memory_candidates WHERE id = 'legacy'`).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	if policy != "gitmem-v1-legacy-unknown" {
		t.Fatalf("retry policy = %s", policy)
	}
}
