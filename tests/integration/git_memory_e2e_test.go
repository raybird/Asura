package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/storage"
)

func initComplexGitRepo(t *testing.T, baseDir string) string {
	t.Helper()
	repoDir := filepath.Join(baseDir, "complex_project")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = []string{
			"GIT_AUTHOR_NAME=Developer",
			"GIT_AUTHOR_EMAIL=dev@example.com",
			"GIT_COMMITTER_NAME=Developer",
			"GIT_COMMITTER_EMAIL=dev@example.com",
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
	_ = os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Complex Project\n"), 0644)
	runGit("add", "README.md")
	runGit("commit", "-m", "docs: initial readme")

	_ = os.WriteFile(filepath.Join(repoDir, "auth.go"), []byte("package auth\nfunc Login() {}\n"), 0644)
	runGit("add", "auth.go")
	runGit("commit", "-m", "feat(auth): initial login")

	// 建立 feature 分支並進行檔案修改與刪檔
	runGit("checkout", "-b", "feature/auth")
	_ = os.WriteFile(filepath.Join(repoDir, "auth_oauth.go"), []byte("package auth\nfunc OAuth() {}\n"), 0644)
	_ = os.Remove(filepath.Join(repoDir, "README.md"))
	runGit("add", "-A")
	runGit("commit", "-m", "feat(auth): add oauth and remove old readme")

	// 切回 main 並 merge feature/auth
	runGit("checkout", "main")
	runGit("merge", "--no-ff", "feature/auth", "-m", "merge: merge feature/auth into main")

	return repoDir
}

func TestGitMemory_EndToEnd_FullWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := initComplexGitRepo(t, tmpDir)

	dbPath := filepath.Join(tmpDir, "e2e_gitmem.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	sockPath := filepath.Join(tmpDir, "e2e_gitmem.sock")

	mockRunner := func(ctx context.Context, ws, prompt string) (string, error) {
		return "Mock runner executed prompt: " + prompt, nil
	}
	eng := engine.NewEngine(store, policy, engine.WithRunner(mockRunner))

	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	var client *daemon.Client
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := daemon.NewClient(sockPath); err == nil {
			client = c
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if client == nil {
		t.Fatalf("failed to connect to daemon socket")
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Capture 完整擷取（涵蓋 merge commit、新增與刪除檔案）
	capResp, err := client.CaptureGitMemory(ctx, repoDir, "HEAD~2..HEAD", false, false)
	if err != nil {
		t.Fatalf("CaptureGitMemory failed: %v", err)
	}
	if capResp.Candidate == nil || capResp.Candidate.ID == "" {
		t.Fatalf("expected candidate generated: %+v", capResp)
	}
	candID := capResp.Candidate.ID

	// 驗證變更事實中包含 auth_oauth.go 與刪除的 README.md
	if capResp.GitFacts == nil || !strings.Contains(capResp.GitFacts.DiffStatSummary, "auth_oauth.go") {
		t.Errorf("expected diff stat to mention auth_oauth.go")
	}

	// 2. Candidates 列表查詢
	cands, err := client.ListGitCandidates(ctx, repoDir, "pending", 50)
	if err != nil {
		t.Fatalf("ListGitCandidates failed: %v", err)
	}
	if len(cands) != 1 || cands[0].ID != candID {
		t.Fatalf("expected 1 candidate %s, got %v", candID, cands)
	}

	// 3. 人工審核核准（Approve）
	decisionContent := "架構決策：整合 OAuth 授權認證並廢棄舊版說明文件，保持唯讀安全邊界"
	memID, mem, err := client.ApproveGitCandidate(ctx, repoDir, candID, decisionContent)
	if err != nil {
		t.Fatalf("ApproveGitCandidate failed: %v", err)
	}
	if memID == "" || mem.Content != decisionContent {
		t.Fatalf("approve failed, memID=%s, mem=%+v", memID, mem)
	}

	// 4. Phase 3F 自動召回驗證（Ask bounded recall）
	task, errStr := recallSubmit(t, sockPath, repoDir, "req-e2e-1", "bounded", "OAuth 授權認證架構決策", nil)
	if errStr != "" {
		t.Fatalf("recall submit failed: %s", errStr)
	}
	completed := waitForTaskCompletionDirect(t, store, task.ID)
	foundRef := false
	for _, ref := range completed.MemoryRefs {
		if ref == memID {
			foundRef = true
			break
		}
	}
	if !foundRef {
		t.Fatalf("E2E FAIL: Approved memory %s was not recalled in task %s (refs: %v)", memID, task.ID, completed.MemoryRefs)
	}
	if !strings.Contains(completed.ResolvedContext, decisionContent) {
		t.Fatalf("E2E FAIL: Decision content not contained in ResolvedContext: %s", completed.ResolvedContext)
	}

	// 5. 增量 Checkpoint 推進驗證（SinceLast）
	// 新增一個 commit
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", "chore: bump version")
	cmd.Dir = repoDir
	cmd.Env = []string{
		"GIT_AUTHOR_NAME=Developer", "GIT_AUTHOR_EMAIL=dev@example.com",
		"GIT_COMMITTER_NAME=Developer", "GIT_COMMITTER_EMAIL=dev@example.com",
		"PATH=" + os.Getenv("PATH"),
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v, out: %s", err, string(out))
	}

	incResp, err := client.CaptureGitMemory(ctx, repoDir, "", true, false)
	if err != nil {
		t.Fatalf("incremental capture failed: %v", err)
	}
	if incResp.Candidate == nil {
		t.Fatalf("expected candidate from incremental capture")
	}

	// 6. 刪除與防復活驗證
	if err := client.DeleteMemory(ctx, repoDir, memID); err != nil {
		t.Fatalf("delete formal memory failed: %v", err)
	}

	// 驗證已無法召回
	taskAfterDelete, _ := recallSubmit(t, sockPath, repoDir, "req-e2e-2", "bounded", "OAuth 授權認證架構決策", nil)
	completedAfter := waitForTaskCompletionDirect(t, store, taskAfterDelete.ID)
	for _, ref := range completedAfter.MemoryRefs {
		if ref == memID {
			t.Fatalf("E2E FAIL: Deleted memory %s was still recalled!", memID)
		}
	}

	// 驗證防復活
	_, _, err = client.ApproveGitCandidate(ctx, repoDir, candID, "")
	if err == nil {
		t.Fatal("E2E SECURITY FAIL: Deleted memory was revived by re-approving candidate!")
	}
}
