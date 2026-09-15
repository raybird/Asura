package daemon_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/daemon"
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
