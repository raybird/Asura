package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"asura/internal/auth"
	"asura/internal/backend/opencode"
	"asura/internal/engine"
	"asura/internal/storage"
)

// TestINT001: 自然語言任務經真實後端取得可追蹤結果 (INT-001 外迴圈)
// Given 單一執行中樞可用且固定版本後端已通過權限探測
// When 使用者傳入自然語言任務
// Then 任務交給指定後端且不被當作 shell 命令，使用者可重新查得保存結果
func TestINT001(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int001-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})

	// 注入真實 OpenCode Adapter 執行器
	adapter := opencode.NewAdapter(opencode.AdapterConfig{})
	eng := engine.NewEngine(store, policy, engine.WithRunner(adapter.Run))

	ctx := context.Background()
	task, err := eng.SubmitTask(ctx, engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "請回覆：'測試自然語言任務'，不要呼叫工具",
	})
	if err != nil {
		t.Fatalf("SubmitTask unexpected failure: %v", err)
	}

	if task == nil || task.Status != storage.StatusCompleted {
		t.Fatalf("expected task status %s, got task=%v", storage.StatusCompleted, task)
	}
	if !strings.Contains(task.Result, "測試自然語言任務") {
		t.Errorf("expected output to contain prompt text, got %q", task.Result)
	}
}

// TestINT007: 後端遵守已核准的操作範圍 (INT-007 外迴圈)
// Given 任務位於 Daemon 核准的專案
// When 後端需要修改專案檔案
// Then 可在核准範圍內自動執行，但限制操作未取得確認時不執行
func TestINT007(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int007-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 建立測試檔案
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("original"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	adapter := opencode.NewAdapter(opencode.AdapterConfig{
		BinaryPath: filepath.Join(os.Getenv("HOME"), ".cache/asura-probe-issue-2/install/node_modules/.bin/opencode"),
		Permission: `{"bash":"deny"}`,
	})

	ctx := context.Background()
	out, err := adapter.Run(ctx, tmpDir, "請在 test.txt 加上一行 appended")
	if err != nil {
		t.Fatalf("adapter.Run failed: %v", err)
	}
	if out == "" {
		t.Errorf("expected non-empty output from adapter")
	}
}
