package gitmem

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

var (
	// ErrStdoutTruncated 代表子程序標準輸出超過硬性緩衝上限
	ErrStdoutTruncated = errors.New("command stdout exceeded buffer limit")
)

const (
	defaultTimeout = 10 * time.Second
	maxStdoutBytes = 512 * 1024
	maxStderrBytes = 64 * 1024
)

// RepoInfo 儲存經過安全驗證與解析後的 Git 儲存庫邊界資訊
type RepoInfo struct {
	Workspace   string
	RepoRoot    string
	RepoID      string
	CurrentHead string
	CurrentRef  string
	GitDir      string
}

// GitExecutor 提供安全隔離的 Git CLI 子程序執行能力
type GitExecutor struct {
	allowedRoots []string
	timeout      time.Duration
}

// NewGitExecutor 建立新的 Git 執行器，限制工作區必須位於 allowedRoots 內
func NewGitExecutor(allowedRoots []string) *GitExecutor {
	cleanedRoots := make([]string, 0, len(allowedRoots))
	for _, r := range allowedRoots {
		abs, err := filepath.Abs(r)
		if err == nil {
			eval, err := filepath.EvalSymlinks(abs)
			if err == nil {
				cleanedRoots = append(cleanedRoots, eval)
			} else {
				cleanedRoots = append(cleanedRoots, abs)
			}
		}
	}
	return &GitExecutor{
		allowedRoots: cleanedRoots,
		timeout:      defaultTimeout,
	}
}

// SetTimeout 設定執行超時上限
func (g *GitExecutor) SetTimeout(d time.Duration) {
	if d > 0 {
		g.timeout = d
	}
}

// isWithinAllowedRoots 檢查路徑是否位於任何一個允許的根目錄內
func (g *GitExecutor) isWithinAllowedRoots(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	target, err := filepath.EvalSymlinks(abs)
	if err != nil {
		target = abs
	}

	for _, root := range g.allowedRoots {
		rel, err := filepath.Rel(root, target)
		if err == nil && (rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))) {
			return true
		}
	}
	return false
}

// ValidateAndResolveRepo 檢查目標目錄是否合法，解析 Git 邊界並回傳 Repo 資訊
func (g *GitExecutor) ValidateAndResolveRepo(ctx context.Context, targetDir string) (*RepoInfo, error) {
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return nil, fmt.Errorf("invalid target directory: %w", err)
	}
	canonicalTarget, err := filepath.EvalSymlinks(absTarget)
	if err != nil {
		canonicalTarget = absTarget
	}

	if !g.isWithinAllowedRoots(canonicalTarget) {
		return nil, fmt.Errorf("target directory outside allowed roots: %s", targetDir)
	}

	// 1. 取得 GitDir 並校驗是否越界
	gitDirOut, err := g.Exec(ctx, canonicalTarget, "rev-parse", "--git-dir")
	if err != nil {
		outStr := string(gitDirOut)
		if strings.Contains(outStr, "not a git repository") || strings.Contains(err.Error(), "exit status 128") {
			return nil, fmt.Errorf("not a git repository: %s", targetDir)
		}
		return nil, fmt.Errorf("failed to resolve git directory: %w (%s)", err, outStr)
	}
	rawGitDir := strings.TrimSpace(string(gitDirOut))
	var gitDir string
	if filepath.IsAbs(rawGitDir) {
		gitDir = rawGitDir
	} else {
		gitDir = filepath.Join(canonicalTarget, rawGitDir)
	}
	canonicalGitDir, err := filepath.EvalSymlinks(gitDir)
	if err != nil {
		canonicalGitDir = gitDir
	}
	if !g.isWithinAllowedRoots(canonicalGitDir) {
		return nil, fmt.Errorf("gitdir outside allowed roots: %s", canonicalGitDir)
	}

	// 2. 檢查 alternates 是否越界
	alternatesFile := filepath.Join(canonicalGitDir, "objects", "info", "alternates")
	if altData, err := os.ReadFile(alternatesFile); err == nil {
		lines := strings.Split(string(altData), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			var altPath string
			if filepath.IsAbs(line) {
				altPath = line
			} else {
				altPath = filepath.Join(canonicalGitDir, "objects", line)
			}
			if !g.isWithinAllowedRoots(altPath) {
				return nil, fmt.Errorf("alternates object store outside allowed roots: %s", altPath)
			}
		}
	}

	// 3. 取得 Repo 根目錄
	topOut, err := g.Exec(ctx, canonicalTarget, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve repo root: %w", err)
	}
	repoRoot := strings.TrimSpace(string(topOut))
	canonicalRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		canonicalRepoRoot = repoRoot
	}
	if !g.isWithinAllowedRoots(canonicalRepoRoot) {
		return nil, fmt.Errorf("repo root outside allowed roots: %s", repoRoot)
	}

	// 4. 取得當前 HEAD commit SHA
	headOut, err := g.Exec(ctx, canonicalTarget, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve current HEAD: %w", err)
	}
	currentHead := strings.TrimSpace(string(headOut))

	// 5. 取得當前 ref 名稱
	refOut, err := g.Exec(ctx, canonicalTarget, "symbolic-ref", "-q", "HEAD")
	var currentRef string
	if err == nil {
		currentRef = strings.TrimSpace(string(refOut))
	} else {
		// detached HEAD
		currentRef = "HEAD"
	}

	// 6. 計算 RepoID (Root commit SHA + Canonical Repo Root SHA-256 前 16 碼)
	rootOut, err := g.Exec(ctx, canonicalTarget, "rev-list", "--max-parents=0", "HEAD")
	var rootSHA string
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(rootOut)), "\n")
		var validRoots []string
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if len(l) == 40 {
				validRoots = append(validRoots, l)
			}
		}
		if len(validRoots) > 0 {
			sort.Strings(validRoots)
			rootSHA = validRoots[0]
		}
	}
	if rootSHA == "" {
		rootSHA = currentHead
	}

	pathHash := sha256.Sum256([]byte(canonicalRepoRoot))
	repoID := fmt.Sprintf("%s-%x", rootSHA[:min(len(rootSHA), 12)], pathHash[:8])

	return &RepoInfo{
		Workspace:   canonicalTarget,
		RepoRoot:    canonicalRepoRoot,
		RepoID:      repoID,
		CurrentHead: currentHead,
		CurrentRef:  currentRef,
		GitDir:      canonicalGitDir,
	}, nil
}

// Exec 在指定目錄安全執行 git 命令，自動套用安全旗標與資源限制
func (g *GitExecutor) Exec(ctx context.Context, dir string, args ...string) ([]byte, error) {
	// 基礎安全全域旗標，嚴格置於子命令之前
	cmdArgs := []string{
		"-c", "diff.external=",
		"-c", "core.fsmonitor=",
		"-c", "core.alternateRefsCommand=",
		"-c", "diff.noprefix=false",
		"-c", "remote.*.promisor=false",
		"-c", "remote.*.partialclonefilter=",
		"--no-replace-objects",
	}

	// 若為 diff 子命令，在子命令之後注入防護旗標
	if len(args) > 0 && args[0] == "diff" {
		cmdArgs = append(cmdArgs, "diff", "--no-ext-diff", "--no-textconv")
		cmdArgs = append(cmdArgs, args[1:]...)
	} else {
		cmdArgs = append(cmdArgs, args...)
	}

	return g.ExecRaw(ctx, dir, "git", cmdArgs...)
}

type limitBuffer struct {
	buf       bytes.Buffer
	limit     int64
	truncated bool
}

func (l *limitBuffer) Write(p []byte) (n int, err error) {
	remaining := l.limit - int64(l.buf.Len())
	if remaining <= 0 {
		l.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		l.truncated = true
		p = p[:remaining]
	}
	return l.buf.Write(p)
}

func (l *limitBuffer) Bytes() []byte {
	return l.buf.Bytes()
}

func (l *limitBuffer) String() string {
	return l.buf.String()
}

// ExecRaw 執行底層命令，套用 Process Group 隔離、超時終止與輸出緩衝上限
func (g *GitExecutor) ExecRaw(ctx context.Context, dir string, bin string, args ...string) ([]byte, error) {
	timeout := g.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Dir = dir
	cmd.Env = []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_NO_LAZY_FETCH=1",
		"LC_ALL=C",
		"PATH=" + os.Getenv("PATH"),
	}

	// 啟用獨立 Process Group，超時或取消時透過 SIGKILL 回收整個進程樹
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 100 * time.Millisecond

	stdoutBuf := &limitBuffer{limit: maxStdoutBytes}
	stderrBuf := &limitBuffer{limit: maxStderrBytes}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	runErr := cmd.Run()

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return stdoutBuf.Bytes(), fmt.Errorf("command timed out after %v: %w", timeout, runCtx.Err())
	}
	if errors.Is(runCtx.Err(), context.Canceled) {
		return stdoutBuf.Bytes(), fmt.Errorf("command canceled: %w", runCtx.Err())
	}

	if stdoutBuf.truncated {
		return stdoutBuf.Bytes(), ErrStdoutTruncated
	}

	if runErr != nil {
		errMsg := strings.TrimSpace(stderrBuf.String())
		if errMsg != "" {
			return stdoutBuf.Bytes(), fmt.Errorf("%w: %s", runErr, errMsg)
		}
		return stdoutBuf.Bytes(), runErr
	}

	return stdoutBuf.Bytes(), nil
}

// IsAncestor 檢查 ancestorSHA 是否為 descendantSHA 的祖先 commits
func (g *GitExecutor) IsAncestor(ctx context.Context, dir string, ancestorSHA, descendantSHA string) (bool, error) {
	if ancestorSHA == descendantSHA {
		return true, nil
	}
	_, err := g.Exec(ctx, dir, "merge-base", "--is-ancestor", ancestorSHA, descendantSHA)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("check git ancestry: %w", err)
	}
	return true, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
