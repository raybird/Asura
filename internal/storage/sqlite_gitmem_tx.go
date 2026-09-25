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
