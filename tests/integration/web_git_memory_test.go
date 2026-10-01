package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"asura/internal/storage"
)

func webGitRepo(t *testing.T, f *webFixture) string {
	t.Helper()
	repo := filepath.Join(f.root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	git("init", "-b", "main")
	git("commit", "--allow-empty", "-m", "initial")
	git("commit", "--allow-empty", "-m", "<img src=x onerror=alert(1)>")
	return repo
}

func saveWebGit(t *testing.T, f *webFixture, ws, id, status, draft string, partial bool) {
	t.Helper()
	c := &storage.MemoryCandidate{ID: id, Workspace: ws, RepoID: "repo", RefName: "main", BaseSHA: id, HeadSHA: "head", DraftContent: draft, FactsSummary: `{"commits":[{"sha":"original"}]}`, Status: status, IsTruncated: partial, CreatedAt: time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)}
	if err := f.store.SaveCandidate(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}

// 2026-09-30 WEBGIT-001～004/006～008：真實 HTTP、Unix IPC、SQLite 與 Git。
func TestWEBGITHTTPWorkflow(t *testing.T) {
	f := startWeb(t)
	repo := webGitRepo(t, f)
	cookie := f.login(t)
	payload := `{"workspace":"` + repo + `","range":"HEAD~1..HEAD"}`
	code, body, _ := f.call(t, "POST", "/api/git-memory/capture", payload, f.server.URL, cookie)
	if code != 200 {
		t.Fatalf("capture contract missing: %d %s", code, body)
	}
	var result struct {
		CandidateID string `json:"candidate_id"`
		Outcome     string `json:"outcome"`
		Reused      bool   `json:"reused"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil || result.CandidateID == "" || result.Outcome != "Complete" || result.Reused {
		t.Fatalf("capture %s %v", body, err)
	}
	id := result.CandidateID
	code, body, _ = f.call(t, "POST", "/api/git-memory/capture", payload, f.server.URL, cookie)
	if code != 200 || !strings.Contains(body, `"reused":true`) {
		t.Fatalf("reuse: %d %s", code, body)
	}
	detail := "/api/git-memory/candidates/" + id + "?workspace=" + url.QueryEscape(repo)
	code, body, _ = f.call(t, "GET", detail, "", "", cookie)
	if code != 200 || !strings.Contains(body, "draft_content") || !strings.Contains(body, "base_sha") {
		t.Fatalf("detail: %d %s", code, body)
	}
	cand, err := f.store.GetCandidate(context.Background(), repo, id)
	if err != nil {
		t.Fatal(err)
	}
	code, body, _ = f.call(t, "POST", "/api/git-memory/candidates/"+id+"/approve", `{"workspace":"`+repo+`","content":"reviewed architecture"}`, f.server.URL, cookie)
	if code != 200 {
		t.Fatalf("approve: %d %s", code, body)
	}
	var approved struct {
		ID string `json:"approved_memory_id"`
	}
	if err := json.Unmarshal([]byte(body), &approved); err != nil || approved.ID == "" {
		t.Fatalf("approve result: %s", body)
	}
	mem, err := f.store.GetMemory(context.Background(), repo, approved.ID)
	if err != nil || mem.Content != "reviewed architecture" || mem.Source != "git-approved" {
		t.Fatalf("formal memory: %+v %v", mem, err)
	}
	provenance, err := f.store.GetProvenance(context.Background(), approved.ID)
	if err != nil || provenance.BaseSHA != cand.BaseSHA || provenance.FactsSummary != cand.FactsSummary {
		t.Fatalf("provenance changed: %+v %v", provenance, err)
	}
	code, body, _ = f.call(t, "POST", "/api/git-memory/candidates/"+id+"/approve", `{"workspace":"`+repo+`"}`, f.server.URL, cookie)
	if code != 200 || !strings.Contains(body, approved.ID) {
		t.Fatalf("retry %d %s", code, body)
	}
	code, _, _ = f.call(t, "POST", "/api/git-memory/candidates/"+id+"/discard", `{"workspace":"`+repo+`"}`, f.server.URL, cookie)
	if code != 409 {
		t.Fatalf("discard approved %d", code)
	}
	for _, x := range []struct {
		id, status string
		partial    bool
	}{{"partial", "pending", true}, {"discarded", "discarded", false}, {"broken", "invalid", false}} {
		saveWebGit(t, f, repo, x.id, x.status, "draft", x.partial)
		code, _, _ = f.call(t, "POST", "/api/git-memory/candidates/"+x.id+"/approve", `{"workspace":"`+repo+`"}`, f.server.URL, cookie)
		if code != 409 && code != 422 {
			t.Fatalf("invalid approve %s %d", x.id, code)
		}
	}
	saveWebGit(t, f, repo, "to-discard", "pending", "draft", false)
	for i := 0; i < 2; i++ {
		code, _, _ = f.call(t, "POST", "/api/git-memory/candidates/to-discard/discard", `{"workspace":"`+repo+`"}`, f.server.URL, cookie)
		if code != 200 {
			t.Fatalf("discard retry %d", code)
		}
	}
	mems, err := f.store.ListMemories(context.Background(), repo)
	if err != nil || len(mems) != 1 {
		t.Fatalf("duplicate/invalid memory %d %v", len(mems), err)
	}
	code, _, _ = f.call(t, "GET", "/api/memories/"+approved.ID+"?workspace="+url.QueryEscape(repo), "", "", cookie)
	if code != 200 {
		t.Fatalf("existing memory view %d", code)
	}
}

func TestWEBGITHTTPBoundaryAndBounds(t *testing.T) {
	f := startWeb(t)
	cookie := f.login(t)
	ws := f.root
	saveWebGit(t, f, ws, "known", "pending", "draft", false)
	routes := []struct{ method, path, body string }{{"POST", "/api/git-memory/capture", `{"workspace":"` + ws + `","range":"HEAD~1..HEAD"}`}, {"GET", "/api/git-memory/candidates?workspace=" + url.QueryEscape(ws), ""}, {"GET", "/api/git-memory/candidates/known?workspace=" + url.QueryEscape(ws), ""}, {"POST", "/api/git-memory/candidates/known/approve", `{"workspace":"` + ws + `"}`}, {"POST", "/api/git-memory/candidates/known/discard", `{"workspace":"` + ws + `"}`}}
	for _, x := range routes {
		code, _, _ := f.call(t, x.method, x.path, x.body, f.server.URL, nil)
		if code != 401 {
			t.Fatalf("unauth %s %d", x.path, code)
		}
		if x.method == "POST" {
			code, _, _ = f.call(t, x.method, x.path, x.body, "https://evil.example", cookie)
			if code != 403 {
				t.Fatalf("origin %d", code)
			}
		}
	}
	for _, ws2 := range []string{f.outside, filepath.Join(ws, "child")} {
		if ws2 != f.outside {
			os.Mkdir(ws2, 0700)
		}
		code, body, _ := f.call(t, "GET", "/api/git-memory/candidates/known?workspace="+url.QueryEscape(ws2), "", "", cookie)
		want := 404
		if ws2 == f.outside {
			want = 403
		}
		if code != want || strings.Contains(body, "draft") {
			t.Fatalf("scope %d %s", code, body)
		}
	}
	for _, extra := range []string{`,"force":true`, `,"ai_draft":true`, `,"argv":["status"]`} {
		code, _, _ := f.call(t, "POST", "/api/git-memory/capture", `{"workspace":"`+ws+`","range":"HEAD~1..HEAD"`+extra+`}`, f.server.URL, cookie)
		if code != 400 {
			t.Fatalf("flag %d", code)
		}
	}
	for _, content := range []string{"", "   ", strings.Repeat("x", 32769)} {
		data, _ := json.Marshal(map[string]string{"workspace": ws, "content": content})
		code, _, _ := f.call(t, "POST", "/api/git-memory/candidates/known/approve", string(data), f.server.URL, cookie)
		want := 400
		if len(content) > 32768 {
			want = 413
		}
		if code != want {
			t.Fatalf("content bound %d want %d", code, want)
		}
	}
	saveWebGit(t, f, ws, "large", "pending", strings.Repeat("x", 32769), false)
	code, _, _ := f.call(t, "GET", "/api/git-memory/candidates/large?workspace="+url.QueryEscape(ws), "", "", cookie)
	if code != 413 {
		t.Fatalf("detail bound %d", code)
	}
	code, _, _ = f.call(t, "GET", "/api/git-memory/candidates?workspace="+url.QueryEscape(ws)+"&limit=51", "", "", cookie)
	if code != 400 {
		t.Fatalf("limit %d", code)
	}
	code, _, _ = f.call(t, "POST", "/api/git-memory/capture", `{"workspace":"`+ws+`","range":"HEAD~1..HEAD"}`, f.server.URL, cookie)
	if code != 422 {
		t.Fatalf("non-git %d", code)
	}
	mems, err := f.store.ListMemories(context.Background(), ws)
	if err != nil || len(mems) != 0 {
		t.Fatal("rejection wrote memory")
	}
}

func TestWEBGITHTTPPaging(t *testing.T) {
	f := startWeb(t)
	cookie := f.login(t)
	for i := 0; i < 10000; i++ {
		saveWebGit(t, f, f.root, fmt.Sprintf("item-%03d", i), "pending", strings.Repeat("草", 200), false)
	}
	cursor := ""
	seen := map[string]bool{}
	for {
		code, body, _ := f.call(t, "GET", "/api/git-memory/candidates?workspace="+url.QueryEscape(f.root)+"&limit=20&cursor="+url.QueryEscape(cursor), "", "", cookie)
		if code != 200 || len(body) > 512<<10 {
			t.Fatalf("page %d bytes %d", code, len(body))
		}
		var page struct {
			Items []storage.CandidateSummary `json:"items"`
			Next  string                     `json:"next_cursor"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatal(err)
		}
		for _, c := range page.Items {
			if seen[c.ID] || len(c.Summary) > 512 {
				t.Fatalf("page duplicate/size %s", c.ID)
			}
			seen[c.ID] = true
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if len(seen) != 10000 {
		t.Fatalf("paging lost rows: %d", len(seen))
	}
}

// 2026-09-30 WEBGIT-010：Task 接受時封存；candidate 永遠不能作為正式 recall。
func TestWEBGITRecallAndSealedContext(t *testing.T) {
	f := startWeb(t)
	cookie := f.login(t)
	ctx := context.Background()
	saveWebGit(t, f, f.root, "review", "pending", "architecture retention policy", false)
	submit := func(id string, explicit string) storage.Task {
		t.Helper()
		body := map[string]any{"id": id, "workspace": f.root, "prompt": "architecture retention policy", "memory_recall": "bounded"}
		if explicit != "" {
			body["memory_ids"] = []string{explicit}
		}
		data, _ := json.Marshal(body)
		code, out, _ := f.call(t, "POST", "/api/tasks", string(data), f.server.URL, cookie)
		if code != 202 {
			t.Fatalf("task %d %s", code, out)
		}
		var task storage.Task
		if err := json.Unmarshal([]byte(out), &task); err != nil {
			t.Fatal(err)
		}
		return task
	}
	before := submit("before-approve", "")
	if len(before.MemoryRefs) != 0 || strings.Contains(before.ResolvedContext, "git-approved") {
		t.Fatal("candidate leaked to recall")
	}
	code, out, _ := f.call(t, "POST", "/api/git-memory/candidates/review/approve", `{"workspace":"`+f.root+`"}`, f.server.URL, cookie)
	if code != 200 {
		t.Fatalf("approve %d %s", code, out)
	}
	var approved struct {
		ID string `json:"approved_memory_id"`
	}
	json.Unmarshal([]byte(out), &approved)
	explicit := submit("explicit-approved", approved.ID)
	if len(explicit.MemoryRefs) != 1 || explicit.MemoryRefs[0] != approved.ID || !strings.Contains(explicit.ResolvedContext, "architecture retention policy") {
		t.Fatalf("explicit %v", explicit.MemoryRefs)
	}
	auto := submit("auto-approved", "")
	if len(auto.MemoryRefs) != 1 || auto.MemoryRefs[0] != approved.ID {
		t.Fatalf("bounded recall %v", auto.MemoryRefs)
	}
	if err := f.store.DeleteMemory(ctx, f.root, approved.ID); err != nil {
		t.Fatal(err)
	}
	saveWebGit(t, f, f.root, "discarded-after", "pending", "later draft", false)
	code, _, _ = f.call(t, "POST", "/api/git-memory/candidates/discarded-after/discard", `{"workspace":"`+f.root+`"}`, f.server.URL, cookie)
	if code != 200 {
		t.Fatal("discard failed")
	}
	for _, original := range []storage.Task{before, explicit, auto} {
		got, err := f.store.GetTaskByRequestID(ctx, original.RequestID)
		if err != nil || got.ResolvedContext != original.ResolvedContext {
			t.Fatalf("sealed context changed: %v", err)
		}
	}
}

func TestWEBGITCaptureModesAndLegacyPayload(t *testing.T) {
	f := startWeb(t)
	repo := webGitRepo(t, f)
	cookie := f.login(t)
	ctx := context.Background()
	call := func(body string) (int, string) {
		code, out, _ := f.call(t, "POST", "/api/git-memory/capture", body, f.server.URL, cookie)
		return code, out
	}
	fixed := `{"workspace":"` + repo + `","range":"HEAD~1..HEAD"}`
	code, out := call(fixed)
	if code != 200 {
		t.Fatalf("fixed: %d %s", code, out)
	}
	var result struct {
		ID string `json:"candidate_id"`
	}
	json.Unmarshal([]byte(out), &result)
	cand, err := f.store.GetCandidate(ctx, repo, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateCandidateDraft(ctx, repo, cand.ID, strings.Repeat("x", 2<<20)); err != nil {
		t.Fatal(err)
	}
	code, out = call(fixed)
	if code != 200 || !strings.Contains(out, `"reused":true`) || !strings.Contains(out, `"detail_available":false`) {
		t.Fatalf("legacy reuse: %d %s", code, out)
	}
	code, _, _ = f.call(t, "POST", "/api/git-memory/candidates/"+cand.ID+"/approve", `{"workspace":"`+repo+`"}`, f.server.URL, cookie)
	if code != 413 {
		t.Fatalf("legacy approval %d", code)
	}
	code, _ = call(`{"workspace":"` + repo + `","range":"invalid..HEAD"}`)
	if code != 400 {
		t.Fatalf("invalid range %d", code)
	}
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v %v %s", args, err, out)
		}
		return out
	}
	for i := 0; i < 501; i++ {
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("file-%03d.txt", i)), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-m", "501 files")
	beforeHead := string(run("rev-parse", "HEAD"))
	beforeBranch := string(run("symbolic-ref", "HEAD"))
	beforeStatus := string(run("status", "--porcelain"))
	beforeConfig, err := os.ReadFile(filepath.Join(repo, ".git/config"))
	if err != nil {
		t.Fatal(err)
	}
	beforeIndex, err := os.ReadFile(filepath.Join(repo, ".git/index"))
	if err != nil {
		t.Fatal(err)
	}
	code, out = call(`{"workspace":"` + repo + `","since_last":true}`)
	if code != 200 || !strings.Contains(out, `"outcome":"Partial"`) {
		t.Fatalf("partial since-last %d %s", code, out)
	}
	json.Unmarshal([]byte(out), &result)
	code, _, _ = f.call(t, "POST", "/api/git-memory/candidates/"+result.ID+"/approve", `{"workspace":"`+repo+`"}`, f.server.URL, cookie)
	if code != 409 {
		t.Fatalf("captured Partial approved: %d", code)
	}
	cp, err := f.store.GetCheckpoint(ctx, repo, cand.RepoID, cand.RefName)
	if err != nil || cp.LastCapturedSHA != cand.HeadSHA {
		t.Fatalf("Partial advanced checkpoint: %+v %v", cp, err)
	}
	afterConfig, _ := os.ReadFile(filepath.Join(repo, ".git/config"))
	afterIndex, _ := os.ReadFile(filepath.Join(repo, ".git/index"))
	if string(run("rev-parse", "HEAD")) != beforeHead || string(run("symbolic-ref", "HEAD")) != beforeBranch || string(run("status", "--porcelain")) != beforeStatus || string(afterConfig) != string(beforeConfig) || string(afterIndex) != string(beforeIndex) {
		t.Fatal("capture mutated repo")
	}
}

func TestWEBGITHTTPMalformedAndEscapingBounds(t *testing.T) {
	f := startWeb(t)
	cookie := f.login(t)
	ws := f.root
	saveWebGit(t, f, ws, "known", "pending", "draft", false)
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/api/git-memory/candidates/known/approve", `{"workspace":"` + ws + `","content":null}`, 400},
		{"POST", "/api/git-memory/candidates/known/approve", `{"workspace":"` + ws + `","content":"x","base_sha":"fake"}`, 400},
		{"POST", "/api/git-memory/candidates/known/approve", `{"workspace":"` + ws + `","content":"x"} {}`, 400},
		{"POST", "/api/git-memory/candidates/known/approve", `{"workspace":"` + ws + `","content":"` + strings.Repeat("x", 70000) + `"}`, 413},
		{"POST", "/api/git-memory/capture", `{"workspace":"` + ws + `","range":"HEAD~1..HEAD","since_last":true}`, 400},
		{"GET", "/api/git-memory/candidates?workspace=" + url.QueryEscape(ws) + "&limit=20&limit=20", "", 400},
		{"GET", "/api/git-memory/candidates?workspace=" + url.QueryEscape(ws) + "&force=true", "", 400},
	} {
		code, out, _ := f.call(t, tc.method, tc.path, tc.body, f.server.URL, cookie)
		if code != tc.want {
			t.Fatalf("malformed %d want %d %s", code, tc.want, out)
		}
	}
	c := &storage.MemoryCandidate{ID: "escaped", Workspace: ws, RepoID: "repo", BaseSHA: "escape", HeadSHA: "head", FactsSummary: `{"diff":"` + strings.Repeat(`\\`, 131060) + `"}`, DraftContent: strings.Repeat("\x00", 32768)}
	if err := f.store.SaveCandidate(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	code, out, _ := f.call(t, "GET", "/api/git-memory/candidates/escaped?workspace="+url.QueryEscape(ws), "", "", cookie)
	if code != 413 || len(out) > 1024 {
		t.Fatalf("escaped JSON bound %d bytes %d", code, len(out))
	}
	saveWebGit(t, f, ws, "invalid-text", "pending", string([]byte{0xff}), false)
	code, _, _ = f.call(t, "GET", "/api/git-memory/candidates/invalid-text?workspace="+url.QueryEscape(ws), "", "", cookie)
	if code != 422 {
		t.Fatalf("legacy invalid UTF8 %d", code)
	}
	mems, err := f.store.ListMemories(context.Background(), ws)
	if err != nil || len(mems) != 0 {
		t.Fatal("malformed operation wrote memory")
	}
}

// 2026-09-30 WEBGIT-001：新 candidate read 明確跨 Origin 也必須拒絕。
func TestWEBGITCrossOriginRead(t *testing.T) {
	f := startWeb(t)
	cookie := f.login(t)
	saveWebGit(t, f, f.root, "known", "pending", "secret", false)
	for _, path := range []string{"/api/git-memory/candidates?workspace=" + url.QueryEscape(f.root), "/api/git-memory/candidates/known?workspace=" + url.QueryEscape(f.root)} {
		code, out, _ := f.call(t, "GET", path, "", "https://evil.example", cookie)
		if code != 403 || strings.Contains(out, "secret") {
			t.Fatalf("cross Origin read %d %s", code, out)
		}
	}
}
