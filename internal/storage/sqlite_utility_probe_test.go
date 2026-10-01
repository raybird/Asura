package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUtilityCapacityProbe(t *testing.T) {
	if os.Getenv("ASURA_UFL_CAPACITY_PROBE") != "1" {
		t.Skip("opt-in 10k/100k SQLite probe")
	}
	ctx := context.Background()
	for _, size := range []int{10000, 100000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "probe.db")
			s, err := NewSQLiteStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			start := time.Now()
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			taskStmt, err := tx.Prepare(`INSERT INTO tasks(id,workspace,prompt,status,execution_mode,created_at,updated_at) VALUES(?,'/probe','','completed','direct',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
			if err != nil {
				t.Fatal(err)
			}
			eventStmt, err := tx.Prepare(`INSERT INTO recall_events(recall_id,task_id,request_id,workspace,audit,created_at,utility_status) VALUES(?,?,?,'/probe',?,'2026-10-01T00:00:00.000000000Z','complete')`)
			if err != nil {
				t.Fatal(err)
			}
			assessmentStmt, err := tx.Prepare(`INSERT INTO utility_assessments(recall_id,workspace,memory_id,state,provenance,observed_at) VALUES(?,'/probe',?,'unused','estimated','2026-10-01T00:00:00.000000000Z')`)
			if err != nil {
				t.Fatal(err)
			}
			selectionStmt, err := tx.Prepare(`INSERT INTO recall_selections VALUES(?,?,'auto',1,1,1000000,1000000,1)`)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < size; i++ {
				id := fmt.Sprintf("r%06d", i)
				memory := fmt.Sprintf("m%03d", i%256)
				audit := fmt.Sprintf(`{"recall_id":%q,"mode":"bounded","utility_policy":"observe","utility_version":"utility-v1","auto_ids":[%q],"selected":[{"id":%q,"score":1,"weight_ppm":1000000,"final_score_scaled":1000000}]}`, id, memory, memory)
				if _, err = taskStmt.Exec(id); err != nil {
					t.Fatal(err)
				}
				if _, err = eventStmt.Exec(id, id, id, audit); err != nil {
					t.Fatal(err)
				}
				if _, err = selectionStmt.Exec(id, memory); err != nil {
					t.Fatal(err)
				}
				if _, err = assessmentStmt.Exec(id, memory); err != nil {
					t.Fatal(err)
				}
			}
			taskStmt.Close()
			eventStmt.Close()
			assessmentStmt.Close()
			selectionStmt.Close()
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			writes := time.Since(start)
			start = time.Now()
			if err = s.RebuildUtility(ctx); err != nil {
				t.Fatal(err)
			}
			rebuild := time.Since(start)
			var plan string
			rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT recall_id,state FROM utility_assessments WHERE workspace='/probe' AND memory_id='m000' AND (observed_at,recall_id)<('2026-10-02','z') ORDER BY observed_at DESC,recall_id DESC LIMIT 21`)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var a, b, c int
				var detail string
				if err = rows.Scan(&a, &b, &c, &detail); err != nil {
					t.Fatal(err)
				}
				plan += detail
			}
			rows.Close()
			if !strings.Contains(plan, "utility_scope_memory_time") || strings.Contains(plan, "SCAN utility_assessments") {
				t.Fatalf("unbounded query plan: %s", plan)
			}
			start = time.Now()
			page, err := s.GetMemoryUtility(ctx, "/probe", "m000", 20, "")
			query := time.Since(start)
			if err != nil || len(page.Items) != 20 || page.Aggregate.Observed != int64((size+255)/256) {
				t.Fatalf("page counts %+v %v", page, err)
			}
			ids := make([]string, 256)
			for i := range ids {
				ids[i] = fmt.Sprintf("m%03d", i)
			}
			start = time.Now()
			_, err = s.UtilityAggregates(ctx, "/probe", ids)
			weights := time.Since(start)
			if err != nil || weights > 250*time.Millisecond {
				t.Fatalf("apply aggregate deadline: %v %v", weights, err)
			}
			// 2026-10-01：留下 100 pending events 作 recovery 對照，不重跑模型。
			if _, err = s.db.Exec(`DELETE FROM utility_assessments WHERE recall_id IN (SELECT recall_id FROM recall_events ORDER BY recall_id LIMIT 100)`); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE recall_events SET utility_status='pending' WHERE recall_id IN (SELECT recall_id FROM recall_events ORDER BY recall_id LIMIT 100)`); err != nil {
				t.Fatal(err)
			}
			if err = s.RebuildUtility(ctx); err != nil {
				t.Fatal(err)
			}
			start = time.Now()
			if err = s.RecoverUtility(ctx); err != nil {
				t.Fatal(err)
			}
			recovery := time.Since(start)
			var count int
			if err = s.db.QueryRow(`SELECT count(*) FROM utility_assessments`).Scan(&count); err != nil || count != size {
				t.Fatalf("recovery count=%d %v", count, err)
			}
			var bytes int64
			for _, suffix := range []string{"", "-wal", "-shm"} {
				if info, err := os.Stat(path + suffix); err == nil {
					bytes += info.Size()
				}
			}
			t.Logf("events=%d writes=%s rebuild=%s query20=%s aggregate256=%s recovery100=%s bytes=%d logical_writes_per_event=4+aggregate plan=%s", size, writes, rebuild, query, weights, recovery, bytes, plan)
		})
	}
}
