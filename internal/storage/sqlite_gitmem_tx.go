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

// SaveCandidateAndCheckpointTx 在單一原子 SQLite 交易中完成候選建立（insert-if-absent）與 Checkpoint 前進
func (s *SQLiteStore) SaveCandidateAndCheckpointTx(ctx context.Context, cand *MemoryCandidate, checkpoint *GitCheckpoint) (*MemoryCandidate, error) {
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

	// 3. 若有 Checkpoint 需推進，在同筆交易內原子更新
	if checkpoint != nil {
		queryCP := `
		INSERT INTO git_checkpoints (workspace, repo_id, ref_name, last_captured_sha, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(workspace, repo_id, ref_name) DO UPDATE SET
			last_captured_sha = excluded.last_captured_sha,
			updated_at = excluded.updated_at
		`
		_, err = tx.ExecContext(ctx, queryCP,
			checkpoint.Workspace, checkpoint.RepoID, checkpoint.RefName,
			checkpoint.LastCapturedSHA, checkpoint.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("save checkpoint in tx: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	return &persisted, nil
}
