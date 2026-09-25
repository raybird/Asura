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

func TestCaptureService_ConcurrentCheckpointAdvancement_NeverRewindsStaleHead(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	ctx := context.Background()

	// 取得當前 commits: HEAD~2 (C1), HEAD~1 (C2), HEAD (C3)
	c1Out, _ := exec.Exec(ctx, repoDir, "rev-parse", "HEAD~2")
	c1 := strings.TrimSpace(string(c1Out))
	c2Out, _ := exec.Exec(ctx, repoDir, "rev-parse", "HEAD~1")
	c2 := strings.TrimSpace(string(c2Out))
	c3Out, _ := exec.Exec(ctx, repoDir, "rev-parse", "HEAD")
	c3 := strings.TrimSpace(string(c3Out))

	repoInfo, _ := exec.ValidateAndResolveRepo(ctx, repoDir)

	// 1. 初始化 Checkpoint 位於 C1
	initialCP := &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: c1,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.SaveCheckpoint(ctx, initialCP); err != nil {
		t.Fatal(err)
	}

	// 2. 模擬並行交易 B：將 Checkpoint 推進至 C3
	bCP := &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: c3,
		UpdatedAt:       time.Now().UTC(),
	}
	ancChecker := func(ctx context.Context, anc, desc string) (bool, error) {
		return exec.IsAncestor(ctx, repoDir, anc, desc)
	}
	if err := store.AdvanceCheckpointTx(ctx, bCP, c1, ancChecker); err != nil {
		t.Fatalf("advance to C3 failed: %v", err)
	}

	// 3. 模擬延遲完成的交易 A：A 是根據舊基線 C1 擷取到 C2（C1..C2）
	// 目標是要推進到 C2，expectedOldSHA 為 C1
	aCP := &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: c2,
		UpdatedAt:       time.Now().UTC(),
	}
	candA := &storage.MemoryCandidate{
		ID:            "cand-delay-a",
		Workspace:     repoInfo.Workspace,
		RepoID:        repoInfo.RepoID,
		RefName:       repoInfo.CurrentRef,
		BaseSHA:       c1,
		HeadSHA:       c2,
		CommitCount:   1,
		FactsSummary:  "{}",
		DraftContent:  "draft A",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}

	// 執行 SaveCandidateAndCheckpointTx
	persistedA, err := store.SaveCandidateAndCheckpointTx(ctx, candA, aCP, c1, ancChecker)
	if err != nil {
		t.Fatalf("expected candidate A to save successfully, got: %v", err)
	}
	if persistedA.ID != candA.ID {
		t.Errorf("expected candidate A to be persisted")
	}

	// 4. 關鍵斷言：Checkpoint 必須保持在 C3，絕對不能倒退回 C2！
	cpFinal, err := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if err != nil {
		t.Fatal(err)
	}
	if cpFinal.LastCapturedSHA != c3 {
		t.Fatalf("MONOTONIC INTEGRITY VIOLATION: Checkpoint rewound from C3 to %s!", cpFinal.LastCapturedSHA)
	}
}

type slowAIGenerator struct {
	delay time.Duration
}

func (s *slowAIGenerator) GenerateDraft(ctx context.Context, facts *CaptureFacts) (string, error) {
	select {
	case <-time.After(s.delay):
		return "slow generated draft", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestCaptureService_CaptureBudgetTimeout_DuringAIGeneration(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)

	// 配置一個延遲 100ms 的 AI Generator，但我們傳入只有 20ms 的 context 模擬逾時
	service.SetAIGenerator(&slowAIGenerator{delay: 100 * time.Millisecond})

	shortCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	// 執行 capture，啟用 --ai-draft
	_, err := service.Capture(shortCtx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD", AIDraft: true})
	if err == nil {
		t.Fatal("expected error due to total capture deadline exceeded, got nil")
	}
	if !strings.Contains(err.Error(), "deadline exceeded") && !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected deadline exceeded error, got: %v", err)
	}

	// 關鍵驗證：超時後絕不可寫入 Candidate，且 Checkpoint 絕不前進（0 進展）
	cands, err := store.ListCandidates(context.Background(), repoDir, "pending", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 {
		t.Fatalf("expected 0 candidates after timed out capture, got %d", len(cands))
	}

	repoInfo, _ := exec.ValidateAndResolveRepo(context.Background(), repoDir)
	cp, err := store.GetCheckpoint(context.Background(), repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if err != nil {
		t.Fatal(err)
	}
	if cp != nil {
		t.Fatalf("expected nil checkpoint after timed out capture, got: %+v", cp)
	}
}

func TestCaptureService_ReopenDiscardedCandidate_HandlesConcurrentApprovalSafely(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	// 1. 初次擷取產生 candidate
	res, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	candID := res.Candidate.ID

	// 2. 標記為 discarded
	if err := store.DiscardCandidate(ctx, repoDir, candID); err != nil {
		t.Fatal(err)
	}

	// 3. 模擬交錯：另一個 goroutine 先完成了 reopen 並執行了核准（狀態變為 approved）
	_ = store.ReopenDiscardedCandidate(ctx, repoDir, candID, "first reopened draft", "{}", 1)
	memID, err := store.ApproveCandidateTx(ctx, repoDir, candID, storage.ApproveOptions{Content: "approved content"})
	if err != nil {
		t.Fatal(err)
	}

	// 4. 現在這個延遲的 Capture 呼叫以 --force 嘗試重開此 candidate
	// 由於底層 ReopenDiscardedCandidate 具備 status='discarded' 的 CAS，此時會觸發狀態衝突並重新讀回最新 approved 候選
	resRace, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD", Force: true})
	if err != nil {
		t.Fatalf("expected capture to handle concurrent approval gracefully, got: %v", err)
	}

	// 關鍵驗證：絕不能覆寫為 pending，必須回傳已核准之候選
	if resRace.Candidate.Status != "approved" || resRace.Candidate.ApprovedMemoryID != memID {
		t.Fatalf("DATA INTEGRITY BUG: Approved candidate was corrupted by concurrent reopen! Got: %+v", resRace.Candidate)
	}
	if !resRace.IsReused {
		t.Errorf("expected candidate to be reused")
	}
}



