package storage

import (
	"asura/internal/audit"
	"asura/internal/backend"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"time"
)

func (s *SQLiteStore) initExecutionAuditSchema() error {
	cols, err := s.getTableColumns("tasks")
	if err != nil {
		return err
	}
	if !cols["execution_audit_version"] {
		if _, err = s.db.Exec(`ALTER TABLE tasks ADD COLUMN execution_audit_version TEXT NOT NULL DEFAULT ''`); err != nil {
			cols, checkErr := s.getTableColumns("tasks")
			if checkErr != nil || !cols["execution_audit_version"] {
				return err
			}
		}
	}
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS task_execution_audits(
 task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
 header TEXT NOT NULL CHECK(length(CAST(header AS BLOB))<=65536),digest TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS task_execution_audit_events(
 task_id TEXT NOT NULL REFERENCES task_execution_audits(task_id) ON DELETE CASCADE,
 seq INTEGER NOT NULL CHECK(seq BETWEEN 1 AND 32),event_key TEXT NOT NULL,
 event TEXT NOT NULL CHECK(length(CAST(event AS BLOB))<=2048),
 PRIMARY KEY(task_id,seq),UNIQUE(task_id,event_key));`)
	return err
}
func contractInContext(raw string) *backend.ExecutionContract {
	var c struct {
		Contract *backend.ExecutionContract `json:"execution_contract"`
	}
	if json.Unmarshal([]byte(raw), &c) != nil {
		return nil
	}
	return c.Contract
}
func (s *SQLiteStore) createExecutionAuditTx(ctx context.Context, tx *sql.Tx, task *Task) error {
	c := contractInContext(task.ResolvedContext)
	if c == nil {
		return nil
	}
	if c.ContractVersion != backend.CapabilityContractVersion && c.ContractVersion != backend.StageBindingContractVersion && c.ContractVersion != backend.InventoryContractVersion {
		return nil
	} // 2026-10-04：未知歷史契約不補造 audit。

	at := task.CreatedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	h, err := audit.NewHeader(task.ID, task.RequestID, at, c)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(h)
	_, err = tx.ExecContext(ctx, `INSERT INTO task_execution_audits(task_id,header,digest) VALUES(?,?,?)`, task.ID, string(raw), audit.Digest(h))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE tasks SET execution_audit_version=? WHERE id=?`, audit.Version, task.ID)
	return err
}
func decodeAudit(raw string, value any) error {
	dec := json.NewDecoder(bytes.NewBufferString(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(value) != nil {
		return audit.ErrInvalid
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return audit.ErrInvalid
	}
	return nil
}

// 2026-10-04：單一讀交易保證 Task 與 event prefix 一致；不回傳完整 Task。
func (s *SQLiteStore) GetTaskAudit(ctx context.Context, id string) (*audit.Record, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, audit.ErrPersistence
	}
	defer tx.Rollback()
	record, err := s.getTaskAuditTx(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	if tx.Commit() != nil {
		return nil, audit.ErrPersistence
	}
	return record, nil
}
func (s *SQLiteStore) getTaskAuditTx(ctx context.Context, tx *sql.Tx, id string, checkSource bool) (*audit.Record, error) {
	var state, mode, backendID, request, rawContract, auditVersion string
	var created time.Time
	err := tx.QueryRowContext(ctx, `SELECT status,execution_mode,backend,request_id,created_at,execution_audit_version,
 CASE WHEN json_valid(resolved_context) THEN CASE WHEN length(CAST(json_extract(resolved_context,'$.execution_contract') AS BLOB))<=262144 THEN coalesce(json_extract(resolved_context,'$.execution_contract'),'') ELSE 'oversized' END ELSE '' END FROM tasks WHERE id=?`, id).Scan(&state, &mode, &backendID, &request, &created, &auditVersion, &rawContract)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, audit.ErrPersistence
	}
	var c *backend.ExecutionContract
	if rawContract != "" {
		if json.Unmarshal([]byte(rawContract), &c) != nil {
			return nil, audit.ErrInvalid
		}
	}
	var raw, rootDigest string
	err = tx.QueryRowContext(ctx, `SELECT header,digest FROM task_execution_audits WHERE task_id=? AND length(CAST(header AS BLOB))<=65536`, id).Scan(&raw, &rootDigest)
	if errors.Is(err, sql.ErrNoRows) {
		if auditVersion != "" {
			return nil, audit.ErrInvalid
		}
		var exists int
		if tx.QueryRowContext(ctx, `SELECT count(*) FROM task_execution_audits WHERE task_id=?`, id).Scan(&exists) != nil || exists != 0 {
			return nil, audit.ErrInvalid
		}
		r := &audit.Record{TaskState: state, Completeness: "legacy", Integrity: "unavailable", Events: []audit.Event{}, Stages: []audit.Stage{}}
		if c != nil {
			h, e := audit.NewHeader(id, request, created, c)
			if e != nil {
				return nil, e
			}
			if c.Mode != mode || c.Runtime.ID != backendID {
				return nil, audit.ErrInvalid
			}
			prefix, e := audit.Fold(h, []audit.Event{}, "pending")
			if e != nil {
				return nil, e
			}
			r.Stages = prefix.Stages
			r.Header = h
			r.Integrity = "valid"
		} else {
			r.Header = audit.Header{Schema: audit.Version, TaskID: id, Mode: mode, CreatedAt: created}
		}
		raw, _ := json.Marshal(r)
		if len(raw) > audit.MaxRecordBytes {
			return nil, audit.ErrInvalid
		}
		if r.Validate() != nil {
			return nil, audit.ErrInvalid
		}
		return r, nil
	}
	if err != nil {
		return nil, audit.ErrPersistence
	}
	if auditVersion != audit.Version {
		return nil, audit.ErrInvalid
	}
	var h audit.Header
	if decodeAudit(raw, &h) != nil || audit.Digest(h) != rootDigest || h.TaskID != id || checkSource && (h.Mode != mode || c == nil || c.Runtime.ID != backendID || audit.Digest(c) != h.ContractDigest) {
		return nil, audit.ErrInvalid
	}
	if checkSource {
		expected, e := audit.NewHeader(id, request, h.CreatedAt, c)
		if e != nil || audit.Digest(expected) != audit.Digest(h) {
			return nil, audit.ErrInvalid
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT seq,event_key,event FROM task_execution_audit_events WHERE task_id=? ORDER BY seq LIMIT 33`, id)
	if err != nil {
		return nil, audit.ErrPersistence
	}
	events := []audit.Event{}
	for rows.Next() {
		var seq int
		var key, raw string
		if rows.Scan(&seq, &key, &raw) != nil || len(raw) > audit.MaxEventBytes {
			rows.Close()
			return nil, audit.ErrInvalid
		}
		var event audit.Event
		if decodeAudit(raw, &event) != nil || seq != event.Seq || key != event.Key {
			rows.Close()
			return nil, audit.ErrInvalid
		}
		events = append(events, event)
	}
	if rows.Err() != nil {
		rows.Close()
		return nil, audit.ErrPersistence
	}
	rows.Close()
	r, e := audit.Fold(h, events, state)
	if e != nil {
		return nil, e
	}
	return &r, nil
}
func (s *SQLiteStore) AppendTaskAudit(ctx context.Context, id string, event audit.Event) error {
	if event.Kind == "terminal" || event.Kind == "recovery" || event.Kind == "task_started" || event.Kind == "cancel_requested" {
		return audit.ErrInvalid
	}
	tx, err := s.beginAuditWrite(ctx)
	if err != nil {
		return audit.ErrPersistence
	}
	defer tx.Rollback()
	if err = s.appendTaskAuditTx(ctx, tx, id, event); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return audit.ErrPersistence
	}
	return nil
}
func (s *SQLiteStore) appendTaskAuditTx(ctx context.Context, tx *sql.Tx, id string, event audit.Event) error {
	var header, root string
	err := tx.QueryRowContext(ctx, `SELECT header,digest FROM task_execution_audits WHERE task_id=?`, id).Scan(&header, &root)
	if errors.Is(err, sql.ErrNoRows) {
		var version string
		if err = tx.QueryRowContext(ctx, `SELECT execution_audit_version FROM tasks WHERE id=?`, id).Scan(&version); err != nil {
			return audit.ErrPersistence
		}
		if version != "" {
			return audit.ErrInvalid
		}
		return nil
	}
	if err != nil {
		return audit.ErrPersistence
	}
	event.Key = audit.EventKey(event)
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT event FROM task_execution_audit_events WHERE task_id=? AND event_key=?`, id, event.Key).Scan(&existing)
	if err == nil {
		var old audit.Event
		if decodeAudit(existing, &old) != nil || old.Validate() != nil {
			return audit.ErrInvalid
		}
		compare := event
		compare.Seq = old.Seq
		compare.At = old.At
		compare.PreviousDigest = old.PreviousDigest
		compare.Digest = old.Digest
		if !reflect.DeepEqual(old, compare) {
			return audit.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return audit.ErrPersistence
	}
	var n int
	if tx.QueryRowContext(ctx, `SELECT count(*) FROM task_execution_audit_events WHERE task_id=?`, id).Scan(&n) != nil {
		return audit.ErrPersistence
	}
	// 2026-10-04：非 terminal/recovery 留兩個槽位，重複 observation 不消耗槽位。
	if n >= audit.MaxEvents || n >= audit.MaxEvents-2 && event.Kind != "terminal" && event.Kind != "recovery" {
		return audit.ErrInvalid
	}
	event.Seq = n + 1
	event.PreviousDigest = root
	if n > 0 {
		var last string
		if tx.QueryRowContext(ctx, `SELECT event FROM task_execution_audit_events WHERE task_id=? AND seq=?`, id, n).Scan(&last) != nil {
			return audit.ErrInvalid
		}
		var prior audit.Event
		if decodeAudit(last, &prior) != nil {
			return audit.ErrInvalid
		}
		event.PreviousDigest = prior.Digest
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	} else {
		event.At = event.At.UTC()
	}
	event.Digest = ""
	event.Digest = audit.Digest(event)
	if event.Validate() != nil {
		return audit.ErrInvalid
	}
	raw, _ := json.Marshal(event)
	if _, err = tx.ExecContext(ctx, `INSERT INTO task_execution_audit_events(task_id,seq,event_key,event) VALUES(?,?,?,?)`, id, event.Seq, event.Key, string(raw)); err != nil {
		return audit.ErrPersistence
	}
	if event.Kind == "recovery" {
		return nil
	} // 2026-10-04：同交易的 terminal append 再驗完整 recovery prefix。
	if _, err = s.getTaskAuditTx(ctx, tx, id, event.Kind != "recovery" && !(event.Kind == "terminal" && event.State == "failed")); err != nil {
		return err
	}
	return nil
}

// 2026-10-04：SQLite CAS 與 terminal event 共用唯一交易與勝者。
func (s *SQLiteStore) updateTaskStateAudit(ctx context.Context, id string, status TaskStatus, query string, args ...any) (sql.Result, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n > 0 {
		var event audit.Event
		switch status {
		case StatusRunning:
			event = audit.Event{Kind: "task_started", State: "running", Reason: "none"}
		case StatusCancelling:
			event = audit.Event{Kind: "cancel_requested", State: "cancelling", Reason: "cancelled", Layer: "cancellation"}
		case StatusCompleted, StatusFailed, StatusCancelled:
			event = audit.TerminalEvent(ctx, string(status))
		}
		if event.Kind != "" {
			if err = s.appendTaskAuditTx(ctx, tx, id, event); err != nil {
				return nil, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

func (s *SQLiteStore) recoverInterruptedTasksAudit(ctx context.Context) (int64, error) {
	tx, err := s.beginAuditWrite(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM tasks WHERE status IN ('pending','queued','running','cancelling')`)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return 0, audit.ErrPersistence
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		rows.Close()
		return 0, audit.ErrPersistence
	}
	rows.Close()
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `UPDATE tasks SET status='failed',error_message=?,updated_at=? WHERE id=?`, "task interrupted: system restarted or terminated unexpectedly", time.Now().UTC(), id); err != nil {
			return 0, err
		}
		if err = s.appendTaskAuditTx(ctx, tx, id, audit.Event{Kind: "recovery", State: "failed", Reason: "interrupted", Layer: "recovery"}); err != nil {
			return 0, err
		}
		if err = s.appendTaskAuditTx(ctx, tx, id, audit.Event{Kind: "terminal", State: "failed", Reason: "interrupted", Layer: "recovery"}); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(ids)), nil
}

func (s *SQLiteStore) TaskAuditWorkspace(ctx context.Context, id string) (string, error) {
	var workspace string
	err := s.db.QueryRowContext(ctx, `SELECT workspace FROM tasks WHERE id=?`, id).Scan(&workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrTaskNotFound
	}
	return workspace, err
}

// 2026-10-04：先取得 WAL writer lock，避免 deferred read snapshot 升級時 SQLITE_BUSY。
// WHERE 0 不改任何 header bytes；CAS/CreateTask 已先寫入，不需此步驟。
func (s *SQLiteStore) beginAuditWrite(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE task_execution_audits SET digest=digest WHERE 0`); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}
