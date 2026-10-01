package recall

import (
	"asura/internal/storage"
	"context"
	"testing"
	"time"
)

func TestLifecycleDurableDoesNotBoost(t *testing.T) {
	got, err := Select(context.Background(), Input{Workspace: "/a", Prompt: "sqlite", UtilityPolicy: "apply", Candidates: []storage.Memory{
		{ID: "a", Workspace: "/a", Content: "sqlite", Source: "manual", CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), LifecycleMetadata: storage.LifecycleMetadata{Lifecycle: "active"}},
		{ID: "b", Workspace: "/a", Content: "sqlite", Source: "manual", CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), LifecycleMetadata: storage.LifecycleMetadata{Lifecycle: "active", IsDurable: true}},
	}, Measure: func([]storage.Memory) Budget { return Budget{} }})
	if err != nil || len(got.Decision.Selected) != 2 || got.Decision.Selected[0].ID != "a" || got.Decision.Selected[1].ID != "b" || got.Decision.Selected[0].Score != 1 || got.Decision.Selected[1].Score != 1 || got.Decision.Selected[1].WeightPPM != 1000000 || got.Decision.Selected[0].FinalScore != got.Decision.Selected[1].FinalScore {
		t.Fatalf("MLIFE-007 ranking %+v %v", got, err)
	}
}
