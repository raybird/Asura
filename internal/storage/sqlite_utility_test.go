package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUtilityLedgerAdmission(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	task := &Task{ID: "task", RequestID: "request", Workspace: "/a", Status: StatusPending, CreatedAt: time.Now().UTC(), ResolvedContext: `{"workspace":"/a","recall":{"recall_id":"recall","mode":"bounded","utility_policy":"observe","utility_version":"utility-v1","auto_ids":["auto"],"explicit_ids":[],"selected":[{"id":"auto","score":2,"weight_ppm":1000000,"final_score_scaled":2000000}]}}`}
	if err := s.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM recall_events WHERE recall_id='recall' AND task_id='task' AND workspace='/a'`).Scan(&count); err != nil {
		t.Fatalf("UFL-002 missing ledger: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one event, got %d", count)
	}
	task.ID = "duplicate"
	if err := s.CreateTask(context.Background(), task); err != ErrDuplicateRequestID {
		t.Fatalf("duplicate request: %v", err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM recall_events`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("redelivery duplicated event: %d %v", count, err)
	}
}

func utilityTask(id, scope string) *Task {
	return &Task{ID: id, RequestID: id, Workspace: scope, Status: StatusCompleted, ExecutionMode: "three-face", ResolvedContext: `{"workspace":"` + scope + `","recall":{"recall_id":"` + id + `","mode":"bounded","utility_policy":"observe","utility_version":"utility-v1","auto_ids":["m"],"selected":[{"id":"m","score":1,"weight_ppm":1000000,"final_score_scaled":1000000}]}}`}
}

func TestUtilityAtomicRetryRebuild(t *testing.T) {
	ctx := context.Background()
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"a", "b"} {
		if err = s.CreateTask(ctx, utilityTask(id, "/a")); err != nil {
			t.Fatal(err)
		}
	}
	input := []UtilityAssessment{{"m", "used", "estimated"}}
	for i := 0; i < 3; i++ {
		if err = s.PersistUtility(ctx, "/a", "a", input); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.PersistUtility(ctx, "/a", "a", []UtilityAssessment{{"m", "unused", "estimated"}}); err == nil {
		t.Fatal("UFL-006 conflicting retry accepted")
	}
	if err = s.PersistUtility(ctx, "/b", "b", input); err == nil {
		t.Fatal("UFL-003 cross scope accepted")
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_aggregate BEFORE UPDATE ON utility_aggregates BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.PersistUtility(ctx, "/a", "b", input); err == nil {
		t.Fatal("UFL-011 expected aggregate failure")
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM utility_assessments`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("partial assessment commit %d %v", count, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER fail_aggregate`); err != nil {
		t.Fatal(err)
	}
	if err = s.PersistUtility(ctx, "/a", "b", []UtilityAssessment{{"m", "unknown", "unknown"}}); err != nil {
		t.Fatal(err)
	}
	before, err := s.UtilityAggregates(ctx, "/a", []string{"m"})
	if err != nil {
		t.Fatal(err)
	}
	if before["m"].Observed != 2 || before["m"].Used != 1 || before["m"].Unknown != 1 {
		t.Fatalf("counts %+v", before)
	}
	if _, err = s.db.Exec(`UPDATE utility_aggregates SET used_count=42`); err != nil {
		t.Fatal(err)
	}
	if err = s.RebuildUtility(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.UtilityAggregates(ctx, "/a", []string{"m"})
	if err != nil || after["m"] != before["m"] {
		t.Fatalf("rebuild %+v %v", after, err)
	}
}

func TestUtilityAdmissionRollback(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.db.Exec(`CREATE TRIGGER fail_event BEFORE INSERT ON recall_events BEGIN SELECT RAISE(ABORT,'injected'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateTask(context.Background(), utilityTask("a", "/a")); err == nil {
		t.Fatal("expected ledger failure")
	}
	if _, err = s.GetTask(context.Background(), "a"); err != ErrTaskNotFound {
		t.Fatalf("UFL-002 orphan task %v", err)
	}
}

func TestUtilityRecoveryAndDeletion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	task := utilityTask("r", "/a")
	task.PipelineTrace = `{"critic_review":{"verdict":"pass","memory_usage":[{"memory_id":"m","state":"used","provenance":"estimated"}]},"stages":[{"name":"critic","status":"completed"}]}`
	if err = s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 2; i++ {
		if err = s.RecoverUtility(ctx); err != nil {
			t.Fatal(err)
		}
	}
	a, err := s.UtilityAggregates(ctx, "/a", []string{"m"})
	if err != nil || a["m"].Used != 1 {
		t.Fatalf("recovery %+v %v", a, err)
	}
	page, err := s.GetMemoryUtility(ctx, "/a", "m", 1, "")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("UFL-009 tombstone audit %+v %v", page, err)
	}
	if _, err = s.GetMemoryUtility(ctx, "/b", "m", 1, ""); err != ErrMemoryNotFound {
		t.Fatalf("scope existence leak %v", err)
	}
}

func TestUtilityPageBounds(t *testing.T) {
	ctx := context.Background()
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"a", "b", "c"} {
		if err = s.CreateTask(ctx, utilityTask(id, "/a")); err != nil {
			t.Fatal(err)
		}
		if err = s.PersistUtility(ctx, "/a", id, []UtilityAssessment{{"m", "used", "estimated"}}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.GetMemoryUtility(ctx, "/a", "m", 1, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].RecallID != "c" || page.NextCursor == "" {
		t.Fatalf("page %+v %v", page, err)
	}
	next, err := s.GetMemoryUtility(ctx, "/a", "m", 1, page.NextCursor)
	if err != nil || next.Items[0].RecallID != "b" {
		t.Fatalf("next %+v %v", next, err)
	}
	if _, err = s.GetMemoryUtility(ctx, "/a", "other", 1, page.NextCursor); err == nil {
		t.Fatal("cursor not scoped")
	}
	if _, err = s.GetMemoryUtility(ctx, "/a", "m", 101, ""); err == nil {
		t.Fatal("query limit")
	}
	if _, err = s.db.Exec(`UPDATE utility_assessments SET observed_at=? WHERE recall_id='c'`, strings.Repeat("x", 100000)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetMemoryUtility(ctx, "/a", "m", 1, ""); err == nil {
		t.Fatal("oversize metadata silently accepted")
	}
}

func TestUtilityWeight(t *testing.T) {
	for _, tc := range []struct{ used, unused, unknown, want int64 }{
		{0, 4, 0, 1000000}, {0, 5, 0, 500000}, {5, 5, 0, 750000}, {0, 0, 100, 1000000}, {1, 5, 999, 583334}, {0, 9223372036854775807, 0, 500000},
	} {
		w, err := UtilityWeightPPM(UtilityAggregate{Observed: tc.used + tc.unused + tc.unknown, Used: tc.used, Unused: tc.unused, Unknown: tc.unknown})
		if err != nil || w != tc.want {
			t.Fatalf("weight %+v got %d %v", tc, w, err)
		}
	}
	if _, err := UtilityWeightPPM(UtilityAggregate{Observed: 5, Unused: 6}); err == nil {
		t.Fatal("bad count accepted")
	}
}

func TestUtilityTerminalOutcomes(t *testing.T) {
	for _, tc := range []struct {
		status   TaskStatus
		trace    string
		wantUsed int64
	}{
		{StatusCancelled, `{"critic_review":{"verdict":"pass","memory_usage":[{"memory_id":"m","state":"used","provenance":"estimated"}]},"stages":[{"name":"critic","status":"completed"}]}`, 0},
		{StatusFailed, `{"critic_review":{"verdict":"failed","memory_usage":[{"memory_id":"m","state":"used","provenance":"estimated"}]},"stages":[{"name":"critic","status":"completed"}]}`, 1},
		{StatusFailed, `{"critic_review":{"verdict":"pass","memory_usage":[{"memory_id":"m","state":"used","provenance":"estimated"}]},"stages":[{"name":"critic","status":"failed"}]}`, 0},
	} {
		t.Run(string(tc.status)+fmt.Sprint(tc.wantUsed)+tc.trace[len(tc.trace)-14:], func(t *testing.T) {
			s, err := NewSQLiteStore(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			task := utilityTask("r", "/a")
			task.Status = tc.status
			task.PipelineTrace = tc.trace
			if err = s.CreateTask(context.Background(), task); err != nil {
				t.Fatal(err)
			}
			if err = s.CompleteTaskUtility(context.Background(), task.ID); err != nil {
				t.Fatal(err)
			}
			a, err := s.UtilityAggregates(context.Background(), "/a", []string{"m"})
			if err != nil || a["m"].Observed != 1 || a["m"].Used != tc.wantUsed {
				t.Fatalf("outcome %+v %v", a, err)
			}
		})
	}
}

func TestUtilityFailureStatus(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	task := utilityTask("r", "/a")
	if err = s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_assessment BEFORE INSERT ON utility_assessments BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteTaskUtility(ctx, "r"); err == nil {
		t.Fatal("expected utility error")
	}
	saved, err := s.GetTask(ctx, "r")
	if err != nil || saved.Status != StatusCompleted {
		t.Fatalf("UFL-011 changed main result %v %v", saved, err)
	}
	state, _, err := s.TaskUtilityStatus(ctx, "r")
	if err != nil || state != "error" {
		t.Fatalf("failure audit %s %v", state, err)
	}
}
