package gitmem

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func initTestGitRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	allowedRoot := filepath.Join(dir, "allowed")
	if err := os.MkdirAll(allowedRoot, 0755); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(allowedRoot, "project")
	runGit := func(args ...string) {
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
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v, out: %s", args, err, string(out))
		}
	}

	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	runGit("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "First commit")

	if err := os.WriteFile(filepath.Join(repoDir, "file.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "file.go")
	runGit("commit", "-m", "Second commit")

	return allowedRoot, repoDir
}

func TestGitExecutor_ValidateAndResolveRepo_Success(t *testing.T) {
	allowedRoot, repoDir := initTestGitRepo(t)

	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	info, err := exec.ValidateAndResolveRepo(ctx, repoDir)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if info.RepoRoot != repoDir {
		t.Errorf("expected RepoRoot %s, got %s", repoDir, info.RepoRoot)
	}
	if info.Workspace != repoDir {
		t.Errorf("expected Workspace %s, got %s", repoDir, info.Workspace)
	}
	if len(info.CurrentHead) != 40 {
		t.Errorf("expected 40-byte commit SHA, got %s", info.CurrentHead)
	}
	if info.CurrentRef != "refs/heads/main" {
		t.Errorf("expected refs/heads/main, got %s", info.CurrentRef)
	}
	if info.RepoID == "" {
		t.Errorf("expected non-empty RepoID")
	}
}

func TestGitExecutor_ValidateAndResolveRepo_Subdirectory(t *testing.T) {
	allowedRoot, repoDir := initTestGitRepo(t)
	subDir := filepath.Join(repoDir, "sub", "pkg")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	info, err := exec.ValidateAndResolveRepo(ctx, subDir)
	if err != nil {
		t.Fatalf("expected success on subdirectory, got error: %v", err)
	}
	if info.RepoRoot != repoDir {
		t.Errorf("expected RepoRoot to be %s, got %s", repoDir, info.RepoRoot)
	}
	if info.Workspace != subDir {
		t.Errorf("expected Workspace to be %s, got %s", subDir, info.Workspace)
	}
}

func TestGitExecutor_ValidateAndResolveRepo_NotGit(t *testing.T) {
	dir := t.TempDir()
	exec := NewGitExecutor([]string{dir})
	ctx := context.Background()

	_, err := exec.ValidateAndResolveRepo(ctx, dir)
	if err == nil {
		t.Fatal("expected error for non-git directory, got nil")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("expected not a git repository error, got: %v", err)
	}
}

func TestGitExecutor_ValidateAndResolveRepo_OutsideAllowedRoots(t *testing.T) {
	dir := t.TempDir()
	allowedRoot := filepath.Join(dir, "allowed")
	outsideRoot := filepath.Join(dir, "outside")
	_ = os.MkdirAll(allowedRoot, 0755)
	_ = os.MkdirAll(outsideRoot, 0755)

	// repo 在 outsideRoot
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = outsideRoot
	_ = cmd.Run()

	executor := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	_, err := executor.ValidateAndResolveRepo(ctx, outsideRoot)
	if err == nil {
		t.Fatal("expected error for repo outside allowed roots, got nil")
	}
	if !strings.Contains(err.Error(), "outside allowed roots") {
		t.Errorf("expected outside allowed roots error, got: %v", err)
	}
}

func TestGitExecutor_ValidateAndResolveRepo_GitDirEscape(t *testing.T) {
	dir := t.TempDir()
	allowedRoot := filepath.Join(dir, "allowed")
	outsideDir := filepath.Join(dir, "outside")
	_ = os.MkdirAll(allowedRoot, 0755)
	_ = os.MkdirAll(outsideDir, 0755)

	repoDir := filepath.Join(allowedRoot, "repo")
	_ = os.MkdirAll(repoDir, 0755)
	externalGit := filepath.Join(outsideDir, "external.git")
	cmd := exec.Command("git", "init", "--bare", externalGit)
	_ = cmd.Run()

	_ = os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: "+externalGit+"\n"), 0644)

	executor := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	_, err := executor.ValidateAndResolveRepo(ctx, repoDir)
	if err == nil {
		t.Fatal("expected error for gitdir escaping allowed roots, got nil")
	}
	if !strings.Contains(err.Error(), "outside allowed roots") {
		t.Errorf("expected gitdir outside allowed roots, got: %v", err)
	}
}

func TestGitExecutor_Exec_ExternalDiffBlocked(t *testing.T) {
	allowedRoot, repoDir := initTestGitRepo(t)
	canaryPath := filepath.Join(t.TempDir(), "canary.txt")

	scriptPath := filepath.Join(t.TempDir(), "bad_diff.sh")
	scriptContent := "#!/bin/sh\necho 'MALICIOUS' > " + canaryPath + "\nexit 0\n"
	_ = os.WriteFile(scriptPath, []byte(scriptContent), 0755)

	cmd := exec.Command("git", "-C", repoDir, "config", "diff.external", scriptPath)
	_ = cmd.Run()

	executor := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	out, err := executor.Exec(ctx, repoDir, "diff", "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("executor diff failed: %v, out: %s", err, string(out))
	}

	if _, err := os.Stat(canaryPath); !os.IsNotExist(err) {
		t.Fatal("SECURITY BUG: External diff was executed!")
	}
	if !strings.Contains(string(out), "+package main") {
		t.Fatalf("expected internal diff output, got: %s", string(out))
	}
}

func TestGitExecutor_Exec_TimeoutWithProcessGroup(t *testing.T) {
	allowedRoot, repoDir := initTestGitRepo(t)
	executor := NewGitExecutor([]string{allowedRoot})
	executor.SetTimeout(150 * time.Millisecond)

	ctx := context.Background()
	start := time.Now()
	// sleep 模擬超時
	_, err := executor.ExecRaw(ctx, repoDir, "sleep", "5")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("timeout took too long to terminate: %v", elapsed)
	}
}

func TestGitExecutor_NoLazyFetchWithPromisorRemote(t *testing.T) {
	allowedRoot, repoDir := initTestGitRepo(t)

	// 配置 promisor remote 與 partial clone 擴充設定
	cmd := exec.Command("git", "-C", repoDir, "config", "remote.origin.url", "http://127.0.0.1:59999/repo.git")
	_ = cmd.Run()
	cmd = exec.Command("git", "-C", repoDir, "config", "remote.origin.promisor", "true")
	_ = cmd.Run()
	cmd = exec.Command("git", "-C", repoDir, "config", "remote.origin.partialclonefilter", "blob:none")
	_ = cmd.Run()

	executor := NewGitExecutor([]string{allowedRoot})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 嘗試檢查一個本地不存在的 SHA
	// 在未注入 GIT_NO_LAZY_FETCH=1 時，Git 會嘗試對外連線 origin
	// 注入 GIT_NO_LAZY_FETCH=1 與 promisor=false 後，Git 會立即失敗並輸出 lazy fetching disabled，絕不進行連線
	out, err := executor.Exec(ctx, repoDir, "cat-file", "-e", "0123456789abcdef0123456789abcdef01234567")
	if err == nil {
		t.Fatal("expected error for missing object, got nil")
	}

	outStr := string(out)
	if strings.Contains(outStr, "Connection refused") || strings.Contains(outStr, "Failed to connect") {
		t.Fatalf("SECURITY VIOLATION: Git attempted network connection to promisor remote! Output: %s", outStr)
	}
}

func TestGitExecutor_IsAncestor_DistinguishesNotAncestorFromExecutionErrors(t *testing.T) {
	allowedRoot, repoDir := initTestGitRepo(t)
	executor := NewGitExecutor([]string{allowedRoot})

	head, err := executor.Exec(context.Background(), repoDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := executor.Exec(context.Background(), repoDir, "rev-parse", "HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	isAncestor, err := executor.IsAncestor(context.Background(), repoDir, strings.TrimSpace(string(head)), strings.TrimSpace(string(parent)))
	if err != nil {
		t.Fatalf("ordinary not-ancestor result returned error: %v", err)
	}
	if isAncestor {
		t.Fatal("expected child commit not to be ancestor of its parent")
	}

	if _, err := executor.IsAncestor(context.Background(), repoDir, "not-a-valid-sha", strings.TrimSpace(string(head))); err == nil {
		t.Fatal("expected invalid SHA to return an execution error")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := executor.IsAncestor(canceled, repoDir, strings.TrimSpace(string(parent)), strings.TrimSpace(string(head))); err == nil {
		t.Fatal("expected canceled context to return an error")
	}
}
