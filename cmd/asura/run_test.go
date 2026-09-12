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
}
