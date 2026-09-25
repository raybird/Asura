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

func initGitRepoForRecall(t *testing.T, baseDir string) string {
	t.Helper()
	repoDir := filepath.Join(baseDir, "git_recall_proj")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

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

	runGit("init", "-b", "main")
	_ = os.WriteFile(filepath.Join(repoDir, "base.txt"), []byte("base"), 0644)
	runGit("add", "base.txt")
	runGit("commit", "-m", "init commit")

	_ = os.WriteFile(filepath.Join(repoDir, "storage.go"), []byte("package storage\n// SQLite 隔離與防復活策略\n"), 0644)
	runGit("add", "storage.go")
	runGit("commit", "-m", "feat(storage): 實作 SQLite 隔離與防復活機制")

	return repoDir
}

func TestGitMem_RecallIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := initGitRepoForRecall(t, tmpDir)

	dbPath := filepath.Join(tmpDir, "gitmem_recall_test.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	sockPath := filepath.Join(tmpDir, "gitmem_recall.sock")

	// 建立模擬 engine runner 以支援 submit 執行
	mockRunner := func(ctx context.Context, ws, prompt string) (string, error) {
		return "Mock completed prompt: " + prompt, nil
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

	// 1. Capture 生成 Candidate
	captureResp, err := client.CaptureGitMemory(ctx, repoDir, "HEAD~1..HEAD", false, false)
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}
	candID := captureResp.Candidate.ID

	containsRef := func(refs []string, target string) bool {
		for _, r := range refs {
			if r == target {
				return true
			}
		}
		return false
	}

	// 2. 驗證 1（未核准草稿 0 召回）：提交 bounded recall task
	task1, errStr := recallSubmit(t, sockPath, repoDir, "req-recall-1", "bounded", "SQLite 隔離與防復活策略", nil)
	if errStr != "" {
		t.Fatalf("submit failed: %s", errStr)
	}
	done1 := waitForTaskCompletionDirect(t, store, task1.ID)
	if len(done1.MemoryRefs) != 0 || strings.Contains(done1.ResolvedContext, "draft") {
		t.Fatalf("GITMEM-008: Unapproved candidate leaked into automatic recall! MemoryRefs: %v", done1.MemoryRefs)
	}

	// 3. 核准候選為正式記憶
	customContent := "架構決策：專案採用 SQLite memory_candidates 表進行實體隔離與防復活機制"
	memID, mem, err := client.ApproveGitCandidate(ctx, repoDir, candID, customContent)
	if err != nil {
		t.Fatalf("approve failed: %v", err)
	}
	if mem.Source != "git-approved" {
		t.Errorf("expected source 'git-approved', got %s", mem.Source)
	}

	// 4. 驗證 2（核准後自然召回）：再次提交相同 prompt
	task2, errStr := recallSubmit(t, sockPath, repoDir, "req-recall-2", "bounded", "SQLite 隔離與防復活策略", nil)
	if errStr != "" {
		t.Fatalf("submit 2 failed: %s", errStr)
	}
	done2 := waitForTaskCompletionDirect(t, store, task2.ID)
	if !containsRef(done2.MemoryRefs, memID) {
		t.Fatalf("GITMEM-008: Approved memory %s not recalled in MemoryRefs: %v", memID, done2.MemoryRefs)
	}
	if !strings.Contains(done2.ResolvedContext, customContent) {
		t.Fatalf("GITMEM-008: Approved memory content not in ResolvedContext: %s", done2.ResolvedContext)
	}

	// 5. 驗證 3（刪除記憶後 0 召回）：使用者刪除該正式記憶
	if err := client.DeleteMemory(ctx, repoDir, memID); err != nil {
		t.Fatalf("delete memory failed: %v", err)
	}

	task3, errStr := recallSubmit(t, sockPath, repoDir, "req-recall-3", "bounded", "SQLite 隔離與防復活策略", nil)
	if errStr != "" {
		t.Fatalf("submit 3 failed: %s", errStr)
	}
	done3 := waitForTaskCompletionDirect(t, store, task3.ID)
	if containsRef(done3.MemoryRefs, memID) {
		t.Fatalf("GITMEM-008: Deleted memory was still recalled! MemoryRefs: %v", done3.MemoryRefs)
	}

	// 6. 驗證 4（Provenance 連動清理與 Candidate 不可變留存）
	prov, err := store.GetProvenance(ctx, memID)
	if err != nil || prov != nil {
		t.Errorf("expected provenance cascade deleted, got: %+v (err: %v)", prov, err)
	}
	cand, err := store.GetCandidate(ctx, repoDir, candID)
	if err != nil || cand == nil || cand.Status != "approved" {
		t.Errorf("expected candidate audit preserved as approved, got: %+v (err: %v)", cand, err)
	}
}

func waitForTaskCompletionDirect(t *testing.T, store *storage.SQLiteStore, taskID string) *storage.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, err := store.GetTask(context.Background(), taskID)
		if err == nil && task != nil && (task.Status == storage.StatusCompleted || task.Status == storage.StatusFailed) {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not complete in time", taskID)
	return nil
}
