package gitmem

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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
	// 0. 在 Capture 服務邊界建立全流程統一時間預算，嚴格受限於 10 秒硬邊界（GITMEM-004）
	// 無論 caller 是否提供更長的 context（如 30s），整次 Capture 總時限絕對不得超過 10s（若 caller 期限更短則自然繼承生效）
	totalTimeout := 10 * time.Second
	if s.exec != nil && s.exec.timeout > 0 && s.exec.timeout < totalTimeout {
		totalTimeout = s.exec.timeout
	}
	captureCtx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	if err := captureCtx.Err(); err != nil {
		return nil, fmt.Errorf("capture deadline exceeded: %w", err)
	}

	// 1. AI 草稿生成模式 Preflight 檢查（GITMEM-009 / Fail-Closed 原則）
	// 若使用者顯式要求 --ai-draft 但未配置 AI Generator，必須在任何去重判定或 Checkpoint 前進前立即明確拒絕，
	// 絕不允許因相同 Range 存在既有候選而靜默跳過，更禁止在增量模式下偷偷前進 Checkpoint
	if opts.AIDraft && s.aiGen == nil {
		return nil, errors.New("AI draft generation requested (--ai-draft), but no AI generator is configured on daemon; please use standard capture or configure an AI generator")
	}

	// 2. 驗證並解析 Repo 邊界
	repoInfo, err := s.exec.ValidateAndResolveRepo(captureCtx, workspace)
	if err != nil {
		return nil, fmt.Errorf("validate repo: %w", err)
	}

	// 2. 決定 Range 表達式與 Checkpoint 期望基準
	var rangeExpr string
	var expectedOldSHA string
	if opts.SinceLast {
		cp, err := s.store.GetCheckpoint(captureCtx, repoInfo.Workspace, repoInfo.RepoID, repoInfo.CurrentRef)
		if err != nil {
			return nil, fmt.Errorf("get checkpoint: %w", err)
		}
		if cp == nil || cp.LastCapturedSHA == "" {
			return nil, fmt.Errorf("no checkpoint found for ref %s in workspace; please specify --range explicitly for initial capture", repoInfo.CurrentRef)
		}
		expectedOldSHA = cp.LastCapturedSHA
		rangeExpr = fmt.Sprintf("%s..%s", expectedOldSHA, repoInfo.CurrentHead)
	} else if opts.RangeExpr != "" {
		rangeExpr = opts.RangeExpr
	} else {
		rangeExpr = "HEAD~1..HEAD"
	}

	// 3. 執行有界擷取
	facts, err := s.capturer.CaptureRange(captureCtx, workspace, rangeExpr)
	if err != nil {
		return nil, fmt.Errorf("capture range: %w", err)
	}

	if err := captureCtx.Err(); err != nil {
		return nil, fmt.Errorf("capture deadline exceeded: %w", err)
	}

	// 4. 評估 Checkpoint 前進條件 (僅在 SinceLast 且無截斷時，並嚴格檢驗新舊 Head 祖先血統)
	newCP, err := s.evaluateCheckpointAdvance(captureCtx, repoInfo.RepoRoot, facts, opts.SinceLast)
	if err != nil {
		return nil, err
	}

	ancestryChecker := func(ctx context.Context, ancestorSHA, descendantSHA string) (bool, error) {
		return s.exec.IsAncestor(ctx, repoInfo.RepoRoot, ancestorSHA, descendantSHA)
	}
	advanceExistingCandidate := func(candidate *storage.MemoryCandidate) error {
		if newCP == nil || candidate.IsTruncated {
			return nil
		}
		return s.store.AdvanceCheckpointForCandidateTx(
			captureCtx, repoInfo.Workspace, candidate.ID, candidate.Status,
			newCP, expectedOldSHA, ancestryChecker,
		)
	}

	// 5. 擷取去重 (Capture Dedup)
	existing, err := s.store.FindCandidateByRange(captureCtx, repoInfo.Workspace, facts.RepoID, facts.RefName, facts.BaseSHA, facts.HeadSHA, "gitmem-v1")
	if err != nil {
		return nil, fmt.Errorf("check existing candidate: %w", err)
	}

	if existing != nil {
		switch existing.Status {
		case "discarded":
			// MUST FIX 1: 若既有候選已被捨棄且未帶 --force，必須立即拒絕！
			// 絕不允許在拒絕前推進 Checkpoint（維持游標在原位，不冒充成功）
			if !opts.Force {
				return nil, fmt.Errorf("candidate for range %s..%s was already discarded; use --force to reopen", facts.BaseSHA[:min(8, len(facts.BaseSHA))], facts.HeadSHA[:min(8, len(facts.HeadSHA))])
			}

			// 若顯式帶入 force，以本次擷取之 facts 重新生成草稿並重開候選為 pending
			factsSummary, err := FormatFactsSummary(facts)
			if err != nil {
				return nil, fmt.Errorf("format facts summary: %w", err)
			}
			var draft string
			if opts.AIDraft {
				if s.aiGen == nil {
					return nil, errors.New("AI draft generation requested (--ai-draft), but no AI generator is configured on daemon; please use standard capture or configure an AI generator")
				}
				var err error
				draft, err = s.aiGen.GenerateDraft(captureCtx, facts)
				if err != nil {
					return nil, fmt.Errorf("AI draft generation failed: %w", err)
				}
				if err := captureCtx.Err(); err != nil {
					return nil, fmt.Errorf("capture deadline exceeded after AI generation: %w", err)
				}
			} else {
				draft = GenerateDraftContent(facts)
			}

			if err := captureCtx.Err(); err != nil {
				return nil, fmt.Errorf("capture deadline exceeded: %w", err)
			}

			if err := s.store.ReopenDiscardedCandidateAndAdvanceCheckpointTx(
				captureCtx, repoInfo.Workspace, existing.ID, draft, factsSummary,
				facts.CommitCount, facts.Truncated, facts.TruncatedReason,
				newCP, expectedOldSHA, ancestryChecker,
			); err != nil {
				if errors.Is(err, storage.ErrCandidateStatusConflict) {
					// 發生並行狀態競爭（例如已被並行重開或並行核准），原子重讀最新持久化資料
					freshCand, getErr := s.store.GetCandidate(captureCtx, repoInfo.Workspace, existing.ID)
					if getErr == nil && freshCand != nil {
						if freshCand.Status == "approved" {
							if err := advanceExistingCandidate(freshCand); err != nil {
								return nil, fmt.Errorf("advance checkpoint: %w", err)
							}
							return &CaptureResult{
								Candidate: freshCand,
								Facts:     facts,
								IsReused:  true,
								Message:   fmt.Sprintf("range already approved as memory %s", freshCand.ApprovedMemoryID),
							}, nil
						} else if freshCand.Status == "pending" {
							if err := advanceExistingCandidate(freshCand); err != nil {
								return nil, fmt.Errorf("advance checkpoint: %w", err)
							}
							return &CaptureResult{
								Candidate: freshCand,
								Facts:     facts,
								IsReused:  true,
								Message:   "reused existing pending candidate",
							}, nil
						}
					}
				}
				return nil, fmt.Errorf("reopen discarded candidate: %w", err)
			}
			existing.Status = "pending"
			existing.DraftContent = draft
			existing.FactsSummary = factsSummary
			existing.CommitCount = facts.CommitCount
			existing.IsTruncated = facts.Truncated
			existing.TruncatedReason = facts.TruncatedReason
			existing.ApprovedMemoryID = ""
			existing.ApprovedAt = nil

			return &CaptureResult{
				Candidate: existing,
				Facts:     facts,
				IsReused:  false,
				Message:   "reopened discarded candidate",
			}, nil

		case "pending":
			// MUST FIX 2 & SHOULD FIX 1 & 2: 若既有候選原本為 Partial，但本次重試為 Complete，執行原子升級
			if existing.IsTruncated && !facts.Truncated {
				factsSummary, err := FormatFactsSummary(facts)
				if err != nil {
					return nil, fmt.Errorf("format facts summary: %w", err)
				}
				var draft string
				// SHOULD FIX 2: 檢查使用者是否曾手動編修草稿（草稿來源與編修版本檢查）
				// 若初次 Partial 生成的預設草稿已被使用者編輯，保留使用者的手動編修內容，僅升級客觀事實與截斷標記
				if existing.IsUserEdited {
					draft = existing.DraftContent // 保留使用者手動編輯之草稿內容
				} else if opts.AIDraft {
					if s.aiGen == nil {
						return nil, errors.New("AI draft generation requested (--ai-draft), but no AI generator is configured on daemon; please use standard capture or configure an AI generator")
					}
					var err error
					draft, err = s.aiGen.GenerateDraft(captureCtx, facts)
					if err != nil {
						return nil, fmt.Errorf("AI draft generation failed: %w", err)
					}
				} else {
					draft = GenerateDraftContent(facts)
				}

				// SHOULD FIX 1: 在單一原子交易中完成 Partial 候選升級與 Checkpoint 前進
				err = s.store.UpgradePartialCandidateAndAdvanceCheckpointTx(
					captureCtx,
					repoInfo.Workspace,
					existing.ID,
					draft,
					factsSummary,
					facts.CommitCount,
					newCP,
					expectedOldSHA,
					ancestryChecker,
				)
				if err != nil {
					if !errors.Is(err, storage.ErrCandidateStatusConflict) {
						return nil, fmt.Errorf("upgrade partial candidate and advance checkpoint: %w", err)
					}

					// MUST FIX 2: 遇到 CAS 並行狀態衝突（例如被另一個請求並行 Discard 或並行 Approve）
					// 必須重新讀取最新持久化記錄，並嚴格依最新狀態重新跑狀態機分流！
					fresh, getErr := s.store.GetCandidate(captureCtx, repoInfo.Workspace, existing.ID)
					if getErr != nil || fresh == nil {
						return nil, fmt.Errorf("%w: failed to reload fresh candidate on status conflict", storage.ErrCandidateStatusConflict)
					}

					switch fresh.Status {
					case "discarded":
						// 若並行請求已將該候選改為 discarded，未帶 force 必須堅決報錯拒絕！
						if !opts.Force {
							return nil, fmt.Errorf("candidate for range %s..%s was already discarded; use --force to reopen", facts.BaseSHA[:min(8, len(facts.BaseSHA))], facts.HeadSHA[:min(8, len(facts.HeadSHA))])
						}
						return nil, fmt.Errorf("%w: candidate was concurrently discarded; please retry capture with --force", storage.ErrCandidateStatusConflict)

					case "approved":
						if err := advanceExistingCandidate(fresh); err != nil {
							return nil, fmt.Errorf("advance checkpoint on approved conflict recovery: %w", err)
						}
						return &CaptureResult{
							Candidate: fresh,
							Facts:     facts,
							IsReused:  true,
							Message:   fmt.Sprintf("range already approved as memory %s", fresh.ApprovedMemoryID),
						}, nil

					case "pending":
						if !fresh.IsTruncated {
							// 另一個並行請求已經成功將其升級為 Complete！直接安全重用
							if err := advanceExistingCandidate(fresh); err != nil {
								return nil, fmt.Errorf("advance checkpoint on pending conflict recovery: %w", err)
							}
							return &CaptureResult{
								Candidate: fresh,
								Facts:     facts,
								IsReused:  true,
								Message:   "reused existing pending candidate",
							}, nil
						}
						return nil, fmt.Errorf("%w: concurrent modification on pending candidate; please retry", storage.ErrCandidateStatusConflict)
					}
				}

				// 升級成功，從資料庫重新載入最新狀態，保證本地物件與資料庫交易結果絕對一致（含 draft 與 is_user_edited）
				if fresh, reloadErr := s.store.GetCandidate(captureCtx, repoInfo.Workspace, existing.ID); reloadErr == nil && fresh != nil {
					*existing = *fresh
				} else {
					existing.DraftContent = draft
					existing.FactsSummary = factsSummary
					existing.CommitCount = facts.CommitCount
					existing.IsTruncated = false
					existing.TruncatedReason = ""
				}

				msg := "reused existing pending candidate (upgraded from partial to complete)"
				if opts.AIDraft && s.aiGen != nil && !existing.IsUserEdited {
					msg = "reused existing pending candidate (upgraded from partial to complete with AI draft)"
				}

				return &CaptureResult{
					Candidate: existing,
					Facts:     facts,
					IsReused:  true,
					Message:   msg,
				}, nil
			}

			// 若無須升級（既有候選原本就已是 Complete）：
			// 只有確認持久化候選為完整（!existing.IsTruncated）時，才前進 Checkpoint
			if err := advanceExistingCandidate(existing); err != nil {
				return nil, fmt.Errorf("advance checkpoint for existing candidate: %w", err)
			}

			msg := "reused existing pending candidate"
			if opts.AIDraft && s.aiGen != nil {
				msg = "reused existing pending candidate (immutable draft preserved; AI generation skipped for existing candidate)"
			}

			return &CaptureResult{
				Candidate: existing,
				Facts:     facts,
				IsReused:  true,
				Message:   msg,
			}, nil

		case "approved":
			// 庫中已核准的 Candidate 保證為完整（Partial Candidate 嚴格禁止被核准，杜絕變更遺漏至正式記憶庫）
			// 因此若符合 newCP != nil 且 !existing.IsTruncated，允許推進 Checkpoint
			if err := advanceExistingCandidate(existing); err != nil {
				return nil, fmt.Errorf("advance checkpoint for approved candidate: %w", err)
			}

			return &CaptureResult{
				Candidate: existing,
				Facts:     facts,
				IsReused:  true,
				Message:   fmt.Sprintf("range already approved as memory %s", existing.ApprovedMemoryID),
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
	if opts.AIDraft {
		if s.aiGen == nil {
			return nil, errors.New("AI draft generation requested (--ai-draft), but no AI generator is configured on daemon; please use standard capture or configure an AI generator")
		}
		var err error
		draft, err = s.aiGen.GenerateDraft(captureCtx, facts)
		if err != nil {
			return nil, fmt.Errorf("AI draft generation failed: %w", err)
		}
		if err := captureCtx.Err(); err != nil {
			return nil, fmt.Errorf("capture deadline exceeded after AI generation: %w", err)
		}
	} else {
		draft = GenerateDraftContent(facts)
	}

	if err := captureCtx.Err(); err != nil {
		return nil, fmt.Errorf("capture deadline exceeded: %w", err)
	}

	cand := &storage.MemoryCandidate{
		ID:              candID,
		Workspace:       repoInfo.Workspace,
		RepoID:          facts.RepoID,
		RefName:         facts.RefName,
		BaseSHA:         facts.BaseSHA,
		HeadSHA:         facts.HeadSHA,
		CommitCount:     facts.CommitCount,
		FactsSummary:    factsSummary,
		DraftContent:    draft,
		Status:          "pending",
		PolicyVersion:   "gitmem-v1",
		CreatedAt:       time.Now().UTC(),
		IsTruncated:     facts.Truncated,
		TruncatedReason: facts.TruncatedReason,
	}

	persistedCand, err := s.store.SaveCandidateAndCheckpointTx(captureCtx, cand, newCP, expectedOldSHA, ancestryChecker)
	if err != nil {
		if errors.Is(err, storage.ErrCandidateStatusConflict) {
			existingCand, findErr := s.store.FindCandidateByRange(captureCtx, facts.Workspace, facts.RepoID, facts.RefName, facts.BaseSHA, facts.HeadSHA, "gitmem-v1")
			if findErr == nil && existingCand != nil && existingCand.Status == "discarded" {
				if !opts.Force {
					return nil, fmt.Errorf("candidate for range %s..%s was already discarded; use --force to reopen", facts.BaseSHA[:min(8, len(facts.BaseSHA))], facts.HeadSHA[:min(8, len(facts.HeadSHA))])
				}
				if err := s.store.ReopenDiscardedCandidateAndAdvanceCheckpointTx(
					captureCtx, repoInfo.Workspace, existingCand.ID, draft, factsSummary,
					facts.CommitCount, facts.Truncated, facts.TruncatedReason,
					newCP, expectedOldSHA, ancestryChecker,
				); err != nil {
					return nil, fmt.Errorf("reopen discarded candidate on conflict: %w", err)
				}
				existingCand.Status = "pending"
				existingCand.DraftContent = draft
				existingCand.FactsSummary = factsSummary
				existingCand.CommitCount = facts.CommitCount
				existingCand.IsTruncated = facts.Truncated
				existingCand.TruncatedReason = facts.TruncatedReason
				existingCand.ApprovedMemoryID = ""
				existingCand.ApprovedAt = nil
				return &CaptureResult{
					Candidate: existingCand,
					Facts:     facts,
					IsReused:  false,
					Message:   "reopened discarded candidate",
				}, nil
			}
		}
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
		ancestryChecker := func(ctx context.Context, ancestorSHA, descendantSHA string) (bool, error) {
			return s.exec.IsAncestor(ctx, facts.Workspace, ancestorSHA, descendantSHA)
		}
		return s.store.AdvanceCheckpointTx(ctx, newCP, facts.BaseSHA, ancestryChecker)
	}
	return nil
}
