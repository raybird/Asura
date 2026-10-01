package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

var ErrLifecycleConflict = errors.New("memory lifecycle conflict")

type LifecycleMetadata struct {
	Lifecycle          string `json:"lifecycle"`
	IsDurable          bool   `json:"is_durable"`
	SupersededBy       string `json:"superseded_by,omitempty"`
	LifecycleChangedAt string `json:"lifecycle_changed_at"`
}

type CreateMemoryOptions struct {
	Supersedes  string `json:"supersedes,omitempty"`
	Durable     bool   `json:"is_durable,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

func ValidateLifecycleOptions(o CreateMemoryOptions) error {
	if len(o.Supersedes) > 256 || len(o.OperationID) > 128 || len(o.Reason) > 1024 || !utf8.ValidString(o.Supersedes+o.OperationID+o.Reason) || (o.Supersedes != "" && o.OperationID == "") {
		return errors.New("invalid lifecycle options: supersedes requires operation_id; field bounds exceeded")
	}
	return nil
}

func (s *SQLiteStore) initLifecycleSchema() error {
	// 2026-10-01：先取得 SQLite 寫鎖，再探測欄位，讓並行啟動及 crash rollback 安全。
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	rows, err := conn.QueryContext(context.Background(), "PRAGMA table_info(memories)")
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if err = rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range []struct{ name, ddl string }{{"lifecycle", "TEXT NOT NULL DEFAULT 'active' CHECK(lifecycle IN ('active','superseded'))"}, {"is_durable", "INTEGER NOT NULL DEFAULT 0 CHECK(is_durable IN (0,1))"}, {"lifecycle_changed_at", "TEXT NOT NULL DEFAULT ''"}} {
		if !columns[c.name] {
			if _, err = conn.ExecContext(context.Background(), "ALTER TABLE memories ADD COLUMN "+c.name+" "+c.ddl); err != nil {
				return err
			}
		}
	}
	_, err = conn.ExecContext(context.Background(), `CREATE TABLE IF NOT EXISTS memory_relations (
 workspace TEXT NOT NULL, predecessor_id TEXT NOT NULL, successor_id TEXT NOT NULL,
 changed_at TEXT NOT NULL, reason TEXT NOT NULL,
 PRIMARY KEY(workspace,predecessor_id), UNIQUE(workspace,successor_id), CHECK(predecessor_id<>successor_id));
 CREATE TABLE IF NOT EXISTS memory_operations (
 workspace TEXT NOT NULL, operation_id TEXT NOT NULL, fingerprint TEXT NOT NULL, memory_id TEXT NOT NULL,
 PRIMARY KEY(workspace,operation_id));
 CREATE INDEX IF NOT EXISTS idx_memory_active ON memories(workspace,lifecycle,id);
 UPDATE memories SET lifecycle_changed_at=CAST(created_at AS TEXT) WHERE lifecycle_changed_at='';
 INSERT INTO schema_migrations(name,applied_at) VALUES ('memory-lifecycle-v1',CURRENT_TIMESTAMP) ON CONFLICT(name) DO NOTHING;`)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(context.Background(), "COMMIT")
	return err
}

func memoryCreationFingerprint(m Memory, o CreateMemoryOptions) string {
	// 2026-10-01：server 產生的 ID/時間不屬於重試 payload。
	b, _ := json.Marshal(struct {
		Content, Source string
		Options         CreateMemoryOptions
	}{m.Content, m.Source, o})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func createLifecycleMemoryTx(ctx context.Context, tx *sql.Tx, m Memory, o CreateMemoryOptions) (string, error) {
	if err := ValidateLifecycleOptions(o); err != nil {
		return "", err
	}
	if m.ID == "" || m.Workspace == "" {
		return "", errors.New("memory id and workspace required")
	}
	if m.Source == "" {
		m.Source = "manual"
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	fingerprint := memoryCreationFingerprint(m, o)
	if o.OperationID != "" {
		var existing, prior string
		err := tx.QueryRowContext(ctx, "SELECT memory_id,fingerprint FROM memory_operations WHERE workspace=? AND operation_id=?", m.Workspace, o.OperationID).Scan(&existing, &prior)
		if err == nil {
			if prior != fingerprint {
				return "", ErrLifecycleConflict
			}
			var exists int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM memories WHERE workspace=? AND id=?", m.Workspace, existing).Scan(&exists); err != nil {
				return "", err
			}
			if exists == 0 {
				return "", ErrMemoryNotFound
			}
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	if o.Supersedes != "" {
		if o.Supersedes == m.ID {
			return "", ErrLifecycleConflict
		}
		var state string
		err := tx.QueryRowContext(ctx, "SELECT lifecycle FROM memories WHERE workspace=? AND id=?", m.Workspace, o.Supersedes).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrMemoryNotFound
		}
		if err != nil {
			return "", err
		}
		if state != "active" {
			return "", ErrLifecycleConflict
		}
	}
	// 2026-10-01：relation/operation ID 永不重用，避免 hard delete 後復活。
	var retired int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_relations WHERE workspace=? AND (predecessor_id=? OR successor_id=?) UNION ALL SELECT 1 FROM memory_operations WHERE workspace=? AND memory_id=?)`, m.Workspace, m.ID, m.ID, m.Workspace, m.ID).Scan(&retired); err != nil {
		return "", err
	}
	if retired != 0 {
		return "", ErrLifecycleConflict
	}
	stamp := m.CreatedAt.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO memories(id,workspace,content,source,created_at,lifecycle,is_durable,lifecycle_changed_at) VALUES(?,?,?,?,?,'active',?,?)`, m.ID, m.Workspace, m.Content, m.Source, m.CreatedAt, o.Durable, stamp); err != nil {
		return "", err
	}
	if o.Supersedes != "" {
		reason := o.Reason
		if reason == "" {
			reason = "explicit-supersession"
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO memory_relations(workspace,predecessor_id,successor_id,changed_at,reason) VALUES(?,?,?,?,?)", m.Workspace, o.Supersedes, m.ID, stamp, reason); err != nil {
			return "", err
		}
		result, err := tx.ExecContext(ctx, "UPDATE memories SET lifecycle='superseded',lifecycle_changed_at=? WHERE workspace=? AND id=? AND lifecycle='active'", stamp, m.Workspace, o.Supersedes)
		if err != nil {
			return "", err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return "", err
		}
		if n != 1 {
			return "", ErrLifecycleConflict
		}
	}
	if o.OperationID != "" {
		if _, err := tx.ExecContext(ctx, "INSERT INTO memory_operations(workspace,operation_id,fingerprint,memory_id) VALUES(?,?,?,?)", m.Workspace, o.OperationID, fingerprint, m.ID); err != nil {
			return "", err
		}
	}
	return m.ID, nil
}

func (s *SQLiteStore) CreateMemoryWithOptions(ctx context.Context, m Memory, o CreateMemoryOptions) (*Memory, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	id, err := createLifecycleMemoryTx(ctx, tx, m, o)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetMemory(ctx, m.Workspace, id)
}

func (s *SQLiteStore) loadLifecycle(ctx context.Context, m *Memory) error {
	var durable int
	err := s.db.QueryRowContext(ctx, `SELECT lifecycle,is_durable,substr(CAST(lifecycle_changed_at AS BLOB),1,65),coalesce((SELECT substr(CAST(successor_id AS BLOB),1,257) FROM memory_relations WHERE workspace=memories.workspace AND predecessor_id=memories.id),'') FROM memories WHERE workspace=? AND id=?`, m.Workspace, m.ID).Scan(&m.Lifecycle, &durable, &m.LifecycleChangedAt, &m.SupersededBy)
	if err != nil {
		return err
	}
	m.IsDurable = durable == 1
	if (m.Lifecycle != "active" && m.Lifecycle != "superseded") || durable < 0 || durable > 1 || len(m.LifecycleChangedAt) > 64 || len(m.SupersededBy) > 256 || !utf8.ValidString(m.SupersededBy) || (m.Lifecycle == "superseded" && m.SupersededBy == "") || (m.Lifecycle == "active" && m.SupersededBy != "") {
		return ErrMemoryMetadata
	}
	return nil
}

func (s *SQLiteStore) MarkMemoryDurable(ctx context.Context, ws, id string) (*Memory, error) {
	current, err := s.GetMemoryBounded(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}
	if len(b) > 131000 {
		return nil, ErrMemoryTooLarge
	}
	result, err := s.db.ExecContext(ctx, `UPDATE memories SET lifecycle_changed_at=CASE WHEN is_durable=0 THEN ? ELSE lifecycle_changed_at END,is_durable=1 WHERE workspace=? AND id=?`, time.Now().UTC().Format(time.RFC3339Nano), ws, id)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrMemoryNotFound
	}
	return s.GetMemoryBounded(ctx, ws, id)
}

type MemoryDetail struct {
	Memory            *Memory          `json:"memory,omitempty"`
	ID                string           `json:"id"`
	Deleted           bool             `json:"deleted"`
	Predecessor       string           `json:"predecessor,omitempty"`
	Successor         string           `json:"successor,omitempty"`
	RelationReason    string           `json:"relation_reason,omitempty"`
	RelationChangedAt string           `json:"relation_changed_at,omitempty"`
	Provenance        json.RawMessage  `json:"provenance,omitempty"`
	Utility           UtilityAggregate `json:"utility"`
	WeightPPM         int64            `json:"weight_ppm"`
}

func (s *SQLiteStore) GetMemoryDetail(ctx context.Context, ws, id string) (MemoryDetail, error) {
	detail := MemoryDetail{ID: id}
	if id == "" || len(id) > 256 || !utf8.ValidString(id) {
		return detail, ErrMemoryMetadata
	}
	m, err := s.GetMemoryBounded(ctx, ws, id)
	if err != nil && !errors.Is(err, ErrMemoryNotFound) {
		return detail, err
	}
	detail.Memory = m
	detail.Deleted = m == nil
	var pred, succ, reason, stamp string
	err = s.db.QueryRowContext(ctx, `SELECT coalesce((SELECT substr(CAST(predecessor_id AS BLOB),1,257) FROM memory_relations WHERE workspace=? AND successor_id=?),''),coalesce((SELECT substr(CAST(successor_id AS BLOB),1,257) FROM memory_relations WHERE workspace=? AND predecessor_id=?),''),coalesce((SELECT substr(CAST(reason AS BLOB),1,1025) FROM memory_relations WHERE workspace=? AND (predecessor_id=? OR successor_id=?) ORDER BY predecessor_id=? DESC LIMIT 1),''),coalesce((SELECT substr(CAST(changed_at AS BLOB),1,65) FROM memory_relations WHERE workspace=? AND (predecessor_id=? OR successor_id=?) ORDER BY predecessor_id=? DESC LIMIT 1),'')`, ws, id, ws, id, ws, id, id, id, ws, id, id, id).Scan(&pred, &succ, &reason, &stamp)
	if err != nil {
		return detail, err
	}
	if m == nil && pred == "" && succ == "" {
		return detail, ErrMemoryNotFound
	}
	if len(pred) > 256 || len(succ) > 256 || len(reason) > 1024 || len(stamp) > 64 || !utf8.ValidString(pred+succ+reason+stamp) {
		return detail, ErrMemoryMetadata
	}
	detail.Predecessor = pred
	detail.Successor = succ
	detail.RelationReason = reason
	detail.RelationChangedAt = stamp
	// 2026-10-01：provenance 只輸出有界 identity，避免 facts_summary/commit 列表無界展開。
	var candidate, repo, ref, base, head, approved string
	var edited int
	err = s.db.QueryRowContext(ctx, `SELECT substr(CAST(candidate_id AS BLOB),1,257),substr(CAST(repo_id AS BLOB),1,4097),substr(CAST(ref_name AS BLOB),1,4097),substr(CAST(base_sha AS BLOB),1,257),substr(CAST(head_sha AS BLOB),1,257),substr(CAST(approved_at AS BLOB),1,65),is_user_edited FROM memory_provenance WHERE workspace=? AND memory_id=?`, ws, id).Scan(&candidate, &repo, &ref, &base, &head, &approved, &edited)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return detail, err
	}
	if err == nil {
		if len(candidate) > 256 || len(repo) > 4096 || len(ref) > 4096 || len(base) > 256 || len(head) > 256 || len(approved) > 64 || !utf8.ValidString(candidate+repo+ref+base+head+approved) || (edited != 0 && edited != 1) {
			return detail, ErrMemoryMetadata
		}
		detail.Provenance, _ = json.Marshal(struct {
			Candidate string `json:"candidate_id"`
			Repo      string `json:"repo_id"`
			Ref       string `json:"ref_name"`
			Base      string `json:"base_sha"`
			Head      string `json:"head_sha"`
			Approved  string `json:"approved_at"`
			Edited    bool   `json:"is_user_edited"`
		}{candidate, repo, ref, base, head, approved, edited == 1})
	}

	aggregates, err := s.UtilityAggregates(ctx, ws, []string{id})
	if err != nil {
		return detail, err
	}
	detail.Utility = UtilityAggregate{MemoryID: id}
	if a, ok := aggregates[id]; ok {
		detail.Utility = a
	}
	detail.WeightPPM, err = UtilityWeightPPM(detail.Utility)
	if err != nil {
		return detail, err
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return detail, err
	}
	if len(b) > 131000 {
		return detail, ErrMemoryTooLarge
	}
	return detail, nil
}
