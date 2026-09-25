package daemon_test

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
	"asura/internal/storage"
)

func initTestGitRepoInDaemon(t *testing.T, baseDir string) string {
	t.Helper()
	repoDir := filepath.Join(baseDir, "test_repo")
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
	_ = os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("init"), 0644)
	runGit("add", "file1.txt")
	runGit("commit", "-m", "init commit")

	_ = os.WriteFile(filepath.Join(repoDir, "file2.txt"), []byte("second"), 0644)
	runGit("add", "file2.txt")
	runGit("commit", "-m", "second commit")

	return repoDir
}

func TestDaemon_GitMemory_IPC(t *testing.T) {
	tmpDir := t.TempDir()
	outsideDir := t.TempDir()

	repoDir := initTestGitRepoInDaemon(t, tmpDir)

	dbPath := filepath.Join(tmpDir, "daemon_gitmem_test.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	sockPath := filepath.Join(tmpDir, "daemon_gitmem.sock")

	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
	}, store, policy)

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("授權邊界校驗", func(t *testing.T) {
		// 外部未授權目錄拒絕
		_, err := client.CaptureGitMemory(ctx, outsideDir, "", false, false)
		if err == nil || !strings.Contains(err.Error(), "unauthorized workspace") {
			t.Fatalf("expected unauthorized workspace error, got: %v", err)
		}

		// 缺少 workspace 參數拒絕
		_, err = client.CaptureGitMemory(ctx, "", "", false, false)
		if err == nil || !strings.Contains(err.Error(), "workspace is required") {
			t.Fatalf("expected workspace is required error, got: %v", err)
		}
	})

	t.Run("完整 Capture、Candidates、Approve、Discard 工作流", func(t *testing.T) {
		// 1. Capture 生成 Candidate
		resp, err := client.CaptureGitMemory(ctx, repoDir, "HEAD~1..HEAD", false, false)
		if err != nil {
			t.Fatalf("CaptureGitMemory failed: %v", err)
		}
		if resp.Candidate == nil || resp.Candidate.ID == "" {
			t.Fatalf("expected candidate returned, got %+v", resp)
		}
		candID := resp.Candidate.ID

		// 2. Candidates 查詢
		cands, err := client.ListGitCandidates(ctx, repoDir, "pending", 10)
		if err != nil {
			t.Fatalf("ListGitCandidates failed: %v", err)
		}
		if len(cands) != 1 || cands[0].ID != candID {
			t.Fatalf("expected 1 pending candidate %s, got %v", candID, cands)
		}

		// 3. Approve 審核核准（含自訂內文）
		customContent := "Architecture Decision: IPC handlers for Git Memory Capture"
		memID, mem, err := client.ApproveGitCandidate(ctx, repoDir, candID, customContent)
		if err != nil {
			t.Fatalf("ApproveGitCandidate failed: %v", err)
		}
		if memID == "" || mem == nil || mem.Content != customContent || mem.Source != "git-approved" {
			t.Errorf("unexpected approved memory: ID=%s, mem=%+v", memID, mem)
		}

		// 驗證候選狀態已變為 approved
		candsAfter, err := client.ListGitCandidates(ctx, repoDir, "approved", 10)
		if err != nil {
			t.Fatalf("ListGitCandidates failed: %v", err)
		}
		if len(candsAfter) != 1 || candsAfter[0].ApprovedMemoryID != memID {
			t.Errorf("expected approved candidate with memory ID %s, got %v", memID, candsAfter)
		}

		// 4. 重複核准維持冪等
		memID2, _, err := client.ApproveGitCandidate(ctx, repoDir, candID, "")
		if err != nil || memID2 != memID {
			t.Fatalf("expected idempotent approve returning %s, got %s (err: %v)", memID, memID2, err)
		}

		// 5. 測試 Discard（建立另一範圍候選進行捨棄）
		// 建立第三個 commit
		_ = os.WriteFile(filepath.Join(repoDir, "file3.txt"), []byte("three"), 0644)
		cmd := exec.Command("git", "add", "file3.txt")
		cmd.Dir = repoDir
		_ = cmd.Run()
		cmd = exec.Command("git", "commit", "-m", "third commit")
		cmd.Dir = repoDir
		cmd.Env = []string{
			"GIT_AUTHOR_NAME=Tester", "GIT_AUTHOR_EMAIL=tester@example.com",
			"GIT_COMMITTER_NAME=Tester", "GIT_COMMITTER_EMAIL=tester@example.com",
			"PATH=" + os.Getenv("PATH"),
		}
		_ = cmd.Run()

		resp2, err := client.CaptureGitMemory(ctx, repoDir, "HEAD~1..HEAD", false, false)
		if err != nil {
			t.Fatalf("capture 2 failed: %v", err)
		}
		candID2 := resp2.Candidate.ID

		err = client.DiscardGitCandidate(ctx, repoDir, candID2)
		if err != nil {
			t.Fatalf("DiscardGitCandidate failed: %v", err)
		}

		// 驗證狀態已為 discarded
		discardedList, err := client.ListGitCandidates(ctx, repoDir, "discarded", 10)
		if err != nil {
			t.Fatalf("ListGitCandidates failed: %v", err)
		}
		if len(discardedList) != 1 || discardedList[0].ID != candID2 {
			t.Errorf("expected 1 discarded candidate %s, got %v", candID2, discardedList)
		}
	})
}
