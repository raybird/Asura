package gitmem

import (
	"context"
	"strings"
	"testing"

	"asura/internal/storage"
)

func TestGitMemory_FullLifecycleAndAntiRevival(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	// 1. 執行擷取：產生候選草稿
	res, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD"})
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}
	candID := res.Candidate.ID

	// 驗證初始草稿處於隔離狀態：正式記憶讀不到
	mems, err := store.ListMemories(ctx, repoDir)
	if err != nil || len(mems) != 0 {
		t.Fatalf("expected 0 formal memories before approval, got %d (err: %v)", len(mems), err)
	}

	// 2. 審核並核准草稿
	customContent := "Architecture Decision: Git-aware memory capture with strict approval gate"
	memID, err := store.ApproveCandidateTx(ctx, repoDir, candID, storage.ApproveOptions{
		Content: customContent,
	})
	if err != nil {
		t.Fatalf("approval failed: %v", err)
	}

	// 3. 驗證正式記憶寫入成功且可被 Phase 3F RecallCandidates 召回
	recalled, _, err := store.RecallCandidates(ctx, repoDir)
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if len(recalled) != 1 || recalled[0].ID != memID {
		t.Fatalf("expected 1 recalled memory with ID %s, got %v", memID, recalled)
	}
	if recalled[0].Source != "git-approved" || recalled[0].Content != customContent {
		t.Errorf("unexpected recalled memory content: %+v", recalled[0])
	}

	// 4. 驗證 Provenance 關聯存在
	prov, err := store.GetProvenance(ctx, memID)
	if err != nil || prov == nil {
		t.Fatalf("expected provenance for memory %s, got err: %v", memID, err)
	}
	if prov.CandidateID != candID || !prov.IsUserEdited {
		t.Errorf("unexpected provenance properties: %+v", prov)
	}

	// 5. 驗證重複核准冪等性
	memIDAgain, err := store.ApproveCandidateTx(ctx, repoDir, candID, storage.ApproveOptions{})
	if err != nil || memIDAgain != memID {
		t.Fatalf("idempotent approval failed, expected %s, got %s (err: %v)", memID, memIDAgain, err)
	}

	// 6. 使用者刪除該正式記憶
	if err := store.DeleteMemory(ctx, repoDir, memID); err != nil {
		t.Fatalf("delete formal memory failed: %v", err)
	}

	// 7. 驗證 Recall 已讀不到該記憶
	recalledAfterDelete, _, err := store.RecallCandidates(ctx, repoDir)
	if err != nil || len(recalledAfterDelete) != 0 {
		t.Fatalf("expected 0 recalled memories after deletion, got %d", len(recalledAfterDelete))
	}

	// 8. 驗證 ON DELETE CASCADE：provenance 自動被清理
	provAfterDelete, err := store.GetProvenance(ctx, memID)
	if err != nil || provAfterDelete != nil {
		t.Errorf("expected provenance cascade deleted, got %+v (err: %v)", provAfterDelete, err)
	}

	// 9. 驗證不可變稽核痕跡：Candidate 仍標註 approved 與 approved_memory_id
	keptCand, err := store.GetCandidate(ctx, repoDir, candID)
	if err != nil || keptCand == nil {
		t.Fatalf("expected candidate audit record preserved, got err: %v", err)
	}
	if keptCand.Status != "approved" || keptCand.ApprovedMemoryID != memID {
		t.Errorf("candidate audit trail broken: %+v", keptCand)
	}

	// 10. 防復活機制（Anti-Revival）：再次嘗試核准同一 Candidate 必須被拒絕，禁止悄悄復活
	_, err = store.ApproveCandidateTx(ctx, repoDir, candID, storage.ApproveOptions{})
	if err == nil {
		t.Fatal("SECURITY BUG: Deleted memory was revived by re-approving candidate!")
	}
	if !strings.Contains(err.Error(), "revival prevented") && !strings.Contains(err.Error(), "deleted") {
		t.Errorf("expected anti-revival error message, got: %v", err)
	}
}

func TestGitMemory_ReopenDiscardedCandidateAfterClear_ProducesNonEmptyMemory(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	store := newTestStore(t)
	service := NewCaptureService(exec, store)
	ctx := context.Background()

	// 1. 初次擷取：產生候選草稿
	res, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD"})
	if err != nil {
		t.Fatalf("initial capture failed: %v", err)
	}
	candID := res.Candidate.ID

	// 2. 捨棄該候選
	if err := store.DiscardCandidate(ctx, repoDir, candID); err != nil {
		t.Fatalf("discard candidate failed: %v", err)
	}

	// 3. 執行清理瘦身：清除 discarded 候選之 draft 與 facts payload
	cleared, err := store.ClearProcessedCandidates(ctx, repoDir)
	if err != nil || cleared != 1 {
		t.Fatalf("ClearProcessedCandidates failed (cleared=%d, err=%v)", cleared, err)
	}

	candAfterClear, err := store.GetCandidate(ctx, repoDir, candID)
	if err != nil {
		t.Fatal(err)
	}
	if candAfterClear.DraftContent != "" {
		t.Fatalf("expected cleared draft to be empty, got %q", candAfterClear.DraftContent)
	}

	// 4. 驗證 discarded 狀態核准防禦：未重開前禁止直接核准
	_, err = store.ApproveCandidateTx(ctx, repoDir, candID, storage.ApproveOptions{})
	if err == nil {
		t.Fatal("SECURITY BUG: Discarded candidate was approved without reopening!")
	}
	if !strings.Contains(err.Error(), "discarded") {
		t.Errorf("expected discarded error, got: %v", err)
	}

	// 5. 再次執行 capture 並傳入 --force 重開候選
	reopenRes, err := service.Capture(ctx, repoDir, CaptureOptions{RangeExpr: "HEAD~1..HEAD", Force: true})
	if err != nil {
		t.Fatalf("reopen capture with force failed: %v", err)
	}

	// 6. 關鍵驗證：候選重開後必須以本次 capture 之 facts 重新生成有界草稿，絕不能留空！
	if reopenRes.Candidate.Status != "pending" {
		t.Errorf("expected status 'pending', got %s", reopenRes.Candidate.Status)
	}
	if strings.TrimSpace(reopenRes.Candidate.DraftContent) == "" {
		t.Fatal("BUG: Reopened discarded candidate still has empty draft content!")
	}
	if !strings.Contains(reopenRes.Candidate.DraftContent, "專案架構決策與變更摘要") {
		t.Errorf("expected standard draft template, got: %s", reopenRes.Candidate.DraftContent)
	}

	// 7. 隨後核准候選（不帶 --content），確認正式記憶完整保存重新生成的草稿，絕非空白記憶
	memID, err := store.ApproveCandidateTx(ctx, repoDir, candID, storage.ApproveOptions{})
	if err != nil {
		t.Fatalf("approve reopened candidate failed: %v", err)
	}

	mems, err := store.ListMemories(ctx, repoDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(mems) != 1 || mems[0].ID != memID {
		t.Fatalf("expected 1 formal memory with ID %s, got %v", memID, mems)
	}
	if strings.TrimSpace(mems[0].Content) == "" {
		t.Fatal("SECURITY BUG: Formal memory created with empty content!")
	}
	if mems[0].Content != reopenRes.Candidate.DraftContent {
		t.Errorf("formal memory content mismatch: got %q, expected %q", mems[0].Content, reopenRes.Candidate.DraftContent)
	}
}

