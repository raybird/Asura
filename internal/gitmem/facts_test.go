package gitmem

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sampleCaptureFacts() *CaptureFacts {
	return &CaptureFacts{
		Workspace: "/workspace/asura",
		RepoID:    "repo-999",
		RefName:   "refs/heads/main",
		BaseSHA:   "1111111111111111111111111111111111111111",
		HeadSHA:   "2222222222222222222222222222222222222222",
		CommitCount: 2,
		Commits: []CommitInfo{
			{
				SHA:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Subject: "feat(gitmem): add git executor",
				Author:  "Alice",
				Date:    "2026-09-25T10:00:00Z",
			},
			{
				SHA:     "2222222222222222222222222222222222222222",
				Subject: "feat(storage): add candidate table",
				Author:  "Bob",
				Date:    "2026-09-25T11:00:00Z",
			},
		},
		ChangedFiles:    []string{"internal/gitmem/git.go", "internal/storage/sqlite.go"},
		TotalFilesCount: 2,
		Truncated:       false,
		DiffStatSummary: " 2 files changed, 50 insertions(+)",
		FilteredDiff:    "diff --git ...",
	}
}

func TestFormatFactsSummary(t *testing.T) {
	facts := sampleCaptureFacts()
	summary, err := FormatFactsSummary(facts)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if !strings.Contains(summary, "11111111") || !strings.Contains(summary, "22222222") {
		t.Errorf("expected summary to contain base and head SHAs, got: %s", summary)
	}
	if !strings.Contains(summary, "feat(gitmem): add git executor") {
		t.Errorf("expected summary to contain commit subject, got: %s", summary)
	}
	if !strings.Contains(summary, "internal/gitmem/git.go") {
		t.Errorf("expected summary to list changed files, got: %s", summary)
	}
}

func TestGenerateDraftContent(t *testing.T) {
	facts := sampleCaptureFacts()
	draft := GenerateDraftContent(facts)

	if !strings.Contains(draft, "feat(gitmem): add git executor") {
		t.Errorf("expected draft to reference commit subject")
	}
	if !strings.Contains(draft, "架構決策與技術約束") {
		t.Errorf("expected draft to contain architecture decision guidance section")
	}
	// 驗證內文純淨性：不應包含 raw diff 內容
	if strings.Contains(draft, "diff --git") {
		t.Errorf("draft must not contain raw diff content to preserve recall purity")
	}
}

func TestBuildProvenance(t *testing.T) {
	facts := sampleCaptureFacts()
	draft := GenerateDraftContent(facts)
	approvedAt := time.Now().UTC()

	// 1. 未修改內容 (is_user_edited == false)
	prov1, err := BuildProvenance("mem-1", "cand-1", facts, draft, approvedAt)
	if err != nil {
		t.Fatalf("failed to build provenance: %v", err)
	}
	if prov1.IsUserEdited {
		t.Errorf("expected IsUserEdited=false when content matches draft")
	}
	if prov1.MemoryID != "mem-1" || prov1.CandidateID != "cand-1" {
		t.Errorf("mismatched IDs in provenance")
	}

	// 驗證 commit_shas_json 為合法 JSON array
	var shas []string
	if err := json.Unmarshal([]byte(prov1.CommitSHAsJSON), &shas); err != nil {
		t.Fatalf("invalid commit SHAs JSON: %v", err)
	}
	if len(shas) != 2 || shas[0] != facts.Commits[0].SHA {
		t.Errorf("unexpected SHAs in JSON: %v", shas)
	}

	// 2. 使用者有編輯內容 (is_user_edited == true)
	editedContent := draft + "\n補充決策：經考量安全性，禁用外部 diff。"
	prov2, err := BuildProvenance("mem-2", "cand-2", facts, editedContent, approvedAt)
	if err != nil {
		t.Fatalf("failed to build provenance: %v", err)
	}
	if !prov2.IsUserEdited {
		t.Errorf("expected IsUserEdited=true when content is edited")
	}
}
