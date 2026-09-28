package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func newTestGitMemStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_gitmem.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestGitMem_SchemaAndIsolation(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/test"

	candidate := &MemoryCandidate{
		ID:            "cand-1",
		Workspace:     ws,
		RepoID:        "repo-123",
		RefName:       "refs/heads/main",
		BaseSHA:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		HeadSHA:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CommitCount:   2,
		FactsSummary:  "facts summary",
		DraftContent:  "draft memory content",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}

	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("failed to save candidate: %v", err)
	}

	// 驗證 100% 隔離：正式記憶清單中讀不到
	memories, err := store.ListMemories(ctx, ws)
	if err != nil {
		t.Fatalf("failed to list memories: %v", err)
	}
	if len(memories) != 0 {
		t.Errorf("expected 0 formal memories, got %d", len(memories))
	}

	// 驗證 100% 隔離：Recall 讀不到
	recalled, _, err := store.RecallCandidates(ctx, ws)
	if err != nil {
		t.Fatalf("failed to recall memories: %v", err)
	}
	if len(recalled) != 0 {
		t.Errorf("expected 0 recalled memories, got %d", len(recalled))
	}
}

func TestGitMem_CandidateCRUD(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/test"

	c := &MemoryCandidate{
		ID:            "cand-100",
		Workspace:     ws,
		RepoID:        "repo-abc",
		RefName:       "refs/heads/feature",
		BaseSHA:       "1111111111111111111111111111111111111111",
		HeadSHA:       "2222222222222222222222222222222222222222",
		CommitCount:   1,
		FactsSummary:  "summary abc",
		DraftContent:  "draft abc",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}

	if err := store.SaveCandidate(ctx, c); err != nil {
		t.Fatalf("failed to save candidate: %v", err)
	}

	// 依 Range 查詢
	found, err := store.FindCandidateByRange(ctx, ws, c.RepoID, c.RefName, c.BaseSHA, c.HeadSHA, "gitmem-v1")
	if err != nil {
		t.Fatalf("failed to find candidate by range: %v", err)
	}
	if found == nil || found.ID != c.ID {
		t.Fatalf("expected candidate ID %s, got %v", c.ID, found)
	}

	// 透過原子核准交易更新狀態為 approved
	memID, err := store.ApproveCandidateTx(ctx, ws, c.ID, ApproveOptions{Content: "approved content"})
	if err != nil {
		t.Fatalf("failed to approve candidate: %v", err)
	}

	// 依 ID 重新取得並驗證欄位
	reloaded, err := store.GetCandidate(ctx, ws, c.ID)
	if err != nil {
		t.Fatalf("failed to get candidate: %v", err)
	}
	if reloaded.Status != "approved" {
		t.Errorf("expected status 'approved', got '%s'", reloaded.Status)
	}
	if reloaded.ApprovedMemoryID != memID {
		t.Errorf("expected ApprovedMemoryID %s, got '%s'", memID, reloaded.ApprovedMemoryID)
	}
	if reloaded.ApprovedAt == nil {
		t.Errorf("expected ApprovedAt to be non-nil")
	}

	// 依 status 列出候選
	list, err := store.ListCandidates(ctx, ws, "approved", 10)
	if err != nil {
		t.Fatalf("failed to list candidates: %v", err)
	}
	if len(list) != 1 || list[0].ID != c.ID {
		t.Errorf("expected 1 approved candidate, got %d", len(list))
	}
}

func TestGitMem_CheckpointCRUD(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/test"

	cpMain := &GitCheckpoint{
		Workspace:       ws,
		RepoID:          "repo-1",
		RefName:         "refs/heads/main",
		LastCapturedSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		UpdatedAt:       time.Now().UTC(),
	}
	cpFeat := &GitCheckpoint{
		Workspace:       ws,
		RepoID:          "repo-1",
		RefName:         "refs/heads/feature",
		LastCapturedSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		UpdatedAt:       time.Now().UTC(),
	}

	if err := store.SaveCheckpoint(ctx, cpMain); err != nil {
		t.Fatalf("failed to save main checkpoint: %v", err)
	}
	if err := store.SaveCheckpoint(ctx, cpFeat); err != nil {
		t.Fatalf("failed to save feat checkpoint: %v", err)
	}

	// 驗證分支隔離
	gotMain, err := store.GetCheckpoint(ctx, ws, "repo-1", "refs/heads/main")
	if err != nil {
		t.Fatalf("failed to get main checkpoint: %v", err)
	}
	if gotMain == nil || gotMain.LastCapturedSHA != cpMain.LastCapturedSHA {
		t.Errorf("expected main SHA %s, got %v", cpMain.LastCapturedSHA, gotMain)
	}

	gotFeat, err := store.GetCheckpoint(ctx, ws, "repo-1", "refs/heads/feature")
	if err != nil {
		t.Fatalf("failed to get feat checkpoint: %v", err)
	}
	if gotFeat == nil || gotFeat.LastCapturedSHA != cpFeat.LastCapturedSHA {
		t.Errorf("expected feat SHA %s, got %v", cpFeat.LastCapturedSHA, gotFeat)
	}

	// 更新 checkpoint
	cpMain.LastCapturedSHA = "cccccccccccccccccccccccccccccccccccccccc"
	cpMain.UpdatedAt = time.Now().UTC()
	if err := store.SaveCheckpoint(ctx, cpMain); err != nil {
		t.Fatalf("failed to update main checkpoint: %v", err)
	}

	reloadedMain, err := store.GetCheckpoint(ctx, ws, "repo-1", "refs/heads/main")
	if err != nil {
		t.Fatalf("failed to reload main checkpoint: %v", err)
	}
	if reloadedMain.LastCapturedSHA != "cccccccccccccccccccccccccccccccccccccccc" {
		t.Errorf("expected updated SHA, got %s", reloadedMain.LastCapturedSHA)
	}
}

func TestGitMem_ProvenanceCascadeDelete(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/test"

	// 1. 建立正式記憶
	mem := Memory{
		ID:        "mem-123",
		Workspace: ws,
		Content:   "Architecture decision: isolated memory_candidates table",
		Source:    "git-approved",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateMemory(ctx, mem); err != nil {
		t.Fatalf("failed to create memory: %v", err)
	}

	// 2. 建立候選記憶
	cand := &MemoryCandidate{
		ID:               "cand-123",
		Workspace:        ws,
		RepoID:           "repo-1",
		RefName:          "refs/heads/main",
		BaseSHA:          "base-sha",
		HeadSHA:          "head-sha",
		CommitCount:      1,
		FactsSummary:     "facts",
		DraftContent:     "draft",
		Status:           "approved",
		PolicyVersion:    "gitmem-v1",
		ApprovedMemoryID: mem.ID,
		CreatedAt:        time.Now().UTC(),
	}
	if err := store.SaveCandidate(ctx, cand); err != nil {
		t.Fatalf("failed to save candidate: %v", err)
	}

	// 3. 建立 Provenance 關聯
	prov := &MemoryProvenance{
		MemoryID:       mem.ID,
		CandidateID:    cand.ID,
		Workspace:      ws,
		RepoID:         "repo-1",
		RefName:        "refs/heads/main",
		BaseSHA:        "base-sha",
		HeadSHA:        "head-sha",
		CommitCount:    1,
		CommitSHAsJSON: "[\"head-sha\"]",
		FactsSummary:   "facts",
		IsUserEdited:   false,
		ApprovedAt:     time.Now().UTC(),
	}
	if err := store.SaveProvenance(ctx, prov); err != nil {
		t.Fatalf("failed to save provenance: %v", err)
	}

	// 驗證讀取 Provenance
	gotProv, err := store.GetProvenance(ctx, mem.ID)
	if err != nil {
		t.Fatalf("failed to get provenance: %v", err)
	}
	if gotProv == nil || gotProv.CandidateID != cand.ID {
		t.Fatalf("expected candidate ID %s in provenance, got %v", cand.ID, gotProv)
	}

	// 4. 刪除正式記憶
	if err := store.DeleteMemory(ctx, ws, mem.ID); err != nil {
		t.Fatalf("failed to delete memory: %v", err)
	}

	// 5. 驗證 ON DELETE CASCADE：provenance 自動被刪除
	deletedProv, err := store.GetProvenance(ctx, mem.ID)
	if err != nil {
		t.Fatalf("expected nil or error, got err: %v", err)
	}
	if deletedProv != nil {
		t.Errorf("expected provenance to be cascade deleted, but it still exists")
	}

	// 6. 驗證不可變稽核痕跡：candidate 仍然存在
	keptCand, err := store.GetCandidate(ctx, ws, cand.ID)
	if err != nil {
		t.Fatalf("failed to get candidate: %v", err)
	}
	if keptCand == nil || keptCand.ApprovedMemoryID != mem.ID {
		t.Errorf("expected candidate to survive and retain audit record")
	}
}

func TestTruncateUTF8Safe_MultiByteCharacters(t *testing.T) {
	// 繁體中文每個漢字佔 3 個 bytes
	chineseText := "架構決策與變更摘要：專案記憶安全邊界與有界隔離防護策略"

	for maxBytes := 1; maxBytes <= len(chineseText)+5; maxBytes++ {
		truncated := TruncateUTF8Safe(chineseText, maxBytes)
		if len(truncated) > maxBytes {
			t.Fatalf("maxBytes=%d: truncated length %d exceeds maxBytes", maxBytes, len(truncated))
		}
		if !utf8.ValidString(truncated) {
			t.Fatalf("maxBytes=%d: truncated string is not valid UTF-8! bytes: %v", maxBytes, []byte(truncated))
		}
		if strings.ContainsRune(truncated, utf8.RuneError) {
			t.Fatalf("maxBytes=%d: truncated string contains utf8.RuneError (U+FFFD)", maxBytes)
		}
	}

	// 測試在 ListCandidates 中使用超長繁體中文
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/utf8-test"

	// 構造約 600 bytes 的繁體中文草稿
	longChineseDraft := strings.Repeat("架構決策與核心隔離約束規範。", 20)
	cand := &MemoryCandidate{
		ID:            "cand-utf8-1",
		Workspace:     ws,
		RepoID:        "repo-utf8",
		RefName:       "refs/heads/main",
		BaseSHA:       "base-utf8",
		HeadSHA:       "head-utf8",
		CommitCount:   1,
		FactsSummary:  "{}",
		DraftContent:  longChineseDraft,
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}
	if err := store.SaveCandidate(ctx, cand); err != nil {
		t.Fatal(err)
	}

	cands, err := store.ListCandidates(ctx, ws, "pending", 10)
	if err != nil {
		t.Fatalf("ListCandidates failed: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(cands))
	}

	summary := cands[0].Summary
	if !strings.HasSuffix(summary, "...") {
		t.Errorf("expected summary to end with '...', got: %s", summary)
	}
	rawWithoutEllipsis := strings.TrimSuffix(summary, "...")
	if len(rawWithoutEllipsis) > 512 {
		t.Errorf("expected raw summary <= 512 bytes, got %d", len(rawWithoutEllipsis))
	}
	if !utf8.ValidString(summary) {
		t.Fatalf("summary is not valid UTF-8: %v", []byte(summary))
	}
	if strings.ContainsRune(summary, utf8.RuneError) {
		t.Fatal("summary contains broken UTF-8 character (U+FFFD)")
	}
}

func TestSQLiteStore_PartialCandidateMigrationBackfill(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy_migration.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 手動建立舊版 memory_candidates 表（不含 is_truncated 與 truncated_reason）
	createOldTable := `
	CREATE TABLE memory_candidates (
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
	INSERT INTO memory_candidates (
		id, workspace, repo_id, ref_name, base_sha, head_sha,
		commit_count, facts_summary, draft_content, status,
		policy_version, approved_memory_id, created_at
	) VALUES (
		'cand-old-partial', '/ws', 'r1', 'refs/heads/main', 'b1', 'h1',
		1, '{"truncated":true,"truncated_reason":"limit"}', 'draft', 'pending',
		'gitmem-v1', '', '2026-09-25 12:00:00'
	);
	`
	if _, err := db.Exec(createOldTable); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// 2. 透過 NewSQLiteStore 開啟，觸發 schema migration 與 backfill
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed on legacy schema: %v", err)
	}
	defer store.Close()

	// 3. 讀回舊候選，驗證已被正確遷移為 is_truncated=true 與 is_user_edited=false
	cand, err := store.GetCandidate(context.Background(), "/ws", "cand-old-partial")
	if err != nil {
		t.Fatalf("failed to get migrated candidate: %v", err)
	}
	if !cand.IsTruncated {
		t.Fatalf("expected legacy truncated candidate to be backfilled as is_truncated=true, got: %v", cand.IsTruncated)
	}
	if cand.IsUserEdited {
		t.Fatalf("expected legacy candidate is_user_edited to default to false, got: %v", cand.IsUserEdited)
	}
}

func TestLegacyMigration_BackfillIsTruncatedFromMemoryProvenanceAfterClearCandidates(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "legacy_cleared.db")

	// 1. 手動建立舊版 schema（不具備 is_truncated 欄位）
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	createOldTable := `
	CREATE TABLE memory_candidates (
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

	CREATE TABLE memories (
		id TEXT PRIMARY KEY,
		workspace TEXT NOT NULL,
		content TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT 'manual',
		created_at DATETIME NOT NULL
	);

	CREATE TABLE memory_provenance (
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
		approved_at DATETIME NOT NULL
	);

	CREATE TABLE git_checkpoints (
		workspace TEXT NOT NULL,
		repo_id TEXT NOT NULL,
		ref_name TEXT NOT NULL,
		last_captured_sha TEXT NOT NULL,
		updated_at DATETIME NOT NULL,
		PRIMARY KEY (workspace, repo_id, ref_name)
	);

	-- 插入已被 clear-candidates 清除的候選（facts_summary='{}'，draft_content=''）
	INSERT INTO memory_candidates (
		id, workspace, repo_id, ref_name, base_sha, head_sha,
		commit_count, facts_summary, draft_content, status,
		policy_version, approved_memory_id, created_at, approved_at
	) VALUES (
		'cand-cleared-approved', '/ws/cleared', 'r1', 'refs/heads/main', 'base-old', 'head-old',
		1, '{}', '', 'approved',
		'gitmem-v1', 'mem-cleared-1', '2026-09-25 10:00:00', '2026-09-25 10:05:00'
	);

	INSERT INTO memories (id, workspace, content, source, created_at)
	VALUES ('mem-cleared-1', '/ws/cleared', 'approved memory', 'git-approved', '2026-09-25 10:05:00');

	-- 原始截斷事實保留於 memory_provenance
	INSERT INTO memory_provenance (
		memory_id, candidate_id, workspace, repo_id, ref_name,
		base_sha, head_sha, commit_count, commit_shas_json,
		facts_summary, is_user_edited, approved_at
	) VALUES (
		'mem-cleared-1', 'cand-cleared-approved', '/ws/cleared', 'r1', 'refs/heads/main',
		'base-old', 'head-old', 1, '[]',
		'{"truncated":true,"truncated_reason":"limit"}', 0, '2026-09-25 10:05:00'
	);

	-- Checkpoint 停滯在 base-old
	INSERT INTO git_checkpoints (workspace, repo_id, ref_name, last_captured_sha, updated_at)
	VALUES ('/ws/cleared', 'r1', 'refs/heads/main', 'base-old', '2026-09-25 10:00:00');
	`
	if _, err := db.Exec(createOldTable); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// 2. 透過 NewSQLiteStore 開啟，觸發 schema migration、backfill 與 legacy policy 隔離
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed on legacy schema: %v", err)
	}
	defer store.Close()

	// Candidate 需從 provenance 辨識為 Partial，並隔離於新 policy 之外。
	cand, err := store.GetCandidate(context.Background(), "/ws/cleared", "cand-cleared-approved")
	if err != nil {
		t.Fatalf("get candidate failed: %v", err)
	}
	if !cand.IsTruncated {
		t.Fatalf("expected cleared candidate to be backfilled as is_truncated=true via provenance, got: %v", cand.IsTruncated)
	}
	if cand.PolicyVersion != "gitmem-v1-legacy-approved-partial" {
		t.Fatalf("expected legacy approved partial policy quarantine, got: %s", cand.PolicyVersion)
	}
	legacyMemory, err := store.GetMemory(context.Background(), "/ws/cleared", "mem-cleared-1")
	if err != nil {
		t.Fatalf("get quarantined legacy memory: %v", err)
	}
	if legacyMemory.Source != "git-approved-legacy-partial" {
		t.Fatalf("expected quarantined legacy memory source, got: %s", legacyMemory.Source)
	}
	recalled, _, err := store.RecallCandidates(context.Background(), "/ws/cleared")
	if err != nil {
		t.Fatalf("recall candidates: %v", err)
	}
	if len(recalled) != 0 {
		t.Fatalf("expected legacy partial memory to be excluded from bounded recall, got: %+v", recalled)
	}

	// Migration 不得跳過未完整擷取的 range。
	cp, err := store.GetCheckpoint(context.Background(), "/ws/cleared", "r1", "refs/heads/main")
	if err != nil {
		t.Fatalf("get checkpoint failed: %v", err)
	}
	if cp.LastCapturedSHA != "base-old" {
		t.Fatalf("expected checkpoint to remain at base-old, got: %s", cp.LastCapturedSHA)
	}

	// 新 policy 可對同一 range 建立 Complete 候選，並於同一交易推進 Checkpoint。
	complete := &MemoryCandidate{
		ID:            "cand-complete-recapture",
		Workspace:     "/ws/cleared",
		RepoID:        "r1",
		RefName:       "refs/heads/main",
		BaseSHA:       "base-old",
		HeadSHA:       "head-old",
		CommitCount:   1,
		FactsSummary:  `{"truncated":false}`,
		DraftContent:  "complete recapture",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}
	newCP := &GitCheckpoint{
		Workspace:       "/ws/cleared",
		RepoID:          "r1",
		RefName:         "refs/heads/main",
		LastCapturedSHA: "head-old",
		UpdatedAt:       time.Now().UTC(),
	}
	if _, err := store.SaveCandidateAndCheckpointTx(context.Background(), complete, newCP, "base-old", func(context.Context, string, string) (bool, error) {
		return true, nil
	}); err != nil {
		t.Fatalf("save complete recapture after migration: %v", err)
	}
	cp, err = store.GetCheckpoint(context.Background(), "/ws/cleared", "r1", "refs/heads/main")
	if err != nil {
		t.Fatalf("get checkpoint after complete recapture: %v", err)
	}
	if cp.LastCapturedSHA != "head-old" {
		t.Fatalf("expected complete recapture to advance checkpoint to head-old, got: %s", cp.LastCapturedSHA)
	}
}

func TestLegacyMigration_QuarantinesApprovedCandidateWithNoRemainingPartialEvidence(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "legacy_unknown.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
	CREATE TABLE memory_candidates (
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
	CREATE TABLE git_checkpoints (
		workspace TEXT NOT NULL,
		repo_id TEXT NOT NULL,
		ref_name TEXT NOT NULL,
		last_captured_sha TEXT NOT NULL,
		updated_at DATETIME NOT NULL,
		PRIMARY KEY (workspace, repo_id, ref_name)
	);
	INSERT INTO memory_candidates (
		id, workspace, repo_id, ref_name, base_sha, head_sha,
		commit_count, facts_summary, draft_content, status,
		policy_version, approved_memory_id, created_at, approved_at
	) VALUES (
		'cand-legacy-unknown', '/ws/unknown', 'r1', 'refs/heads/main', 'base-old', 'head-old',
		1, '{}', '', 'approved', 'gitmem-v1', 'mem-deleted',
		'2026-09-25 10:00:00', '2026-09-25 10:05:00'
	);
	INSERT INTO git_checkpoints (workspace, repo_id, ref_name, last_captured_sha, updated_at)
	VALUES ('/ws/unknown', 'r1', 'refs/heads/main', 'base-old', '2026-09-25 10:00:00');
	`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cand, err := store.GetCandidate(context.Background(), "/ws/unknown", "cand-legacy-unknown")
	if err != nil {
		t.Fatal(err)
	}
	if cand.PolicyVersion != "gitmem-v1-legacy-unknown" {
		t.Fatalf("expected legacy unknown quarantine, got: %s", cand.PolicyVersion)
	}
	cp, err := store.GetCheckpoint(context.Background(), "/ws/unknown", "r1", "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if cp.LastCapturedSHA != "base-old" {
		t.Fatalf("migration advanced checkpoint: got %s", cp.LastCapturedSHA)
	}
	current, err := store.FindCandidateByRange(context.Background(), "/ws/unknown", "r1", "refs/heads/main", "base-old", "head-old", "gitmem-v1")
	if err != nil {
		t.Fatal(err)
	}
	if current != nil {
		t.Fatalf("legacy unknown candidate still occupies gitmem-v1 range: %+v", current)
	}
	complete := &MemoryCandidate{
		ID: "cand-unknown-recapture", Workspace: "/ws/unknown", RepoID: "r1", RefName: "refs/heads/main",
		BaseSHA: "base-old", HeadSHA: "head-old", CommitCount: 1,
		FactsSummary: `{"truncated":false}`, DraftContent: "complete recapture",
		Status: "pending", PolicyVersion: "gitmem-v1", CreatedAt: time.Now().UTC(),
	}
	if _, err := store.SaveCandidateAndCheckpointTx(context.Background(), complete, &GitCheckpoint{
		Workspace: "/ws/unknown", RepoID: "r1", RefName: "refs/heads/main",
		LastCapturedSHA: "head-old", UpdatedAt: time.Now().UTC(),
	}, "base-old", func(context.Context, string, string) (bool, error) { return true, nil }); err != nil {
		t.Fatalf("save complete recapture after legacy unknown quarantine: %v", err)
	}
}

func TestCurrentSchema_DoesNotQuarantineClearedApprovedCompleteCandidate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "current_schema.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	cand := &MemoryCandidate{
		ID: "cand-current-complete", Workspace: "/ws/current", RepoID: "r1", RefName: "refs/heads/main",
		BaseSHA: "base", HeadSHA: "head", CommitCount: 1, FactsSummary: `{}`, DraftContent: "",
		Status: "approved", PolicyVersion: "gitmem-v1", ApprovedMemoryID: "mem-deleted",
		CreatedAt: time.Now().UTC(), IsTruncated: false,
	}
	if err := store.SaveCandidate(context.Background(), cand); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.GetCandidate(context.Background(), "/ws/current", cand.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PolicyVersion != "gitmem-v1" {
		t.Fatalf("current-schema complete candidate was quarantined: %s", got.PolicyVersion)
	}
}
