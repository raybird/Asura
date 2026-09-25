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
	AIDraft   bool
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
	aiGen    AIDraftGenerator
}

// NewCaptureService 建立新的擷取協調服務實例
func NewCaptureService(exec *GitExecutor, store *storage.SQLiteStore) *CaptureService {
	return &CaptureService{
		exec:     exec,
		capturer: NewCapturer(exec),
		store:    store,
	}
}

// SetAIGenerator 注入可選的 AI 輔助草稿生成器 (GITMEM-009)
func (s *CaptureService) SetAIGenerator(gen AIDraftGenerator) {
	s.aiGen = gen
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

	// 4. 評估 Checkpoint 前進條件 (僅在 SinceLast 且無截斷時，並嚴格檢驗新舊 Head 祖先血統)
	newCP, err := s.evaluateCheckpointAdvance(ctx, repoInfo.RepoRoot, facts, opts.SinceLast)
	if err != nil {
		return nil, err
	}

	// 5. 擷取去重 (Capture Dedup)
	existing, err := s.store.FindCandidateByRange(ctx, repoInfo.Workspace, facts.RepoID, facts.RefName, facts.BaseSHA, facts.HeadSHA, "gitmem-v1")
	if err != nil {
		return nil, fmt.Errorf("check existing candidate: %w", err)
	}

	if existing != nil {
		if newCP != nil {
			if err := s.store.SaveCheckpoint(ctx, newCP); err != nil {
				return nil, fmt.Errorf("advance checkpoint for existing candidate: %w", err)
			}
		}

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

	// 6. 插入新候選與推進 Checkpoint（在單一原子 Transaction 內完成）
	candID := generateCandidateID()
	factsSummary, err := FormatFactsSummary(facts)
	if err != nil {
		return nil, fmt.Errorf("format facts summary: %w", err)
	}
	var draft string
	if opts.AIDraft && s.aiGen != nil {
		draft, _ = GenerateAIDraftWithFallback(ctx, facts, s.aiGen)
	} else {
		draft = GenerateDraftContent(facts)
	}

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

	persistedCand, err := s.store.SaveCandidateAndCheckpointTx(ctx, cand, newCP)
	if err != nil {
		return nil, fmt.Errorf("save candidate and checkpoint: %w", err)
	}

	isReused := (persistedCand.ID != cand.ID)
	msg := "created new candidate draft"
	if isReused {
		msg = "reused existing candidate due to concurrent creation"
	}

	return &CaptureResult{
		Candidate: persistedCand,
		Facts:     facts,
		IsReused:  isReused,
		Message:   msg,
	}, nil
}

// evaluateCheckpointAdvance 根據擷取狀態安全評估 Checkpoint 前進
// 嚴格保證：
// 1. Initial Capture (既無 Checkpoint 且未截斷)：建立初始基準 Checkpoint
// 2. 既有 Checkpoint 時，只有顯式 SinceLast (增量模式) 允許前進 Checkpoint；手動 --range 絕對不覆寫也不倒退既有 Checkpoint
// 3. Partial Capture (Truncated == true) 絕對禁止前進 Checkpoint
// 4. 新 Head 必須為既有 Checkpoint 之子裔 (Ancestry Check)，防止 force push 導致 Checkpoint 倒退
func (s *CaptureService) evaluateCheckpointAdvance(ctx context.Context, repoRoot string, facts *CaptureFacts, sinceLast bool) (*storage.GitCheckpoint, error) {
	if facts == nil || facts.Truncated {
		return nil, nil
	}

	// 查詢既有 checkpoint
	cp, err := s.store.GetCheckpoint(ctx, facts.Workspace, facts.RepoID, facts.RefName)
	if err != nil {
		return nil, fmt.Errorf("query existing checkpoint: %w", err)
	}

	// 情況 1：尚無 checkpoint（初始擷取建立基準）
	if cp == nil || cp.LastCapturedSHA == "" {
		return &storage.GitCheckpoint{
			Workspace:       facts.Workspace,
			RepoID:          facts.RepoID,
			RefName:         facts.RefName,
			LastCapturedSHA: facts.HeadSHA,
			UpdatedAt:       time.Now().UTC(),
		}, nil
	}

	// 情況 2：既有 checkpoint 存在，但為手動指定 --range（非增量模式）
	// 手動指定 --range 絕對禁止覆寫或前進既有 checkpoint，防止增量基線倒退或混淆
	if !sinceLast {
		return nil, nil
	}

	// 情況 3：增量模式 (sinceLast == true)
	// 若 SHA 與當前 Head 相同，無須重複更新
	if cp.LastCapturedSHA == facts.HeadSHA {
		return nil, nil
	}

	// 增量校驗：當前 HeadSHA 必須是 LastCapturedSHA 的後裔（防止 force push 倒退或分叉）
	isAnc, err := s.exec.IsAncestor(ctx, repoRoot, cp.LastCapturedSHA, facts.HeadSHA)
	if err != nil {
		return nil, fmt.Errorf("verify ancestry for checkpoint advance: %w", err)
	}
	if !isAnc {
		return nil, fmt.Errorf("checkpoint advance rejected: current head %s is not a descendant of previous checkpoint %s (history diverged or rewound)", facts.HeadSHA, cp.LastCapturedSHA)
	}

	return &storage.GitCheckpoint{
		Workspace:       facts.Workspace,
		RepoID:          facts.RepoID,
		RefName:         facts.RefName,
		LastCapturedSHA: facts.HeadSHA,
		UpdatedAt:       time.Now().UTC(),
	}, nil
}

// UpdateCheckpointIfEligible 根據擷取狀態安全更新 Checkpoint
// 嚴格保證：手動 --range 或 Partial 截斷絕對禁止推進 Checkpoint
func (s *CaptureService) UpdateCheckpointIfEligible(ctx context.Context, facts *CaptureFacts, sinceLast bool) error {
	if facts == nil {
		return nil
	}
	newCP, err := s.evaluateCheckpointAdvance(ctx, facts.Workspace, facts, sinceLast)
	if err != nil {
		return err
	}
	if newCP != nil {
		return s.store.SaveCheckpoint(ctx, newCP)
	}
	return nil
}
