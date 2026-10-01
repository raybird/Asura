package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const UtilityVersion = "utility-v1"

type UtilitySelection struct {
	Reason      string `json:"reason"`
	Provenance  string `json:"provenance"`
	ID          string `json:"id"`
	Score       int    `json:"score"`
	LexicalRank int    `json:"lexical_rank"`
	WeightPPM   int64  `json:"weight_ppm"`
	FinalScore  int64  `json:"final_score_scaled"`
}

type UtilityRecall struct {
	Policy             json.RawMessage    `json:"policy"`
	ContextConfig      json.RawMessage    `json:"context_config"`
	CandidatesRead     int                `json:"candidates_read"`
	CandidatesScored   int                `json:"candidates_scored"`
	CandidateTruncated bool               `json:"candidate_truncated"`
	QueryTruncated     bool               `json:"query_truncated"`
	RecallID           string             `json:"recall_id"`
	Mode               string             `json:"mode"`
	PolicyVersion      string             `json:"policy_version"`
	UtilityPolicy      string             `json:"utility_policy"`
	UtilityVersion     string             `json:"utility_version"`
	AutoIDs            []string           `json:"auto_ids"`
	ExplicitIDs        []string           `json:"explicit_ids"`
	Selected           []UtilitySelection `json:"selected"`
	Budget             json.RawMessage    `json:"budget"`
	Skipped            json.RawMessage    `json:"skipped"`
}

type UtilityAssessment struct {
	MemoryID   string `json:"memory_id"`
	State      string `json:"state"`
	Provenance string `json:"provenance"`
}

type UtilityAggregate struct {
	MemoryID        string `json:"memory_id"`
	Observed        int64  `json:"observed_count"`
	Used            int64  `json:"used_count"`
	Unused          int64  `json:"unused_count"`
	Unknown         int64  `json:"unknown_count"`
	LastObservation string `json:"last_observation,omitempty"`
}

func (s *SQLiteStore) initUtilitySchema() error {
	_, err := s.db.Exec(`
 CREATE TABLE IF NOT EXISTS recall_events (
 recall_id TEXT PRIMARY KEY, task_id TEXT NOT NULL UNIQUE REFERENCES tasks(id),
 request_id TEXT NOT NULL, workspace TEXT NOT NULL, audit TEXT NOT NULL, created_at TEXT NOT NULL,
 utility_status TEXT NOT NULL DEFAULT 'pending', utility_error TEXT NOT NULL DEFAULT '');
 CREATE INDEX IF NOT EXISTS recall_scope_time ON recall_events(workspace,created_at,recall_id);
 CREATE TABLE IF NOT EXISTS recall_selections (
 recall_id TEXT NOT NULL REFERENCES recall_events(recall_id), memory_id TEXT NOT NULL, kind TEXT NOT NULL,
 lexical_score INTEGER NOT NULL, lexical_rank INTEGER NOT NULL, weight_ppm INTEGER NOT NULL,
 final_score_scaled INTEGER NOT NULL, final_rank INTEGER NOT NULL,
 PRIMARY KEY(recall_id,memory_id));
 CREATE INDEX IF NOT EXISTS recall_selection_memory ON recall_selections(memory_id,recall_id);
 CREATE TABLE IF NOT EXISTS utility_assessments (
 recall_id TEXT NOT NULL REFERENCES recall_events(recall_id), workspace TEXT NOT NULL, memory_id TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('used','unused','unknown')),
 provenance TEXT NOT NULL CHECK(provenance IN ('estimated','unknown')), observed_at TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(recall_id,memory_id));
 CREATE INDEX IF NOT EXISTS utility_scope_memory_time ON utility_assessments(workspace,memory_id,observed_at DESC,recall_id DESC);
 CREATE TABLE IF NOT EXISTS utility_aggregates (
 workspace TEXT NOT NULL, memory_id TEXT NOT NULL, observed_count INTEGER NOT NULL,
 used_count INTEGER NOT NULL, unused_count INTEGER NOT NULL, unknown_count INTEGER NOT NULL,
 last_observation TEXT NOT NULL, PRIMARY KEY(workspace,memory_id));
 `)
	return err
}

func (s *SQLiteStore) CreateTask(ctx context.Context, task *Task) error {
	if task == nil {
		return errors.New("task cannot be nil")
	}
	var sealed struct {
		Workspace string         `json:"workspace"`
		Recall    *UtilityRecall `json:"recall"`
	}
	// 2026-10-01：legacy context 不補建歷史事件，維持既有 CreateTask 契約。
	if task.ResolvedContext == "" || json.Unmarshal([]byte(task.ResolvedContext), &sealed) != nil || sealed.Recall == nil || sealed.Recall.RecallID == "" {
		return s.createTask(ctx, task, s.db)
	}
	r := sealed.Recall
	if sealed.Workspace != task.Workspace || r.Mode != "bounded" || len(r.AutoIDs) > 4 || len(r.Selected) != len(r.AutoIDs) || (r.UtilityPolicy != "observe" && r.UtilityPolicy != "apply") || r.UtilityVersion != UtilityVersion {
		return errors.New("invalid sealed utility recall")
	}
	eventAudit := *r
	eventAudit.ExplicitIDs = nil
	audit, err := json.Marshal(eventAudit)
	if err != nil {
		return err
	}
	if len(audit) > 65536 || len(r.RecallID) > 256 {
		return errors.New("utility audit exceeds bounds")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.createTask(ctx, task, tx); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO recall_events(recall_id,task_id,request_id,workspace,audit,created_at) VALUES(?,?,?,?,?,?)`, r.RecallID, task.ID, task.RequestID, task.Workspace, string(audit), task.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, u := range r.Selected {
		if u.ID != r.AutoIDs[i] || len(u.ID) == 0 || len(u.ID) > 256 || seen[u.ID] || u.Score <= 0 || u.WeightPPM < 500000 || u.WeightPPM > 1000000 || u.FinalScore != int64(u.Score)*u.WeightPPM {
			return errors.New("invalid sealed selection")
		}
		seen[u.ID] = true
		if _, err = tx.ExecContext(ctx, `INSERT INTO recall_selections VALUES(?,?,?,?,?,?,?,?)`, r.RecallID, u.ID, "auto", u.Score, u.LexicalRank, u.WeightPPM, u.FinalScore, i+1); err != nil {
			return err
		}
	}
	for _, id := range r.ExplicitIDs {
		if seen[id] {
			return errors.New("duplicate sealed memory")
		}
		seen[id] = true
		if _, err = tx.ExecContext(ctx, `INSERT INTO recall_selections VALUES(?,?,?,?,?,?,?,?)`, r.RecallID, id, "explicit", 0, 0, 1000000, 0, 0); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) PersistUtility(ctx context.Context, workspace, recallID string, input []UtilityAssessment) error {
	if len(input) > 4 {
		return errors.New("too many utility assessments")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var audit string
	if err = tx.QueryRowContext(ctx, `SELECT audit FROM recall_events WHERE workspace=? AND recall_id=? AND length(CAST(audit AS BLOB))<=65536`, workspace, recallID).Scan(&audit); err != nil {
		return errors.New("utility recall unavailable")
	}
	var r UtilityRecall
	if json.Unmarshal([]byte(audit), &r) != nil {
		return errors.New("invalid utility audit")
	}
	if len(r.AutoIDs) > 4 || len(input) != len(r.AutoIDs) {
		return errors.New("utility assessment set mismatch")
	}
	created := time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	allowed := map[string]bool{}
	for _, id := range r.AutoIDs {
		allowed[id] = true
	}
	for _, a := range input {
		if !allowed[a.MemoryID] || (a.State != "used" && a.State != "unused" && a.State != "unknown") || (a.Provenance != "estimated" && a.Provenance != "unknown") || (a.State != "unknown" && a.Provenance != "estimated") {
			return errors.New("invalid utility assessment")
		}
		delete(allowed, a.MemoryID)
		// 2026-10-01：首次 INSERT 的觀測時間不可變，retry 不更新歷史。
		reason := "no_valid_signal"
		if a.Provenance == "estimated" {
			reason = "critic_estimated"
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO utility_assessments(recall_id,workspace,memory_id,state,provenance,observed_at,reason) VALUES(?,?,?,?,?,?,?) ON CONFLICT(recall_id,memory_id) DO NOTHING`, recallID, workspace, a.MemoryID, a.State, a.Provenance, created, reason)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			var state, provenance string
			if err = tx.QueryRowContext(ctx, `SELECT state,provenance FROM utility_assessments WHERE recall_id=? AND memory_id=?`, recallID, a.MemoryID).Scan(&state, &provenance); err != nil {
				return err
			}
			if state != a.State || provenance != a.Provenance {
				return errors.New("utility assessment conflict")
			}
			continue
		}
		if err = incrementUtilityAggregate(ctx, tx, workspace, a.MemoryID, a.State, created); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE recall_events SET utility_status='complete',utility_error='' WHERE recall_id=? AND workspace=?`, recallID, workspace); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) UtilityAggregates(ctx context.Context, workspace string, ids []string) (map[string]UtilityAggregate, error) {
	if len(ids) > 256 {
		return nil, errors.New("utility candidate limit")
	}
	out := map[string]UtilityAggregate{}
	for _, id := range ids {
		a := UtilityAggregate{MemoryID: id}
		err := s.db.QueryRowContext(ctx, `SELECT observed_count,used_count,unused_count,unknown_count,substr(CAST(last_observation AS BLOB),1,65) FROM utility_aggregates WHERE workspace=? AND memory_id=?`, workspace, id).Scan(&a.Observed, &a.Used, &a.Unused, &a.Unknown, &a.LastObservation)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if a.Used < 0 || a.Unused < 0 || a.Unknown < 0 || a.Observed < 0 || a.Used > a.Observed || a.Unused > a.Observed-a.Used || a.Unknown != a.Observed-a.Used-a.Unused || len(a.LastObservation) > 64 {
			return nil, fmt.Errorf("invalid utility aggregate")
		}
		out[id] = a
	}
	return out, nil
}

func (s *SQLiteStore) CompleteTaskUtility(ctx context.Context, taskID string) error {
	var workspace, recallID, audit, status, mode, trace string
	err := s.db.QueryRowContext(ctx, `SELECT e.workspace,e.recall_id,substr(CAST(e.audit AS BLOB),1,65537),t.status,t.execution_mode,coalesce(substr(CAST(t.pipeline_trace AS BLOB),1,262145),'') FROM recall_events e JOIN tasks t ON t.id=e.task_id WHERE t.id=? AND e.utility_status!='complete'`, taskID).Scan(&workspace, &recallID, &audit, &status, &mode, &trace)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != "completed" && status != "failed" && status != "cancelled" {
		return nil
	}
	var r UtilityRecall
	if len(audit) > 65536 || json.Unmarshal([]byte(audit), &r) != nil || len(r.AutoIDs) > 4 {
		return errors.New("invalid sealed utility audit")
	}
	assessments := make([]UtilityAssessment, len(r.AutoIDs))
	allowed := map[string]bool{}
	for i, id := range r.AutoIDs {
		assessments[i] = UtilityAssessment{id, "unknown", "unknown"}
		allowed[id] = true
	}
	var wire struct {
		Review *struct {
			Verdict string              `json:"verdict"`
			Usage   []UtilityAssessment `json:"memory_usage"`
		} `json:"critic_review"`
		Stages []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"stages"`
	}
	if mode == "three-face" && status != "cancelled" && len(trace) <= 262144 && json.Unmarshal([]byte(trace), &wire) == nil && wire.Review != nil {
		completed := false
		for _, stage := range wire.Stages {
			if stage.Name == "critic" && stage.Status == "completed" {
				completed = true
			}
		}
		valid := completed && (wire.Review.Verdict == "pass" || wire.Review.Verdict == "needs_attention" || wire.Review.Verdict == "failed") && len(wire.Review.Usage) <= 4
		seen := map[string]UtilityAssessment{}
		for _, a := range wire.Review.Usage {
			if !allowed[a.MemoryID] || seen[a.MemoryID].MemoryID != "" || (a.State != "used" && a.State != "unused" && a.State != "unknown") || (a.Provenance != "estimated" && a.Provenance != "unknown") || (a.State != "unknown" && a.Provenance != "estimated") {
				valid = false
				break
			}
			seen[a.MemoryID] = a
		}
		if valid {
			for i, a := range assessments {
				if u, ok := seen[a.MemoryID]; ok {
					assessments[i] = u
				}
			}
		}
	}
	if err = s.PersistUtility(ctx, workspace, recallID, assessments); err != nil {
		_, _ = s.db.ExecContext(ctx, `UPDATE recall_events SET utility_status='error',utility_error='utility persistence failed' WHERE recall_id=? AND utility_status!='complete'`, recallID)
	}
	return err
}

func (s *SQLiteStore) RecoverUtility(ctx context.Context) error {
	var high int64
	if err := s.db.QueryRowContext(ctx, `SELECT coalesce(max(rowid),0) FROM recall_events`).Scan(&high); err != nil {
		return err
	}
	var last int64
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT e.rowid,e.task_id FROM recall_events e JOIN tasks t ON t.id=e.task_id WHERE e.rowid>? AND e.rowid<=? AND e.utility_status!='complete' AND t.status IN ('completed','failed','cancelled') ORDER BY e.rowid LIMIT 100`, last, high)
		if err != nil {
			return err
		}
		var batch []struct {
			row  int64
			task string
		}
		for rows.Next() {
			var item struct {
				row  int64
				task string
			}
			if err = rows.Scan(&item.row, &item.task); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, item := range batch {
			if err = s.CompleteTaskUtility(ctx, item.task); err != nil {
				return err
			}
			last = item.row
		}
	}
}

func (s *SQLiteStore) RebuildUtility(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var high int64
	if err = tx.QueryRowContext(ctx, `SELECT coalesce(max(rowid),0) FROM utility_assessments`).Scan(&high); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM utility_aggregates`); err != nil {
		return err
	}
	var last int64
	for {
		rows, err := tx.QueryContext(ctx, `SELECT rowid,workspace,memory_id,state,observed_at FROM utility_assessments WHERE rowid>? AND rowid<=? ORDER BY rowid LIMIT 100`, last, high)
		if err != nil {
			return err
		}
		var batch []struct {
			row                      int64
			workspace, id, state, at string
		}
		for rows.Next() {
			var item struct {
				row                      int64
				workspace, id, state, at string
			}
			if err = rows.Scan(&item.row, &item.workspace, &item.id, &item.state, &item.at); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, a := range batch {
			if err = incrementUtilityAggregate(ctx, tx, a.workspace, a.id, a.state, a.at); err != nil {
				return err
			}
			last = a.row
		}
	}
	return tx.Commit()
}

func incrementUtilityAggregate(ctx context.Context, tx *sql.Tx, workspace, id, state, at string) error {
	used, unused, unknown := 0, 0, 0
	switch state {
	case "used":
		used = 1
	case "unused":
		unused = 1
	case "unknown":
		unknown = 1
	default:
		return errors.New("invalid utility state")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO utility_aggregates VALUES(?,?,1,?,?,?,?) ON CONFLICT(workspace,memory_id) DO UPDATE SET observed_count=observed_count+1,used_count=used_count+excluded.used_count,unused_count=unused_count+excluded.unused_count,unknown_count=unknown_count+excluded.unknown_count,last_observation=max(last_observation,excluded.last_observation)`, workspace, id, used, unused, unknown, at)
	return err
}
