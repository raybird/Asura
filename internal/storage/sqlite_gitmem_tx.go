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
	       commit_count, facts_summary, draft_content, status, approved_memory_id
	FROM memory_candidates
	WHERE workspace = ? AND id = ?
	`
	var cand MemoryCandidate
	err = tx.QueryRowContext(ctx, queryCand, workspace, candidateID).Scan(
		&cand.ID, &cand.Workspace, &cand.RepoID, &cand.RefName, &cand.BaseSHA, &cand.HeadSHA,
		&cand.CommitCount, &cand.FactsSummary, &cand.DraftContent, &cand.Status, &cand.ApprovedMemoryID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrCandidateNotFound
		}
		return "", fmt.Errorf("query candidate: %w", err)
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
	isUserEdited := false
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
	isUserEditedInt := 0
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

	// 7. 更新候選表狀態 (memory_candidates)
	updateCand := `
	UPDATE memory_candidates
	SET status = 'approved', approved_memory_id = ?, approved_at = ?
	WHERE workspace = ? AND id = ?
	`
	if _, err := tx.ExecContext(ctx, updateCand, memoryID, now, workspace, cand.ID); err != nil {
		return "", fmt.Errorf("update memory_candidates status: %w", err)
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

	// 1. Insert-if-absent 候選草稿
	queryCand := `
	INSERT INTO memory_candidates (
		id, workspace, repo_id, ref_name, base_sha, head_sha,
		commit_count, facts_summary, draft_content, status,
		policy_version, approved_memory_id, created_at, approved_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(workspace, repo_id, ref_name, base_sha, head_sha, policy_version) DO NOTHING
	`
	_, err = tx.ExecContext(ctx, queryCand,
		cand.ID, cand.Workspace, cand.RepoID, cand.RefName, cand.BaseSHA, cand.HeadSHA,
		cand.CommitCount, cand.FactsSummary, cand.DraftContent, cand.Status,
		cand.PolicyVersion, cand.ApprovedMemoryID, cand.CreatedAt, cand.ApprovedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert candidate in tx: %w", err)
	}

	// 2. 讀回持久化的候選記錄（若衝突則讀回既有記錄）
	selectCand := `
	SELECT id, workspace, repo_id, ref_name, base_sha, head_sha,
	       commit_count, facts_summary, draft_content, status,
	       policy_version, approved_memory_id, created_at, approved_at
	FROM memory_candidates
	WHERE workspace = ? AND repo_id = ? AND ref_name = ?
	  AND base_sha = ? AND head_sha = ? AND policy_version = ?
	`
	var persisted MemoryCandidate
	var approvedAt sql.NullTime
	err = tx.QueryRowContext(ctx, selectCand,
		cand.Workspace, cand.RepoID, cand.RefName, cand.BaseSHA, cand.HeadSHA, cand.PolicyVersion,
	).Scan(
		&persisted.ID, &persisted.Workspace, &persisted.RepoID, &persisted.RefName, &persisted.BaseSHA, &persisted.HeadSHA,
		&persisted.CommitCount, &persisted.FactsSummary, &persisted.DraftContent, &persisted.Status,
		&persisted.PolicyVersion, &persisted.ApprovedMemoryID, &persisted.CreatedAt, &approvedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan persisted candidate: %w", err)
	}
	if approvedAt.Valid {
		persisted.ApprovedAt = &approvedAt.Time
	}

	// 3. 若有 Checkpoint 需推進，在同筆交易內原子更新並進行 Monotonic / CAS 校驗
	if checkpoint != nil {
		if err := advanceCheckpointInTx(ctx, tx, checkpoint, expectedOldSHA, isAncestor); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	return &persisted, nil
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
		if err == nil && isDescendant {
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
		isAhead, _ := isAncestor(ctx, targetSHA, currentSHA)
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
