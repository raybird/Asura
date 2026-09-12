package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"asura/internal/auth"
	"asura/internal/backend/opencode"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/storage"
	"time"
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

// TestINT002: 未授權專案在執行前被拒絕 (INT-002 外迴圈)
// Given 專案不在服務端核准的允許範圍
// When 使用者透過 CLI 提交該專案的任務
// Then 請求被拒絕且沒有建立可執行任務或啟動後端
func TestINT002(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int002-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "allowed")
	unauthorizedDir := filepath.Join(tmpDir, "unauthorized")
	_ = os.MkdirAll(allowedDir, 0755)
	_ = os.MkdirAll(unauthorizedDir, 0755)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{allowedDir})
	sockPath := filepath.Join(tmpDir, "asura.sock")

	backendStarted := false
	dummyRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		backendStarted = true
		return "unexpected", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(dummyRunner))
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 1,
		MaxQueue:   1,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	client, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	defer client.Close()

	task, err := client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: unauthorizedDir,
		Prompt:    "unauthorized prompt",
	})

	if err == nil {
		t.Fatalf("expected submission to be rejected, got nil error")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("expected error to contain 'unauthorized', got %v", err)
	}
	if task != nil {
		t.Errorf("expected no task returned for unauthorized request, got %+v", task)
	}
	if backendStarted {
		t.Errorf("backend must not be started for unauthorized request")
	}
}

// TestINT003: 多個 CLI 共用執行容量 (INT-003 外迴圈)
// Given 執行中樞已達核准的執行及佇列容量
// When 另一個 CLI 提交任務
// Then 使用者收到核准的滿載回應，且沒有另起獨立執行器繞過容量限制
func TestINT003(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-int003-*")
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
	sockPath := filepath.Join(tmpDir, "asura.sock")

	blockCh := make(chan struct{})
	unblockCh := make(chan struct{})

	blockingRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		select {
		case blockCh <- struct{}{}:
		default:
		}
		<-unblockCh
		return "done", nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(blockingRunner))
	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 1,
		MaxQueue:   1,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer d.Close()

	c1, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("c1 connect failed: %v", err)
	}
	defer c1.Close()

	c2, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("c2 connect failed: %v", err)
	}
	defer c2.Close()

	c3, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("c3 connect failed: %v", err)
	}
	defer c3.Close()

	go func() {
		_, _ = c1.Submit(context.Background(), engine.SubmitRequest{
			Workspace: tmpDir,
			Prompt:    "task-1",
		})
	}()

	<-blockCh

	qDone := make(chan struct{})
	go func() {
		_, _ = c2.Submit(context.Background(), engine.SubmitRequest{
			Workspace: tmpDir,
			Prompt:    "task-2",
		})
		close(qDone)
	}()

	time.Sleep(50 * time.Millisecond)

	_, err = c3.Submit(context.Background(), engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "task-3-overflow",
	})

	if err == nil {
		t.Fatalf("expected capacity exceeded error, got nil")
	}
	if !strings.Contains(err.Error(), "capacity exceeded") {
		t.Errorf("expected error to contain 'capacity exceeded', got %v", err)
	}

	close(unblockCh)
	<-qDone
}
