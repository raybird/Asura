package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"asura/internal/engine"
	"asura/internal/storage"
)

// waitForTaskCompletion 等待任務達到 Completed / Failed / Cancelled
func waitForTaskCompletion(t *testing.T, f *memoryFixture, taskID string) *storage.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	ctx := context.Background()
	for time.Now().Before(deadline) {
		task, err := f.client.GetTask(ctx, taskID)
		if err == nil && task != nil {
			if task.Status == storage.StatusCompleted || task.Status == storage.StatusFailed || task.Status == storage.StatusCancelled {
				return task
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for task %s to complete", taskID)
	return nil
}

// TestCTXMEM001_MemoryInjectedIntoPromptAndOriginalPromptProtected (CTXMEM-001)
// 驗證同專案記憶可被任務選取並注入 Context，runner 取得組裝 Prompt，但 DB task.Prompt 維持原始內容
func TestCTXMEM001_MemoryInjectedIntoPromptAndOriginalPromptProtected(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	memContent := "系統架構核心記憶-2026-09-17"
	mem, err := f.client.AddMemory(ctx, f.projA, memContent)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	origPrompt := "分析專案架構"
	task, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projA,
		Prompt:    origPrompt,
		MemoryIDs: []string{mem.ID},
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	completed := waitForTaskCompletion(t, f, task.ID)
	if completed.Status != storage.StatusCompleted {
		t.Fatalf("expected status completed, got %s: %s", completed.Status, completed.ErrorMessage)
	}

	// 1. Runner 執行結果（包含傳入之組裝 prompt）必須包含 Markdown 區塊與記憶內容
	if !strings.Contains(completed.Result, "### Project Context") || !strings.Contains(completed.Result, memContent) {
		t.Errorf("expected runner to receive context block with memory content, got result:\n%s", completed.Result)
	}

	// 2. task.Prompt 與 DB 中任務 Prompt 必須完全保持為原始 Prompt
	if completed.Prompt != origPrompt {
		t.Errorf("expected task.Prompt to be %q, got %q", origPrompt, completed.Prompt)
	}
	saved, err := f.store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("store.GetTask failed: %v", err)
	}
	if saved.Prompt != origPrompt {
		t.Errorf("expected DB task.Prompt to remain %q, got %q", origPrompt, saved.Prompt)
	}

	// 3. Task 保存 MemoryRefs 與 ResolvedContext
	if len(saved.MemoryRefs) != 1 || saved.MemoryRefs[0] != mem.ID {
		t.Errorf("expected MemoryRefs [%s], got %v", mem.ID, saved.MemoryRefs)
	}
	if !strings.Contains(saved.ResolvedContext, memContent) {
		t.Errorf("expected ResolvedContext to contain %q, got %q", memContent, saved.ResolvedContext)
	}
}

// TestCTXMEM002_CrossProjectMemoryRejection (CTXMEM-002)
// 驗證跨專案記憶隔離，在 runner 啟動前即拒絕，runner 調用為 0 且不洩漏記憶
func TestCTXMEM002_CrossProjectMemoryRejection(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	memB, err := f.client.AddMemory(ctx, f.projB, fixedMemoryContentB)
	if err != nil {
		t.Fatalf("AddMemory B failed: %v", err)
	}

	beforeCalls := atomic.LoadInt32(f.runnerCalls)
	_, err = f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projA,
		Prompt:    "嘗試非法使用專案 B 記憶",
		MemoryIDs: []string{memB.ID},
	})
	if err == nil {
		t.Fatalf("expected error when selecting cross-project memory, got nil")
	}

	afterCalls := atomic.LoadInt32(f.runnerCalls)
	if afterCalls != beforeCalls {
		t.Errorf("expected runner calls to remain %d, but got %d (must fail-closed at 0 calls)", beforeCalls, afterCalls)
	}
	if strings.Contains(err.Error(), fixedMemoryContentB) {
		t.Errorf("error message leaked confidential memory content: %v", err)
	}
}

// TestCTXMEM003_RevokedOrOutsideWorkspaceRejection (CTXMEM-003)
// 驗證未授權或已撤權 workspace 無法使用記憶，runner 調用次數為 0
func TestCTXMEM003_RevokedOrOutsideWorkspaceRejection(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	memA, err := f.client.AddMemory(ctx, f.projA, fixedMemoryContentA)
	if err != nil {
		t.Fatalf("AddMemory A failed: %v", err)
	}

	beforeCalls := atomic.LoadInt32(f.runnerCalls)
	_, err = f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.outsideDir,
		Prompt:    "未授權 workspace 任務",
		MemoryIDs: []string{memA.ID},
	})
	if err == nil {
		t.Fatalf("expected error for unauthorized workspace, got nil")
	}

	afterCalls := atomic.LoadInt32(f.runnerCalls)
	if afterCalls != beforeCalls {
		t.Errorf("expected runner calls to remain %d, got %d", beforeCalls, afterCalls)
	}
}

// TestCTXMEM004_SealedContextImmutableOnRedelivery (CTXMEM-004)
// 驗證任務接受後 Context 密封不可變，底層記憶刪除或異動後重送仍保持原始 Context 且無二次執行
func TestCTXMEM004_SealedContextImmutableOnRedelivery(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	initialContent := "不可變密封記憶-原始版本"
	mem, err := f.client.AddMemory(ctx, f.projA, initialContent)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	requestID := "req-sealed-ctx-004"
	prompt := "執行 Context 密封驗證"
	task1, err := f.client.Submit(ctx, engine.SubmitRequest{
		ID:        requestID,
		Workspace: f.projA,
		Prompt:    prompt,
		MemoryIDs: []string{mem.ID},
	})
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}

	completed1 := waitForTaskCompletion(t, f, task1.ID)
	callsAfterFirst := atomic.LoadInt32(f.runnerCalls)

	// 刪除記憶
	if err := f.client.DeleteMemory(ctx, f.projA, mem.ID); err != nil {
		t.Fatalf("DeleteMemory failed: %v", err)
	}

	// 重新提交相同的 Request ID (redelivery)
	task2, err := f.client.Submit(ctx, engine.SubmitRequest{
		ID:        requestID,
		Workspace: f.projA,
		Prompt:    prompt,
		MemoryIDs: []string{mem.ID},
	})
	if err != nil {
		t.Fatalf("redelivery submit should succeed, got: %v", err)
	}

	if task2.ID != task1.ID {
		t.Errorf("expected redelivered task ID %s, got %s", task1.ID, task2.ID)
	}
	if !strings.Contains(task2.ResolvedContext, initialContent) {
		t.Errorf("expected ResolvedContext to retain %q, got %q", initialContent, task2.ResolvedContext)
	}
	callsAfterSecond := atomic.LoadInt32(f.runnerCalls)
	if callsAfterSecond != callsAfterFirst {
		t.Errorf("expected runner calls to remain %d, got %d (double execution detected)", callsAfterFirst, callsAfterSecond)
	}
	_ = completed1
}

// TestCTXMEM005_NonExistentAndDeletedMemoryRejection (CTXMEM-005)
// 驗證已刪除或不存在記憶 ID 於 runner 啟動前被拒絕且 runner 調用為 0
func TestCTXMEM005_NonExistentAndDeletedMemoryRejection(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	beforeCalls := atomic.LoadInt32(f.runnerCalls)
	_, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projA,
		Prompt:    "不存在記憶測試",
		MemoryIDs: []string{"mem-does-not-exist"},
	})
	if err == nil {
		t.Fatalf("expected error for non-existent memory ID, got nil")
	}
	if atomic.LoadInt32(f.runnerCalls) != beforeCalls {
		t.Errorf("expected runner calls to remain %d, got %d", beforeCalls, atomic.LoadInt32(f.runnerCalls))
	}
}

// TestCTXMEM006_TaskQueryExposesMemoryRefsAndScope (CTXMEM-006)
// 驗證查詢已提交或已完成任務時，可識別關聯之 Memory IDs 與 workspace
func TestCTXMEM006_TaskQueryExposesMemoryRefsAndScope(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mem, err := f.client.AddMemory(ctx, f.projA, "查詢可追溯性記憶")
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	task, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projA,
		Prompt:    "追溯性測試",
		MemoryIDs: []string{mem.ID},
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	queried, err := f.client.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}

	if len(queried.MemoryRefs) != 1 || queried.MemoryRefs[0] != mem.ID {
		t.Errorf("expected queried MemoryRefs [%s], got %v", mem.ID, queried.MemoryRefs)
	}
	if queried.Workspace != f.projA {
		t.Errorf("expected queried Workspace %s, got %s", f.projA, queried.Workspace)
	}
}

// TestCTXMEM007_NoMemoryMaintainsLegacyPrompt (CTXMEM-007)
// 驗證未選取記憶時維持 Phase 3A 語意，Prompt 完全原樣無 Markdown 區塊
func TestCTXMEM007_NoMemoryMaintainsLegacyPrompt(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	origPrompt := "純文字原始需求，無記憶區塊"
	task, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projA,
		Prompt:    origPrompt,
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	completed := waitForTaskCompletion(t, f, task.ID)
	if strings.Contains(completed.Result, "### Project Context") {
		t.Errorf("expected no Project Context markdown block in prompt, got:\n%s", completed.Result)
	}
	if completed.Prompt != origPrompt {
		t.Errorf("expected task.Prompt to be %q, got %q", origPrompt, completed.Prompt)
	}
}

// TestCTXMEM_CLI_EndToEnd 驗證 CLI asura ask 參數串接端到端行為
func TestCTXMEM_CLI_EndToEnd(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mem, err := f.client.AddMemory(ctx, f.projA, "CLI 端到端測試記憶")
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	// 1. 正向 CLI 呼叫
	cmd := exec.Command("go", "run", cmdDir,
		"ask",
		"-socket", f.sockPath,
		"--workspace", f.projA,
		"--memory-id", mem.ID,
		"--wait",
		"CLI 端到端任務執行",
	)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		t.Fatalf("CLI ask failed: %v, stderr: %s", err, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "Task ID:") {
		t.Errorf("expected 'Task ID:' in stdout, got: %s", outBuf.String())
	}

	// 2. 負向 CLI 呼叫（傳入不存在記憶 ID）
	cmdFail := exec.Command("go", "run", cmdDir,
		"ask",
		"-socket", f.sockPath,
		"--workspace", f.projA,
		"--memory-id", "mem-invalid-cli",
		"應該失敗的任務",
	)
	var failOut, failErr bytes.Buffer
	cmdFail.Stdout = &failOut
	cmdFail.Stderr = &failErr

	if err := cmdFail.Run(); err == nil {
		t.Fatalf("expected CLI ask to fail with invalid memory-id, but succeeded")
	}
	if !strings.Contains(failErr.String(), "resolve memories") {
		t.Errorf("expected error output in stderr, got: %s", failErr.String())
	}
}

// TestCTXMEM_QueuedTaskPreservesSealedContextOnWake 驗證排隊任務（StatusQueued）喚醒後仍完整保留 Sealed Context
func TestCTXMEM_QueuedTaskPreservesSealedContextOnWake(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	memContent := "排隊喚醒密封記憶-2026-09-17"
	mem, err := f.client.AddMemory(ctx, f.projA, memContent)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	// 1. 提交任務 1（佔用 projA 專案鎖）
	task1, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projA,
		Prompt:    "任務 1 先行佔用",
	})
	if err != nil {
		t.Fatalf("Submit task 1 failed: %v", err)
	}

	// 2. 緊接著提交帶記憶的任務 2（應進入 StatusQueued 排隊）
	task2, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projA,
		Prompt:    "任務 2 帶記憶排隊",
		MemoryIDs: []string{mem.ID},
	})
	if err != nil {
		t.Fatalf("Submit task 2 failed: %v", err)
	}

	// 3. 等待任務 1 完成
	completed1 := waitForTaskCompletion(t, f, task1.ID)
	if completed1.Status != storage.StatusCompleted {
		t.Fatalf("task 1 failed: %s", completed1.ErrorMessage)
	}

	// 4. 等待任務 2 被喚醒並完成
	completed2 := waitForTaskCompletion(t, f, task2.ID)
	if completed2.Status != storage.StatusCompleted {
		t.Fatalf("task 2 failed: %s", completed2.ErrorMessage)
	}

	// 5. 斷言任務 2 喚醒後 runner prompt 正確包含記憶區塊
	if !strings.Contains(completed2.Result, "### Project Context") || !strings.Contains(completed2.Result, memContent) {
		t.Errorf("expected queued task to receive context block after waking, got:\n%s", completed2.Result)
	}

	// 6. 斷言資料庫中記憶欄位完好保存
	saved2, err := f.store.GetTask(ctx, task2.ID)
	if err != nil {
		t.Fatalf("GetTask 2 failed: %v", err)
	}
	if len(saved2.MemoryRefs) != 1 || saved2.MemoryRefs[0] != mem.ID {
		t.Errorf("expected saved MemoryRefs [%s], got %v", mem.ID, saved2.MemoryRefs)
	}
	if !strings.Contains(saved2.ResolvedContext, memContent) {
		t.Errorf("expected saved ResolvedContext to contain memory content, got %q", saved2.ResolvedContext)
	}
}

