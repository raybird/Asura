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
	approvedMemID, err := store.ApproveCandidateTx(ctx, res1.Candidate.Workspace, res1.Candidate.ID, storage.ApproveOptions{Content: "approved content"})
	if err != nil {
		t.Fatalf("approve candidate failed: %v", err)
	}

	// 4. 第三次擷取相同 Range：回傳已核准之候選與既有 approved_memory_id，不重複插入
	res3, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD"})
	if err != nil {
		t.Fatalf("third capture failed: %v", err)
	}
	if !res3.IsReused || res3.Candidate.Status != "approved" || res3.Candidate.ApprovedMemoryID != approvedMemID {
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
	if err := store.DiscardCandidate(ctx, res.Candidate.Workspace, res.Candidate.ID); err != nil {
		t.Fatalf("discard candidate failed: %v", err)
	}

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

type signalingAIGenerator struct {
	entered chan struct{}
}

func (s *signalingAIGenerator) GenerateDraft(ctx context.Context, facts *CaptureFacts) (string, error) {
	select {
	case <-s.entered:
	default:
		close(s.entered)
	}
	<-ctx.Done()
	return "", ctx.Err()
}

func TestCaptureService_CaptureBudgetTimeout_DuringAIGeneration(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)

	enteredCh := make(chan struct{})
	service.SetAIGenerator(&signalingAIGenerator{entered: enteredCh})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD", AIDraft: true})
		errCh <- err
	}()

	// 確保必定進入 AI 階段（證明 Git 階段成功，超時發生在後續階段）
	select {
	case <-enteredCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for AI generation stage to start")
	}

	// 確定進入 AI 階段後觸發取消/超時
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error due to total capture deadline exceeded, got nil")
		}
		if !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "deadline exceeded") && !strings.Contains(err.Error(), "timed out") {
			t.Errorf("expected deadline exceeded or canceled error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for capture to abort")
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

type concurrentInterleavingGenerator struct {
	onGenerate func()
}

func (g *concurrentInterleavingGenerator) GenerateDraft(ctx context.Context, facts *CaptureFacts) (string, error) {
	if g.onGenerate != nil {
		g.onGenerate()
	}
	return "interleaved draft", nil
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

	var approvedMemID string
	// 3. 透過注入的 AIGenerator，精確模擬在 A 讀到 Discarded 候選後、執行 ReopenDiscardedCandidate 寫入前，
	// 並行交易 B 完成 Reopen 並完成了核准（狀態變為 approved）
	interleaver := &concurrentInterleavingGenerator{
		onGenerate: func() {
			_ = store.ReopenDiscardedCandidate(ctx, repoDir, candID, "parallel reopened draft", "{}", 1, false, "")
			memID, approveErr := store.ApproveCandidateTx(ctx, repoDir, candID, storage.ApproveOptions{Content: "approved content"})
			if approveErr != nil {
				t.Errorf("concurrent approval failed: %v", approveErr)
			}
			approvedMemID = memID
		},
	}
	service.SetAIGenerator(interleaver)

	// 4. 現在 A 執行帶 Force 的 Capture，此時 A 的程式碼已讀取到 discarded 狀態，但底層記錄被 B 更改為 approved；
	// A 呼叫 ReopenDiscardedCandidate 時因 status='discarded' 條件不符觸發 ErrCandidateStatusConflict，
	// 並安全重新讀回已被 B 核准的候選
	resRace, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD", Force: true, AIDraft: true})
	if err != nil {
		t.Fatalf("expected capture to handle concurrent approval gracefully, got: %v", err)
	}

	// 關鍵驗證：絕不能覆寫為 pending，必須回傳已被並行核准之候選
	if resRace.Candidate.Status != "approved" || resRace.Candidate.ApprovedMemoryID != approvedMemID {
		t.Fatalf("DATA INTEGRITY BUG: Approved candidate was corrupted by concurrent reopen! Got: %+v", resRace.Candidate)
	}
	if !resRace.IsReused {
		t.Errorf("expected candidate to be reused")
	}

	// 再次從資料庫確認狀態依然是 approved
	dbCand, err := store.GetCandidate(ctx, repoDir, candID)
	if err != nil {
		t.Fatal(err)
	}
	if dbCand.Status != "approved" {
		t.Fatalf("expected database status to remain approved, got: %s", dbCand.Status)
	}
}

func TestCaptureService_AIDraft_PreflightFailClosed_EvenIfCandidateExists(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	repoInfo, err := exec.ValidateAndResolveRepo(ctx, repoDir)
	if err != nil {
		t.Fatal(err)
	}

	// 初始化 Checkpoint 至 HEAD~1
	resolved, err := exec.ResolveRange(ctx, repoDir, "HEAD~2..HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	initialCP := &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: resolved.HeadSHA,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.SaveCheckpoint(ctx, initialCP); err != nil {
		t.Fatal(err)
	}

	// 1. 先用普通模式建立一個 pending candidate (HEAD~1..HEAD)
	resNormal, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD"})
	if err != nil {
		t.Fatalf("normal capture failed: %v", err)
	}
	if resNormal.Candidate.Status != "pending" {
		t.Fatalf("expected pending status, got %s", resNormal.Candidate.Status)
	}

	// 2. 當未配置 AI Generator 時，若使用者指定 --ai-draft，即使候選已存在，也必須 Fail-Closed 拒絕
	_, err = service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD", AIDraft: true})
	if err == nil {
		t.Fatal("expected preflight fail-closed error for --ai-draft without generator, got nil")
	}
	if !strings.Contains(err.Error(), "no AI generator is configured") {
		t.Fatalf("expected 'no AI generator is configured' error, got: %v", err)
	}

	// 3. 測試增量模式 (--since-last) 下帶 --ai-draft 且未配置 generator：
	// 絕不能因為命中既有 pending 候選而放行，更絕對禁止前進 Checkpoint
	_, err = service.Capture(ctx, repoDir, CaptureOptions{SinceLast: true, AIDraft: true})
	if err == nil {
		t.Fatal("expected fail-closed error under --since-last with --ai-draft, got nil")
	}
	if !strings.Contains(err.Error(), "no AI generator is configured") {
		t.Fatalf("expected 'no AI generator is configured' error, got: %v", err)
	}

	// 驗證 Checkpoint 保持不變，絕未推進
	cpAfter, err := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if err != nil {
		t.Fatal(err)
	}
	if cpAfter.LastCapturedSHA != initialCP.LastCapturedSHA {
		t.Fatalf("SECURITY VIOLATION: Checkpoint advanced despite missing AI generator! Was %s, became %s", initialCP.LastCapturedSHA, cpAfter.LastCapturedSHA)
	}

	// 4. 將該候選核准為 approved
	if _, err := store.ApproveCandidateTx(ctx, repoDir, resNormal.Candidate.ID, storage.ApproveOptions{Content: "approved content"}); err != nil {
		t.Fatal(err)
	}

	// 5. 候選已核准的狀況下，帶 --ai-draft 再次呼叫：依然必須 Fail-Closed 拒絕，不允許跳過去重
	_, err = service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD", AIDraft: true})
	if err == nil {
		t.Fatal("expected fail-closed error even when candidate is approved, got nil")
	}
	if !strings.Contains(err.Error(), "no AI generator is configured") {
		t.Fatalf("expected 'no AI generator is configured' error, got: %v", err)
	}
}

type deadlineCheckingGenerator struct {
	observedDeadline time.Time
	hasDeadline      bool
}

func (g *deadlineCheckingGenerator) GenerateDraft(ctx context.Context, facts *CaptureFacts) (string, error) {
	g.observedDeadline, g.hasDeadline = ctx.Deadline()
	return "deadline checked draft", nil
}

func TestCaptureService_LongCallerDeadline_EnforcesHardTenSecondLimit(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)

	dlGen := &deadlineCheckingGenerator{}
	service.SetAIGenerator(dlGen)

	// Caller 提供超長 30 秒的 Context
	callerCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	res, err := service.Capture(callerCtx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD", AIDraft: true})
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}
	if res.Candidate == nil {
		t.Fatal("expected candidate to be generated")
	}

	// 驗證 AI 階段觀察到的 Context 期限絕對不超過 10 秒硬邊界（GITMEM-004）
	if !dlGen.hasDeadline {
		t.Fatal("expected context to have deadline")
	}
	timeRemainingFromStart := dlGen.observedDeadline.Sub(start)
	if timeRemainingFromStart > 10*time.Second+500*time.Millisecond {
		t.Fatalf("SECURITY VIOLATION: Hard 10s deadline bypassed by long caller deadline! Remaining: %v", timeRemainingFromStart)
	}
}

func TestCaptureService_DiscardedCandidate_NeverAdvancesCheckpointOnFailure(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	repoInfo, err := exec.ValidateAndResolveRepo(ctx, repoDir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 初始化 Checkpoint 在 H0 (HEAD~1)
	resolved, err := exec.ResolveRange(ctx, repoDir, "HEAD~2..HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	h0 := resolved.HeadSHA
	initialCP := &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: h0,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.SaveCheckpoint(ctx, initialCP); err != nil {
		t.Fatal(err)
	}

	// 2. 第一次擷取 H0..HEAD，產生 candidate
	res, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD"})
	if err != nil {
		t.Fatalf("first capture failed: %v", err)
	}
	candID := res.Candidate.ID

	// 3. 捨棄該候選
	if err := store.DiscardCandidate(ctx, repoDir, candID); err != nil {
		t.Fatalf("discard candidate failed: %v", err)
	}

	// 4. 執行增量擷取 (--since-last)，未帶 --force
	// 期望：立即報錯拒絕，且 Checkpoint 絕不前進（必須留在 H0）
	_, err = service.Capture(ctx, repoDir, CaptureOptions{SinceLast: true, Force: false})
	if err == nil {
		t.Fatal("expected error when capturing discarded range without force, got nil")
	}
	if !strings.Contains(err.Error(), "already discarded") {
		t.Fatalf("expected 'already discarded' error, got: %v", err)
	}

	// 關鍵斷言 (MUST FIX 1)：驗證 Checkpoint 保持在 H0，絕對未前進至 HEAD！
	cpAfterFail, err := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if err != nil {
		t.Fatal(err)
	}
	if cpAfterFail.LastCapturedSHA != h0 {
		t.Fatalf("CRITICAL SECURITY BUG: Checkpoint advanced from %s to %s despite discarded candidate rejection!", h0, cpAfterFail.LastCapturedSHA)
	}

	// 5. 測試帶 --force 但 AI 生成失敗時（例如未配置 AI generator），Checkpoint 同樣絕對不前進
	_, err = service.Capture(ctx, repoDir, CaptureOptions{SinceLast: true, Force: true, AIDraft: true})
	if err == nil {
		t.Fatal("expected error when AI generator is missing, got nil")
	}
	cpAfterAIFail, _ := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if cpAfterAIFail.LastCapturedSHA != h0 {
		t.Fatalf("CRITICAL SECURITY BUG: Checkpoint advanced despite AI generation failure! Got %s", cpAfterAIFail.LastCapturedSHA)
	}

	// 6. 帶 --force 且成功重開：此時才合法前進 Checkpoint
	resReopened, err := service.Capture(ctx, repoDir, CaptureOptions{SinceLast: true, Force: true})
	if err != nil {
		t.Fatalf("expected success with force, got: %v", err)
	}
	if resReopened.Candidate.Status != "pending" {
		t.Fatalf("expected candidate status pending, got: %s", resReopened.Candidate.Status)
	}

	cpAfterSuccess, _ := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if cpAfterSuccess.LastCapturedSHA != repoInfo.CurrentHead {
		t.Fatalf("expected checkpoint to advance to %s after successful reopen, got: %s", repoInfo.CurrentHead, cpAfterSuccess.LastCapturedSHA)
	}
}

func TestCaptureService_PartialCandidate_CompleteRetryUpgradesCandidateAndAdvancesCheckpoint(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	repoInfo, err := exec.ValidateAndResolveRepo(ctx, repoDir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 初始化 Checkpoint 在 H0 (HEAD~1)
	resolved, err := exec.ResolveRange(ctx, repoDir, "HEAD~2..HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	h0 := resolved.HeadSHA
	initialCP := &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: h0,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.SaveCheckpoint(ctx, initialCP); err != nil {
		t.Fatal(err)
	}

	// 2. 模擬第一次擷取因為暫態問題或超過限制儲存了 Partial Candidate（is_truncated=true）
	partialCand := &storage.MemoryCandidate{
		ID:              "cand-partial-123",
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		BaseSHA:         h0,
		HeadSHA:         repoInfo.CurrentHead,
		CommitCount:     1,
		FactsSummary:    `{"truncated":true,"truncated_reason":"temporary io error"}`,
		DraftContent:    "incomplete partial draft content",
		Status:          "pending",
		PolicyVersion:   "gitmem-v1",
		CreatedAt:       time.Now().UTC(),
		IsTruncated:     true,
		TruncatedReason: "temporary io error",
	}
	if err := store.SaveCandidate(ctx, partialCand); err != nil {
		t.Fatal(err)
	}

	// 驗證第一次儲存後 Checkpoint 依然保持在 H0（0 前進）
	cpBeforeRetry, _ := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if cpBeforeRetry.LastCapturedSHA != h0 {
		t.Fatalf("expected checkpoint at h0, got: %s", cpBeforeRetry.LastCapturedSHA)
	}

	// 3. 第二次執行增量擷取 (SinceLast)，此時完整讀取成功（!Truncated）
	resComplete, err := service.Capture(ctx, repoDir, CaptureOptions{SinceLast: true})
	if err != nil {
		t.Fatalf("complete retry capture failed: %v", err)
	}

	// 關鍵驗證 1：回傳的 Candidate 必須已被升級為完整狀態，不可沿用第一次不完整草稿
	if resComplete.Candidate.IsTruncated {
		t.Fatal("expected candidate to be upgraded to non-truncated (complete)")
	}
	if resComplete.Candidate.DraftContent == "incomplete partial draft content" {
		t.Fatalf("DATA INTEGRITY BUG: Complete capture returned stale partial draft content!")
	}
	if !strings.Contains(resComplete.Candidate.DraftContent, "專案架構決策與變更摘要") {
		t.Fatalf("expected complete structured draft content, got: %s", resComplete.Candidate.DraftContent)
	}

	// 關鍵驗證 2：檢查資料庫中的持久化 Candidate，確認已在 SQLite 中原子升級為 Complete
	dbCand, err := store.GetCandidate(ctx, repoInfo.Workspace, partialCand.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dbCand.IsTruncated {
		t.Fatal("expected database candidate is_truncated to be false after complete retry")
	}
	if dbCand.DraftContent != resComplete.Candidate.DraftContent {
		t.Fatalf("database draft mismatch: got %q, expected %q", dbCand.DraftContent, resComplete.Candidate.DraftContent)
	}

	// 關鍵驗證 3：確認持久化候選確認完整後，Checkpoint 成功推進至最新 HEAD，與保存內容完全一致
	cpAfterRetry, _ := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if cpAfterRetry.LastCapturedSHA != repoInfo.CurrentHead {
		t.Fatalf("expected checkpoint advanced to %s after complete upgrade, got %s", repoInfo.CurrentHead, cpAfterRetry.LastCapturedSHA)
	}
}

func TestCaptureService_AIDraft_DisclosesExistingCandidateSkipped(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	// 1. 先用普通模式建立一個 pending candidate (HEAD~1..HEAD)
	_, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD"})
	if err != nil {
		t.Fatal(err)
	}

	// 2. 配置 AI Generator，再次以 --ai-draft 呼叫相同 Range
	dlGen := &deadlineCheckingGenerator{}
	service.SetAIGenerator(dlGen)

	res, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD", AIDraft: true})
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}
	if !res.IsReused {
		t.Fatal("expected candidate to be reused")
	}
	// 驗證回傳 Message 明確揭露既有候選未重新執行 AI
	if !strings.Contains(res.Message, "immutable draft preserved") && !strings.Contains(res.Message, "AI generation skipped") {
		t.Fatalf("expected message to disclose AI draft skip for existing candidate, got: %s", res.Message)
	}
}

func TestCaptureService_PartialCandidate_CannotBeApprovedUntilCompleteUpgrade(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	repoInfo, err := exec.ValidateAndResolveRepo(ctx, repoDir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 初始化 Checkpoint 在 H0 (HEAD~1)
	resolved, err := exec.ResolveRange(ctx, repoDir, "HEAD~2..HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	h0 := resolved.HeadSHA
	initialCP := &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: h0,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.SaveCheckpoint(ctx, initialCP); err != nil {
		t.Fatal(err)
	}

	// 2. 模擬第一次擷取產生了 Partial 候選（is_truncated=true）
	partialCand := &storage.MemoryCandidate{
		ID:              "cand-partial-cannot-approve",
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		BaseSHA:         h0,
		HeadSHA:         repoInfo.CurrentHead,
		CommitCount:     1,
		FactsSummary:    `{"truncated":true,"truncated_reason":"exceeded diff limit"}`,
		DraftContent:    "partial draft content",
		Status:          "pending",
		PolicyVersion:   "gitmem-v1",
		CreatedAt:       time.Now().UTC(),
		IsTruncated:     true,
		TruncatedReason: "exceeded diff limit",
	}
	if err := store.SaveCandidate(ctx, partialCand); err != nil {
		t.Fatal(err)
	}

	// 3. MUST FIX 1 核心驗證：嘗試核准 Partial Candidate 必須被嚴格拒絕！
	_, err = store.ApproveCandidateTx(ctx, repoInfo.Workspace, partialCand.ID, storage.ApproveOptions{Content: "try approve partial"})
	if err == nil {
		t.Fatal("SECURITY BUG: ApproveCandidateTx allowed approving partial candidate!")
	}
	if !strings.Contains(err.Error(), "cannot approve partial candidate") {
		t.Fatalf("expected cannot approve partial error, got: %v", err)
	}

	// 驗證 Checkpoint 保持在 H0 零前進
	cpBefore, _ := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if cpBefore.LastCapturedSHA != h0 {
		t.Fatalf("expected checkpoint to remain at h0, got: %s", cpBefore.LastCapturedSHA)
	}

	// 4. 再次執行增量擷取 (--since-last)，完整重試升級為 Complete，Checkpoint 推進至 HEAD
	res, err := service.Capture(ctx, repoDir, CaptureOptions{SinceLast: true})
	if err != nil {
		t.Fatalf("since-last capture for complete upgrade failed: %v", err)
	}
	if res.Candidate.IsTruncated {
		t.Fatal("expected candidate to be upgraded to complete")
	}

	// 5. 升級為 Complete 後，再次調用 ApproveCandidateTx 成功核准
	memID, err := store.ApproveCandidateTx(ctx, repoInfo.Workspace, partialCand.ID, storage.ApproveOptions{Content: "approved complete memory"})
	if err != nil {
		t.Fatalf("approve complete candidate failed: %v", err)
	}
	if memID == "" {
		t.Fatal("expected approved memory ID")
	}

	// 驗證 Checkpoint 成功推進至 HEAD
	cpAfter, err := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if err != nil {
		t.Fatal(err)
	}
	if cpAfter.LastCapturedSHA != repoInfo.CurrentHead {
		t.Fatalf("expected checkpoint advanced to %s, got: %s", repoInfo.CurrentHead, cpAfter.LastCapturedSHA)
	}
}

func TestCaptureService_PartialUpgrade_ConcurrentDraftEdit_PreservesManualContent(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	repoInfo, err := exec.ValidateAndResolveRepo(ctx, repoDir)
	if err != nil {
		t.Fatal(err)
	}

	resolved, _ := exec.ResolveRange(ctx, repoDir, "HEAD~2..HEAD~1")
	h0 := resolved.HeadSHA
	_ = store.SaveCheckpoint(ctx, &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: h0,
		UpdatedAt:       time.Now().UTC(),
	})

	// 1. 建立一個未被編輯的 pending partial candidate (is_user_edited=false)
	candID := "cand-partial-race-edit"
	partialCand := &storage.MemoryCandidate{
		ID:              candID,
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		BaseSHA:         h0,
		HeadSHA:         repoInfo.CurrentHead,
		CommitCount:     1,
		FactsSummary:    `{"truncated":true}`,
		DraftContent:    "initial unedited partial draft",
		Status:          "pending",
		PolicyVersion:   "gitmem-v1",
		CreatedAt:       time.Now().UTC(),
		IsTruncated:     true,
		TruncatedReason: "temporary error",
		IsUserEdited:    false,
	}
	if err := store.SaveCandidate(ctx, partialCand); err != nil {
		t.Fatal(err)
	}

	// 2. 注入 AI Generator 作為 barrier：在 A 讀取完候選（A 讀到的 IsUserEdited 為 false）並生成 AI 草稿期間，
	// 插入並行操作 B：使用者透過 UpdateCandidateDraft 寫入人工內容並設 is_user_edited=true！
	manualEditedDraft := "Critical manual decision by User B during capture"
	raceGen := &concurrentInterleavingGenerator{
		onGenerate: func() {
			if updateErr := store.UpdateCandidateDraft(ctx, repoInfo.Workspace, candID, manualEditedDraft); updateErr != nil {
				t.Errorf("concurrent update draft failed: %v", updateErr)
			}
		},
	}
	service.SetAIGenerator(raceGen)

	// 3. A 完成升級交易 (--since-last, AIDraft: true)
	// MUST FIX 2 核心驗證：即便 A 讀取時 is_user_edited 為 false，SQL transaction 透過 CASE WHEN
	// 仍能原子依當前資料列保護人工草稿，絕不被 A 生成的 draft 覆蓋！
	res, err := service.Capture(ctx, repoDir, CaptureOptions{SinceLast: true, AIDraft: true})
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}

	if res.Candidate.IsTruncated {
		t.Fatal("expected candidate to be complete")
	}

	// 斷言：回傳的 Candidate 草稿必須是使用者 B 的手動編修內容，絕不可是生成的草稿
	if res.Candidate.DraftContent != manualEditedDraft {
		t.Fatalf("DATA INTEGRITY BUG: Manual edited draft was overwritten by race! Got %q, expected %q", res.Candidate.DraftContent, manualEditedDraft)
	}
	if !res.Candidate.IsUserEdited {
		t.Fatal("expected candidate is_user_edited to be true")
	}

	// 斷言：資料庫中的 Candidate 草稿也必須是使用者 B 的手動編修內容
	dbCand, err := store.GetCandidate(ctx, repoInfo.Workspace, candID)
	if err != nil {
		t.Fatal(err)
	}
	if dbCand.DraftContent != manualEditedDraft {
		t.Fatalf("DATABASE INTEGRITY BUG: DB draft was overwritten! Got %q, expected %q", dbCand.DraftContent, manualEditedDraft)
	}
	if !dbCand.IsUserEdited {
		t.Fatal("expected database is_user_edited to be true")
	}
}

func TestCaptureService_PartialUpgrade_ConcurrentDiscard_RejectsWithoutForce(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	repoInfo, err := exec.ValidateAndResolveRepo(ctx, repoDir)
	if err != nil {
		t.Fatal(err)
	}

	resolved, _ := exec.ResolveRange(ctx, repoDir, "HEAD~2..HEAD~1")
	h0 := resolved.HeadSHA
	_ = store.SaveCheckpoint(ctx, &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: h0,
		UpdatedAt:       time.Now().UTC(),
	})

	// 1. 建立一個 pending partial candidate
	candID := "cand-partial-race"
	partialCand := &storage.MemoryCandidate{
		ID:              candID,
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		BaseSHA:         h0,
		HeadSHA:         repoInfo.CurrentHead,
		CommitCount:     1,
		FactsSummary:    `{"truncated":true}`,
		DraftContent:    "partial draft",
		Status:          "pending",
		PolicyVersion:   "gitmem-v1",
		CreatedAt:       time.Now().UTC(),
		IsTruncated:     true,
		TruncatedReason: "temporary error",
	}
	if err := store.SaveCandidate(ctx, partialCand); err != nil {
		t.Fatal(err)
	}

	// 2. 注入 AI Generator，在 A 讀到 pending partial 之後、升級 CAS 之前，插入並行操作 B 將候選 Discard！
	raceGen := &concurrentInterleavingGenerator{
		onGenerate: func() {
			if discardErr := store.DiscardCandidate(ctx, repoInfo.Workspace, candID); discardErr != nil {
				t.Errorf("concurrent discard failed: %v", discardErr)
			}
		},
	}
	service.SetAIGenerator(raceGen)

	// 3. A 發起完整重試擷取 (--since-last, AIDraft: true, Force: false)
	// MUST FIX 2 核心斷言：A 在 CAS 升級衝突後，重新讀取資料庫發現已為 discarded；
	// 因未帶 --force，必須堅決報錯拒絕，絕對不可將已 discarded 候選回傳為成功的 pending reuse！
	_, err = service.Capture(ctx, repoDir, CaptureOptions{SinceLast: true, AIDraft: true, Force: false})
	if err == nil {
		t.Fatal("SECURITY BUG: Concurrent discarded candidate was silently reused as pending without force!")
	}
	if !strings.Contains(err.Error(), "already discarded") && !strings.Contains(err.Error(), "concurrently discarded") {
		t.Fatalf("expected discarded rejection error, got: %v", err)
	}

	// Checkpoint 必須依然留在 H0，絕對未前進
	cp, _ := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if cp.LastCapturedSHA != h0 {
		t.Fatalf("SECURITY BUG: Checkpoint advanced despite discarded candidate conflict! Got %s", cp.LastCapturedSHA)
	}
}

func TestCaptureService_PartialUpgrade_PreservesUserEditedDraft(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	repoInfo, err := exec.ValidateAndResolveRepo(ctx, repoDir)
	if err != nil {
		t.Fatal(err)
	}

	resolved, _ := exec.ResolveRange(ctx, repoDir, "HEAD~2..HEAD~1")
	h0 := resolved.HeadSHA
	_ = store.SaveCheckpoint(ctx, &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: h0,
		UpdatedAt:       time.Now().UTC(),
	})

	// 1. 建立一個 pending partial candidate
	candID := "cand-partial-edited"
	factsJSON := `{"workspace":"` + repoInfo.Workspace + `","repo_id":"` + repoInfo.RepoID + `","ref_name":"` + repoInfo.CurrentRef + `","base_sha":"` + h0 + `","head_sha":"` + repoInfo.CurrentHead + `","commit_count":1,"truncated":true}`
	partialCand := &storage.MemoryCandidate{
		ID:              candID,
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		BaseSHA:         h0,
		HeadSHA:         repoInfo.CurrentHead,
		CommitCount:     1,
		FactsSummary:    factsJSON,
		DraftContent:    "initial default draft",
		Status:          "pending",
		PolicyVersion:   "gitmem-v1",
		CreatedAt:       time.Now().UTC(),
		IsTruncated:     true,
		TruncatedReason: "temporary",
	}
	if err := store.SaveCandidate(ctx, partialCand); err != nil {
		t.Fatal(err)
	}

	// 2. 使用者手動編輯了該草稿
	userEditedDraft := "User customized architecture decision: Must preserve this content at all costs!"
	if err := store.UpdateCandidateDraft(ctx, repoInfo.Workspace, candID, userEditedDraft); err != nil {
		t.Fatal(err)
	}

	// 3. 完整重試擷取
	// SHOULD FIX 2 核心斷言：Candidate 升級為 Complete，但使用者手動編修的 Draft 必須被完整保留，不被抹除！
	res, err := service.Capture(ctx, repoDir, CaptureOptions{SinceLast: true})
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}
	if res.Candidate.IsTruncated {
		t.Fatal("expected candidate to be complete")
	}
	if res.Candidate.DraftContent != userEditedDraft {
		t.Fatalf("DATA INTEGRITY BUG: User edited draft was overwritten! Got %q, expected %q", res.Candidate.DraftContent, userEditedDraft)
	}

	// 資料庫中也確實保留
	dbCand, _ := store.GetCandidate(ctx, repoInfo.Workspace, candID)
	if dbCand.DraftContent != userEditedDraft {
		t.Fatalf("database draft mismatch: got %q, expected %q", dbCand.DraftContent, userEditedDraft)
	}
}

func TestCaptureService_ConflictRecovery_AdvanceCheckpointErrorPropagated(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	repoInfo, err := exec.ValidateAndResolveRepo(ctx, repoDir)
	if err != nil {
		t.Fatal(err)
	}

	resolved, _ := exec.ResolveRange(ctx, repoDir, "HEAD~2..HEAD~1")
	h0 := resolved.HeadSHA
	_ = store.SaveCheckpoint(ctx, &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: h0,
		UpdatedAt:       time.Now().UTC(),
	})

	// 1. 建立一個 pending partial candidate
	candID := "cand-partial-conflict-cp-err"
	partialCand := &storage.MemoryCandidate{
		ID:              candID,
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		BaseSHA:         h0,
		HeadSHA:         repoInfo.CurrentHead,
		CommitCount:     1,
		FactsSummary:    `{"truncated":true}`,
		DraftContent:    "initial draft",
		Status:          "pending",
		PolicyVersion:   "gitmem-v1",
		CreatedAt:       time.Now().UTC(),
		IsTruncated:     true,
		TruncatedReason: "timeout",
	}
	if err := store.SaveCandidate(ctx, partialCand); err != nil {
		t.Fatal(err)
	}

	// 2. 注入 AI Generator，在 A 準備升級前，操作 B 並行將該候選升級為 Complete，
	// 同時取消 context，使得後續 Checkpoint 推進必定失敗！
	cancelCtx, cancel := context.WithCancel(ctx)
	raceGen := &concurrentInterleavingGenerator{
		onGenerate: func() {
			// B 並行將候選升級為 complete
			if upgradeErr := store.UpgradePartialCandidate(ctx, repoInfo.Workspace, candID, "complete draft", "{}", 2); upgradeErr != nil {
				t.Errorf("concurrent upgrade failed: %v", upgradeErr)
			}
			// 取消 context，導致後續 CAS conflict recovery 中的 AdvanceCheckpointTx 失敗
			cancel()
		},
	}
	service.SetAIGenerator(raceGen)

	// 3. A 執行 Capture (--since-last, AIDraft: true)
	// MUST FIX 3 核心驗證：CAS conflict 分支在呼叫 AdvanceCheckpointTx 遇到錯誤時，
	// 絕不允許丟棄錯誤回傳成功，必須將錯誤向外傳回！
	_, err = service.Capture(cancelCtx, repoDir, CaptureOptions{SinceLast: true, AIDraft: true})
	if err == nil {
		t.Fatal("SECURITY BUG: AdvanceCheckpointTx error in conflict recovery was swallowed and returned success!")
	}
	if !strings.Contains(err.Error(), "advance checkpoint") && !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected advance checkpoint error, got: %v", err)
	}
}

// TestCaptureService_ConcurrentCreateDiscard_SaveConflictRequiresForce
// MUST FIX 1 Barrier / E2E 測試：
// 模擬 cp=H0，A 執行 --since-last 並在 service 層查重得到 nil；
// B 在 A 進入 SaveCandidateAndCheckpointTx 之前，以手動 range 建立同 H0..H1 的 Complete candidate 並 discard；
// A 的 SaveCandidateAndCheckpointTx 觸發 UNIQUE conflict 並讀到 discarded candidate。
// 驗證：
// 1. 未帶 --force 時：Capture 必須失敗被拒絕，且 Checkpoint 絕對維持在 H0！
// 2. 帶有 --force 時：Capture 成功 reopen 該 discarded candidate，且 Checkpoint 安全推進至 H1！
func TestCaptureService_ConcurrentCreateDiscard_SaveConflictRequiresForce(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	repoInfo, err := exec.ValidateAndResolveRepo(ctx, repoDir)
	if err != nil {
		t.Fatal(err)
	}

	resolved, _ := exec.ResolveRange(ctx, repoDir, "HEAD~2..HEAD~1")
	h0 := resolved.HeadSHA
	_ = store.SaveCheckpoint(ctx, &storage.GitCheckpoint{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		LastCapturedSHA: h0,
		UpdatedAt:       time.Now().UTC(),
	})

	bCandID := "cand-b-concurrent-discard"

	// 1. 測試情境一：A 未帶 --force
	// 利用 AIGenerator 在查重（nil）之後、儲存之前觸發 B 建立 Complete 候選並 discard
	raceGenNoForce := &concurrentInterleavingGenerator{
		onGenerate: func() {
			// B 並行建立同 range complete candidate 並將其 discard
			bCand := &storage.MemoryCandidate{
				ID:              bCandID,
				Workspace:       repoInfo.Workspace,
				RepoID:          repoInfo.RepoID,
				RefName:         repoInfo.CurrentRef,
				BaseSHA:         h0,
				HeadSHA:         repoInfo.CurrentHead,
				CommitCount:     1,
				FactsSummary:    `{"truncated":false}`,
				DraftContent:    "b manual draft",
				Status:          "pending",
				PolicyVersion:   "gitmem-v1",
				CreatedAt:       time.Now().UTC(),
				IsTruncated:     false,
				TruncatedReason: "",
			}
			if err := store.SaveCandidate(ctx, bCand); err != nil {
				t.Errorf("concurrent B save candidate failed: %v", err)
			}
			if err := store.DiscardCandidate(ctx, repoInfo.Workspace, bCandID); err != nil {
				t.Errorf("concurrent B discard candidate failed: %v", err)
			}
		},
	}
	service.SetAIGenerator(raceGenNoForce)

	// A 執行 Capture (--since-last, Force: false)
	res, err := service.Capture(ctx, repoDir, CaptureOptions{SinceLast: true, AIDraft: true, Force: false})
	if err == nil {
		t.Fatal("SECURITY BUG: Capture succeeded without --force when concurrent discarded candidate caused save conflict!")
	}
	if !strings.Contains(err.Error(), "was already discarded; use --force to reopen") {
		t.Fatalf("expected already discarded error with --force guide, got: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result on error, got: %+v", res)
	}

	// 驗證 Checkpoint 絲毫未動，依然停留在 h0！
	cpAfter, err := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if err != nil {
		t.Fatalf("get checkpoint failed: %v", err)
	}
	if cpAfter.LastCapturedSHA != h0 {
		t.Fatalf("CRITICAL SECURITY BUG: Checkpoint was advanced to %s despite discarded candidate conflict! Expected %s", cpAfter.LastCapturedSHA, h0)
	}

	// 2. 測試情境二：A 帶有 --force
	// 重新執行 Capture (--since-last, Force: true)
	// 此時既有候選在查重或 conflict 時會被正確 reopen 並推進 Checkpoint
	service.SetAIGenerator(nil) // 重置為標準草稿
	resForce, err := service.Capture(ctx, repoDir, CaptureOptions{SinceLast: true, Force: true})
	if err != nil {
		t.Fatalf("expected Capture with --force to succeed, got: %v", err)
	}
	if resForce.Candidate.Status != "pending" {
		t.Fatalf("expected reopened candidate to be pending, got: %s", resForce.Candidate.Status)
	}

	// 驗證 Checkpoint 成功且安全地推進到 CurrentHead
	cpAfterForce, err := store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
	if err != nil {
		t.Fatalf("get checkpoint after force failed: %v", err)
	}
	if cpAfterForce.LastCapturedSHA != repoInfo.CurrentHead {
		t.Fatalf("expected checkpoint to advance to %s, got: %s", repoInfo.CurrentHead, cpAfterForce.LastCapturedSHA)
	}
}
