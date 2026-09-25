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
