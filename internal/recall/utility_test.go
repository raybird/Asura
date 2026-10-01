package recall

import (
	"asura/internal/storage"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestUtilityApplyRanking(t *testing.T) {
	in := Input{Workspace: "/a", Prompt: "sqlite workspace", Measure: func([]storage.Memory) Budget { return Budget{} }, Candidates: []storage.Memory{
		{ID: "z", Workspace: "/a", Content: "sqlite workspace", CreatedAt: time.Now()},
		{ID: "a", Workspace: "/a", Content: "sqlite", CreatedAt: time.Now()},
	}}
	if err := json.Unmarshal([]byte(`{"UtilityPolicy":"apply","UtilityAggregates":{"z":{"observed_count":5,"used_count":0,"unused_count":5,"unknown_count":0}}}`), &in); err != nil {
		t.Fatal(err)
	}
	got, err := Select(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Decision.AutoIDs, ",") != "a,z" || got.Decision.Selected[1].WeightPPM != 500000 || got.Decision.Selected[1].FinalScore != 1000000 || got.Decision.Selected[1].LexicalRank != 1 {
		t.Fatalf("UFL-007 apply/tie/sealed evidence: %+v", got.Decision)
	}
}

func TestUtilityObserveAndExplicit(t *testing.T) {
	in := Input{Workspace: "/a", Prompt: "sqlite workspace", UtilityPolicy: "observe", Measure: func([]storage.Memory) Budget { return Budget{} }, Candidates: []storage.Memory{
		{ID: "z", Workspace: "/a", Content: "sqlite workspace", CreatedAt: time.Now()},
		{ID: "a", Workspace: "/a", Content: "sqlite", CreatedAt: time.Now()},
	}, UtilityAggregates: map[string]storage.UtilityAggregate{"z": {Observed: 5, Unused: 5}}}
	got, err := Select(context.Background(), in)
	if err != nil || strings.Join(got.Decision.AutoIDs, ",") != "z,a" || got.Decision.Selected[0].WeightPPM != 1000000 {
		t.Fatalf("UFL-001 observe %+v %v", got, err)
	}
	in.UtilityPolicy = "apply"
	in.ExplicitIDs = []string{"z"}
	in.UtilityAggregates["z"] = storage.UtilityAggregate{Observed: 1, Unused: 2}
	got, err = Select(context.Background(), in)
	if err != nil || strings.Join(got.Decision.AutoIDs, ",") != "a" {
		t.Fatalf("explicit altered %+v %v", got, err)
	}
	in.ExplicitIDs = nil
	if _, err = Select(context.Background(), in); err == nil {
		t.Fatal("invalid aggregate silently applied")
	}
	in.UtilityAggregates["z"] = storage.UtilityAggregate{Observed: 100, Unknown: 100}
	got, err = Select(context.Background(), in)
	if err != nil || got.Decision.Selected[0].WeightPPM != 1000000 {
		t.Fatalf("UFL-008 unknown penalty %+v %v", got, err)
	}
}
