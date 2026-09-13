package main

import (
	"bytes"
	"strings"
	"testing"
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
}
