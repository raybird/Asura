package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

var (
	// ErrCandidateNotFound 代表候選記憶不存在
	ErrCandidateNotFound = errors.New("memory candidate not found")
)

// MemoryCandidate 代表 Git 變更分析後產生的候選專案記憶草稿
type MemoryCandidate struct {
	ID               string     `json:"id"`
	Workspace        string     `json:"workspace"`
	RepoID           string     `json:"repo_id"`
	RefName          string     `json:"ref_name"`
	BaseSHA          string     `json:"base_sha"`
	HeadSHA          string     `json:"head_sha"`
	CommitCount      int        `json:"commit_count"`
	FactsSummary     string     `json:"facts_summary"`
	DraftContent     string     `json:"draft_content"`
	Status           string     `json:"status"` // pending, approved, discarded
	PolicyVersion    string     `json:"policy_version"`
	ApprovedMemoryID string     `json:"approved_memory_id"`
	CreatedAt        time.Time  `json:"created_at"`
	ApprovedAt       *time.Time `json:"approved_at,omitempty"`
}

// CandidateSummary 代表有界候選清單項目（不包含龐大 facts_summary JSON 與全文 diff，約束傳輸大小）
type CandidateSummary struct {
	ID               string     `json:"id"`
	Workspace        string     `json:"workspace"`
	RepoID           string     `json:"repo_id"`
	RefName          string     `json:"ref_name"`
	BaseSHA          string     `json:"base_sha"`
	HeadSHA          string     `json:"head_sha"`
	CommitCount      int        `json:"commit_count"`
	Summary          string     `json:"summary"` // 截短至最多 512 UTF-8 bytes
	Status           string     `json:"status"`
	PolicyVersion    string     `json:"policy_version"`
	ApprovedMemoryID string     `json:"approved_memory_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	ApprovedAt       *time.Time `json:"approved_at,omitempty"`
}

// GitCheckpoint 記錄單一儲存庫在特定分支上的完整擷取進度游標
type GitCheckpoint struct {
	Workspace       string    `json:"workspace"`
	RepoID          string    `json:"repo_id"`
	RefName         string    `json:"ref_name"`
	LastCapturedSHA string    `json:"last_captured_sha"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// MemoryProvenance 記錄正式記憶與原始 Git 候選的完整稽核與來源關聯
type MemoryProvenance struct {
	MemoryID       string    `json:"memory_id"`
	CandidateID    string    `json:"candidate_id"`
	Workspace      string    `json:"workspace"`
	RepoID         string    `json:"repo_id"`
	RefName        string    `json:"ref_name"`
	BaseSHA        string    `json:"base_sha"`
	HeadSHA        string    `json:"head_sha"`
	CommitCount    int       `json:"commit_count"`
	CommitSHAsJSON string    `json:"commit_shas_json"`
	FactsSummary   string    `json:"facts_summary"`
	IsUserEdited   bool      `json:"is_user_edited"`
	ApprovedAt     time.Time `json:"approved_at"`
}

// SaveCandidate 儲存或插入候選記憶（insert-if-absent，保證不覆寫既有候選草稿與狀態）
func (s *SQLiteStore) SaveCandidate(ctx context.Context, c *MemoryCandidate) error {
	if c.ID == "" {
		return errors.New("candidate id cannot be empty")
	}
	if c.Workspace == "" {
		return errors.New("workspace cannot be empty")
	}
	if c.Status == "" {
		c.Status = "pending"
	}
	if c.PolicyVersion == "" {
		c.PolicyVersion = "gitmem-v1"
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}

	query := `
	INSERT INTO memory_candidates (
		id, workspace, repo_id, ref_name, base_sha, head_sha,
		commit_count, facts_summary, draft_content, status,
		policy_version, approved_memory_id, created_at, approved_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(workspace, repo_id, ref_name, base_sha, head_sha, policy_version) DO NOTHING
	`
	_, err := s.db.ExecContext(ctx, query,
		c.ID, c.Workspace, c.RepoID, c.RefName, c.BaseSHA, c.HeadSHA,
		c.CommitCount, c.FactsSummary, c.DraftContent, c.Status,
		c.PolicyVersion, c.ApprovedMemoryID, c.CreatedAt, c.ApprovedAt,
	)
	if err != nil {
		return fmt.Errorf("save candidate: %w", err)
	}

	// 讀回持久化記錄確保回傳真實入庫的 ID 與狀態
	persisted, err := s.FindCandidateByRange(ctx, c.Workspace, c.RepoID, c.RefName, c.BaseSHA, c.HeadSHA, c.PolicyVersion)
	if err != nil {
		return fmt.Errorf("reload candidate: %w", err)
	}
	if persisted != nil {
		*c = *persisted
	}
	return nil
}

// GetCandidate 依工作區與候選 ID 取得候選記憶
func (s *SQLiteStore) GetCandidate(ctx context.Context, workspace, id string) (*MemoryCandidate, error) {
	query := `
	SELECT id, workspace, repo_id, ref_name, base_sha, head_sha,
	       commit_count, facts_summary, draft_content, status,
	       policy_version, approved_memory_id, created_at, approved_at
	FROM memory_candidates
	WHERE workspace = ? AND id = ?
	`
	var c MemoryCandidate
	var approvedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, query, workspace, id).Scan(
		&c.ID, &c.Workspace, &c.RepoID, &c.RefName, &c.BaseSHA, &c.HeadSHA,
		&c.CommitCount, &c.FactsSummary, &c.DraftContent, &c.Status,
		&c.PolicyVersion, &c.ApprovedMemoryID, &c.CreatedAt, &approvedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCandidateNotFound
		}
		return nil, fmt.Errorf("get candidate: %w", err)
	}
	if approvedAt.Valid {
		c.ApprovedAt = &approvedAt.Time
	}
	return &c, nil
}

// FindCandidateByRange 依 Range 複合唯一索引比對既有候選
func (s *SQLiteStore) FindCandidateByRange(ctx context.Context, workspace, repoID, refName, baseSHA, headSHA, policyVersion string) (*MemoryCandidate, error) {
	if policyVersion == "" {
		policyVersion = "gitmem-v1"
	}
	query := `
	SELECT id, workspace, repo_id, ref_name, base_sha, head_sha,
	       commit_count, facts_summary, draft_content, status,
	       policy_version, approved_memory_id, created_at, approved_at
	FROM memory_candidates
	WHERE workspace = ? AND repo_id = ? AND ref_name = ?
	  AND base_sha = ? AND head_sha = ? AND policy_version = ?
	`
	var c MemoryCandidate
	var approvedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, query, workspace, repoID, refName, baseSHA, headSHA, policyVersion).Scan(
		&c.ID, &c.Workspace, &c.RepoID, &c.RefName, &c.BaseSHA, &c.HeadSHA,
		&c.CommitCount, &c.FactsSummary, &c.DraftContent, &c.Status,
		&c.PolicyVersion, &c.ApprovedMemoryID, &c.CreatedAt, &approvedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("find candidate by range: %w", err)
	}
	if approvedAt.Valid {
		c.ApprovedAt = &approvedAt.Time
	}
	return &c, nil
}

// ListCandidates 依工作區與狀態篩選候選清單（有界摘要，不傳輸龐大 facts_summary JSON，每頁最多 50 筆）
func (s *SQLiteStore) ListCandidates(ctx context.Context, workspace string, status string, limit int) ([]CandidateSummary, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}

	var query string
	var args []interface{}
	if status != "" {
		query = `
		SELECT id, workspace, repo_id, ref_name, base_sha, head_sha,
		       commit_count, substr(CAST(draft_content AS BLOB), 1, 513), status,
		       policy_version, COALESCE(approved_memory_id, ''), created_at, approved_at
		FROM memory_candidates
		WHERE workspace = ? AND status = ?
		ORDER BY created_at DESC, id ASC
		LIMIT ?
		`
		args = []interface{}{workspace, status, limit}
	} else {
		query = `
		SELECT id, workspace, repo_id, ref_name, base_sha, head_sha,
		       commit_count, substr(CAST(draft_content AS BLOB), 1, 513), status,
		       policy_version, COALESCE(approved_memory_id, ''), created_at, approved_at
		FROM memory_candidates
		WHERE workspace = ?
		ORDER BY created_at DESC, id ASC
		LIMIT ?
		`
		args = []interface{}{workspace, limit}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]CandidateSummary, 0)
	for rows.Next() {
		var c CandidateSummary
		var rawSummary string
		var approvedAt sql.NullTime
		if err := rows.Scan(
			&c.ID, &c.Workspace, &c.RepoID, &c.RefName, &c.BaseSHA, &c.HeadSHA,
			&c.CommitCount, &rawSummary, &c.Status,
			&c.PolicyVersion, &c.ApprovedMemoryID, &c.CreatedAt, &approvedAt,
		); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		if len(rawSummary) > 512 {
			c.Summary = TruncateUTF8Safe(rawSummary, 512) + "..."
		} else {
			c.Summary = rawSummary
		}
		if approvedAt.Valid {
			c.ApprovedAt = &approvedAt.Time
		}
		candidates = append(candidates, c)
	}
	return candidates, rows.Err()
}

// TruncateUTF8Safe 將字串截短至最多 maxBytes 位元組，保證絕不腰斬 UTF-8 多位元組字元
func TruncateUTF8Safe(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cutoff := maxBytes
	for cutoff > 0 && !utf8.RuneStart(s[cutoff]) {
		cutoff--
	}
	for cutoff > 0 && !utf8.ValidString(s[:cutoff]) {
		cutoff--
		for cutoff > 0 && !utf8.RuneStart(s[cutoff]) {
			cutoff--
		}
	}
	return s[:cutoff]
}

// ClearProcessedCandidates 清理指定工作區中已處理完畢之候選（approved 與 discarded）
// 安全約束：絕對禁止 DELETE 整個 Candidate 列（否則會抹除 approved_memory_id、唯一索引與審核痕跡，引發防復活繞過風險）
// 僅清理其佔用空間的大型 facts_summary 與 draft_content payload
func (s *SQLiteStore) ClearProcessedCandidates(ctx context.Context, workspace string) (int64, error) {
	query := `
	UPDATE memory_candidates 
	SET draft_content = '', facts_summary = '{}' 
	WHERE workspace = ? 
	  AND status IN ('approved', 'discarded') 
	  AND (draft_content != '' OR facts_summary != '{}')
	`
	res, err := s.db.ExecContext(ctx, query, workspace)
	if err != nil {
		return 0, fmt.Errorf("clear processed candidates: %w", err)
	}
	return res.RowsAffected()
}

// ReopenDiscardedCandidate 重開已捨棄之候選，以新的草稿與事實更新其內容，並重設狀態為 pending
func (s *SQLiteStore) ReopenDiscardedCandidate(ctx context.Context, workspace, id string, draftContent, factsSummary string, commitCount int) error {
	query := `
	UPDATE memory_candidates
	SET status = 'pending', draft_content = ?, facts_summary = ?, commit_count = ?, approved_memory_id = '', approved_at = NULL
	WHERE workspace = ? AND id = ?
	`
	res, err := s.db.ExecContext(ctx, query, draftContent, factsSummary, commitCount, workspace, id)
	if err != nil {
		return fmt.Errorf("reopen candidate: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rows == 0 {
		return ErrCandidateNotFound
	}
	return nil
}

// UpdateCandidateStatus 更新候選狀態
func (s *SQLiteStore) UpdateCandidateStatus(ctx context.Context, workspace, id string, status string, approvedMemoryID string, approvedAt *time.Time) error {
	query := `
	UPDATE memory_candidates
	SET status = ?, approved_memory_id = ?, approved_at = ?
	WHERE workspace = ? AND id = ?
	`
	res, err := s.db.ExecContext(ctx, query, status, approvedMemoryID, approvedAt, workspace, id)
	if err != nil {
		return fmt.Errorf("update candidate status: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rows == 0 {
		return ErrCandidateNotFound
	}
	return nil
}

// UpdateCandidateDraft 更新候選的草稿內文
func (s *SQLiteStore) UpdateCandidateDraft(ctx context.Context, workspace, id, draftContent string) error {
	query := `
	UPDATE memory_candidates
	SET draft_content = ?
	WHERE workspace = ? AND id = ?
	`
	res, err := s.db.ExecContext(ctx, query, draftContent, workspace, id)
	if err != nil {
		return fmt.Errorf("update candidate draft: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rows == 0 {
		return ErrCandidateNotFound
	}
	return nil
}

// GetCheckpoint 依 (workspace, repo_id, ref_name) 複合鍵取得最新檢查點
func (s *SQLiteStore) GetCheckpoint(ctx context.Context, workspace, repoID, refName string) (*GitCheckpoint, error) {
	query := `
	SELECT workspace, repo_id, ref_name, last_captured_sha, updated_at
	FROM git_checkpoints
	WHERE workspace = ? AND repo_id = ? AND ref_name = ?
	`
	var cp GitCheckpoint
	err := s.db.QueryRowContext(ctx, query, workspace, repoID, refName).Scan(
		&cp.Workspace, &cp.RepoID, &cp.RefName, &cp.LastCapturedSHA, &cp.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get checkpoint: %w", err)
	}
	return &cp, nil
}

// SaveCheckpoint 儲存或更新檢查點游標
func (s *SQLiteStore) SaveCheckpoint(ctx context.Context, cp *GitCheckpoint) error {
	if cp.Workspace == "" || cp.RepoID == "" || cp.RefName == "" || cp.LastCapturedSHA == "" {
		return errors.New("checkpoint fields cannot be empty")
	}
	if cp.UpdatedAt.IsZero() {
		cp.UpdatedAt = time.Now().UTC()
	}

	query := `
	INSERT INTO git_checkpoints (workspace, repo_id, ref_name, last_captured_sha, updated_at)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(workspace, repo_id, ref_name) DO UPDATE SET
		last_captured_sha = excluded.last_captured_sha,
		updated_at = excluded.updated_at
	`
	_, err := s.db.ExecContext(ctx, query, cp.Workspace, cp.RepoID, cp.RefName, cp.LastCapturedSHA, cp.UpdatedAt)
	if err != nil {
		return fmt.Errorf("save checkpoint: %w", err)
	}
	return nil
}

// GetProvenance 依正式記憶 ID 取得來源關聯
func (s *SQLiteStore) GetProvenance(ctx context.Context, memoryID string) (*MemoryProvenance, error) {
	query := `
	SELECT memory_id, candidate_id, workspace, repo_id, ref_name,
	       base_sha, head_sha, commit_count, commit_shas_json,
	       facts_summary, is_user_edited, approved_at
	FROM memory_provenance
	WHERE memory_id = ?
	`
	var p MemoryProvenance
	var isUserEdited int
	err := s.db.QueryRowContext(ctx, query, memoryID).Scan(
		&p.MemoryID, &p.CandidateID, &p.Workspace, &p.RepoID, &p.RefName,
		&p.BaseSHA, &p.HeadSHA, &p.CommitCount, &p.CommitSHAsJSON,
		&p.FactsSummary, &isUserEdited, &p.ApprovedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get provenance: %w", err)
	}
	p.IsUserEdited = isUserEdited == 1
	return &p, nil
}

// SaveProvenance 寫入正式記憶來源記錄
func (s *SQLiteStore) SaveProvenance(ctx context.Context, p *MemoryProvenance) error {
	if p.MemoryID == "" || p.CandidateID == "" {
		return errors.New("memory_id and candidate_id cannot be empty")
	}
	if p.ApprovedAt.IsZero() {
		p.ApprovedAt = time.Now().UTC()
	}

	isUserEditedInt := 0
	if p.IsUserEdited {
		isUserEditedInt = 1
	}

	query := `
	INSERT INTO memory_provenance (
		memory_id, candidate_id, workspace, repo_id, ref_name,
		base_sha, head_sha, commit_count, commit_shas_json,
		facts_summary, is_user_edited, approved_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(memory_id) DO UPDATE SET
		facts_summary = excluded.facts_summary,
		is_user_edited = excluded.is_user_edited
	`
	_, err := s.db.ExecContext(ctx, query,
		p.MemoryID, p.CandidateID, p.Workspace, p.RepoID, p.RefName,
		p.BaseSHA, p.HeadSHA, p.CommitCount, p.CommitSHAsJSON,
		p.FactsSummary, isUserEditedInt, p.ApprovedAt,
	)
	if err != nil {
		return fmt.Errorf("save provenance: %w", err)
	}
	return nil
}
