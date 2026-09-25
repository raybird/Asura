package gitmem

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"asura/internal/storage"
)

func newTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_service.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestCaptureService_DedupPendingAndApproved(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	// 1. 第一次擷取：產生新 pending candidate
	res1, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD"})
	if err != nil {
		t.Fatalf("first capture failed: %v", err)
	}
	if res1.IsReused {
		t.Errorf("expected first capture not reused")
	}
	if res1.Candidate.Status != "pending" {
		t.Errorf("expected pending status, got %s", res1.Candidate.Status)
	}

	// 2. 第二次擷取相同 Range：去重重用既有 pending candidate
	res2, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD"})
	if err != nil {
		t.Fatalf("second capture failed: %v", err)
	}
	if !res2.IsReused || res2.Candidate.ID != res1.Candidate.ID {
		t.Errorf("expected reused pending candidate ID %s, got %s (isReused=%v)", res1.Candidate.ID, res2.Candidate.ID, res2.IsReused)
	}

	// 3. 模擬核准該 candidate
	now := time.Now().UTC()
	_ = store.UpdateCandidateStatus(ctx, res1.Candidate.Workspace, res1.Candidate.ID, "approved", "mem-formal-99", &now)

	// 4. 第三次擷取相同 Range：回傳已核准之候選與既有 approved_memory_id，不重複插入
	res3, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD"})
	if err != nil {
		t.Fatalf("third capture failed: %v", err)
	}
	if !res3.IsReused || res3.Candidate.Status != "approved" || res3.Candidate.ApprovedMemoryID != "mem-formal-99" {
		t.Errorf("expected approved reused candidate, got: %+v", res3.Candidate)
	}
}

func TestCaptureService_DiscardedRequiresForce(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	res, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD"})
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}

	// 標記為 discarded
	_ = store.UpdateCandidateStatus(ctx, res.Candidate.Workspace, res.Candidate.ID, "discarded", "", nil)

	// 不帶 Force 再次擷取：應回報已被捨棄
	_, err = service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD", Force: false})
	if err == nil {
		t.Fatal("expected error for discarded candidate without force, got nil")
	}
	if !strings.Contains(err.Error(), "discarded") {
		t.Errorf("expected discarded error, got: %v", err)
	}

	// 帶 Force 再次擷取：重開為 pending
	resForce, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD", Force: true})
	if err != nil {
		t.Fatalf("expected success with force, got: %v", err)
	}
	if resForce.Candidate.Status != "pending" {
		t.Errorf("expected status reset to pending with force, got: %s", resForce.Candidate.Status)
	}
}

func TestCaptureService_CheckpointProgressionAndPartialBlocking(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	repoInfo, err := exec.ValidateAndResolveRepo(ctx, repoDir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Complete Capture：啟用增量模式（SinceLast）時推進 Checkpoint
	// 先顯式初始化一次 capture 並建立 baseline checkpoint
	resolved, err := exec.ResolveRange(ctx, repoDir, "HEAD~2..HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	_ = store.SaveCheckpoint(ctx, &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: resolved.HeadSHA,
		UpdatedAt:       time.Now().UTC(),
	})

	// 使用 SinceLast 執行增量擷取
	resComplete, err := service.Capture(ctx, repoDir, CaptureOptions{SinceLast: true})
	if err != nil {
		t.Fatalf("since-last complete capture failed: %v", err)
	}
	if resComplete.Facts.Truncated {
		t.Fatal("expected complete capture")
	}

	// 驗證 Checkpoint 前進至 HEAD
	cp, err := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if err != nil || cp == nil {
		t.Fatalf("failed to get checkpoint: %v", err)
	}
	if cp.LastCapturedSHA != resComplete.Facts.HeadSHA {
		t.Errorf("expected checkpoint advanced to %s, got %s", resComplete.Facts.HeadSHA, cp.LastCapturedSHA)
	}

	// 2. Partial Capture：驗證絕對禁止自動推進 Checkpoint
	// 手動模擬一次 Partial Capture
	partialFacts := &CaptureFacts{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		BaseSHA:         cp.LastCapturedSHA,
		HeadSHA:         "9999999999999999999999999999999999999999",
		CommitCount:     1,
		Truncated:       true,
		TruncatedReason: "file limit exceeded",
	}

	err = service.UpdateCheckpointIfEligible(ctx, partialFacts, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 再次檢查 Checkpoint，必須保持原狀，未前進至 9999999999999999999999999999999999999999
	cpAfterPartial, _ := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if cpAfterPartial.LastCapturedSHA == "9999999999999999999999999999999999999999" {
		t.Fatal("SECURITY/DATA BUG: Checkpoint advanced on Partial Capture!")
	}
	if cpAfterPartial.LastCapturedSHA != cp.LastCapturedSHA {
		t.Errorf("expected checkpoint unchanged, got %s", cpAfterPartial.LastCapturedSHA)
	}
}
