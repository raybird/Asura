package integration

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"asura/internal/contextwin"
	"asura/internal/recall"
	"asura/internal/storage"

	_ "modernc.org/sqlite"
)

// TestProbe_SQLite_GitApprovedMemoryCompat 探測 source='git-approved' 與既有查詢和召回引擎之相容性
func TestProbe_SQLite_GitApprovedMemoryCompat(t *testing.T) {
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "probe_sqlite.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init sqlite store: %v", err)
	}

	ctx := context.Background()
	ws := "/workspace/project-alpha"

	// 1. 寫入 manual 記憶與 git-approved 記憶
	memManual := storage.Memory{
		ID:        "mem-manual-01",
		Workspace: ws,
		Content:   "Database migration rule: always use transactions for DDL",
		Source:    "manual",
		CreatedAt: time.Now().UTC().Add(-1 * time.Hour),
	}
	memGitApproved := storage.Memory{
		ID:        "mem-git-01",
		Workspace: ws,
		Content:   "Architecture decision: memory_candidates table is isolated from memories",
		Source:    "git-approved",
		CreatedAt: time.Now().UTC(),
	}

	if err := store.CreateMemory(ctx, memManual); err != nil {
		t.Fatalf("create manual memory failed: %v", err)
	}
	if err := store.CreateMemory(ctx, memGitApproved); err != nil {
		t.Fatalf("create git-approved memory failed: %v", err)
	}

	// 2. 測試 ListMemories
	list, err := store.ListMemories(ctx, ws)
	if err != nil {
		t.Fatalf("ListMemories failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 memories, got %d", len(list))
	}
	foundGit := false
	for _, m := range list {
		if m.ID == "mem-git-01" && m.Source == "git-approved" {
			foundGit = true
		}
	}
	if !foundGit {
		t.Fatal("git-approved memory not found in ListMemories")
	}

	// 3. 測試 ResolveMemoriesForTask
	resolved, err := store.ResolveMemoriesForTask(ctx, ws, []string{"mem-git-01", "mem-manual-01"})
	if err != nil {
		t.Fatalf("ResolveMemoriesForTask failed: %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved memories, got %d", len(resolved))
	}

	// 4. 測試 Phase 3F recall.Select 詞彙重疊計數相容性
	recallIn := recall.Input{
		Workspace:   ws,
		Prompt:      "Please check architecture decision about memory_candidates table isolation",
		ExplicitIDs: nil,
		Candidates:  list,
		ContextConfig: contextwin.Config{
			ContextLimit: 32000,
			Policy:       contextwin.PolicyObserve,
			ReserveRatio: contextwin.DefaultReserveRatio,
		},
		Measure: func(m []storage.Memory) recall.Budget {
			return recall.Budget{
				FormattedBytes:   512,
				EstimatedTokens:  128,
				EstimationMethod: "token_count_exact",
			}
		},
	}

	result, err := recall.Select(ctx, recallIn)
	if err != nil {
		t.Fatalf("recall.Select failed: %v", err)
	}
	if len(result.Memories) == 0 {
		t.Fatal("expected at least 1 memory recalled by lexical match")
	}
	if result.Memories[0].ID != "mem-git-01" {
		t.Fatalf("expected mem-git-01 to have higher score, got %s", result.Memories[0].ID)
	}
	t.Logf("PASS: Recall correctly selected git-approved memory with score: %d", result.Decision.Selected[0].Score)
}

// TestProbe_SQLite_CandidateIsolationAndProvenanceCascade 探測候選隔離性與 Provenance 外鍵串聯
func TestProbe_SQLite_CandidateIsolationAndProvenanceCascade(t *testing.T) {
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "probe_isolation.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init sqlite store: %v", err)
	}

	ctx := context.Background()
	ws := "/workspace/test-repo"

	// 探測建立 memory_candidates 與 memory_provenance
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open raw sqlite db: %v", err)
	}
	defer db.Close()
	// 啟用外鍵約束
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatal(err)
	}

	schema := `
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
		UNIQUE(workspace, repo_id, ref_name, base_sha, head_sha, policy_version)
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
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create probe candidate tables: %v", err)
	}

	// 插入待審核 Candidate
	candQuery := `
	INSERT INTO memory_candidates (id, workspace, repo_id, ref_name, base_sha, head_sha, commit_count, facts_summary, draft_content, status, created_at)
	VALUES ('cand-01', ?, 'repo-123', 'refs/heads/main', 'base111', 'head222', 3, '3 commits', 'Draft architectural memory', 'pending', datetime('now'))
	`
	if _, err := db.Exec(candQuery, ws); err != nil {
		t.Fatalf("insert candidate failed: %v", err)
	}

	// 1. 驗證 ListMemories 零洩漏（只讀 memories 表）
	list, err := store.ListMemories(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 official memories, got %d (Candidate leaked!)", len(list))
	}
	t.Log("PASS: Candidate storage is 100% isolated from ListMemories.")

	// 2. 模擬核准：寫入 memories 與 memory_provenance，並更新 candidate
	approvedMemID := "mem-approved-01"
	mem := storage.Memory{
		ID:        approvedMemID,
		Workspace: ws,
		Content:   "Draft architectural memory",
		Source:    "git-approved",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateMemory(ctx, mem); err != nil {
		t.Fatal(err)
	}

	provQuery := `
	INSERT INTO memory_provenance (memory_id, candidate_id, workspace, repo_id, ref_name, base_sha, head_sha, commit_count, approved_at)
	VALUES (?, 'cand-01', ?, 'repo-123', 'refs/heads/main', 'base111', 'head222', 3, datetime('now'))
	`
	if _, err := db.Exec(provQuery, approvedMemID, ws); err != nil {
		t.Fatal(err)
	}

	// 3. 測試刪除正式記憶時，memory_provenance 透過 ON DELETE CASCADE 同步清理
	if err := store.DeleteMemory(ctx, ws, approvedMemID); err != nil {
		t.Fatal(err)
	}

	var provCount int
	err = db.QueryRow("SELECT COUNT(*) FROM memory_provenance WHERE memory_id = ?", approvedMemID).Scan(&provCount)
	if err != nil {
		t.Fatal(err)
	}
	if provCount != 0 {
		t.Fatalf("expected 0 provenance records after cascade delete, got %d", provCount)
	}
	t.Log("PASS: ON DELETE CASCADE cleanly removed provenance row upon memory deletion.")
}
