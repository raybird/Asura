package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// 2026-09-30 WEBGIT-004：現有 CLI query 的容量特性，非新 Web bounded contract 的驗收。
func TestWEBGITLegacyCapacityProbe(t *testing.T) {
	s := newTestGitMemStore(t)
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		c := &MemoryCandidate{ID: fmt.Sprintf("probe-%04d", i), Workspace: "/probe", RepoID: "repo", RefName: "main", BaseSHA: fmt.Sprint(i), HeadSHA: "head", DraftContent: strings.Repeat("草", 400), FactsSummary: "{}"}
		if err := s.SaveCandidate(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ListCandidates(ctx, "/probe", "pending", 50)
	if err != nil || len(page) != 50 {
		t.Fatalf("page %d %v", len(page), err)
	}
	giant := strings.Repeat("x", 2<<20)
	if _, err := s.db.ExecContext(ctx, "UPDATE memory_candidates SET draft_content=?,facts_summary=? WHERE id='probe-0000'", giant, giant); err != nil {
		t.Fatal(err)
	}
	detail, err := s.GetCandidate(ctx, "/probe", "probe-0000")
	if err != nil || len(detail.DraftContent) != 2<<20 || len(detail.FactsSummary) != 2<<20 {
		t.Fatalf("legacy contract changed: %v", err)
	}
	t.Log("1000 candidates: list limited to 50; legacy detail loads 2 MiB draft + 2 MiB facts, bounded Web projection required")
}
