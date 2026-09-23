package recall

import (
	"asura/internal/contextwin"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"asura/internal/storage"
)

func TestSelect(t *testing.T) {
	in := Input{Workspace: "/a", Prompt: "SQLITE workspace 隔離", Measure: func(ms []storage.Memory) Budget {
		return Budget{FormattedBytes: len(ms) * 100, EstimatedTokens: int64(len(ms) * 50)}
	}, Candidates: []storage.Memory{
		{ID: "b", Workspace: "/a", Content: "SQLite transaction rollback", Source: "manual", CreatedAt: time.Now()},
		{ID: "a", Workspace: "/a", Content: "SQLite workspace 隔離", Source: "manual", CreatedAt: time.Now()},
		{ID: "c", Workspace: "/b", Content: "SQLite workspace 隔離 SECRET", Source: "manual", CreatedAt: time.Now()},
	}}
	for i := 0; i < 20; i++ {
		got, err := Select(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got.Decision.AutoIDs, ",") != "a,b" {
			t.Fatalf("RECALL-003: ranking got %v", got.Decision.AutoIDs)
		}
	}
	in.ExplicitIDs = []string{"a"}
	got, err := Select(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Decision.AutoIDs, ",") != "b" || got.Decision.Skipped["duplicate"] != 1 {
		t.Fatalf("dedup %+v", got)
	}
	for _, q := range []string{"香蕉果汁", "資料庫", "隔"} {
		in.Prompt = q
		got, err = Select(context.Background(), in)
		if err != nil || len(got.Memories) != 0 {
			t.Fatalf("unmatched %s: %+v %v", q, got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Select(ctx, in); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestSelectLimits(t *testing.T) {
	base := storage.Memory{ID: "a", Workspace: "/a", Content: "SQLite", Source: "manual", CreatedAt: time.Now()}
	for _, tc := range []struct {
		name   string
		budget Budget
		reason string
	}{
		{"bytes", Budget{FormattedBytes: 8193}, "byte_limit"},
		{"tokens", Budget{EstimatedTokens: 2049}, "token_limit"},
		{"stage", Budget{FirstStage: contextwin.StageAdmission{MaxTokens: 1, Estimate: contextwin.StageEstimate{TotalEstimatedTokens: 2}}}, "stage_limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Select(context.Background(), Input{Workspace: "/a", Prompt: "SQLite", Candidates: []storage.Memory{base}, Measure: func([]storage.Memory) Budget { return tc.budget }})
			if err != nil || len(got.Memories) != 0 || got.Decision.Skipped[tc.reason] != 1 {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
	in := Input{Workspace: "/a", Prompt: "SQLite", Measure: func([]storage.Memory) Budget { return Budget{FormattedBytes: 8192, EstimatedTokens: 2048} }}
	for i := 0; i < 6; i++ {
		m := base
		m.ID = fmt.Sprintf("%02d", i)
		in.Candidates = append(in.Candidates, m)
	}
	got, err := Select(context.Background(), in)
	if err != nil || strings.Join(got.Decision.AutoIDs, ",") != "00,01,02,03" || got.Decision.Skipped["item_limit"] != 2 {
		t.Fatalf("boundary: %+v %v", got, err)
	}
	in.Candidates[0].Content = strings.Repeat("x", 8193)
	in.Candidates[1].Source = strings.Repeat("s", 129)
	in.Candidates[2].CreatedAt = time.Time{}
	in.Candidates[3].Content = "\xff"
	got, err = Select(context.Background(), in)
	if err != nil || strings.Join(got.Decision.AutoIDs, ",") != "04,05" || got.Decision.Skipped["oversize"] != 2 || got.Decision.Skipped["invalid_metadata"] != 2 {
		t.Fatalf("metadata: %+v %v", got, err)
	}
	in.Prompt = strings.Repeat(" ", 4096) + "SQLite"
	got, err = Select(context.Background(), in)
	if err != nil || !got.Decision.QueryTruncated || len(got.Memories) != 0 {
		t.Fatalf("query cap: %+v %v", got, err)
	}
	in.Prompt = ""
	for i := 0; i < 129; i++ {
		in.Prompt += fmt.Sprintf("q%d ", i)
	}
	in.Candidates = []storage.Memory{base}
	in.Candidates[0].Content = "q128"
	got, err = Select(context.Background(), in)
	if err != nil || !got.Decision.QueryTruncated || len(got.Memories) != 0 {
		t.Fatalf("term cap: %+v %v", got, err)
	}
}

func BenchmarkSelectBounded(b *testing.B) {
	in := Input{Workspace: "/a", Prompt: "SQLite 隔離", Measure: func([]storage.Memory) Budget { return Budget{} }}
	for i := 0; i < 256; i++ {
		in.Candidates = append(in.Candidates, storage.Memory{ID: fmt.Sprintf("%03d", i), Workspace: "/a", Content: strings.Repeat("SQLite 隔離 ", 400), Source: "manual", CreatedAt: time.Now()})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Select(context.Background(), in); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSelectDeadlineAndQueryRune(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := Select(ctx, Input{Measure: func([]storage.Memory) Budget { cancel(); return Budget{} }})
	if err != context.Canceled {
		t.Fatalf("cancellation during measure lost: %v", err)
	}
	in := Input{Workspace: "/a", Prompt: strings.Repeat(" ", 4095) + "隔離", Measure: func([]storage.Memory) Budget { return Budget{} }, Candidates: []storage.Memory{{ID: "a", Workspace: "/a", Content: "隔離", CreatedAt: time.Now()}}}
	got, err := Select(context.Background(), in)
	if err != nil || !got.Decision.QueryTruncated || len(got.Memories) != 0 {
		t.Fatalf("split rune: %+v %v", got, err)
	}
}
