package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestApproveCandidate_SuccessAndIdempotency(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/test"

	factsJSON := `{"commits":[{"sha":"c1","subject":"s1"},{"sha":"c2","subject":"s2"}]}`
	cand := &MemoryCandidate{
		ID:            "cand-test-1",
		Workspace:     ws,
		RepoID:        "repo-1",
		RefName:       "refs/heads/main",
		BaseSHA:       "base1",
		HeadSHA:       "head1",
		CommitCount:   2,
		FactsSummary:  factsJSON,
		DraftContent:  "draft architecture decision",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}

	if err := store.SaveCandidate(ctx, cand); err != nil {
		t.Fatalf("save candidate failed: %v", err)
	}

	// 1. 首次核准
	memID, err := store.ApproveCandidateTx(ctx, ws, cand.ID, ApproveOptions{})
	if err != nil {
		t.Fatalf("first approve failed: %v", err)
	}
	if memID == "" {
		t.Fatal("expected non-empty memory ID")
	}

	// 驗證 memories 表
	mem, err := store.GetMemory(ctx, ws, memID)
	if err != nil {
		t.Fatalf("get formal memory failed: %v", err)
	}
	if mem.Source != "git-approved" {
		t.Errorf("expected source 'git-approved', got '%s'", mem.Source)
	}
	if mem.Content != cand.DraftContent {
		t.Errorf("expected content to match draft content")
	}

	// 驗證 provenance 表
	prov, err := store.GetProvenance(ctx, memID)
	if err != nil || prov == nil {
		t.Fatalf("get provenance failed: %v", err)
	}
	if prov.CandidateID != cand.ID || prov.IsUserEdited {
		t.Errorf("unexpected provenance: %+v", prov)
	}

	// 驗證 candidate 狀態
	reloadedCand, err := store.GetCandidate(ctx, ws, cand.ID)
	if err != nil {
		t.Fatalf("get candidate failed: %v", err)
	}
	if reloadedCand.Status != "approved" || reloadedCand.ApprovedMemoryID != memID {
		t.Errorf("candidate status not updated properly: %+v", reloadedCand)
	}

	// 2. 第二次核准（冪等性驗證）
	memID2, err := store.ApproveCandidateTx(ctx, ws, cand.ID, ApproveOptions{})
	if err != nil {
		t.Fatalf("idempotent approve failed: %v", err)
	}
	if memID2 != memID {
		t.Errorf("expected identical memory ID %s, got %s", memID, memID2)
	}
}

func TestApproveCandidate_WithCustomContent(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/test"

	factsJSON := `{"commits":[{"sha":"c1","subject":"s1"}]}`
	cand := &MemoryCandidate{
		ID:            "cand-test-2",
		Workspace:     ws,
		RepoID:        "repo-1",
		RefName:       "refs/heads/main",
		BaseSHA:       "base1",
		HeadSHA:       "head1",
		CommitCount:   1,
		FactsSummary:  factsJSON,
		DraftContent:  "draft",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}
	_ = store.SaveCandidate(ctx, cand)

	customContent := "User edited decision: adopted isolated candidate storage"
	memID, err := store.ApproveCandidateTx(ctx, ws, cand.ID, ApproveOptions{Content: customContent})
	if err != nil {
		t.Fatalf("approve with custom content failed: %v", err)
	}

	mem, err := store.GetMemory(ctx, ws, memID)
	if err != nil {
		t.Fatal(err)
	}
	if mem.Content != customContent {
		t.Errorf("expected custom content, got %s", mem.Content)
	}

	prov, err := store.GetProvenance(ctx, memID)
	if err != nil || prov == nil {
		t.Fatal(err)
	}
	if !prov.IsUserEdited {
		t.Errorf("expected IsUserEdited=true when custom content was passed")
	}
}

func TestApproveCandidate_AntiRevival(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/test"

	cand := &MemoryCandidate{
		ID:            "cand-test-3",
		Workspace:     ws,
		RepoID:        "repo-1",
		RefName:       "refs/heads/main",
		BaseSHA:       "base1",
		HeadSHA:       "head1",
		CommitCount:   1,
		FactsSummary:  `{"commits":[]}`,
		DraftContent:  "draft",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}
	_ = store.SaveCandidate(ctx, cand)

	memID, err := store.ApproveCandidateTx(ctx, ws, cand.ID, ApproveOptions{})
	if err != nil {
		t.Fatalf("first approve failed: %v", err)
	}

	// 使用者刪除了該正式記憶
	if err := store.DeleteMemory(ctx, ws, memID); err != nil {
		t.Fatalf("delete memory failed: %v", err)
	}

	// 防復活機制：再次嘗試核准此 candidate 時，必須拒絕重插入
	_, err = store.ApproveCandidateTx(ctx, ws, cand.ID, ApproveOptions{})
	if err == nil {
		t.Fatal("expected error preventing deleted memory from revival, got nil")
	}
	if !strings.Contains(err.Error(), "deleted") && !strings.Contains(err.Error(), "revival prevented") {
		t.Errorf("expected deleted or revival prevented error message, got: %v", err)
	}
}

func TestSaveCandidateAndCheckpointTx_RollbackOnFailure(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/test-tx"

	// 1. 在 git_checkpoints 上設定失敗注入 Trigger，模擬 Checkpoint 寫入崩潰/失敗
	injectSQL := `
	CREATE TRIGGER fail_checkpoint_trigger
	BEFORE INSERT ON git_checkpoints
	BEGIN
		SELECT RAISE(ABORT, 'injected checkpoint failure for rollback test');
	END;
	`
	if _, err := store.db.Exec(injectSQL); err != nil {
		t.Fatalf("failed to create injection trigger: %v", err)
	}

	cand := &MemoryCandidate{
		ID:            "cand-atomic-fail-1",
		Workspace:     ws,
		RepoID:        "repo-tx",
		RefName:       "refs/heads/main",
		BaseSHA:       "base1",
		HeadSHA:       "head1",
		CommitCount:   1,
		FactsSummary:  `{"commits":[]}`,
		DraftContent:  "atomic candidate test",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}

	cp := &GitCheckpoint{
		Workspace:       ws,
		RepoID:          "repo-tx",
		RefName:         "refs/heads/main",
		LastCapturedSHA: "head1",
		UpdatedAt:       time.Now().UTC(),
	}

	// 2. 執行原子交易寫入：預期因 Checkpoint 寫入失敗而整體回滾
	_, err := store.SaveCandidateAndCheckpointTx(ctx, cand, cp, "", nil)
	if err == nil {
		t.Fatal("expected error due to injected checkpoint failure, got nil")
	}
	if !strings.Contains(err.Error(), "injected checkpoint failure") {
		t.Errorf("expected injected checkpoint failure message, got: %v", err)
	}

	// 3. 驗證原子性：Candidate 絕不可殘留於資料庫中（已被交易完整 ROLLBACK）
	savedCand, err := store.GetCandidate(ctx, ws, cand.ID)
	if err == nil || savedCand != nil {
		t.Fatalf("ATOMICITY FAIL: Candidate %s persisted despite Checkpoint failure (got: %+v)", cand.ID, savedCand)
	}
	if !strings.Contains(err.Error(), "candidate not found") {
		t.Errorf("expected candidate not found error, got: %v", err)
	}

	// Checkpoint 亦不得存在
	savedCP, err := store.GetCheckpoint(ctx, ws, cp.RepoID, cp.RefName)
	if err != nil {
		t.Fatalf("unexpected error querying checkpoint: %v", err)
	}
	if savedCP != nil {
		t.Fatalf("ATOMICITY FAIL: Checkpoint persisted despite failure: %+v", savedCP)
	}

	// 4. 移除 Trigger，驗證正常狀態下兩者原子寫入成功
	if _, err := store.db.Exec("DROP TRIGGER fail_checkpoint_trigger;"); err != nil {
		t.Fatalf("drop trigger failed: %v", err)
	}

	persisted, err := store.SaveCandidateAndCheckpointTx(ctx, cand, cp, "", nil)
	if err != nil {
		t.Fatalf("expected successful atomic save after dropping trigger, got: %v", err)
	}
	if persisted == nil || persisted.ID != cand.ID {
		t.Errorf("expected persisted candidate, got: %+v", persisted)
	}

	// 驗證兩者皆存在
	cCheck, err := store.GetCandidate(ctx, ws, cand.ID)
	if err != nil || cCheck == nil {
		t.Fatalf("expected candidate exists in DB: %v", err)
	}
	cpCheck, err := store.GetCheckpoint(ctx, ws, cp.RepoID, cp.RefName)
	if err != nil || cpCheck == nil || cpCheck.LastCapturedSHA != "head1" {
		t.Fatalf("expected checkpoint exists in DB: %v, cp: %+v", err, cpCheck)
	}
}

func TestSaveCandidateAndCheckpointTx_MonotonicConcurrentAdvance(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/monotonic-test"

	// 模擬歷史關係：H0 -> H1 -> H2
	h0 := "0000000000000000000000000000000000000000"
	h1 := "1111111111111111111111111111111111111111"
	h2 := "2222222222222222222222222222222222222222"

	isAncestorMock := func(ctx context.Context, anc, desc string) (bool, error) {
		if anc == h0 && (desc == h1 || desc == h2) {
			return true, nil
		}
		if anc == h1 && desc == h2 {
			return true, nil
		}
		return false, nil
	}

	// 1. 初始 Checkpoint 為 H0
	initialCP := &GitCheckpoint{
		Workspace:       ws,
		RepoID:          "repo-mono",
		RefName:         "refs/heads/main",
		LastCapturedSHA: h0,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.SaveCheckpoint(ctx, initialCP); err != nil {
		t.Fatal(err)
	}

	// 2. 模擬並行交易 Capture B（H0 -> H2）先完成落盤
	candB := &MemoryCandidate{
		ID:            "cand-b",
		Workspace:     ws,
		RepoID:        "repo-mono",
		RefName:       "refs/heads/main",
		BaseSHA:       h0,
		HeadSHA:       h2,
		CommitCount:   2,
		FactsSummary:  "{}",
		DraftContent:  "draft B",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
	}
	cpB := &GitCheckpoint{
		Workspace:       ws,
		RepoID:          "repo-mono",
		RefName:         "refs/heads/main",
		LastCapturedSHA: h2,
		UpdatedAt:       time.Now().UTC(),
	}
	_, err := store.SaveCandidateAndCheckpointTx(ctx, candB, cpB, h0, isAncestorMock)
	if err != nil {
		t.Fatalf("save B failed: %v", err)
	}

	// 驗證庫中當前 Checkpoint 已前進到 H2
	cpCurrent, _ := store.GetCheckpoint(ctx, ws, "repo-mono", "refs/heads/main")
	if cpCurrent.LastCapturedSHA != h2 {
		t.Fatalf("expected checkpoint at H2, got %s", cpCurrent.LastCapturedSHA)
	}

	// 3. 模擬延遲完成的並行交易 Capture A（H0 -> H1）後完成落盤
	// A 進入交易前以為 Checkpoint 是 H0，目標是推進到 H1
	candA := &MemoryCandidate{
		ID:            "cand-a",
		Workspace:     ws,
		RepoID:        "repo-mono",
		RefName:       "refs/heads/main",
		BaseSHA:       h0,
		HeadSHA:       h1,
		CommitCount:   1,
		FactsSummary:  "{}",
		DraftContent:  "draft A",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
	}
	cpA := &GitCheckpoint{
		Workspace:       ws,
		RepoID:          "repo-mono",
		RefName:         "refs/heads/main",
		LastCapturedSHA: h1,
		UpdatedAt:       time.Now().UTC(),
	}

	persistedA, err := store.SaveCandidateAndCheckpointTx(ctx, candA, cpA, h0, isAncestorMock)
	if err != nil {
		t.Fatalf("expected A to succeed saving candidate despite stale checkpoint, got: %v", err)
	}
	if persistedA.ID != candA.ID {
		t.Errorf("expected candidate A to be persisted, got %+v", persistedA)
	}

	// 4. 關鍵斷言：Checkpoint 必須保持在 H2，絕對不能被 A 逆序覆寫倒退回 H1！
	cpFinal, _ := store.GetCheckpoint(ctx, ws, "repo-mono", "refs/heads/main")
	if cpFinal.LastCapturedSHA != h2 {
		t.Fatalf("MONOTONIC INTEGRITY BUG: Checkpoint rewound from %s to %s due to concurrent stale advance!", h2, cpFinal.LastCapturedSHA)
	}
}

func TestClearProcessedCandidates_AntiRevivalAndDedupProtected(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/clear-safety"

	cand := &MemoryCandidate{
		ID:            "cand-clear-1",
		Workspace:     ws,
		RepoID:        "repo-1",
		RefName:       "refs/heads/main",
		BaseSHA:       "base1",
		HeadSHA:       "head1",
		CommitCount:   2,
		FactsSummary:  `{"commits":[{"sha":"c1","subject":"s1"}]}`,
		DraftContent:  "massive draft content occupying lots of disk space",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}
	if err := store.SaveCandidate(ctx, cand); err != nil {
		t.Fatal(err)
	}

	// 1. 核准產生正式記憶
	memID, err := store.ApproveCandidateTx(ctx, ws, cand.ID, ApproveOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// 2. 執行 ClearProcessedCandidates 清理
	clearedCount, err := store.ClearProcessedCandidates(ctx, ws)
	if err != nil {
		t.Fatalf("ClearProcessedCandidates failed: %v", err)
	}
	if clearedCount != 1 {
		t.Fatalf("expected 1 row cleared, got %d", clearedCount)
	}

	// 3. 驗證瘦身效果：大型 Draft 與 FactsSummary 已被清空
	cAfter, err := store.GetCandidate(ctx, ws, cand.ID)
	if err != nil {
		t.Fatalf("candidate should still exist after clear: %v", err)
	}
	if cAfter.DraftContent != "" || cAfter.FactsSummary != "{}" {
		t.Errorf("expected payload cleared, got draft=%q, facts=%q", cAfter.DraftContent, cAfter.FactsSummary)
	}
	if cAfter.Status != "approved" || cAfter.ApprovedMemoryID != memID {
		t.Errorf("expected approved metadata preserved, got status=%s, memID=%s", cAfter.Status, cAfter.ApprovedMemoryID)
	}

	// 4. 驗證去重防重複生成：相同 Range 查詢依然能查到該 candidate，絕不重複生成 pending！
	existing, err := store.FindCandidateByRange(ctx, ws, cand.RepoID, cand.RefName, cand.BaseSHA, cand.HeadSHA, "gitmem-v1")
	if err != nil || existing == nil {
		t.Fatalf("expected candidate range to remain indexed, got: %v", existing)
	}

	// 5. 驗證防復活（Anti-Revival）：刪除正式記憶後，再次嘗試核准該 candidate 必須被嚴格阻絕！
	if err := store.DeleteMemory(ctx, ws, memID); err != nil {
		t.Fatal(err)
	}
	_, err = store.ApproveCandidateTx(ctx, ws, cand.ID, ApproveOptions{})
	if err == nil {
		t.Fatal("SECURITY BUG: Anti-revival bypassed after clear-candidates!")
	}
	if !strings.Contains(err.Error(), "deleted") && !strings.Contains(err.Error(), "revival prevented") {
		t.Errorf("expected revival prevented error, got: %v", err)
	}
}

func TestSaveCandidateAndCheckpointTx_TargetNotDescendant_NeverRewindsEvenIfExpectedMatches(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/ancestry-override-test"

	// 歷史關係：H0 -> H1 -> H2
	h0 := "0000000000000000000000000000000000000000"
	h1 := "1111111111111111111111111111111111111111"
	h2 := "2222222222222222222222222222222222222222"

	isAncestorMock := func(ctx context.Context, anc, desc string) (bool, error) {
		if anc == h0 && (desc == h1 || desc == h2) {
			return true, nil
		}
		if anc == h1 && desc == h2 {
			return true, nil
		}
		return false, nil
	}

	// 1. Checkpoint 目前已經在 H2
	cpInitial := &GitCheckpoint{
		Workspace:       ws,
		RepoID:          "repo-anc",
		RefName:         "refs/heads/main",
		LastCapturedSHA: h2,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.SaveCheckpoint(ctx, cpInitial); err != nil {
		t.Fatal(err)
	}

	// 2. 模擬反例：調用方傳入 expectedOldSHA 剛好等於當前庫中的 H2（例如事後重新讀取 checkpoint 取得 H2）
	// 但其 Capture 目標 checkpoint 卻是舊的 H1！
	// 舊實作中 currentSHA == expectedOldSHA 會直接把 H2 覆寫倒退回 H1！
	candStale := &MemoryCandidate{
		ID:            "cand-stale-h1",
		Workspace:     ws,
		RepoID:        "repo-anc",
		RefName:       "refs/heads/main",
		BaseSHA:       h0,
		HeadSHA:       h1,
		CommitCount:   1,
		FactsSummary:  "{}",
		DraftContent:  "stale draft",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
	}
	cpStale := &GitCheckpoint{
		Workspace:       ws,
		RepoID:          "repo-anc",
		RefName:         "refs/heads/main",
		LastCapturedSHA: h1, // 舊目標！
		UpdatedAt:       time.Now().UTC(),
	}

	// 傳入 expectedOldSHA = h2
	_, err := store.SaveCandidateAndCheckpointTx(ctx, candStale, cpStale, h2, isAncestorMock)
	if err != nil {
		t.Fatalf("expected candidate saving to succeed, got: %v", err)
	}

	// 3. 關鍵驗證：Checkpoint 必須依然維持在 H2，絕對不能被覆寫成 H1！
	cpCheck, err := store.GetCheckpoint(ctx, ws, "repo-anc", "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if cpCheck.LastCapturedSHA != h2 {
		t.Fatalf("SECURITY VULNERABILITY: Checkpoint rewound from H2 to %s because expectedOldSHA matched H2!", cpCheck.LastCapturedSHA)
	}
}

func TestApproveCandidateTx_EmptyContentRejected(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/empty-approve"

	// 建立一個 draft_content 為空的 candidate（例如被 clear-candidates 清理過）
	cand := &MemoryCandidate{
		ID:            "cand-empty",
		Workspace:     ws,
		RepoID:        "repo-empty",
		RefName:       "refs/heads/main",
		BaseSHA:       "base",
		HeadSHA:       "head",
		CommitCount:   1,
		FactsSummary:  "{}",
		DraftContent:  "", // 空白草稿！
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}
	if err := store.SaveCandidate(ctx, cand); err != nil {
		t.Fatal(err)
	}

	// 嘗試不帶 --content 直接 approve
	_, err := store.ApproveCandidateTx(ctx, ws, cand.ID, ApproveOptions{Content: ""})
	if err == nil {
		t.Fatal("expected error when approving candidate with empty draft and no content, got nil")
	}
	if !strings.Contains(err.Error(), "empty content") {
		t.Errorf("expected empty content error, got: %v", err)
	}

	// 確保 memories 表中沒有任何記錄被寫入
	mems, err := store.ListMemories(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(mems) != 0 {
		t.Fatalf("expected 0 memories, found %d", len(mems))
	}
}

func TestReopenDiscardedCandidate_ReplacesPayloadAndResetsPending(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/reopen-test"

	cand := &MemoryCandidate{
		ID:            "cand-reopen",
		Workspace:     ws,
		RepoID:        "repo-reopen",
		RefName:       "refs/heads/main",
		BaseSHA:       "base",
		HeadSHA:       "head",
		CommitCount:   1,
		FactsSummary:  "{}",
		DraftContent:  "",
		Status:        "discarded",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}
	if err := store.SaveCandidate(ctx, cand); err != nil {
		t.Fatal(err)
	}

	// 重新開啟候選並注入新草稿與事實
	newDraft := "regenerated draft from re-capture"
	newFacts := `{"commits":[{"sha":"c1","subject":"refactored"}]}`
	if err := store.ReopenDiscardedCandidate(ctx, ws, cand.ID, newDraft, newFacts, 2); err != nil {
		t.Fatalf("ReopenDiscardedCandidate failed: %v", err)
	}

	updated, err := store.GetCandidate(ctx, ws, cand.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "pending" {
		t.Errorf("expected status 'pending', got %s", updated.Status)
	}
	if updated.DraftContent != newDraft {
		t.Errorf("expected draft %q, got %q", newDraft, updated.DraftContent)
	}
	if updated.FactsSummary != newFacts {
		t.Errorf("expected facts %q, got %q", newFacts, updated.FactsSummary)
	}
	if updated.CommitCount != 2 {
		t.Errorf("expected commit count 2, got %d", updated.CommitCount)
	}

	// 隨後能順利核准
	memID, err := store.ApproveCandidateTx(ctx, ws, cand.ID, ApproveOptions{})
	if err != nil {
		t.Fatalf("failed to approve reopened candidate: %v", err)
	}
	if memID == "" {
		t.Fatal("expected valid memory ID")
	}
}

func TestAdvanceCheckpointTx_MonotonicAndCAS(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/advance-cp-tx"

	h0 := "0000000000000000000000000000000000000000"
	h1 := "1111111111111111111111111111111111111111"
	h2 := "2222222222222222222222222222222222222222"

	isAncestorMock := func(ctx context.Context, anc, desc string) (bool, error) {
		if anc == h0 && (desc == h1 || desc == h2) {
			return true, nil
		}
		if anc == h1 && desc == h2 {
			return true, nil
		}
		return false, nil
	}

	// 1. 初始前進（無既有 checkpoint）
	cp0 := &GitCheckpoint{
		Workspace:       ws,
		RepoID:          "repo-adv",
		RefName:         "refs/heads/main",
		LastCapturedSHA: h0,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.AdvanceCheckpointTx(ctx, cp0, "", isAncestorMock); err != nil {
		t.Fatalf("initial advance failed: %v", err)
	}

	// 2. 正向單調推進至 H2
	cp2 := &GitCheckpoint{
		Workspace:       ws,
		RepoID:          "repo-adv",
		RefName:         "refs/heads/main",
		LastCapturedSHA: h2,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.AdvanceCheckpointTx(ctx, cp2, h0, isAncestorMock); err != nil {
		t.Fatalf("advance to h2 failed: %v", err)
	}

	// 3. 嘗試以過時的 H1 推進（H1 比庫中的 H2 舊）
	cp1 := &GitCheckpoint{
		Workspace:       ws,
		RepoID:          "repo-adv",
		RefName:         "refs/heads/main",
		LastCapturedSHA: h1,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.AdvanceCheckpointTx(ctx, cp1, h0, isAncestorMock); err != nil {
		t.Fatalf("expected advance to ignore stale target without failing, got: %v", err)
	}

	// 驗證仍停留在 H2
	res, _ := store.GetCheckpoint(ctx, ws, "repo-adv", "refs/heads/main")
	if res.LastCapturedSHA != h2 {
		t.Fatalf("checkpoint was rewound to %s, expected %s", res.LastCapturedSHA, h2)
	}
}

func TestDiscardCandidate_AtomicStatusCAS(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/discard-cas"

	cand := &MemoryCandidate{
		ID:            "cand-discard-cas",
		Workspace:     ws,
		RepoID:        "repo-1",
		RefName:       "refs/heads/main",
		BaseSHA:       "base",
		HeadSHA:       "head",
		CommitCount:   1,
		FactsSummary:  "{}",
		DraftContent:  "draft content",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}
	if err := store.SaveCandidate(ctx, cand); err != nil {
		t.Fatal(err)
	}

	// 1. 正常情況：pending -> discarded 成功
	if err := store.DiscardCandidate(ctx, ws, cand.ID); err != nil {
		t.Fatalf("discard pending candidate failed: %v", err)
	}

	c, _ := store.GetCandidate(ctx, ws, cand.ID)
	if c.Status != "discarded" {
		t.Fatalf("expected status discarded, got %s", c.Status)
	}

	// 2. 冪等性：再次 discard 已 discarded 的候選應保持冪等成功
	if err := store.DiscardCandidate(ctx, ws, cand.ID); err != nil {
		t.Fatalf("expected idempotent discard to succeed, got: %v", err)
	}

	// 3. 安全防禦：如果候選已被核准（approved），絕對禁止被 discard！
	candApproved := &MemoryCandidate{
		ID:            "cand-approved-nodiscard",
		Workspace:     ws,
		RepoID:        "repo-1",
		RefName:       "refs/heads/main",
		BaseSHA:       "b2",
		HeadSHA:       "h2",
		CommitCount:   1,
		FactsSummary:  "{}",
		DraftContent:  "approved content",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}
	_ = store.SaveCandidate(ctx, candApproved)
	memID, err := store.ApproveCandidateTx(ctx, ws, candApproved.ID, ApproveOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// 嘗試 discard 已 approved 的候選：必須被嚴格阻絕！
	err = store.DiscardCandidate(ctx, ws, candApproved.ID)
	if err == nil {
		t.Fatal("SECURITY BUG: Approved candidate was discarded!")
	}
	if !strings.Contains(err.Error(), "already approved") {
		t.Errorf("expected already approved error, got: %v", err)
	}

	// 驗證 approved 候選的元資料與記憶 ID 完好無損
	cA, _ := store.GetCandidate(ctx, ws, candApproved.ID)
	if cA.Status != "approved" || cA.ApprovedMemoryID != memID {
		t.Fatalf("approved candidate metadata was corrupted: status=%s, memID=%s", cA.Status, cA.ApprovedMemoryID)
	}
}

func TestReopenDiscardedCandidate_AtomicCAS_RejectsApproved(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/reopen-cas"

	// 建立一個 approved 的候選
	cand := &MemoryCandidate{
		ID:            "cand-approved-noreopen",
		Workspace:     ws,
		RepoID:        "repo-1",
		RefName:       "refs/heads/main",
		BaseSHA:       "base",
		HeadSHA:       "head",
		CommitCount:   1,
		FactsSummary:  "{}",
		DraftContent:  "approved draft",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}
	_ = store.SaveCandidate(ctx, cand)
	memID, err := store.ApproveCandidateTx(ctx, ws, cand.ID, ApproveOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// 模擬並行競爭：另一個請求試圖 ReopenDiscardedCandidate 把此 approved 候選重開為 pending
	err = store.ReopenDiscardedCandidate(ctx, ws, cand.ID, "malicious draft", "{}", 1)
	if err == nil {
		t.Fatal("SECURITY BUG: Approved candidate was reopened back to pending!")
	}
	if !errors.Is(err, ErrCandidateStatusConflict) {
		t.Errorf("expected ErrCandidateStatusConflict, got: %v", err)
	}

	// 驗證 approved 狀態與 approved_memory_id 絕無被抹除
	c, _ := store.GetCandidate(ctx, ws, cand.ID)
	if c.Status != "approved" || c.ApprovedMemoryID != memID {
		t.Fatalf("approved candidate was corrupted by reopen: %+v", c)
	}
}

func TestApproveCandidateTx_AtomicStatusCAS_RejectsNonPending(t *testing.T) {
	store := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/workspace/approve-cas"

	// 建立一個已 discarded 的候選
	cand := &MemoryCandidate{
		ID:            "cand-discarded-noapprove",
		Workspace:     ws,
		RepoID:        "repo-1",
		RefName:       "refs/heads/main",
		BaseSHA:       "base",
		HeadSHA:       "head",
		CommitCount:   1,
		FactsSummary:  "{}",
		DraftContent:  "some draft",
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}
	_ = store.SaveCandidate(ctx, cand)
	_ = store.DiscardCandidate(ctx, ws, cand.ID)

	// 嘗試直接核准 discarded 候選：交易內的原子 CAS 必須拒絕
	_, err := store.ApproveCandidateTx(ctx, ws, cand.ID, ApproveOptions{})
	if err == nil {
		t.Fatal("SECURITY BUG: Discarded candidate was approved!")
	}
	if !strings.Contains(err.Error(), "discarded") && !strings.Contains(err.Error(), "no longer pending") {
		t.Errorf("expected rejection error, got: %v", err)
	}
}



