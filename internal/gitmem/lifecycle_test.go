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
