package gitmem

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"asura/internal/storage"
)

// CaptureOptions 包含擷取作業的參數選項
type CaptureOptions struct {
	RangeExpr string
	SinceLast bool
	Force     bool
}

// CaptureResult 代表擷取作業的執行結果與候選中繼資料
type CaptureResult struct {
	Candidate *storage.MemoryCandidate
	Facts     *CaptureFacts
	IsReused  bool
	Message   string
}

// CaptureService 協調整合 Git 擷取、候選去重與 Checkpoint 增量游標
type CaptureService struct {
	exec     *GitExecutor
	capturer *Capturer
	store    *storage.SQLiteStore
}

// NewCaptureService 建立新的擷取協調服務實例
func NewCaptureService(exec *GitExecutor, store *storage.SQLiteStore) *CaptureService {
	return &CaptureService{
		exec:     exec,
		capturer: NewCapturer(exec),
		store:    store,
	}
}

func generateCandidateID() string {
	bytes := make([]byte, 4)
	_, _ = rand.Read(bytes)
	return fmt.Sprintf("cand-%d-%s", time.Now().Unix(), hex.EncodeToString(bytes))
}

// Capture 執行變更擷取、嚴格去重判斷與 Checkpoint 前進或阻斷 (H-TBD-1, H-TBD-3, H-TBD-6)
func (s *CaptureService) Capture(ctx context.Context, workspace string, opts CaptureOptions) (*CaptureResult, error) {
	// 1. 驗證並解析 Repo 邊界
	repoInfo, err := s.exec.ValidateAndResolveRepo(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("validate repo: %w", err)
	}

	// 2. 決定 Range 表達式
	var rangeExpr string
	if opts.SinceLast {
		cp, err := s.store.GetCheckpoint(ctx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
		if err != nil {
			return nil, fmt.Errorf("get checkpoint: %w", err)
		}
		if cp == nil || cp.LastCapturedSHA == "" {
			return nil, fmt.Errorf("no checkpoint found for ref %s in workspace; please specify --range explicitly for initial capture", repoInfo.CurrentRef)
		}
		rangeExpr = fmt.Sprintf("%s..%s", cp.LastCapturedSHA, repoInfo.CurrentHead)
	} else if opts.RangeExpr != "" {
		rangeExpr = opts.RangeExpr
	} else {
		rangeExpr = "HEAD~1..HEAD"
	}

	// 3. 執行有界擷取
	facts, err := s.capturer.CaptureRange(ctx, workspace, rangeExpr)
	if err != nil {
		return nil, fmt.Errorf("capture range: %w", err)
	}

	// 4. 擷取去重 (Capture Dedup)
	existing, err := s.store.FindCandidateByRange(ctx, repoInfo.Workspace, facts.RepoID, facts.RefName, facts.BaseSHA, facts.HeadSHA, "gitmem-v1")
	if err != nil {
		return nil, fmt.Errorf("check existing candidate: %w", err)
	}

	if existing != nil {
		switch existing.Status {
		case "pending":
			return &CaptureResult{
				Candidate: existing,
				Facts:     facts,
				IsReused:  true,
				Message:   "reused existing pending candidate",
			}, nil
		case "approved":
			return &CaptureResult{
				Candidate: existing,
				Facts:     facts,
				IsReused:  true,
				Message:   fmt.Sprintf("range already approved as memory %s", existing.ApprovedMemoryID),
			}, nil
		case "discarded":
			if !opts.Force {
				return nil, fmt.Errorf("candidate for range %s..%s was already discarded; use --force to reopen", facts.BaseSHA[:min(8, len(facts.BaseSHA))], facts.HeadSHA[:min(8, len(facts.HeadSHA))])
			}
			// 若顯式帶入 force，重開候選為 pending
			if err := s.store.UpdateCandidateStatus(ctx, repoInfo.Workspace, existing.ID, "pending", "", nil); err != nil {
				return nil, fmt.Errorf("reopen discarded candidate: %w", err)
			}
			existing.Status = "pending"
			existing.ApprovedMemoryID = ""
			existing.ApprovedAt = nil
			return &CaptureResult{
				Candidate: existing,
				Facts:     facts,
				IsReused:  false,
				Message:   "reopened discarded candidate",
			}, nil
		}
	}

	// 5. 插入新候選
	candID := generateCandidateID()
	factsSummary, err := FormatFactsSummary(facts)
	if err != nil {
		return nil, fmt.Errorf("format facts summary: %w", err)
	}
	draft := GenerateDraftContent(facts)

	cand := &storage.MemoryCandidate{
		ID:            candID,
		Workspace:     repoInfo.Workspace,
		RepoID:        facts.RepoID,
		RefName:       facts.RefName,
		BaseSHA:       facts.BaseSHA,
		HeadSHA:       facts.HeadSHA,
		CommitCount:   facts.CommitCount,
		FactsSummary:  factsSummary,
		DraftContent:  draft,
		Status:        "pending",
		PolicyVersion: "gitmem-v1",
		CreatedAt:     time.Now().UTC(),
	}

	if err := s.store.SaveCandidate(ctx, cand); err != nil {
		return nil, fmt.Errorf("save new candidate: %w", err)
	}

	// 6. 依截斷政策評估 Checkpoint 前進 (僅 Complete Capture 且 SinceLast 時前進)
	if err := s.UpdateCheckpointIfEligible(ctx, facts, opts.SinceLast); err != nil {
		return nil, fmt.Errorf("update checkpoint: %w", err)
	}

	return &CaptureResult{
		Candidate: cand,
		Facts:     facts,
		IsReused:  false,
		Message:   "created new candidate draft",
	}, nil
}

// UpdateCheckpointIfEligible 根據擷取狀態更新 Checkpoint
// 嚴格保證：Partial Capture (Truncated == true) 絕對禁止自動前進 Checkpoint (H-TBD-3)
func (s *CaptureService) UpdateCheckpointIfEligible(ctx context.Context, facts *CaptureFacts, sinceLast bool) error {
	if facts == nil || facts.Truncated || !sinceLast {
		return nil
	}

	cp := &storage.GitCheckpoint{
		Workspace:       facts.Workspace,
		RepoID:          facts.RepoID,
		RefName:         facts.RefName,
		LastCapturedSHA: facts.HeadSHA,
		UpdatedAt:       time.Now().UTC(),
	}
	return s.store.SaveCheckpoint(ctx, cp)
}
