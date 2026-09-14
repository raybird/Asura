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

// TestDaemon_Memory_UnitRed (P1-T2.c Daemon 單元紅燈)
// 驗證 Daemon 在收到記憶 IPC 操作時的請求處理行為。
// 在目前階段（P1-T2），Daemon 尚未實作 memory_* action，
// Client 調用將取得明確的 unknown action 行為紅燈，記錄缺少操作之現況；
// 正式授權守門與同次 canonical scope 實作由 P2-T2 交付。
func TestDaemon_Memory_UnitRed(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	t.Run("memory_add 尚缺 action 行為紅燈", func(t *testing.T) {
		_, err := client.AddMemory(ctx, projA, "測試記憶內容")
		if err == nil {
			t.Fatalf("預期失敗但得到 nil 錯誤")
		}
		if !strings.Contains(err.Error(), "unknown action") {
			t.Fatalf("預期收到 unknown action: memory_add，實際收到: %v", err)
		}
		t.Logf("Daemon 單元行為紅燈確認 (功能尚缺)：%v (待 P2-T2 實作)", err)
	})

	t.Run("越界路徑請求尚未進到授權閘門即遭 unknown action 阻擋", func(t *testing.T) {
		_, err := client.AddMemory(ctx, escapedLink, "越界測試")
		if err == nil {
			t.Fatalf("預期失敗但得到 nil 錯誤")
		}
		// 記錄：此時被拒絕是因 action 未實作，不能誤當成越界防護已生效
		if strings.Contains(err.Error(), "unknown action") {
			t.Logf("單元紅燈確認：目前受限於缺少 action 處理器，無法證明越界授權閘門已生效 (待 P2-T2 接線)")
		} else {
			t.Fatalf("預期 unknown action，但收到: %v", err)
		}
	})
}
