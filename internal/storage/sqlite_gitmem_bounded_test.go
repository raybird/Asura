package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// 2026-09-30 WEBGIT-004：以可選介面取得真實缺少行為紅燈，非編譯錯誤。
type candidatePager interface {
	PageGitCandidates(context.Context, string, string, int, string) ([]CandidateSummary, string, error)
	GetGitCandidateBounded(context.Context, string, string) (*MemoryCandidate, error)
}

func TestWEBGITBoundedPageAndDetail(t *testing.T) {
	s := newTestGitMemStore(t)
	pager, ok := any(s).(candidatePager)
	if !ok {
		t.Fatal("missing bounded candidate page/detail contract")
	}
	ctx := context.Background()
	ws := "/bounded"
	for i := 0; i < 123; i++ {
		c := &MemoryCandidate{ID: fmt.Sprintf("c-%03d", i), Workspace: ws, RepoID: "repo", RefName: "main", BaseSHA: fmt.Sprint(i), HeadSHA: "head", DraftContent: strings.Repeat("草", 300), FactsSummary: "{}", CreatedAt: time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)}
		if i%3 == 0 {
			c.Status = "discarded"
		}
		if err := s.SaveCandidate(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		items, next, err := pager.PageGitCandidates(ctx, ws, "pending", 7, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) > 7 {
			t.Fatal("page exceeds limit")
		}
		for _, c := range items {
			if seen[c.ID] || c.Status != "pending" || len(c.Summary) > 512 || !utf8.ValidString(c.Summary) {
				t.Fatalf("bad item: %+v", c)
			}
			seen[c.ID] = true
		}
		if next == "" {
			break
		}
		if next == cursor {
			t.Fatal("cursor stuck")
		}
		cursor = next
	}
	if len(seen) != 82 {
		t.Fatalf("missing rows: %d", len(seen))
	}
	_, cursor, err := pager.PageGitCandidates(ctx, ws, "pending", 1, "")
	if err != nil || cursor == "" {
		t.Fatal("missing next page")
	}
	for _, x := range []struct {
		ws, status, cur string
		limit           int
	}{{"/other", "pending", cursor, 1}, {ws, "approved", cursor, 1}, {ws, "unknown", "", 1}, {ws, "pending", "garbage", 1}, {ws, "pending", "", 51}} {
		if _, _, err := pager.PageGitCandidates(ctx, x.ws, x.status, x.limit, x.cur); err == nil {
			t.Fatalf("invalid request accepted: %+v", x)
		}
	}
	if _, err := pager.GetGitCandidateBounded(ctx, "/other", "c-001"); err != ErrCandidateNotFound {
		t.Fatalf("scope: %v", err)
	}
	detail, err := pager.GetGitCandidateBounded(ctx, ws, "c-001")
	if err != nil || detail.DraftContent != strings.Repeat("草", 300) {
		t.Fatalf("detail: %v", err)
	}
	for _, field := range []string{"draft_content", "facts_summary", "repo_id", "truncated_reason"} {
		t.Run(field, func(t *testing.T) {
			limit := 4096
			if field == "draft_content" {
				limit = 32768
			}
			if field == "facts_summary" {
				limit = 256 << 10
			}
			q := "UPDATE memory_candidates SET " + field + "=? WHERE id='c-001'"
			if _, err := s.db.ExecContext(ctx, q, strings.Repeat("x", limit+1)); err != nil {
				t.Fatal(err)
			}
			if _, err := pager.GetGitCandidateBounded(ctx, ws, "c-001"); err == nil {
				t.Fatal("oversize detail accepted")
			}
			if _, err := s.db.ExecContext(ctx, q, "{}"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// 2026-09-30 WEBGIT-007/008：交易內 bounds、空內容、終態重送與防復活。
func TestWEBGITBoundedApproval(t *testing.T) {
	s := newTestGitMemStore(t)
	ctx := context.Background()
	ws := "/approve"
	for _, tc := range []struct {
		id, draft, status string
		partial           bool
		content           string
		set               bool
		want              error
	}{
		{"large", strings.Repeat("x", 32769), "pending", false, "", false, ErrGitTooLarge},
		{"empty", "draft", "pending", false, " ", true, ErrGitRequest},
		{"oversized-edit", "draft", "pending", false, strings.Repeat("x", 32769), true, ErrGitTooLarge},
		{"partial", "draft", "pending", true, "", false, ErrCandidateStatusConflict},
		{"discarded", "draft", "discarded", false, "", false, ErrCandidateStatusConflict},
		{"invalid", "draft", "invalid", false, "", false, ErrGitMetadata},
	} {
		t.Run(tc.id, func(t *testing.T) {
			c := &MemoryCandidate{ID: tc.id, Workspace: ws, RepoID: "repo", BaseSHA: tc.id, HeadSHA: "head", FactsSummary: "{}", DraftContent: tc.draft, Status: tc.status, IsTruncated: tc.partial}
			if err := s.SaveCandidate(ctx, c); err != nil {
				t.Fatal(err)
			}
			_, err := s.ApproveCandidateTx(ctx, ws, tc.id, ApproveOptions{Bounded: true, Content: tc.content, ContentSet: tc.set})
			if err != tc.want {
				t.Fatalf("want %v got %v", tc.want, err)
			}
		})
	}
	mems, err := s.ListMemories(ctx, ws)
	if err != nil || len(mems) != 0 {
		t.Fatalf("rejected write %d %v", len(mems), err)
	}
	c := &MemoryCandidate{ID: "good", Workspace: ws, RepoID: "repo", BaseSHA: "good", HeadSHA: "head", FactsSummary: `{"commits":[{"sha":"orig"}]}`, DraftContent: "draft"}
	if err := s.SaveCandidate(ctx, c); err != nil {
		t.Fatal(err)
	}
	id, err := s.ApproveCandidateTx(ctx, ws, c.ID, ApproveOptions{Bounded: true, ContentSet: true, Content: "edited"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMemory(ctx, ws, id); err != nil {
		t.Fatal(err)
	}
	got, err := s.ApproveCandidateTx(ctx, ws, c.ID, ApproveOptions{Bounded: true})
	if err != nil || got != id {
		t.Fatalf("deleted formal memory retry %s %v", got, err)
	}
	mems, err = s.ListMemories(ctx, ws)
	if err != nil || len(mems) != 0 {
		t.Fatal("deleted memory revived")
	}
}

func TestWEBGITConcurrentReview(t *testing.T) {
	s := newTestGitMemStore(t)
	ctx := context.Background()
	c := &MemoryCandidate{ID: "race", Workspace: "/race", RepoID: "repo", BaseSHA: "base", HeadSHA: "head", FactsSummary: "{}", DraftContent: "draft"}
	if err := s.SaveCandidate(ctx, c); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	done := make(chan struct{}, 16)
	for i := 0; i < 16; i++ {
		go func(approve bool) {
			<-start
			if approve {
				s.ApproveCandidateTx(ctx, c.Workspace, c.ID, ApproveOptions{Bounded: true})
			} else {
				s.DiscardGitCandidateBounded(ctx, c.Workspace, c.ID)
			}
			done <- struct{}{}
		}(i%2 == 0)
	}
	close(start)
	for i := 0; i < 16; i++ {
		<-done
	}
	final, err := s.GetCandidate(ctx, c.Workspace, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	mems, err := s.ListMemories(ctx, c.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status == "approved" {
		if len(mems) != 1 || mems[0].ID != final.ApprovedMemoryID {
			t.Fatal("approve race duplicated/inconsistent memory")
		}
	} else if final.Status == "discarded" {
		if len(mems) != 0 {
			t.Fatal("discard race leaked memory")
		}
	} else {
		t.Fatalf("no review completed: %s", final.Status)
	}
}

// 2026-09-30 WEBGIT-007：legacy approved Partial 也不能以重送成功繞過 Complete gate。
func TestWEBGITApprovedPartialRejected(t *testing.T) {
	s := newTestGitMemStore(t)
	ctx := context.Background()
	c := &MemoryCandidate{ID: "legacy-partial", Workspace: "/legacy", RepoID: "repo", BaseSHA: "base", HeadSHA: "head", Status: "approved", ApprovedMemoryID: "old-memory", IsTruncated: true, FactsSummary: "{}", DraftContent: "draft"}
	if err := s.SaveCandidate(ctx, c); err != nil {
		t.Fatal(err)
	}
	_, err := s.ApproveCandidateTx(ctx, c.Workspace, c.ID, ApproveOptions{Bounded: true})
	if err != ErrCandidateStatusConflict {
		t.Fatalf("approved Partial must reject: %v", err)
	}
}
