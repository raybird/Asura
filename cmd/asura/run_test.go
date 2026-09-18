package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"asura/internal/daemon"
	"asura/internal/storage"
)

func TestRunCLI_UsageAndValidation(t *testing.T) {
	t.Run("無參數輸出 Usage 並返回非 0 狀態碼", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "Usage:") {
			t.Errorf("expected Usage in stderr, got: %s", stderr.String())
		}
	})

	t.Run("未知子命令返回非 0 狀態碼", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"unknown"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "unknown command") {
			t.Errorf("expected unknown command in stderr, got: %s", stderr.String())
		}
	})

	t.Run("run 指令缺少 prompt 返回非 0 狀態碼", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"run"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "prompt is required") {
			t.Errorf("expected 'prompt is required' in stderr, got: %s", stderr.String())
		}
	})

	t.Run("status 指令缺少 task_id 返回非 0 狀態碼", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"status"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "task_id is required") {
			t.Errorf("expected 'task_id is required' in stderr, got: %s", stderr.String())
		}
	})

	t.Run("ask 指令缺少 prompt 返回非 0 狀態碼", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"ask"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "prompt is required") {
			t.Errorf("expected 'prompt is required' in stderr, got: %s", stderr.String())
		}
	})

	t.Run("ask 指令在 Daemon 不可用時回報錯誤且不自動啟動 (P4-T1)", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"ask", "-socket", "/tmp/non-existent-asura.sock", "test prompt"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "daemon unavailable") {
			t.Errorf("expected 'daemon unavailable' in stderr, got: %s", stderr.String())
		}
	})

	t.Run("cancel 指令缺少 task_id 返回非 0 狀態碼", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"cancel"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "task_id is required") {
			t.Errorf("expected 'task_id is required' in stderr, got: %s", stderr.String())
		}
	})

	t.Run("web 指令缺少 access key 返回非 0 狀態碼", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		t.Setenv("ASURA_WEB_KEY", "")
		code := RunCLI([]string{"web"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "access key is required") {
			t.Errorf("expected 'access key is required' in stderr, got: %s", stderr.String())
		}
	})

	t.Run("web 指令在監聽 0.0.0.0 且未指定 origin 時輸出警告", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"web", "-key", "test-key", "-listen", "0.0.0.0:999999"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code for invalid port, got 0")
		}
		if !strings.Contains(stderr.String(), "warning: listening on 0.0.0.0 without explicit -origin") {
			t.Errorf("expected 0.0.0.0 warning in stderr, got: %s", stderr.String())
		}
	})

	t.Run("ask 指令傳入未知 backend 拒絕 (MBACK-003)", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"ask", "-backend", "unknown_backend", "prompt"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code for unknown backend, got 0")
		}
		if !strings.Contains(stderr.String(), "unknown backend") {
			t.Errorf("expected 'unknown backend' in stderr, got: %s", stderr.String())
		}
	})

	t.Run("run 指令傳入未知 backend 拒絕 (MBACK-003)", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"run", "-backend", "unknown_backend", "prompt"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code for unknown backend, got 0")
		}
		if !strings.Contains(stderr.String(), "unknown backend") {
			t.Errorf("expected 'unknown backend' in stderr, got: %s", stderr.String())
		}
	})
}

func TestRunCLI_CronValidation(t *testing.T) {
	t.Run("cron 指令無子操作輸出 Usage 並返回非 0", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"cron"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "cron subcommands:") {
			t.Errorf("expected cron subcommands usage, got: %s", stderr.String())
		}
	})

	t.Run("cron add 缺少必要參數返回非 0", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"cron", "add", "--name", "test"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "are all required") {
			t.Errorf("expected 'are all required' in stderr, got: %s", stderr.String())
		}
	})

	t.Run("cron pause 缺少 id 返回非 0", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"cron", "pause"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "schedule id is required") {
			t.Errorf("expected 'schedule id is required', got: %s", stderr.String())
		}
	})
}

// TestRunCLI_MemoryValidation 驗證 memory 命令之參數校驗與錯誤回報行為 (P3-T1.a, MEM-001, MEM-006)
func TestRunCLI_MemoryValidation(t *testing.T) {
	t.Run("memory 指令無子操作輸出 Usage 並返回非 0", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"memory"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "memory subcommands:") {
			t.Errorf("expected memory subcommands usage, got: %s", stderr.String())
		}
	})

	t.Run("memory add 缺少必要參數返回非 0", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"memory", "add", "--workspace", "/tmp"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "workspace and content are required") {
			t.Errorf("expected 'workspace and content are required', got: %s", stderr.String())
		}
	})

	t.Run("memory list 缺少 workspace 返回非 0", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"memory", "list"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "workspace is required") {
			t.Errorf("expected 'workspace is required', got: %s", stderr.String())
		}
	})

	t.Run("memory get 缺少參數返回非 0", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"memory", "get", "--workspace", "/tmp"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "workspace and memory_id are required") {
			t.Errorf("expected 'workspace and memory_id are required', got: %s", stderr.String())
		}
	})

	t.Run("memory remove 缺少參數返回非 0", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"memory", "remove", "--workspace", "/tmp"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "workspace and memory_id are required") {
			t.Errorf("expected 'workspace and memory_id are required', got: %s", stderr.String())
		}
	})

	t.Run("memory 指令在 Daemon 不可用時回報錯誤 (MEM-006)", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := RunCLI([]string{"memory", "list", "--workspace", "/tmp", "-socket", "/tmp/non-existent-mem.sock"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "daemon unavailable") {
			t.Errorf("expected 'daemon unavailable' in stderr, got: %s", stderr.String())
		}
	})
}

// TestHandleAsk_MemoryFlags 驗證 asura ask 支援 --memory-id 重複與逗點分隔格式並傳遞至 Daemon (TBD-2, CTXMEM-001, Task 4.1)
func TestHandleAsk_MemoryFlags(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "mock_ask.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	var receivedReq daemon.IPCRequest
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = json.NewDecoder(conn).Decode(&receivedReq)
		_ = json.NewEncoder(conn).Encode(daemon.IPCResponse{
			Task: &storage.Task{ID: "task-mock-ask-123"},
		})
	}()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := handleAsk([]string{
		"-socket", sockPath,
		"--memory-id", "mem-1",
		"--memory-id", "mem-2,mem-3,mem-1",
		"測試 prompt",
	}, stdout, stderr)

	if code != 0 {
		t.Fatalf("expected code 0, got %d, stderr: %s", code, stderr.String())
	}
	expectedIDs := []string{"mem-1", "mem-2", "mem-3"}
	if len(receivedReq.MemoryIDs) != 3 || receivedReq.MemoryIDs[0] != "mem-1" || receivedReq.MemoryIDs[1] != "mem-2" || receivedReq.MemoryIDs[2] != "mem-3" {
		t.Fatalf("expected MemoryIDs %v, got %v", expectedIDs, receivedReq.MemoryIDs)
	}
}

// TestHandleAsk_ModeFlags 驗證 asura ask 支援 --mode 參數並校驗合法性 (TFACE-001, TFACE-002, Task 5.1)
func TestHandleAsk_ModeFlags(t *testing.T) {
	t.Run("非法 mode 返回非 0 並輸出錯誤", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := handleAsk([]string{"--mode", "invalid-mode", "test prompt"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "invalid mode") {
			t.Errorf("expected 'invalid mode' in stderr, got: %s", stderr.String())
		}
	})

	t.Run("合法 three-face mode 正確傳遞至 Daemon", func(t *testing.T) {
		tmpDir := t.TempDir()
		sockPath := filepath.Join(tmpDir, "mock_ask_mode.sock")
		l, err := net.Listen("unix", sockPath)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()

		var receivedReq daemon.IPCRequest
		go func() {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			_ = json.NewDecoder(conn).Decode(&receivedReq)
			_ = json.NewEncoder(conn).Encode(daemon.IPCResponse{
				Task: &storage.Task{ID: "task-mock-three-face"},
			})
		}()

		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := handleAsk([]string{
			"-socket", sockPath,
			"--mode", "three-face",
			"測試 prompt",
		}, stdout, stderr)

		if code != 0 {
			t.Fatalf("expected code 0, got %d, stderr: %s", code, stderr.String())
		}
		if receivedReq.Mode != "three-face" {
			t.Fatalf("expected Mode 'three-face', got %q", receivedReq.Mode)
		}
	})
}

// MBACK-001, MBACK-002: 驗證 asura ask 支援 --backend 參數並傳遞至 Daemon
func TestHandleAsk_BackendFlags(t *testing.T) {
	t.Run("指定 --backend codex 正確傳遞至 Daemon", func(t *testing.T) {
		tmpDir := t.TempDir()
		sockPath := filepath.Join(tmpDir, "c.sock")
		l, err := net.Listen("unix", sockPath)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()

		var receivedReq daemon.IPCRequest
		go func() {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			_ = json.NewDecoder(conn).Decode(&receivedReq)
			_ = json.NewEncoder(conn).Encode(daemon.IPCResponse{
				Task: &storage.Task{ID: "task-mock-codex", Backend: "codex"},
			})
		}()

		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := handleAsk([]string{
			"-socket", sockPath,
			"--backend", "codex",
			"測試 prompt",
		}, stdout, stderr)

		if code != 0 {
			t.Fatalf("expected code 0, got %d, stderr: %s", code, stderr.String())
		}
		if receivedReq.Backend != "codex" {
			t.Fatalf("expected Backend 'codex', got %q", receivedReq.Backend)
		}
	})

	t.Run("未指定 --backend 預設為 opencode 並傳遞至 Daemon", func(t *testing.T) {
		tmpDir := t.TempDir()
		sockPath := filepath.Join(tmpDir, "d.sock")
		l, err := net.Listen("unix", sockPath)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()

		var receivedReq daemon.IPCRequest
		go func() {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			_ = json.NewDecoder(conn).Decode(&receivedReq)
			_ = json.NewEncoder(conn).Encode(daemon.IPCResponse{
				Task: &storage.Task{ID: "task-mock-default", Backend: "opencode"},
			})
		}()

		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := handleAsk([]string{
			"-socket", sockPath,
			"測試 prompt",
		}, stdout, stderr)

		if code != 0 {
			t.Fatalf("expected code 0, got %d, stderr: %s", code, stderr.String())
		}
		if receivedReq.Backend != "opencode" {
			t.Fatalf("expected Backend 'opencode', got %q", receivedReq.Backend)
		}
	})
}

// TestHandleRun_ModeValidation 驗證 asura run 支援 --mode 參數與校驗 (TFACE-001, TFACE-002, Task 5.1)
func TestHandleRun_ModeValidation(t *testing.T) {
	t.Run("非法 mode 返回非 0 並輸出錯誤", func(t *testing.T) {
		out := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := handleRun([]string{"--mode", "bad-mode", "test prompt"}, out, stderr)
		if code == 0 {
			t.Errorf("expected non-zero exit code, got 0")
		}
		if !strings.Contains(stderr.String(), "invalid mode") {
			t.Errorf("expected 'invalid mode' in stderr, got: %s", stderr.String())
		}
	})
}

// TestHandleStatus_PipelineTrace 驗證 status 指令格式化輸出 Mode 與 Pipeline 狀態 (Task 5.1)
func TestHandleStatus_PipelineTrace(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_status.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	taskID := "task-trace-test"
	traceJSON := `{"mode":"three-face","stages":[{"name":"oracle","status":"completed","duration_ms":120},{"name":"striker","status":"completed","duration_ms":350},{"name":"critic","status":"completed","duration_ms":80}],"critic_review":{"verdict":"pass","summary":"All good"}}`
	task := &storage.Task{
		ID:            taskID,
		Prompt:        "demo",
		Workspace:     tmpDir,
		Status:        storage.StatusCompleted,
		ExecutionMode: "three-face",
		PipelineTrace: traceJSON,
		Result:        "final output",
	}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := handleStatus([]string{"-db", dbPath, taskID}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d, stderr: %s", code, stderr.String())
	}
	outStr := stdout.String()
	if !strings.Contains(outStr, "Mode:      three-face") {
		t.Errorf("expected 'Mode:      three-face' in stdout, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Pipeline:  oracle (completed, 120ms) -> striker (completed, 350ms) -> critic (completed, 80ms)") {
		t.Errorf("expected Pipeline trace summary in stdout, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Verdict:   pass") {
		t.Errorf("expected 'Verdict:   pass' in stdout, got:\n%s", outStr)
	}
}


