package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// hashFile 計算檔案 SHA-256 雜湊
func hashFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// initProbeGitRepo 初始化隔離的測試 Git 儲存庫
func initProbeGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = []string{
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=/dev/null",
			"PATH=" + os.Getenv("PATH"),
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v, out: %s", args, err, string(out))
		}
	}

	runGit("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Probe Repo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "Initial commit")

	if err := os.WriteFile(filepath.Join(dir, "doc.txt"), []byte("line 1\nline 2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "doc.txt")
	runGit("commit", "-m", "Add doc.txt")

	return dir
}

// TestProbe_GitBoundary_ReadOnlyWorktree 探測唯讀操作是否保持原 repo 零修改
func TestProbe_GitBoundary_ReadOnlyWorktree(t *testing.T) {
	repoDir := initProbeGitRepo(t)

	// 記錄起始狀態
	getHead := func() string {
		out, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	initialHead := getHead()
	initialIndexHash, err := hashFile(filepath.Join(repoDir, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}

	// 執行各類唯讀安全 Git 讀取指令
	safeEnv := []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"PATH=" + os.Getenv("PATH"),
	}
	// 全域安全旗標（置於 subcommand 之前）
	safeGlobalFlags := []string{
		"-c", "core.fsmonitor=",
		"-c", "diff.external=",
		"-c", "diff.noprefix=false",
		"--no-replace-objects",
	}

	runSafeGit := func(subcmd string, args ...string) string {
		cmdArgs := append([]string{}, safeGlobalFlags...)
		cmdArgs = append(cmdArgs, subcmd)
		if subcmd == "diff" {
			cmdArgs = append(cmdArgs, "--no-ext-diff", "--no-textconv")
		}
		cmdArgs = append(cmdArgs, args...)

		cmd := exec.Command("git", cmdArgs...)
		cmd.Dir = repoDir
		cmd.Env = safeEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("safe git failed: %v, out: %s", err, string(out))
		}
		return string(out)
	}

	// 1. rev-parse
	rootOut := runSafeGit("rev-parse", "--show-toplevel")
	if strings.TrimSpace(rootOut) != repoDir {
		t.Fatalf("expected root %s, got %s", repoDir, rootOut)
	}

	// 2. log
	logOut := runSafeGit("log", "--format=%H|%s", "-n", "2")
	if !strings.Contains(logOut, "Initial commit") || !strings.Contains(logOut, "Add doc.txt") {
		t.Fatalf("unexpected log out: %s", logOut)
	}

	// 3. diff
	diffOut := runSafeGit("diff", "HEAD~1..HEAD")
	if !strings.Contains(diffOut, "+line 1") {
		t.Fatalf("unexpected diff: %s", diffOut)
	}

	// 驗證結束後狀態完全未被觸碰
	afterHead := getHead()
	if afterHead != initialHead {
		t.Fatalf("HEAD changed: %s -> %s", initialHead, afterHead)
	}

	afterIndexHash, err := hashFile(filepath.Join(repoDir, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if afterIndexHash != initialIndexHash {
		t.Fatalf("Index changed: %s -> %s", initialIndexHash, afterIndexHash)
	}

	statusOut, err := exec.Command("git", "-C", repoDir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(statusOut))) != 0 {
		t.Fatalf("Worktree not clean: %s", string(statusOut))
	}
}

// TestProbe_GitBoundary_ExternalDiffBlocked 探測外部 Diff 惡意腳本是否被安全旗標徹底阻斷
func TestProbe_GitBoundary_ExternalDiffBlocked(t *testing.T) {
	repoDir := initProbeGitRepo(t)
	canaryPath := filepath.Join(t.TempDir(), "canary.txt")

	// 建立惡意外部 diff 腳本，被呼叫時會寫入 canaryPath
	scriptPath := filepath.Join(t.TempDir(), "bad_external_diff.sh")
	scriptContent := "#!/bin/sh\necho 'MALICIOUS_DIFF_EXECUTED' > " + canaryPath + "\nexit 0\n"
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatal(err)
	}

	// 在 repo 內設定 diff.external
	cmd := exec.Command("git", "-C", repoDir, "config", "diff.external", scriptPath)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// 對照組：未帶安全旗標時，git diff 會觸發 external diff 腳本
	_ = os.Remove(canaryPath)
	unsafeCmd := exec.Command("git", "-C", repoDir, "diff", "HEAD~1..HEAD")
	_ = unsafeCmd.Run()
	if _, err := os.Stat(canaryPath); os.IsNotExist(err) {
		t.Log("Note: default git did not trigger external diff (or platform variation)")
	} else {
		t.Log("Confirmed: unsafe git diff triggered external diff script!")
	}

	// 實驗組：加入安全旗標後，強制禁用 external diff
	_ = os.Remove(canaryPath)
	safeArgs := []string{
		"-c", "diff.external=",
		"diff",
		"--no-ext-diff",
		"--no-textconv",
		"HEAD~1..HEAD",
	}
	safeCmd := exec.Command("git", safeArgs...)
	safeCmd.Dir = repoDir
	safeCmd.Env = []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"PATH=" + os.Getenv("PATH"),
	}
	out, err := safeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("safe diff failed: %v, out: %s", err, string(out))
	}

	if _, err := os.Stat(canaryPath); !os.IsNotExist(err) {
		t.Fatalf("SECURITY FAILURE: canary file was created! External diff was NOT blocked!")
	}
	if !strings.Contains(string(out), "+line 1") {
		t.Fatalf("expected internal diff output, got: %s", string(out))
	}
	t.Log("PASS: External diff script was completely suppressed by safe flags.")
}

// TestProbe_GitBoundary_BinaryAndStats 探測二進位檔案與 --numstat 邊界行為
func TestProbe_GitBoundary_BinaryAndStats(t *testing.T) {
	repoDir := initProbeGitRepo(t)

	// 新增二進位檔案
	binContent := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0x00, 0x12}
	if err := os.WriteFile(filepath.Join(repoDir, "image.bin"), binContent, 0644); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repoDir
		c.Env = []string{
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=/dev/null",
			"PATH=" + os.Getenv("PATH"),
		}
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v, out: %s", args, err, string(out))
		}
	}

	runGit("add", "image.bin")
	runGit("commit", "-m", "Add binary file")

	// 探測 diff --numstat
	numstatCmd := exec.Command("git", "-C", repoDir, "diff", "--numstat", "HEAD~1..HEAD")
	out, err := numstatCmd.Output()
	if err != nil {
		t.Fatal(err)
	}

	// Git numstat 對 binary 會輸出 "-       -       image.bin"
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	foundBinary := false
	for _, l := range lines {
		parts := strings.Fields(l)
		if len(parts) >= 3 && parts[0] == "-" && parts[1] == "-" && parts[2] == "image.bin" {
			foundBinary = true
			break
		}
	}
	if !foundBinary {
		t.Fatalf("expected binary marker '- - image.bin' in numstat, got: %s", string(out))
	}
	t.Log("PASS: Git numstat accurately reports binary file with '-' sentinel.")
}

// TestProbe_GitBoundary_ProcessGroupKill 探測行程群組終止機制
func TestProbe_GitBoundary_ProcessGroupKill(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sleep", "10")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	start := time.Now()
	err := cmd.Start()
	if err != nil {
		t.Fatal(err)
	}

	pgid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		// 超時強制終止整個進程組
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
	case <-done:
	}

	elapsed := time.Since(start)
	if elapsed > 1*time.Second {
		t.Fatalf("process group kill took too long: %v", elapsed)
	}
	t.Logf("PASS: Process group killed cleanly in %v", elapsed)
}

// TestProbe_GitBoundary_LimitReader 探測 io.LimitReader 截斷行為
func TestProbe_GitBoundary_LimitReader(t *testing.T) {
	largeData := bytes.Repeat([]byte("A"), 1024*1024) // 1 MiB
	reader := io.LimitReader(bytes.NewReader(largeData), 256*1024) // 限制 256 KiB

	buf, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(buf) != 256*1024 {
		t.Fatalf("expected 256KiB, got %d bytes", len(buf))
	}
	t.Log("PASS: LimitReader strictly bounds output to 256 KiB.")
}

// TestProbe_GitBoundary_GitDirEscape 探測 .git 逃逸至 allowed roots 外部時之檢測機制
func TestProbe_GitBoundary_GitDirEscape(t *testing.T) {
	parentDir := t.TempDir()
	allowedRoot := filepath.Join(parentDir, "allowed")
	outsideDir := filepath.Join(parentDir, "outside")
	if err := os.MkdirAll(allowedRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 在 allowedRoot 內建立 repo
	repoDir := filepath.Join(allowedRoot, "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 在 outsideDir 內建立實際的 gitdir
	externalGitDir := filepath.Join(outsideDir, "external.git")
	cmd := exec.Command("git", "init", "--bare", externalGitDir)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// 在 repoDir/.git 以檔案（gitdir: ...）或符號連結指向外部 gitdir
	gitFileContent := "gitdir: " + externalGitDir + "\n"
	if err := os.WriteFile(filepath.Join(repoDir, ".git"), []byte(gitFileContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 探測 git rev-parse --git-dir
	gitDirCmd := exec.Command("git", "-C", repoDir, "rev-parse", "--git-dir")
	out, err := gitDirCmd.Output()
	if err != nil {
		t.Fatalf("rev-parse --git-dir failed: %v", err)
	}
	resolvedGitDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(resolvedGitDir) {
		resolvedGitDir = filepath.Clean(filepath.Join(repoDir, resolvedGitDir))
	}
	evalGitDir, err := filepath.EvalSymlinks(resolvedGitDir)
	if err != nil {
		evalGitDir = resolvedGitDir
	}

	// 比對是否超出 allowedRoot
	cleanedAllowed := filepath.Clean(allowedRoot)
	isInside := evalGitDir == cleanedAllowed || strings.HasPrefix(evalGitDir, cleanedAllowed+string(filepath.Separator))
	if isInside {
		t.Fatalf("expected git-dir %s to be OUTSIDE %s", evalGitDir, cleanedAllowed)
	}
	t.Logf("PASS: Successfully detected external gitdir escape (%s outside %s)", evalGitDir, cleanedAllowed)
}
