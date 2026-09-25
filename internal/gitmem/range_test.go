package gitmem

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestRepoWithBranches(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	allowedRoot := filepath.Join(dir, "allowed")
	repoDir := filepath.Join(allowedRoot, "project")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = []string{
			"GIT_AUTHOR_NAME=Tester",
			"GIT_AUTHOR_EMAIL=tester@example.com",
			"GIT_COMMITTER_NAME=Tester",
			"GIT_COMMITTER_EMAIL=tester@example.com",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=/dev/null",
			"PATH=" + os.Getenv("PATH"),
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v, out: %s", args, err, string(out))
		}
		return strings.TrimSpace(string(out))
	}

	runGit("init", "-b", "main")
	_ = os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("c1"), 0644)
	runGit("add", "file1.txt")
	runGit("commit", "-m", "commit 1")

	_ = os.WriteFile(filepath.Join(repoDir, "file2.txt"), []byte("c2"), 0644)
	runGit("add", "file2.txt")
	runGit("commit", "-m", "commit 2")

	_ = os.WriteFile(filepath.Join(repoDir, "file3.txt"), []byte("c3"), 0644)
	runGit("add", "file3.txt")
	runGit("commit", "-m", "commit 3")

	// 建立分叉分支 feature
	runGit("checkout", "-b", "feature", "HEAD~1") // 基於 commit 2
	_ = os.WriteFile(filepath.Join(repoDir, "feature.txt"), []byte("f1"), 0644)
	runGit("add", "feature.txt")
	runGit("commit", "-m", "feature commit")

	// 切回 main
	runGit("checkout", "main")

	return allowedRoot, repoDir
}

func TestResolveRange_ValidRange(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	resolved, err := exec.ResolveRange(ctx, repoDir, "HEAD~2..HEAD")
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if len(resolved.BaseSHA) != 40 || len(resolved.HeadSHA) != 40 {
		t.Fatalf("expected 40-char commit SHAs, got base=%s, head=%s", resolved.BaseSHA, resolved.HeadSHA)
	}
	if len(resolved.Commits) != 2 {
		t.Fatalf("expected 2 commits in HEAD~2..HEAD, got %d", len(resolved.Commits))
	}
	if resolved.Commits[0].Subject != "commit 2" {
		t.Errorf("expected first commit subject 'commit 2', got '%s'", resolved.Commits[0].Subject)
	}
	if resolved.Commits[1].Subject != "commit 3" {
		t.Errorf("expected second commit subject 'commit 3', got '%s'", resolved.Commits[1].Subject)
	}
}

func TestResolveRange_SingleCommit(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	resolved, err := exec.ResolveRange(ctx, repoDir, "HEAD")
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if len(resolved.Commits) != 1 {
		t.Fatalf("expected 1 commit for single commit range, got %d", len(resolved.Commits))
	}
	if resolved.Commits[0].Subject != "commit 3" {
		t.Errorf("expected commit 3, got '%s'", resolved.Commits[0].Subject)
	}
}

func TestResolveRange_InvalidRef(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	_, err := exec.ResolveRange(ctx, repoDir, "nonexistent..HEAD")
	if err == nil {
		t.Fatal("expected error for nonexistent base ref, got nil")
	}
	if !strings.Contains(err.Error(), "invalid revision") && !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected invalid ref error, got: %v", err)
	}
}

func TestResolveRange_NotAncestor(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	// main HEAD (commit 3) 與 feature HEAD 是分叉的，main HEAD 不是 feature HEAD 的祖先
	_, err := exec.ResolveRange(ctx, repoDir, "main..feature")
	if err == nil {
		t.Fatal("expected error when base is not an ancestor of head, got nil")
	}
	if !strings.Contains(err.Error(), "not an ancestor") {
		t.Errorf("expected 'not an ancestor' error, got: %v", err)
	}
}

func TestResolveRange_UnrelatedHistory(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	// 建立一個孤立分支 unrelated (無共同祖先)
	cmd := execCommand(repoDir, "checkout", "--orphan", "unrelated")
	_ = cmd.Run()
	_ = os.WriteFile(filepath.Join(repoDir, "unrelated.txt"), []byte("orphan"), 0644)
	cmd = execCommand(repoDir, "add", "unrelated.txt")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "orphan commit")
	_ = cmd.Run()

	_, err := exec.ResolveRange(ctx, repoDir, "main..unrelated")
	if err == nil {
		t.Fatal("expected error for unrelated history, got nil")
	}
	if !strings.Contains(err.Error(), "not an ancestor") && !strings.Contains(err.Error(), "unrelated history") {
		t.Errorf("expected unrelated history or not an ancestor error, got: %v", err)
	}
}

func TestResolveRange_EmptyRange(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	_, err := exec.ResolveRange(ctx, repoDir, "HEAD..HEAD")
	if err == nil {
		t.Fatal("expected error for empty range, got nil")
	}
	if !strings.Contains(err.Error(), "empty range") {
		t.Errorf("expected empty range error, got: %v", err)
	}
}

func TestResolveRange_TooManyCommits(t *testing.T) {
	dir := t.TempDir()
	allowedRoot := filepath.Join(dir, "allowed")
	repoDir := filepath.Join(allowedRoot, "project")
	_ = os.MkdirAll(repoDir, 0755)

	cmd := execCommand(repoDir, "init", "-b", "main")
	_ = cmd.Run()

	// 建立 105 個 commits
	for i := 1; i <= 105; i++ {
		_ = os.WriteFile(filepath.Join(repoDir, "dummy.txt"), []byte(fmt.Sprintf("%d", i)), 0644)
		cmd = execCommand(repoDir, "add", "dummy.txt")
		_ = cmd.Run()
		cmd = execCommand(repoDir, "commit", "-m", fmt.Sprintf("commit %d", i))
		_ = cmd.Run()
	}

	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	_, err := exec.ResolveRange(ctx, repoDir, "HEAD~102..HEAD")
	if err == nil {
		t.Fatal("expected error when commits exceed limit, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum limit") {
		t.Errorf("expected exceeds limit error, got: %v", err)
	}
}

func execCommand(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{
		"GIT_AUTHOR_NAME=Tester",
		"GIT_AUTHOR_EMAIL=tester@example.com",
		"GIT_COMMITTER_NAME=Tester",
		"GIT_COMMITTER_EMAIL=tester@example.com",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"PATH=" + os.Getenv("PATH"),
	}
	return cmd
}
