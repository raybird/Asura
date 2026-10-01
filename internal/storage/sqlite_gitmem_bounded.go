package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const GitDraftMaxBytes = 32 << 10
const GitFactsMaxBytes = 256 << 10

var (
	ErrGitRequest  = errors.New("invalid git memory request")
	ErrGitTooLarge = errors.New("git memory data too large")
	ErrGitMetadata = errors.New("git memory data unsupported")
)

type candidateCursor struct {
	Version   int    `json:"v"`
	Workspace string `json:"w"`
	Status    string `json:"s"`
	CreatedAt string `json:"t"`
	ID        string `json:"i"`
}

func ValidCandidateStatus(status string) bool {
	return status == "pending" || status == "approved" || status == "discarded"
}

func boundedGitText(s string, limit int) error {
	if len(s) > limit {
		return ErrGitTooLarge
	}
	if !utf8.ValidString(s) {
		return ErrGitMetadata
	}
	return nil
}

// 2026-09-30 WEBGIT-004：僅讀 limit+1 個有界投影，cursor 綁定 exact scope 與 status。
func (s *SQLiteStore) PageGitCandidates(ctx context.Context, workspace, status string, limit int, raw string) ([]CandidateSummary, string, error) {
	if !ValidCandidateStatus(status) || limit < 1 || limit > 50 || len(raw) > 1024 {
		return nil, "", ErrGitRequest
	}
	query := `SELECT COALESCE(substr(CAST(id AS BLOB),1,257),''),COALESCE(substr(CAST(draft_content AS BLOB),1,513),''),COALESCE(substr(CAST(created_at AS BLOB),1,65),''),is_truncated,COALESCE(substr(CAST(approved_memory_id AS BLOB),1,257),'')
 FROM memory_candidates WHERE workspace=? AND status=?`
	args := []any{workspace, status}
	if raw != "" {
		data, err := base64.RawURLEncoding.DecodeString(raw)
		var c candidateCursor
		if err != nil || len(data) > 768 || json.Unmarshal(data, &c) != nil || c.Version != 1 || c.Workspace != memoryWorkspaceHash(workspace) || c.Status != status || c.ID == "" || len(c.ID) > 256 || !utf8.ValidString(c.ID) || len(c.CreatedAt) > 64 || c.CreatedAt == "" || !utf8.ValidString(c.CreatedAt) {
			return nil, "", ErrGitRequest
		}
		if _, err := parseGitCreatedAt(c.CreatedAt); err != nil {
			return nil, "", ErrGitRequest
		}
		query += ` AND (created_at < ? OR (created_at = ? AND id > ?))`
		args = append(args, c.CreatedAt, c.CreatedAt, c.ID)
	}
	query += ` ORDER BY created_at DESC,id ASC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]CandidateSummary, 0, limit)
	lastTime := ""
	more := false
	for rows.Next() {
		var id, draft, created, memID string
		var partial int
		if err := rows.Scan(&id, &draft, &created, &partial, &memID); err != nil {
			return nil, "", err
		}
		if len(items) == limit {
			more = true
			break
		}
		for _, text := range []struct {
			s     string
			limit int
		}{{id, 256}, {created, 64}, {memID, 256}} {
			if err := boundedGitText(text.s, text.limit); err != nil {
				return nil, "", err
			}
		}
		if id == "" {
			return nil, "", ErrGitMetadata
		}
		at, err := parseGitCreatedAt(created)
		if err != nil {
			return nil, "", err
		}
		if len(draft) > 512 {
			draft = TruncateUTF8Safe(draft, 509) + "..."
		}
		if !utf8.ValidString(draft) {
			return nil, "", ErrGitMetadata
		}
		items = append(items, CandidateSummary{ID: id, Summary: draft, Status: status, CreatedAt: at, IsTruncated: partial != 0, ApprovedMemoryID: memID})
		lastTime = created
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if more {
		data, err := json.Marshal(candidateCursor{Version: 1, Workspace: memoryWorkspaceHash(workspace), Status: status, CreatedAt: lastTime, ID: items[len(items)-1].ID})
		if err != nil {
			return nil, "", err
		}
		next = base64.RawURLEncoding.EncodeToString(data)
		if len(next) > 1024 {
			return nil, "", ErrGitRequest
		}
	}
	return items, next, nil
}

func parseGitCreatedAt(raw string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05.999999999-07:00", time.RFC3339Nano, "2006-01-02 15:04:05.999999999"} {
		if at, err := time.Parse(layout, raw); err == nil {
			return at, nil
		}
	}
	return time.Time{}, ErrGitMetadata
}

const candidateIdentityColumns = `COALESCE(substr(CAST(id AS BLOB),1,257),''),COALESCE(substr(CAST(workspace AS BLOB),1,4097),''),COALESCE(substr(CAST(repo_id AS BLOB),1,4097),''),COALESCE(substr(CAST(ref_name AS BLOB),1,4097),''),COALESCE(substr(CAST(base_sha AS BLOB),1,4097),''),COALESCE(substr(CAST(head_sha AS BLOB),1,4097),'')`
const candidatePayloadColumns = `COALESCE(substr(CAST(facts_summary AS BLOB),1,262145),''),COALESCE(substr(CAST(draft_content AS BLOB),1,32769),'')`
const candidateReviewColumns = `COALESCE(substr(CAST(status AS BLOB),1,4097),''),COALESCE(substr(CAST(policy_version AS BLOB),1,4097),''),COALESCE(substr(CAST(approved_memory_id AS BLOB),1,257),''),COALESCE(substr(CAST(created_at AS BLOB),1,65),''),substr(CAST(approved_at AS BLOB),1,65),is_truncated,COALESCE(substr(CAST(truncated_reason AS BLOB),1,4097),''),is_user_edited`
const boundedCandidateColumns = candidateIdentityColumns + ",commit_count," + candidatePayloadColumns + "," + candidateReviewColumns
const candidateHeaderColumns = candidateIdentityColumns + ",commit_count,'{}',''," + candidateReviewColumns

func validateGitCandidate(c *MemoryCandidate) error {
	for _, text := range []struct {
		s     string
		limit int
	}{{c.ID, 256}, {c.Workspace, 4096}, {c.RepoID, 4096}, {c.RefName, 4096}, {c.BaseSHA, 4096}, {c.HeadSHA, 4096}, {c.PolicyVersion, 4096}, {c.Status, 4096}, {c.ApprovedMemoryID, 256}, {c.TruncatedReason, 4096}, {c.DraftContent, GitDraftMaxBytes}, {c.FactsSummary, GitFactsMaxBytes}} {
		if err := boundedGitText(text.s, text.limit); err != nil {
			return err
		}
	}
	if c.ID == "" || !ValidCandidateStatus(c.Status) || !json.Valid([]byte(c.FactsSummary)) {
		return ErrGitMetadata
	}
	return nil
}

// 2026-09-30 WEBGIT-004：SQL 投影上限加一，超限拒絕，不把截斷草稿當完整 detail。
func (s *SQLiteStore) GetGitCandidateBounded(ctx context.Context, workspace, id string) (*MemoryCandidate, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+boundedCandidateColumns+` FROM memory_candidates WHERE workspace=? AND id=?`, workspace, id)
	return scanGitCandidate(row)
}

func scanGitCandidate(row *sql.Row) (*MemoryCandidate, error) {
	var c MemoryCandidate
	var approved sql.NullString
	var created string
	var partial, edited int
	err := row.Scan(&c.ID, &c.Workspace, &c.RepoID, &c.RefName, &c.BaseSHA, &c.HeadSHA, &c.CommitCount, &c.FactsSummary, &c.DraftContent, &c.Status, &c.PolicyVersion, &c.ApprovedMemoryID, &created, &approved, &partial, &c.TruncatedReason, &edited)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCandidateNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := boundedGitText(created, 64); err != nil {
		return nil, err
	}
	c.CreatedAt, err = parseGitCreatedAt(created)
	if err != nil {
		return nil, err
	}
	c.IsTruncated = partial != 0
	c.IsUserEdited = edited != 0
	if approved.Valid {
		if err := boundedGitText(approved.String, 64); err != nil {
			return nil, err
		}
		at, err := parseGitCreatedAt(approved.String)
		if err != nil {
			return nil, err
		}
		c.ApprovedAt = &at
	}
	if err := validateGitCandidate(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

func ValidateGitApprovalContent(content string) error {
	if err := boundedGitText(content, GitDraftMaxBytes); err != nil {
		return err
	}
	if strings.TrimSpace(content) == "" {
		return ErrGitRequest
	}
	return nil
}

// 2026-09-30：Capture 僅需要狀態 metadata，legacy 草稿/facts 不載入、不回傳。
func (s *SQLiteStore) GetGitCandidateHeader(ctx context.Context, workspace, id string) (*MemoryCandidate, error) {
	return scanGitCandidate(s.db.QueryRowContext(ctx, `SELECT `+candidateHeaderColumns+` FROM memory_candidates WHERE workspace=? AND id=?`, workspace, id))
}

func (s *SQLiteStore) FindGitCandidateHeader(ctx context.Context, workspace, repoID, refName, baseSHA, headSHA, policyVersion string) (*MemoryCandidate, error) {
	c, err := scanGitCandidate(s.db.QueryRowContext(ctx, `SELECT `+candidateHeaderColumns+` FROM memory_candidates WHERE workspace=? AND repo_id=? AND ref_name=? AND base_sha=? AND head_sha=? AND policy_version=?`, workspace, repoID, refName, baseSHA, headSHA, policyVersion))
	if errors.Is(err, ErrCandidateNotFound) {
		return nil, nil
	}
	return c, err
}
