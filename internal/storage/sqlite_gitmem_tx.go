package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ApproveOptions 包含核准候選記憶時的自訂參數
type ApproveOptions struct {
	Content string // 可選的使用者自訂或編修後的正式記憶內文
}

func generateMemoryID() string {
	bytes := make([]byte, 4)
	_, _ = rand.Read(bytes)
	return fmt.Sprintf("mem-%d-%s", time.Now().Unix(), hex.EncodeToString(bytes))
}

// ApproveCandidateTx 在單一原子性 SQLite Transaction 中完成候選審核與正式記憶入庫 (H-TBD-4, H-TBD-6)
// 具備冪等性保證與防復活機制：已刪除的正式記憶嚴禁重新插入
func (s *SQLiteStore) ApproveCandidateTx(ctx context.Context, workspace, candidateID string, opts ApproveOptions) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// 1. 查詢並驗證候選狀態
	queryCand := `
	SELECT id, workspace, repo_id, ref_name, base_sha, head_sha,
	       commit_count, facts_summary, draft_content, status, approved_memory_id,
	       is_user_edited, is_truncated
	FROM memory_candidates
	WHERE workspace = ? AND id = ?
	`
	var cand MemoryCandidate
	var isUserEditedInt int
	var isTruncatedInt int
	err = tx.QueryRowContext(ctx, queryCand, workspace, candidateID).Scan(
		&cand.ID, &cand.Workspace, &cand.RepoID, &cand.RefName, &cand.BaseSHA, &cand.HeadSHA,
		&cand.CommitCount, &cand.FactsSummary, &cand.DraftContent, &cand.Status, &cand.ApprovedMemoryID,
		&isUserEditedInt, &isTruncatedInt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrCandidateNotFound
		}
		return "", fmt.Errorf("query candidate: %w", err)
	}
	cand.IsUserEdited = (isUserEditedInt == 1)
	cand.IsTruncated = (isTruncatedInt == 1)

	// MUST FIX 1: 嚴格禁止核准 Partial Candidate（避免未處理變更永久遺漏至正式記憶庫）
	if cand.IsTruncated {
		return "", fmt.Errorf("cannot approve partial candidate %s (is_truncated=true): please retry capture to obtain a complete candidate before approving", candidateID)
	}

	// 2. 狀態與冪等性處理
	if cand.Status == "approved" {
		// 檢查既有的 approved_memory_id 是否仍存在於 memories
		var dummyID string
		err := tx.QueryRowContext(ctx, "SELECT id FROM memories WHERE workspace = ? AND id = ?", workspace, cand.ApprovedMemoryID).Scan(&dummyID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// 防復活機制：原記憶已被手動刪除，嚴禁重新插入
				return "", fmt.Errorf("candidate %s was previously approved as memory %s, which was subsequently deleted; revival prevented to respect user deletion", candidateID, cand.ApprovedMemoryID)
			}
			return "", fmt.Errorf("check memory existence: %w", err)
		}
		// 原記憶依然存在，維持冪等成功
		return cand.ApprovedMemoryID, nil
	}

	if cand.Status == "discarded" {
		return "", fmt.Errorf("candidate %s has been discarded; cannot approve discarded candidate without reopening", candidateID)
	}

	// 3. 生成新 memory_id
	now := time.Now().UTC()
	memoryID := generateMemoryID()

	finalContent := cand.DraftContent
	isUserEdited := cand.IsUserEdited
	if strings.TrimSpace(opts.Content) != "" {
		finalContent = opts.Content
		if strings.TrimSpace(opts.Content) != strings.TrimSpace(cand.DraftContent) {
			isUserEdited = true
		}
	}
	if strings.TrimSpace(finalContent) == "" {
		return "", errors.New("cannot approve candidate with empty content: draft is empty and no edited content provided")
	}

	// 4. 寫入正式記憶 (memories)
	insertMem := `
	INSERT INTO memories (id, workspace, content, source, created_at)
	VALUES (?, ?, ?, 'git-approved', ?)
	`
	if _, err := tx.ExecContext(ctx, insertMem, memoryID, workspace, finalContent, now); err != nil {
		return "", fmt.Errorf("insert into memories: %w", err)
	}

	// 5. 解析 commit SHAs JSON
	commitSHAsJSON := "[]"
	var parsedFacts struct {
		Commits []struct {
			SHA string `json:"sha"`
		} `json:"commits"`
	}
	if err := json.Unmarshal([]byte(cand.FactsSummary), &parsedFacts); err == nil && len(parsedFacts.Commits) > 0 {
		shas := make([]string, 0, len(parsedFacts.Commits))
		for _, c := range parsedFacts.Commits {
			shas = append(shas, c.SHA)
		}
		if bytes, err := json.Marshal(shas); err == nil {
			commitSHAsJSON = string(bytes)
		}
	}

	// 6. 寫入來源關聯 (memory_provenance)
	insertProv := `
	INSERT INTO memory_provenance (
		memory_id, candidate_id, workspace, repo_id, ref_name,
		base_sha, head_sha, commit_count, commit_shas_json,
		facts_summary, is_user_edited, approved_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	isUserEditedInt = 0
	if isUserEdited {
		isUserEditedInt = 1
	}
	if _, err := tx.ExecContext(ctx, insertProv,
		memoryID, cand.ID, workspace, cand.RepoID, cand.RefName,
		cand.BaseSHA, cand.HeadSHA, cand.CommitCount, commitSHAsJSON,
		cand.FactsSummary, isUserEditedInt, now,
	); err != nil {
		return "", fmt.Errorf("insert into memory_provenance: %w", err)
	}

	// 7. 更新候選表狀態 (memory_candidates) - 嚴格原子 CAS 限定 status = 'pending'
	updateCand := `
	UPDATE memory_candidates
	SET status = 'approved', approved_memory_id = ?, approved_at = ?
	WHERE workspace = ? AND id = ? AND status = 'pending'
	`
	res, err := tx.ExecContext(ctx, updateCand, memoryID, now, workspace, cand.ID)
	if err != nil {
		return "", fmt.Errorf("update memory_candidates status: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("get rows affected: %w", err)
	}
	if rows == 0 {
		return "", fmt.Errorf("candidate %s is no longer pending (concurrent modification)", cand.ID)
	}

	// 8. 提交交易
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit approval transaction: %w", err)
	}

	return memoryID, nil
}

// CheckpointAncestryCheckFunc 檢查 ancestorSHA 是否為 descendantSHA 的祖先
type CheckpointAncestryCheckFunc func(ctx context.Context, ancestorSHA, descendantSHA string) (bool, error)

// SaveCandidateAndCheckpointTx 在單一原子 SQLite 交易中完成候選建立（insert-if-absent）與 Checkpoint 前進
// 具備 CAS 樂觀鎖與祖先單調遞增檢查，嚴格保證並行擷取時絕不發生逆序落盤導致 Checkpoint 倒退
func (s *SQLiteStore) SaveCandidateAndCheckpointTx(
	ctx context.Context,
	cand *MemoryCandidate,
	checkpoint *GitCheckpoint,
	expectedOldSHA string,
	isAncestor CheckpointAncestryCheckFunc,
) (*MemoryCandidate, error) {
	if cand == nil {
		return nil, errors.New("candidate cannot be nil")
	}
	if cand.ID == "" {
		return nil, errors.New("candidate id cannot be empty")
	}
	if cand.Workspace == "" {
		return nil, errors.New("workspace cannot be empty")
	}
	if cand.Status == "" {
		cand.Status = "pending"
	}
	if cand.PolicyVersion == "" {
		cand.PolicyVersion = "gitmem-v1"
	}
	if cand.CreatedAt.IsZero() {
		cand.CreatedAt = time.Now().UTC()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	isTruncatedInt := 0
	if cand.IsTruncated {
		isTruncatedInt = 1
	}
	isUserEditedInt := 0
	if cand.IsUserEdited {
		isUserEditedInt = 1
	}

	// 1. Insert-if-absent 候選草稿
	queryCand := `
	INSERT INTO memory_candidates (
		id, workspace, repo_id, ref_name, base_sha, head_sha,
		commit_count, facts_summary, draft_content, status,
		policy_version, approved_memory_id, created_at, approved_at,
		is_truncated, truncated_reason, is_user_edited
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(workspace, repo_id, ref_name, base_sha, head_sha, policy_version) DO NOTHING
	`
	_, err = tx.ExecContext(ctx, queryCand,
		cand.ID, cand.Workspace, cand.RepoID, cand.RefName, cand.BaseSHA, cand.HeadSHA,
		cand.CommitCount, cand.FactsSummary, cand.DraftContent, cand.Status,
		cand.PolicyVersion, cand.ApprovedMemoryID, cand.CreatedAt, cand.ApprovedAt,
		isTruncatedInt, cand.TruncatedReason, isUserEditedInt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert candidate in tx: %w", err)
	}

	// 2. 讀回持久化的候選記錄（若衝突則讀回既有記錄）
	selectCand := `
	SELECT id, workspace, repo_id, ref_name, base_sha, head_sha,
	       commit_count, facts_summary, draft_content, status,
	       policy_version, approved_memory_id, created_at, approved_at,
	       is_truncated, truncated_reason, is_user_edited
	FROM memory_candidates
	WHERE workspace = ? AND repo_id = ? AND ref_name = ?
	  AND base_sha = ? AND head_sha = ? AND policy_version = ?
	`
	var persisted MemoryCandidate
	var approvedAt sql.NullTime
	var persistedIsTruncatedInt int
	var persistedIsUserEditedInt int
	err = tx.QueryRowContext(ctx, selectCand,
		cand.Workspace, cand.RepoID, cand.RefName, cand.BaseSHA, cand.HeadSHA, cand.PolicyVersion,
	).Scan(
		&persisted.ID, &persisted.Workspace, &persisted.RepoID, &persisted.RefName, &persisted.BaseSHA, &persisted.HeadSHA,
		&persisted.CommitCount, &persisted.FactsSummary, &persisted.DraftContent, &persisted.Status,
		&persisted.PolicyVersion, &persisted.ApprovedMemoryID, &persisted.CreatedAt, &approvedAt,
		&persistedIsTruncatedInt, &persisted.TruncatedReason, &persistedIsUserEditedInt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan persisted candidate: %w", err)
	}
	if approvedAt.Valid {
		persisted.ApprovedAt = &approvedAt.Time
	}
	persisted.IsTruncated = (persistedIsTruncatedInt == 1)
	persisted.IsUserEdited = (persistedIsUserEditedInt == 1)

	// insert-if-absent 衝突時，只有 pending 或 approved 候選可參與 Checkpoint 推進。
	if persisted.Status != "pending" && persisted.Status != "approved" {
		return nil, fmt.Errorf("%w: candidate %s was already discarded; cannot advance checkpoint without force reopen", ErrCandidateStatusConflict, persisted.ID)
	}

	// 若既有候選為 Partial (is_truncated=1, status='pending')，但本次傳入為 Complete (!cand.IsTruncated)，執行原子升級
	if persisted.IsTruncated && !cand.IsTruncated && persisted.Status == "pending" {
		upgradeQuery := `
		UPDATE memory_candidates
		SET draft_content = CASE WHEN is_user_edited = 1 THEN draft_content ELSE ? END,
		    facts_summary = ?,
		    commit_count = ?,
		    is_truncated = 0,
		    truncated_reason = ''
		WHERE workspace = ? AND id = ? AND status = 'pending' AND is_truncated = 1
		`
		if _, err := tx.ExecContext(ctx, upgradeQuery, cand.DraftContent, cand.FactsSummary, cand.CommitCount, cand.Workspace, persisted.ID); err != nil {
			return nil, fmt.Errorf("upgrade partial candidate in tx: %w", err)
		}
		var finalDraft string
		var isUserEditedInt int
		if err := tx.QueryRowContext(ctx, "SELECT draft_content, is_user_edited FROM memory_candidates WHERE workspace = ? AND id = ?", cand.Workspace, persisted.ID).Scan(&finalDraft, &isUserEditedInt); err != nil {
			return nil, fmt.Errorf("reload draft after partial upgrade: %w", err)
		}
		persisted.DraftContent = finalDraft
		persisted.IsUserEdited = (isUserEditedInt == 1)
		persisted.FactsSummary = cand.FactsSummary
		persisted.CommitCount = cand.CommitCount
		persisted.IsTruncated = false
		persisted.TruncatedReason = ""
	}

	// 3. 若有 Checkpoint 需推進，在同筆交易內原子更新並進行 Monotonic / CAS 校驗
	// 安全約束：只有在確認持久化 Candidate 完整（!persisted.IsTruncated）時，才允許 advance Checkpoint
	// 未完整（Partial）的候選絕對禁止 advance（避免未處理變更永久遺漏）！
	if checkpoint != nil && !persisted.IsTruncated {
		if err := advanceCheckpointInTx(ctx, tx, checkpoint, expectedOldSHA, isAncestor); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	return &persisted, nil
}

// UpgradePartialCandidateAndAdvanceCheckpointTx 在單一原子交易中完成 Partial 候選升級與 Checkpoint 推進 (SHOULD FIX 1)
// 保證候選升級與游標前進命運同調：任何一步失敗即整體 Rollback，絕無中途孤立不一致
// MUST FIX 2: 依資料庫當前列原子保護人工草稿（若 is_user_edited=1 則絕不覆寫草稿）
func (s *SQLiteStore) UpgradePartialCandidateAndAdvanceCheckpointTx(
	ctx context.Context,
	workspace, id string,
	draftContent, factsSummary string,
	commitCount int,
	checkpoint *GitCheckpoint,
	expectedOldSHA string,
	isAncestor CheckpointAncestryCheckFunc,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for upgrade partial candidate: %w", err)
	}
	defer tx.Rollback()

	query := `
	UPDATE memory_candidates
	SET draft_content = CASE WHEN is_user_edited = 1 THEN draft_content ELSE ? END,
	    facts_summary = ?,
	    commit_count = ?,
	    is_truncated = 0,
	    truncated_reason = ''
	WHERE workspace = ? AND id = ? AND status = 'pending' AND is_truncated = 1
	`
	res, err := tx.ExecContext(ctx, query, draftContent, factsSummary, commitCount, workspace, id)
	if err != nil {
		return fmt.Errorf("upgrade partial candidate: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rows == 0 {
		return ErrCandidateStatusConflict
	}

	if checkpoint != nil {
		if err := advanceCheckpointInTx(ctx, tx, checkpoint, expectedOldSHA, isAncestor); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// AdvanceCheckpointForCandidateTx 在同一交易內重新確認完整 Candidate 狀態並推進 Checkpoint。
func (s *SQLiteStore) AdvanceCheckpointForCandidateTx(
	ctx context.Context,
	workspace, candidateID, expectedStatus string,
	checkpoint *GitCheckpoint,
	expectedOldSHA string,
	isAncestor CheckpointAncestryCheckFunc,
) error {
	if expectedStatus != "pending" && expectedStatus != "approved" {
		return ErrCandidateStatusConflict
	}
	if checkpoint == nil {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for candidate checkpoint advance: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE memory_candidates SET status = status
		WHERE workspace = ? AND id = ? AND status = ? AND is_truncated = 0
	`, workspace, candidateID, expectedStatus)
	if err != nil {
		return fmt.Errorf("lock candidate for checkpoint advance: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get candidate rows affected: %w", err)
	}
	if rows == 0 {
		return ErrCandidateStatusConflict
	}
	if err := advanceCheckpointInTx(ctx, tx, checkpoint, expectedOldSHA, isAncestor); err != nil {
		return err
	}
	return tx.Commit()
}

// ReopenDiscardedCandidateAndAdvanceCheckpointTx 原子重開 discarded Candidate，並在完整時推進 Checkpoint。
func (s *SQLiteStore) ReopenDiscardedCandidateAndAdvanceCheckpointTx(
	ctx context.Context,
	workspace, id, draftContent, factsSummary string,
	commitCount int,
	isTruncated bool,
	truncatedReason string,
	checkpoint *GitCheckpoint,
	expectedOldSHA string,
	isAncestor CheckpointAncestryCheckFunc,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for reopen candidate: %w", err)
	}
	defer tx.Rollback()

	isTruncatedInt := 0
	if isTruncated {
		isTruncatedInt = 1
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE memory_candidates
		SET status = 'pending', draft_content = ?, facts_summary = ?, commit_count = ?,
		    is_truncated = ?, truncated_reason = ?, is_user_edited = 0,
		    approved_memory_id = '', approved_at = NULL
		WHERE workspace = ? AND id = ? AND status = 'discarded'
	`, draftContent, factsSummary, commitCount, isTruncatedInt, truncatedReason, workspace, id)
	if err != nil {
		return fmt.Errorf("reopen candidate: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rows == 0 {
		return ErrCandidateStatusConflict
	}
	if checkpoint != nil && !isTruncated {
		if err := advanceCheckpointInTx(ctx, tx, checkpoint, expectedOldSHA, isAncestor); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AdvanceCheckpointTx 在獨立交易內推進 Checkpoint，嚴格保證單調前進（Monotonicity）與 CAS 防倒退
func (s *SQLiteStore) AdvanceCheckpointTx(
	ctx context.Context,
	checkpoint *GitCheckpoint,
	expectedOldSHA string,
	isAncestor CheckpointAncestryCheckFunc,
) error {
	if checkpoint == nil {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for advance checkpoint: %w", err)
	}
	defer tx.Rollback()

	if err := advanceCheckpointInTx(ctx, tx, checkpoint, expectedOldSHA, isAncestor); err != nil {
		return err
	}
	return tx.Commit()
}

func advanceCheckpointInTx(
	ctx context.Context,
	tx *sql.Tx,
	checkpoint *GitCheckpoint,
	expectedOldSHA string,
	isAncestor CheckpointAncestryCheckFunc,
) error {
	var currentSHA string
	err := tx.QueryRowContext(ctx, `
		SELECT last_captured_sha FROM git_checkpoints 
		WHERE workspace = ? AND repo_id = ? AND ref_name = ?
	`, checkpoint.Workspace, checkpoint.RepoID, checkpoint.RefName).Scan(&currentSHA)

	if errors.Is(err, sql.ErrNoRows) {
		// 庫中尚無 Checkpoint（初始擷取），直接寫入
		queryInsert := `
		INSERT INTO git_checkpoints (workspace, repo_id, ref_name, last_captured_sha, updated_at)
		VALUES (?, ?, ?, ?, ?)
		`
		_, err = tx.ExecContext(ctx, queryInsert,
			checkpoint.Workspace, checkpoint.RepoID, checkpoint.RefName,
			checkpoint.LastCapturedSHA, checkpoint.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("insert initial checkpoint in tx: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("query current checkpoint in tx: %w", err)
	}

	targetSHA := checkpoint.LastCapturedSHA
	if currentSHA == targetSHA {
		// 已經是同一 SHA，更新 updated_at
		_, err = tx.ExecContext(ctx, `
			UPDATE git_checkpoints SET updated_at = ?
			WHERE workspace = ? AND repo_id = ? AND ref_name = ?
		`, checkpoint.UpdatedAt, checkpoint.Workspace, checkpoint.RepoID, checkpoint.RefName)
		if err != nil {
			return fmt.Errorf("update checkpoint timestamp in tx: %w", err)
		}
		return nil
	}

	// 關鍵安全檢查：要將 Checkpoint 從 currentSHA 推進到 targetSHA，
	// 強制驗證祖先單調關係（Monotonic Ancestry）：
	if isAncestor != nil {
		// 1. 檢查 targetSHA 是否為 currentSHA 之子裔（正向單調推進）
		isDescendant, err := isAncestor(ctx, currentSHA, targetSHA)
		if err != nil {
			return fmt.Errorf("verify target checkpoint ancestry: %w", err)
		}
		if isDescendant {
			// target 比 current 更先進，安全前進！
			_, err = tx.ExecContext(ctx, `
				UPDATE git_checkpoints SET last_captured_sha = ?, updated_at = ?
				WHERE workspace = ? AND repo_id = ? AND ref_name = ?
			`, targetSHA, checkpoint.UpdatedAt, checkpoint.Workspace, checkpoint.RepoID, checkpoint.RefName)
			if err != nil {
				return fmt.Errorf("advance checkpoint in tx: %w", err)
			}
			return nil
		}

		// 2. target 不是 current 的後裔，檢查 current 是否已比 target 更新（current 是 target 的後裔）
		isAhead, err := isAncestor(ctx, targetSHA, currentSHA)
		if err != nil {
			return fmt.Errorf("verify current checkpoint ancestry: %w", err)
		}
		if isAhead {
			// 庫中當前 SHA 已經超越了目標 SHA，為了單調性絕對不倒退！
			// 安全跳過，保持庫中更新的 currentSHA
			return nil
		}

		// 3. 兩者互不為祖先，歷史發生分叉或無法確認血統
		return fmt.Errorf("concurrent checkpoint update conflict: current %s and target %s diverged", currentSHA, targetSHA)
	}

	// 若未提供 isAncestor 檢查器，僅仰賴嚴格的 expectedOldSHA 比對（嚴防 stale update）
	if expectedOldSHA != "" && currentSHA == expectedOldSHA {
		_, err = tx.ExecContext(ctx, `
			UPDATE git_checkpoints SET last_captured_sha = ?, updated_at = ?
			WHERE workspace = ? AND repo_id = ? AND ref_name = ?
		`, targetSHA, checkpoint.UpdatedAt, checkpoint.Workspace, checkpoint.RepoID, checkpoint.RefName)
		if err != nil {
			return fmt.Errorf("advance checkpoint in tx: %w", err)
		}
		return nil
	}

	return fmt.Errorf("concurrent checkpoint conflict: expected %s, found %s", expectedOldSHA, currentSHA)
}
