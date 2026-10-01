package daemon_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/storage"
)

// 2026-09-30 WEBGIT-002/007/008：Web 專用入口不能繞過 scope、Partial 與禁用 flags。
func TestWEBGITBoundedIPC(t *testing.T) {
	root := t.TempDir()
	repo := initTestGitRepoInDaemon(t, root)
	s, e := storage.NewSQLiteStore(filepath.Join(root, "d.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	sock := filepath.Join(root, "d.sock")
	d := daemon.NewDaemon(daemon.Config{SocketPath: sock}, s, auth.NewPolicy([]string{root}))
	if e := d.Start(); e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	call := func(action, ws, id string, force bool) map[string]any {
		t.Helper()
		c, e := net.Dial("unix", sock)
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close()
		if e := json.NewEncoder(c).Encode(daemon.IPCRequest{Action: action, Workspace: ws, CandidateID: id, Status: "pending", Limit: 20, Range: "HEAD~1..HEAD", Force: force}); e != nil {
			t.Fatal(e)
		}
		var v map[string]any
		if e := json.NewDecoder(c).Decode(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	r := call("git_memory_page", repo, "", false)
	if r["error"] != nil || r["candidate_page"] == nil {
		t.Fatalf("missing bounded IPC: %v", r)
	}
	c := &storage.MemoryCandidate{ID: "partial", Workspace: repo, RepoID: "repo", BaseSHA: "a", HeadSHA: "b", FactsSummary: "{}", DraftContent: "draft", IsTruncated: true}
	if e := s.SaveCandidate(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	r = call("git_memory_approve_bounded", repo, "partial", false)
	if r["git_error"] != "conflict" {
		t.Fatalf("Partial: %v", r)
	}
	r = call("git_memory_detail_bounded", root, "partial", false)
	if r["git_error"] != "not_found" {
		t.Fatalf("scope: %v", r)
	}
	r = call("git_memory_capture_bounded", repo, "", true)
	if r["git_error"] != "invalid" {
		t.Fatalf("force: %v", r)
	}
	mems, e := s.ListMemories(context.Background(), repo)
	if e != nil || len(mems) != 0 {
		t.Fatal("invalid approve wrote memory")
	}
}

func TestWEBGITIPCReauthorization(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	s, e := storage.NewSQLiteStore(filepath.Join(root, "data.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	cand := &storage.MemoryCandidate{ID: "known", Workspace: root, RepoID: "repo", BaseSHA: "base", HeadSHA: "head", FactsSummary: "{}", DraftContent: "secret"}
	if e := s.SaveCandidate(context.Background(), cand); e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(root, "alias")
	if e := os.Symlink(root, alias); e != nil {
		t.Fatal(e)
	}
	sock := filepath.Join(root, "d.sock")
	call := func(action, ws string) map[string]any {
		t.Helper()
		c, e := net.Dial("unix", sock)
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close()
		json.NewEncoder(c).Encode(daemon.IPCRequest{Action: action, Workspace: ws, CandidateID: "known", Range: "HEAD~1..HEAD"})
		var v map[string]any
		if e := json.NewDecoder(c).Decode(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	d := daemon.NewDaemon(daemon.Config{SocketPath: sock}, s, auth.NewPolicy([]string{root}))
	if e := d.Start(); e != nil {
		t.Fatal(e)
	}
	r := call("git_memory_detail_bounded", alias)
	if r["error"] != nil || r["candidate"] == nil {
		t.Fatalf("canonical alias: %v", r)
	}
	os.Remove(alias)
	if e := os.Symlink(outside, alias); e != nil {
		t.Fatal(e)
	}
	for _, action := range []string{"git_memory_detail_bounded", "git_memory_approve_bounded", "git_memory_discard_bounded", "git_memory_capture_bounded"} {
		r := call(action, alias)
		if r["git_error"] != "forbidden" {
			t.Fatalf("escape %s %v", action, r)
		}
	}
	d.Close()
	d = daemon.NewDaemon(daemon.Config{SocketPath: sock}, s, auth.NewPolicy([]string{outside}))
	if e := d.Start(); e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	for _, action := range []string{"git_memory_detail_bounded", "git_memory_approve_bounded", "git_memory_discard_bounded", "git_memory_capture_bounded"} {
		r := call(action, root)
		if r["git_error"] != "forbidden" {
			t.Fatalf("revocation %s %v", action, r)
		}
	}
	got, e := s.GetCandidate(context.Background(), root, "known")
	if e != nil || got.Status != "pending" {
		t.Fatalf("revocation mutated store %v", e)
	}
}
