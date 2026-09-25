package storage

import (
	"context"
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
	_, err := store.SaveCandidateAndCheckpointTx(ctx, cand, cp)
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

	persisted, err := store.SaveCandidateAndCheckpointTx(ctx, cand, cp)
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

