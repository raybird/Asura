package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"time"
	"unicode/utf8"
)

type UtilityObservation struct {
	Reason     string `json:"reason"`
	RecallID   string `json:"recall_id"`
	State      string `json:"state"`
	Provenance string `json:"provenance"`
	ObservedAt string `json:"observed_at"`
}

type UtilityPage struct {
	Aggregate  UtilityAggregate     `json:"aggregate"`
	Items      []UtilityObservation `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
	WeightPPM  int64                `json:"weight_ppm"`
}

type utilityCursor struct {
	Scope string `json:"s"`
	Time  string `json:"t"`
	ID    string `json:"i"`
}

func (s *SQLiteStore) GetMemoryUtility(ctx context.Context, workspace, id string, limit int, rawCursor string) (UtilityPage, error) {
	var page UtilityPage
	if limit < 1 || limit > 100 || id == "" || len(id) > 256 || !utf8.ValidString(id) || len(rawCursor) > 512 {
		return page, errors.New("invalid utility query")
	}
	scope := memoryWorkspaceHash(workspace + "\x00" + id)
	var cursor utilityCursor
	if rawCursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(rawCursor)
		if err != nil || json.Unmarshal(b, &cursor) != nil || cursor.Scope != scope || cursor.ID == "" || len(cursor.ID) > 256 || len(cursor.Time) > 64 || !utf8.ValidString(cursor.ID) {
			return page, errors.New("invalid utility cursor")
		}
		if _, err = time.Parse(time.RFC3339Nano, cursor.Time); err != nil {
			return page, errors.New("invalid utility cursor")
		}
	}
	// 2026-10-01：fresh memory 可查 neutral；deleted memory 可查同 scope ledger，跨 scope 一律相同錯誤。
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memories WHERE workspace=? AND id=? UNION ALL SELECT 1 FROM recall_selections s JOIN recall_events e ON e.recall_id=s.recall_id WHERE e.workspace=? AND s.memory_id=? LIMIT 1)`, workspace, id, workspace, id).Scan(&exists); err != nil {
		return page, err
	}
	if exists == 0 {
		return page, ErrMemoryNotFound
	}
	aggregates, err := s.UtilityAggregates(ctx, workspace, []string{id})
	if err != nil {
		return page, err
	}
	page.Aggregate = UtilityAggregate{MemoryID: id}
	if a, ok := aggregates[id]; ok {
		page.Aggregate = a
	}
	page.WeightPPM, err = UtilityWeightPPM(page.Aggregate)
	if err != nil {
		return page, err
	}
	query := `SELECT substr(CAST(recall_id AS BLOB),1,257),substr(CAST(state AS BLOB),1,8),substr(CAST(provenance AS BLOB),1,10),substr(CAST(observed_at AS BLOB),1,65),coalesce(substr(CAST(reason AS BLOB),1,65),'') FROM utility_assessments WHERE workspace=? AND memory_id=?`
	args := []any{workspace, id}
	if rawCursor != "" {
		query += ` AND (observed_at,recall_id)<(?,?)`
		args = append(args, cursor.Time, cursor.ID)
	}
	query += ` ORDER BY observed_at DESC,recall_id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	page.Items = make([]UtilityObservation, 0, limit)
	for rows.Next() {
		var item UtilityObservation
		if err = rows.Scan(&item.RecallID, &item.State, &item.Provenance, &item.ObservedAt, &item.Reason); err != nil {
			return UtilityPage{}, err
		}
		if len(item.RecallID) > 256 || item.RecallID == "" || !utf8.ValidString(item.RecallID) || (item.State != "used" && item.State != "unused" && item.State != "unknown") || (item.Provenance != "estimated" && item.Provenance != "unknown") || len(item.ObservedAt) > 64 || len(item.Reason) > 64 || !utf8.ValidString(item.Reason) {
			return UtilityPage{}, errors.New("invalid utility observation")
		}
		if _, err = time.Parse(time.RFC3339Nano, item.ObservedAt); err != nil {
			return UtilityPage{}, errors.New("invalid utility observation")
		}
		if len(page.Items) == limit {
			last := page.Items[len(page.Items)-1]
			b, _ := json.Marshal(utilityCursor{scope, last.ObservedAt, last.RecallID})
			page.NextCursor = base64.RawURLEncoding.EncodeToString(b)
			if len(page.NextCursor) > 512 {
				return UtilityPage{}, errors.New("utility cursor exceeds bounds")
			}
			break
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return UtilityPage{}, err
	}
	b, err := json.Marshal(page)
	if err != nil || len(b) > 65500 {
		return UtilityPage{}, errors.New("utility response too large")
	}
	return page, nil
}

func UtilityWeightPPM(a UtilityAggregate) (int64, error) {
	if a.Used < 0 || a.Unused < 0 || a.Unknown < 0 || a.Observed < 0 || a.Used > a.Observed || a.Unused > a.Observed-a.Used || a.Unknown != a.Observed-a.Used-a.Unused {
		return 0, errors.New("invalid utility aggregate")
	}
	n := a.Used + a.Unused
	if n < 5 {
		return 1000000, nil
	}
	// 2026-10-01：避免 int64 count 乘法溢位，結果為 floor(500000*unused/n)。
	var numerator, denominator big.Int
	numerator.SetInt64(a.Unused)
	numerator.Mul(&numerator, big.NewInt(500000))
	denominator.SetInt64(n)
	numerator.Quo(&numerator, &denominator)
	return 1000000 - numerator.Int64(), nil
}

func (s *SQLiteStore) TaskUtilityStatus(ctx context.Context, taskID string) (string, string, error) {
	var status, message string
	err := s.db.QueryRowContext(ctx, `SELECT utility_status,coalesce(substr(CAST(utility_error AS BLOB),1,257),'') FROM recall_events WHERE task_id=?`, taskID).Scan(&status, &message)
	if errors.Is(err, sql.ErrNoRows) {
		return "unavailable", "", nil
	}
	if err != nil {
		return "unavailable", "", err
	}
	if len(message) > 256 || (status != "pending" && status != "error" && status != "complete") {
		return "error", "invalid utility metadata", errors.New("invalid utility metadata")
	}
	return status, message, nil
}
