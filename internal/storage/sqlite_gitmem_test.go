package storage

import (
	"context"
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
