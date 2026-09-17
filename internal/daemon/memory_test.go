package daemon_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/storage"
)

// TestDaemon_Memory_IPC 驗證 Daemon IPC 處理器對記憶操作之同次授權、參數校驗與 CRUD 功能 (MEM-002, MEM-003, MEM-004, P2-T2.b)
func TestDaemon_Memory_IPC(t *testing.T) {
	tmpDir := t.TempDir()
	outsideDir := t.TempDir()

	projA := filepath.Join(tmpDir, "projA")
	if err := os.MkdirAll(projA, 0755); err != nil {
		t.Fatal(err)
	}

	escapedLink := filepath.Join(tmpDir, "escaped")
	if err := os.Symlink(outsideDir, escapedLink); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "daemon_memory_test.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	sockPath := filepath.Join(tmpDir, "daemon_mem.sock")

	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
	}, store, policy)

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// 等待 socket 就緒
	deadline := time.Now().Add(3 * time.Second)
	var client *daemon.Client
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

	t.Run("漏參數拒絕", func(t *testing.T) {
		if _, err := client.AddMemory(ctx, "", "content"); err == nil {
			t.Fatalf("expected error for empty workspace in AddMemory")
		}
		if _, err := client.AddMemory(ctx, projA, ""); err == nil {
			t.Fatalf("expected error for empty content in AddMemory")
		}
		if _, err := client.ListMemories(ctx, ""); err == nil {
			t.Fatalf("expected error for empty workspace in ListMemories")
		}
		if _, err := client.GetMemory(ctx, "", "id"); err == nil {
			t.Fatalf("expected error for empty workspace in GetMemory")
		}
		if _, err := client.GetMemory(ctx, projA, ""); err == nil {
			t.Fatalf("expected error for empty memoryID in GetMemory")
		}
		if err := client.DeleteMemory(ctx, "", "id"); err == nil {
			t.Fatalf("expected error for empty workspace in DeleteMemory")
		}
		if err := client.DeleteMemory(ctx, projA, ""); err == nil {
			t.Fatalf("expected error for empty memoryID in DeleteMemory")
		}
	})

	t.Run("越界符號連結與外部目錄拒絕", func(t *testing.T) {
		if _, err := client.AddMemory(ctx, escapedLink, "越界寫入"); err == nil || !strings.Contains(err.Error(), "unauthorized workspace") {
			t.Fatalf("越界符號連結應被拒絕且回傳 unauthorized workspace，got: %v", err)
		}
		if _, err := client.AddMemory(ctx, outsideDir, "外部寫入"); err == nil || !strings.Contains(err.Error(), "unauthorized workspace") {
			t.Fatalf("外部目錄應被拒絕且回傳 unauthorized workspace，got: %v", err)
		}
	})

	t.Run("正向 CRUD 流程完整驗證", func(t *testing.T) {
		// 寫入記憶
		mem, err := client.AddMemory(ctx, projA, "IPC 測試記憶內容")
		if err != nil {
			t.Fatalf("AddMemory failed: %v", err)
		}
		if mem == nil || mem.ID == "" || mem.Content != "IPC 測試記憶內容" || mem.Source != "manual" {
			t.Fatalf("AddMemory response invalid: %+v", mem)
		}

		// 列出記憶
		list, err := client.ListMemories(ctx, projA)
		if err != nil {
			t.Fatalf("ListMemories failed: %v", err)
		}
		if len(list) != 1 || list[0].ID != mem.ID {
			t.Fatalf("ListMemories result invalid: %+v", list)
		}

		// 依 ID 取得記憶
		got, err := client.GetMemory(ctx, projA, mem.ID)
		if err != nil {
			t.Fatalf("GetMemory failed: %v", err)
		}
		if got.Content != mem.Content {
			t.Fatalf("GetMemory content mismatch: got %q, expected %q", got.Content, mem.Content)
		}

		// 刪除記憶
		if err := client.DeleteMemory(ctx, projA, mem.ID); err != nil {
			t.Fatalf("DeleteMemory failed: %v", err)
		}

		// 再次查詢應得 memory not found
		if _, err := client.GetMemory(ctx, projA, mem.ID); err == nil || !strings.Contains(err.Error(), "memory not found") {
			t.Fatalf("expected memory not found after deletion, got: %v", err)
		}

		// 重複刪除應得 memory not found
		if err := client.DeleteMemory(ctx, projA, mem.ID); err == nil || !strings.Contains(err.Error(), "memory not found") {
			t.Fatalf("expected memory not found on duplicate deletion, got: %v", err)
		}
	})
}

// TestDaemon_SubmitMemoryFingerprint 驗證 MemoryIDs 納入請求指紋與重送識別 (CTXMEM-004, Task 3.1)
func TestDaemon_SubmitMemoryFingerprint(t *testing.T) {
	tmpDir := t.TempDir()
	projDir := filepath.Join(tmpDir, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "daemon_fp_test.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	sockPath := filepath.Join(tmpDir, "daemon_fp.sock")

	eng := engine.NewEngine(store, policy, engine.WithRunner(func(ctx context.Context, ws, prompt string) (string, error) {
		return "dummy result", nil
	}))

	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	deadline := time.Now().Add(3 * time.Second)
	var client *daemon.Client
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

	mem1, err := client.AddMemory(ctx, projDir, "指紋記憶一")
	if err != nil {
		t.Fatalf("AddMemory 1 failed: %v", err)
	}
	mem2, err := client.AddMemory(ctx, projDir, "指紋記憶二")
	if err != nil {
		t.Fatalf("AddMemory 2 failed: %v", err)
	}
	mem3, err := client.AddMemory(ctx, projDir, "指紋記憶三")
	if err != nil {
		t.Fatalf("AddMemory 3 failed: %v", err)
	}

	requestID := "req-fp-001"
	origPrompt := "測試指紋計算"

	// 1. 第一次提交：帶入 MemoryIDs [mem1.ID, mem2.ID]
	task1, err := client.Submit(ctx, engine.SubmitRequest{
		ID:        requestID,
		Workspace: projDir,
		Prompt:    origPrompt,
		MemoryIDs: []string{mem1.ID, mem2.ID},
	})
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	if task1 == nil || task1.ID == "" {
		t.Fatalf("expected valid task, got %+v", task1)
	}

	// 2. 第二次提交：相同 RequestID，但 MemoryIDs 改為 [mem3.ID] -> 應被拒絕並判定為不同 payload
	_, err = client.Submit(ctx, engine.SubmitRequest{
		ID:        requestID,
		Workspace: projDir,
		Prompt:    origPrompt,
		MemoryIDs: []string{mem3.ID},
	})
	if err == nil || !strings.Contains(err.Error(), "request ID reused with different payload") {
		t.Fatalf("expected 'request ID reused with different payload', got err: %v", err)
	}

	// 3. 第三次提交：相同 RequestID，MemoryIDs 順序相反 [mem2.ID, mem1.ID] -> 應視為相同 payload，成功 redelivery 回傳 task1
	task3, err := client.Submit(ctx, engine.SubmitRequest{
		ID:        requestID,
		Workspace: projDir,
		Prompt:    origPrompt,
		MemoryIDs: []string{mem2.ID, mem1.ID},
	})
	if err != nil {
		t.Fatalf("redelivery submit failed: %v", err)
	}
	if task3 == nil || task3.ID != task1.ID {
		t.Fatalf("expected redelivered task %s, got %+v", task1.ID, task3)
	}
}

// TestDaemon_MemoryScopeRejection 驗證跨專案、不存在、已刪除、未授權記憶選取於 runner 啟動前拒絕，runner 啟動為 0 (CTXMEM-002, CTXMEM-003, CTXMEM-005, CTXMEM-001, Task 3.2)
func TestDaemon_MemoryScopeRejection(t *testing.T) {
	tmpDir := t.TempDir()
	projA := filepath.Join(tmpDir, "projA")
	projB := filepath.Join(tmpDir, "projB")
	outsideDir := filepath.Join(tmpDir, "outside")
	for _, dir := range []string{projA, projB, outsideDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	dbPath := filepath.Join(tmpDir, "daemon_scope_test.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{projA, projB})
	sockPath := filepath.Join(tmpDir, "daemon_scope.sock")

	var runnerCalls int32
	var promptMu sync.Mutex
	var receivedPrompt string
	runner := func(ctx context.Context, ws, prompt string) (string, error) {
		atomic.AddInt32(&runnerCalls, 1)
		promptMu.Lock()
		receivedPrompt = prompt
		promptMu.Unlock()
		return "runner output", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	deadline := time.Now().Add(3 * time.Second)
	var client *daemon.Client
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

	// 預先在 projA 與 projB 寫入記憶
	memA, err := client.AddMemory(ctx, projA, "專案 A 記憶內容")
	if err != nil {
		t.Fatalf("AddMemory projA failed: %v", err)
	}
	memB, err := client.AddMemory(ctx, projB, "專案 B 記憶內容")
	if err != nil {
		t.Fatalf("AddMemory projB failed: %v", err)
	}

	t.Run("CTXMEM-002: 跨專案記憶於 runner 前拒絕且調用為 0", func(t *testing.T) {
		atomic.StoreInt32(&runnerCalls, 0)
		// 在 projA 中嘗試使用 projB 的記憶 memB.ID
		_, err := client.Submit(ctx, engine.SubmitRequest{
			Workspace: projA,
			Prompt:    "跨專案測試",
			MemoryIDs: []string{memB.ID},
		})
		if err == nil {
			t.Fatalf("expected error when selecting cross-project memory, got nil")
		}
		if calls := atomic.LoadInt32(&runnerCalls); calls != 0 {
			t.Errorf("expected runner to be called 0 times, got %d", calls)
		}
	})

	t.Run("CTXMEM-005: 不存在或已刪除記憶於 runner 前拒絕且調用為 0", func(t *testing.T) {
		atomic.StoreInt32(&runnerCalls, 0)
		// 不存在之 ID
		_, err := client.Submit(ctx, engine.SubmitRequest{
			Workspace: projA,
			Prompt:    "不存在記憶測試",
			MemoryIDs: []string{"mem-not-found-xyz"},
		})
		if err == nil {
			t.Fatalf("expected error for non-existent memory ID, got nil")
		}
		if calls := atomic.LoadInt32(&runnerCalls); calls != 0 {
			t.Errorf("expected runner to be called 0 times, got %d", calls)
		}

		// 刪除 memA 後再次嘗試選取
		if err := client.DeleteMemory(ctx, projA, memA.ID); err != nil {
			t.Fatalf("DeleteMemory memA failed: %v", err)
		}
		atomic.StoreInt32(&runnerCalls, 0)
		_, err = client.Submit(ctx, engine.SubmitRequest{
			Workspace: projA,
			Prompt:    "已刪除記憶測試",
			MemoryIDs: []string{memA.ID},
		})
		if err == nil {
			t.Fatalf("expected error for deleted memory ID, got nil")
		}
		if calls := atomic.LoadInt32(&runnerCalls); calls != 0 {
			t.Errorf("expected runner to be called 0 times, got %d", calls)
		}
	})

	t.Run("CTXMEM-003: 未授權 workspace 拒絕且調用為 0", func(t *testing.T) {
		atomic.StoreInt32(&runnerCalls, 0)
		_, err := client.Submit(ctx, engine.SubmitRequest{
			Workspace: outsideDir,
			Prompt:    "未授權專案測試",
			MemoryIDs: []string{memB.ID},
		})
		if err == nil {
			t.Fatalf("expected error for unauthorized workspace, got nil")
		}
		if calls := atomic.LoadInt32(&runnerCalls); calls != 0 {
			t.Errorf("expected runner to be called 0 times, got %d", calls)
		}
	})

	t.Run("CTXMEM-001: 合法同專案記憶解析注入成功且 runner 取得組裝 Prompt", func(t *testing.T) {
		atomic.StoreInt32(&runnerCalls, 0)
		task, err := client.Submit(ctx, engine.SubmitRequest{
			Workspace: projB,
			Prompt:    "合法專案測試",
			MemoryIDs: []string{memB.ID},
		})
		if err != nil {
			t.Fatalf("expected successful submit, got error: %v", err)
		}
		if task == nil || task.ID == "" {
			t.Fatalf("expected valid task, got %+v", task)
		}

		// 等待 runner 執行完成
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if atomic.LoadInt32(&runnerCalls) >= 1 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}

		if calls := atomic.LoadInt32(&runnerCalls); calls != 1 {
			t.Fatalf("expected runner to be called 1 time, got %d", calls)
		}
		promptMu.Lock()
		promptCopy := receivedPrompt
		promptMu.Unlock()
		if !strings.Contains(promptCopy, "### Project Context") || !strings.Contains(promptCopy, "專案 B 記憶內容") {
			t.Errorf("expected runner prompt to contain memory block, got:\n%s", promptCopy)
		}
	})
}

// TestDaemon_TaskContextSealedAndImmutable 驗證任務接受後 Context 密封不可變，記憶刪除後重送仍保持原始 Context 且無二次執行 (CTXMEM-004, Task 3.3)
func TestDaemon_TaskContextSealedAndImmutable(t *testing.T) {
	tmpDir := t.TempDir()
	projDir := filepath.Join(tmpDir, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "daemon_sealed_test.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	sockPath := filepath.Join(tmpDir, "daemon_sealed.sock")

	var runnerCalls int32
	var receivedPrompts []string
	var runnerMu sync.Mutex
	runner := func(ctx context.Context, ws, prompt string) (string, error) {
		atomic.AddInt32(&runnerCalls, 1)
		runnerMu.Lock()
		receivedPrompts = append(receivedPrompts, prompt)
		runnerMu.Unlock()
		return "sealed output", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	deadline := time.Now().Add(3 * time.Second)
	var client *daemon.Client
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

	// 1. 新增記憶
	origContent := "初始架構規格-不可變版本"
	mem, err := client.AddMemory(ctx, projDir, origContent)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	// 2. 提交任務，指定 RequestID 與 MemoryID
	requestID := "req-sealed-999"
	prompt := "執行架構驗證"
	task1, err := client.Submit(ctx, engine.SubmitRequest{
		ID:        requestID,
		Workspace: projDir,
		Prompt:    prompt,
		MemoryIDs: []string{mem.ID},
	})
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	if task1 == nil || task1.ID == "" {
		t.Fatalf("expected valid task, got %+v", task1)
	}

	// 等待第一次執行完成
	waitDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(waitDeadline) {
		if atomic.LoadInt32(&runnerCalls) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if atomic.LoadInt32(&runnerCalls) != 1 {
		t.Fatalf("expected runner to be called once, got %d", atomic.LoadInt32(&runnerCalls))
	}

	// 3. 在底層將記憶刪除
	if err := client.DeleteMemory(ctx, projDir, mem.ID); err != nil {
		t.Fatalf("DeleteMemory failed: %v", err)
	}
	// 確認記憶在儲存層已被刪除
	if _, err := client.GetMemory(ctx, projDir, mem.ID); err == nil {
		t.Fatalf("expected memory to be deleted")
	}

	// 4. 以相同的 RequestID 重送提交該任務 (Redelivery)
	task2, err := client.Submit(ctx, engine.SubmitRequest{
		ID:        requestID,
		Workspace: projDir,
		Prompt:    prompt,
		MemoryIDs: []string{mem.ID},
	})
	if err != nil {
		t.Fatalf("redelivery submit should succeed even after memory deleted, but got: %v", err)
	}

	// 5. 斷言密封 Context 保持不變，且 runner 調用次數嚴格保持為 1
	if task2.ID != task1.ID {
		t.Errorf("expected redelivered task ID %s, got %s", task1.ID, task2.ID)
	}
	if !strings.Contains(task2.ResolvedContext, origContent) {
		t.Errorf("expected task2.ResolvedContext to retain %q, got %q", origContent, task2.ResolvedContext)
	}
	if calls := atomic.LoadInt32(&runnerCalls); calls != 1 {
		t.Errorf("expected runner calls to remain 1 (no double execution on redelivery), got %d", calls)
	}
}


