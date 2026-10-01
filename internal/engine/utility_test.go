package engine

import (
	"asura/internal/auth"
	"asura/internal/storage"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestDirectUtilityUnknown(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failed"}[failed], func(t *testing.T) {
			ws := t.TempDir()
			s, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			calls := 0
			e := NewEngine(s, auth.NewPolicy([]string{ws}), WithRunner(func(context.Context, string, string) (string, error) {
				calls++
				if failed {
					return "partial", errors.New("failed")
				}
				return "done", nil
			}))
			var ec ExecutionContext
			raw := `{"workspace":` + stringMustJSON(ws) + `,"original_prompt":"test","recall":{"recall_id":"r","mode":"bounded","utility_policy":"observe","utility_version":"utility-v1","auto_ids":["m"],"selected":[{"id":"m","score":1,"weight_ppm":1000000,"final_score_scaled":1000000}]}}`
			if err = json.Unmarshal([]byte(raw), &ec); err != nil {
				t.Fatal(err)
			}
			_, _ = e.SubmitTask(context.Background(), SubmitRequest{Workspace: ws, Prompt: "test", ResolvedContext: &ec})
			aggregate, err := s.UtilityAggregates(context.Background(), ws, []string{"m"})
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || aggregate["m"].Observed != 1 || aggregate["m"].Unknown != 1 {
				t.Fatalf("UFL-005 calls=%d aggregate=%+v", calls, aggregate)
			}
		})
	}
}
func stringMustJSON(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestThreeFaceUtilityPersistence(t *testing.T) {
	for _, tc := range []struct {
		name, trace string
		wantUsed    int64
	}{
		{"used", `{"critic_review":{"verdict":"pass","memory_usage":[{"memory_id":"m","state":"used","provenance":"estimated"}]},"stages":[{"name":"critic","status":"completed"}]}`, 1},
		{"invalid", `{"critic_review":{"verdict":"pass","memory_usage":[{"memory_id":"m","state":"used","provenance":"estimated"},{"memory_id":"foreign","state":"used","provenance":"estimated"}]},"stages":[{"name":"critic","status":"completed"}]}`, 0},
		{"legacy", `{"critic_review":{"verdict":"pass"},"stages":[{"name":"critic","status":"completed"}]}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			s, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			e := NewEngine(s, auth.NewPolicy([]string{ws}), WithPipelineRunner(utilityPipeline{trace: tc.trace}))
			var ec ExecutionContext
			raw := `{"workspace":` + stringMustJSON(ws) + `,"original_prompt":"test","recall":{"recall_id":"r","mode":"bounded","utility_policy":"observe","utility_version":"utility-v1","auto_ids":["m"],"selected":[{"id":"m","score":1,"weight_ppm":1000000,"final_score_scaled":1000000}]}}`
			if err = json.Unmarshal([]byte(raw), &ec); err != nil {
				t.Fatal(err)
			}
			task, err := e.SubmitTask(context.Background(), SubmitRequest{Workspace: ws, Prompt: "test", Mode: "three-face", ResolvedContext: &ec})
			if err != nil || task.Status != storage.StatusCompleted {
				t.Fatalf("task %v %v", task, err)
			}
			a, err := s.UtilityAggregates(context.Background(), ws, []string{"m"})
			if err != nil || a["m"].Observed != 1 || a["m"].Used != tc.wantUsed {
				t.Fatalf("UFL-004 aggregate %+v %v", a, err)
			}
		})
	}
}

type utilityPipeline struct{ trace string }

func (p utilityPipeline) ExecutePipeline(context.Context, string, *ExecutionContext) (string, string, error) {
	return "done", p.trace, nil
}
