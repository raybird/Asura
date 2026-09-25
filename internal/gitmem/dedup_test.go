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

func TestCaptureService_ManualRangeDoesNotRewindCheckpoint(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	repoInfo, err := exec.ValidateAndResolveRepo(ctx, repoDir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 初始化 Checkpoint 指向最新 HEAD
	initialHead := repoInfo.CurrentHead
	if err := store.SaveCheckpoint(ctx, &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: initialHead,
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	// 2. 執行手動歷史舊範圍 --range HEAD~2..HEAD~1
	resOld, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~2..HEAD~1"})
	if err != nil {
		t.Fatalf("manual range capture failed: %v", err)
	}
	if resOld.Candidate == nil {
		t.Fatal("expected candidate generated for manual range")
	}

	// 3. 嚴格斷言：Checkpoint 必須依然維持在 initialHead，絕對不能被手動舊範圍倒退至 HEAD~1
	cpAfter, err := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if err != nil || cpAfter == nil {
		t.Fatalf("failed to query checkpoint: %v", err)
	}
	if cpAfter.LastCapturedSHA != initialHead {
		t.Fatalf("SECURITY/DATA INTEGRITY FAIL: Manual range rewound checkpoint from %s to %s", initialHead, cpAfter.LastCapturedSHA)
	}
}

func TestCaptureService_AncestryCheck_RejectsDivergedHistory(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	repoInfo, err := exec.ValidateAndResolveRepo(ctx, repoDir)
	if err != nil {
		t.Fatal(err)
	}

	// 取得分叉分支 feature 的 HEAD SHA（真實存在於 repo 中，但不是 main 的祖先）
	featureRes, err := exec.ResolveRange(ctx, repoDir, "feature~1..feature")
	if err != nil {
		t.Fatal(err)
	}
	divergedSHA := featureRes.HeadSHA

	// 設定 Checkpoint 為 divergedSHA，但目前處於 main 分支
	_ = store.SaveCheckpoint(ctx, &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: divergedSHA,
		UpdatedAt:       time.Now().UTC(),
	})

	// 執行增量 SinceLast 擷取：因 main HEAD 不是 feature commit 的後裔，必須被 Ancestor 檢查拒絕
	_, err = service.Capture(ctx, repoDir, CaptureOptions{SinceLast: true})
	if err == nil {
		t.Fatal("expected ancestry verification failure on diverged history, got nil")
	}
	if !strings.Contains(err.Error(), "not a descendant of previous checkpoint") && !strings.Contains(err.Error(), "is not an ancestor of head commit") {
		t.Errorf("expected ancestry rejection error message, got: %v", err)
	}
}

func TestCaptureService_ConcurrentCaptureAndDraftImmutability(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	// 1. 並行測試：5 個 goroutine 同時針對相同 range 發起 Capture
	concurrency := 5
	errCh := make(chan error, concurrency)
	candCh := make(chan *storage.MemoryCandidate, concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			res, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD"})
			if err != nil {
				errCh <- err
				return
			}
			candCh <- res.Candidate
			errCh <- nil
		}()
	}

	var firstCandID string
	for i := 0; i < concurrency; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("concurrent capture failed: %v", err)
		}
		cand := <-candCh
		if firstCandID == "" {
			firstCandID = cand.ID
		} else if cand.ID != firstCandID {
			t.Errorf("ID mismatch under concurrent capture: got %s, expected %s", cand.ID, firstCandID)
		}
	}

	// 驗證資料庫中該 ID 確實存在且只有 1 筆候選記錄
	cands, err := store.ListCandidates(ctx, repoDir, "pending", 50)
	if err != nil {
		t.Fatalf("list candidates failed: %v", err)
	}
	if len(cands) != 1 || cands[0].ID != firstCandID {
		t.Fatalf("expected exactly 1 candidate with ID %s, got %v", firstCandID, cands)
	}

	// 2. 使用者手動編輯草稿內容
	userEditedDraft := "User edited draft content: critical security policy decision"
	if err := store.UpdateCandidateDraft(ctx, repoDir, firstCandID, userEditedDraft); err != nil {
		t.Fatalf("update draft failed: %v", err)
	}

	// 3. 再次對相同 Range 發起 Capture：草稿與候選必須保持不可變，絕對不可被預設生成內容覆寫
	resRepeat, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD"})
	if err != nil {
		t.Fatalf("repeat capture failed: %v", err)
	}
	if !resRepeat.IsReused {
		t.Errorf("expected candidate to be reused")
	}
	if resRepeat.Candidate.DraftContent != userEditedDraft {
		t.Fatalf("DATA INTEGRITY BUG: User edited draft content was overwritten! Got %q, expected %q", resRepeat.Candidate.DraftContent, userEditedDraft)
	}
}

