package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/storage"
)

func TestGitMem_SealedSnapshotIntegrity(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := initGitRepoForRecall(t, tmpDir)

	dbPath := filepath.Join(tmpDir, "sealed_compat.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	sockPath := filepath.Join(tmpDir, "sealed_compat.sock")

	mockRunner := func(ctx context.Context, ws, prompt string) (string, error) {
		return "Mock output: " + prompt, nil
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

	// 1. 擷取並核准一個 Git 記憶
	capResp, err := client.CaptureGitMemory(ctx, repoDir, "HEAD~1..HEAD", false, false)
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}
	candID := capResp.Candidate.ID

	originalContent := "Sealed Policy: immutable snapshots must be protected"
	memID, _, err := client.ApproveGitCandidate(ctx, repoDir, candID, originalContent)
	if err != nil {
		t.Fatalf("approval failed: %v", err)
	}

	// 2. 提交一個自動召回任務並等待完成
	task, errStr := recallSubmit(t, sockPath, repoDir, "req-sealed-1", "bounded", "immutable snapshots", nil)
	if errStr != "" {
		t.Fatalf("submit failed: %s", errStr)
	}
	completedTask := waitForTaskCompletionDirect(t, store, task.ID)

	// 記錄歷史任務密封快照
	sealedFingerprint := completedTask.Fingerprint
	sealedResolvedContext := completedTask.ResolvedContext
	sealedMemoryRefs := completedTask.MemoryRefs

	if len(sealedMemoryRefs) == 0 || sealedResolvedContext == "" {
		t.Fatalf("expected memory recalled into snapshot: %+v", completedTask)
	}

	// 3. 執行後續變更：刪除該正式記憶
	if err := client.DeleteMemory(ctx, repoDir, memID); err != nil {
		t.Fatalf("delete formal memory failed: %v", err)
	}

	// 4. 再次擷取新變更或推進 Checkpoint
	_, _ = client.CaptureGitMemory(ctx, repoDir, "HEAD~1..HEAD", false, true)

	// 5. 重新自資料庫讀取已完成的歷史任務
	reloadedTask, err := store.GetTask(ctx, task.ID)
	if err != nil || reloadedTask == nil {
		t.Fatalf("failed to reload historical task: %v", err)
	}

	// 6. 驗證密封性：歷史任務之 Fingerprint、MemoryRefs 與 ResolvedContext 必須完全保持不變！
	if reloadedTask.Fingerprint != sealedFingerprint {
		t.Errorf("SEALED INTEGRITY VIOLATION: Fingerprint changed from %s to %s", sealedFingerprint, reloadedTask.Fingerprint)
	}
	if reloadedTask.ResolvedContext != sealedResolvedContext {
		t.Errorf("SEALED INTEGRITY VIOLATION: ResolvedContext changed from %s to %s", sealedResolvedContext, reloadedTask.ResolvedContext)
	}
	if len(reloadedTask.MemoryRefs) != len(sealedMemoryRefs) || reloadedTask.MemoryRefs[0] != sealedMemoryRefs[0] {
		t.Errorf("SEALED INTEGRITY VIOLATION: MemoryRefs changed from %v to %v", sealedMemoryRefs, reloadedTask.MemoryRefs)
	}
}
