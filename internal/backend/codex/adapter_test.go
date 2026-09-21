package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"asura/internal/backend"
)

func TestAdapter_BackendInterface(t *testing.T) {
	adapter := NewAdapter(AdapterConfig{})

	if adapter.ID() != "codex" {
		t.Errorf("expected ID 'codex', got %q", adapter.ID())
	}

	expectedCaps := []backend.Capability{
		backend.CapReadOnlySandbox,
		backend.CapWorkspaceWrite,
		backend.CapStructuredEvents,
		backend.CapProcessCancellation,
	}

	for _, cap := range expectedCaps {
		if !adapter.HasCapability(cap) {
			t.Errorf("expected adapter to support capability %s", cap)
		}
	}

	ctx := context.Background()
	ver, err := adapter.Version(ctx)
	if err != nil {
		t.Logf("codex version lookup: %v", err)
	} else {
		t.Logf("codex adapter version: %s", ver)
		if !strings.Contains(ver, "0.155.0") {
			t.Logf("installed version %s does not match default 0.155.0", ver)
		}
	}
}

func TestNormalizeCodexVersion(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"codex-cli 0.155.0", "0.155.0"},
		{"  codex-cli 0.155.0  \n", "0.155.0"},
		{"0.155.0", "0.155.0"},
		{"codex-cli 0.155.0-beta", "0.155.0-beta"},
	}

	for _, tc := range cases {
		got := NormalizeCodexVersion(tc.input)
		if got != tc.expected {
			t.Errorf("NormalizeCodexVersion(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

// TestAdapter_VersionMismatch_FailClosed 驗證版本不符時 fail-closed 拒絕執行且 0 execution process (MUST FIX 1)
func TestAdapter_VersionMismatch_FailClosed(t *testing.T) {
	testCases := []struct {
		name        string
		versionOut  string
		expectPass  bool
	}{
		{"exact valid full prefix", "codex-cli 0.155.0", true},
		{"exact valid bare version", "0.155.0", true},
		{"older version", "codex-cli 0.154.0", false},
		{"substring trailing digit", "codex-cli 0.155.01", false},
		{"substring beta suffix", "codex-cli 0.155.0-beta", false},
		{"substring custom suffix", "codex-cli 0.155.0-custom", false},
		{"substring prefix number", "codex-cli 10.155.0", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			fakeScript := filepath.Join(tmpDir, "fake-codex.sh")
			scriptContent := `#!/bin/sh
if [ "$1" = "--version" ]; then
    echo "` + tc.versionOut + `"
    exit 0
fi
if [ "$tc_expect_pass" = "true" ]; then
    echo '{"type":"turn.completed","usage":{}}'
    exit 0
fi
echo "EXECUTION_PROCESS_CALLED" >&2
exit 1
`
			if err := os.WriteFile(fakeScript, []byte(scriptContent), 0755); err != nil {
				t.Fatalf("failed to write fake script: %v", err)
			}

			adapter := NewAdapter(AdapterConfig{
				BinaryPath:      fakeScript,
				ExpectedVersion: "0.155.0",
			})

			ctx := context.Background()
			req := &backend.ExecutionRequest{
				Role:      backend.RoleStriker,
				Workspace: tmpDir,
				Prompt:    "Echo something",
			}

			res, err := adapter.Run(ctx, req)
			if tc.expectPass {
				if err != nil && errors.Is(err, ErrVersionMismatch) {
					t.Fatalf("expected version to pass validation, but got ErrVersionMismatch: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error on version mismatch, got nil")
				}
				if !errors.Is(err, ErrVersionMismatch) {
					t.Errorf("expected ErrVersionMismatch, got %v", err)
				}
				if res == nil || res.FailureClass != backend.FailureUnavailable {
					t.Errorf("expected FailureUnavailable result, got %+v", res)
				}
			}
		})
	}
}

// TestAdapter_UnknownRole_FailClosed 驗證未知 Role 立即 fail-closed 拒絕執行且 0 execution process
func TestAdapter_UnknownRole_FailClosed(t *testing.T) {
	tmpDir := t.TempDir()
	fakeScript := filepath.Join(tmpDir, "fake-codex.sh")
	scriptContent := `#!/bin/sh
if [ "$1" = "--version" ]; then
    echo "codex-cli 0.155.0"
    exit 0
fi
echo "EXECUTION_PROCESS_CALLED" >&2
exit 1
`
	if err := os.WriteFile(fakeScript, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("failed to write fake script: %v", err)
	}

	adapter := NewAdapter(AdapterConfig{
		BinaryPath:      fakeScript,
		ExpectedVersion: "0.155.0",
	})

	ctx := context.Background()
	req := &backend.ExecutionRequest{
		Role:      backend.Role("unknown_role"),
		Workspace: tmpDir,
		Prompt:    "Echo something",
	}

	_, err := adapter.Run(ctx, req)
	if err == nil {
		t.Fatalf("expected error on unknown role, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported backend role") {
		t.Errorf("expected error message to mention unsupported backend role, got: %v", err)
	}
}

// TestAdapter_Run_Cancellation 驗證進程取消與回收 (hermetic fake script，不依賴 live codex) (MBACK-007, MUST FIX 5)
func TestAdapter_Run_Cancellation(t *testing.T) {
	tmpDir := t.TempDir()
	fakeScript := filepath.Join(tmpDir, "fake-codex.sh")
	scriptContent := `#!/bin/sh
if [ "$1" = "--version" ]; then
    echo "codex-cli 0.155.0"
    exit 0
fi
# 模擬執行長時間運行命令
exec sleep 60
`
	if err := os.WriteFile(fakeScript, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("failed to write fake script: %v", err)
	}

	started := make(chan struct{})
	adapter := NewAdapter(AdapterConfig{
		BinaryPath:      fakeScript,
		ExpectedVersion: "0.155.0",
		OnProcessStarted: func(c context.Context, pgid int) {
			close(started)
		},
	})

	ctx, cancel := context.WithCancel(context.Background())

	req := &backend.ExecutionRequest{
		Role:      backend.RoleStriker,
		Workspace: tmpDir,
		Prompt:    "Long running task",
	}

	go func() {
		<-started
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	res, err := adapter.Run(ctx, req)
	if err == nil {
		t.Fatalf("expected error on cancelled context, got nil")
	}

	if res == nil {
		t.Fatalf("expected non-nil result on cancellation")
	}

	if res.FailureClass != backend.FailureCancelled {
		t.Errorf("expected FailureCancelled, got %s", res.FailureClass)
	}

	var execErr *backend.ExecutionError
	if !errors.As(err, &execErr) {
		t.Errorf("expected error to be ExecutionError, got %T: %v", err, err)
	}
}

// TestAdapter_Run_Live 驗證真實 Codex CLI 連線 (需要 live codex 0.155.0)
func TestAdapter_Run_Live(t *testing.T) {
	requireLiveCodex(t, "0.155.0")

	adapter := NewAdapter(AdapterConfig{ExpectedVersion: "0.155.0"})
	tmpDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	req := &backend.ExecutionRequest{
		Role:      backend.RoleStriker,
		Workspace: tmpDir,
		Prompt:    "Respond with exactly the text 'CODEX_ADAPTER_OK'",
	}

	res, err := adapter.Run(ctx, req)
	if err != nil {
		t.Fatalf("adapter.Run failed: %v", err)
	}

	if res.BackendID != "codex" {
		t.Errorf("expected BackendID codex, got %s", res.BackendID)
	}
	if !strings.Contains(res.Output, "CODEX_ADAPTER_OK") {
		t.Errorf("expected output to contain 'CODEX_ADAPTER_OK', got %q", res.Output)
	}
	if len(res.Events) == 0 {
		t.Errorf("expected events to be populated, got 0")
	}
}

func TestAdapter_Run_UsagePreservedOnSuccessAndFailure(t *testing.T) {
	tmpDir := t.TempDir()
	fakeScript := filepath.Join(tmpDir, "fake-codex.sh")
	scriptContent := `#!/bin/sh
if [ "$1" = "--version" ]; then
    echo "codex-cli 0.155.0"
    exit 0
fi

if grep -q "FAIL_TRIGGER" <<EOF
$*
EOF
then
    echo '{"type":"thread.started","thread_id":"t-fail"}'
    echo '{"type":"turn.started"}'
    echo '{"type":"item.completed","item":{"id":"1","type":"agent_message","text":"Partial failed text"}}'
    echo '{"type":"error","message":"command failed with exit code 42"}'
    echo '{"type":"turn.completed","usage":{"input_tokens":555,"output_tokens":22,"cached_input_tokens":111}}'
    exit 1
fi

echo '{"type":"thread.started","thread_id":"t-success"}'
echo '{"type":"turn.started"}'
echo '{"type":"item.completed","item":{"id":"1","type":"agent_message","text":"Success response"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":120,"output_tokens":15,"cached_input_tokens":30}}'
exit 0
`
	if err := os.WriteFile(fakeScript, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("failed to write fake script: %v", err)
	}

	adapter := NewAdapter(AdapterConfig{
		BinaryPath:      fakeScript,
		ExpectedVersion: "0.155.0",
	})
	ctx := context.Background()

	// 1. 成功路徑
	resSuccess, err := adapter.Run(ctx, &backend.ExecutionRequest{
		Role:      backend.RoleStriker,
		Workspace: tmpDir,
		Prompt:    "normal prompt",
	})
	if err != nil {
		t.Fatalf("unexpected error on success: %v", err)
	}
	if resSuccess.Usage == nil {
		t.Fatalf("expected usage on success")
	}
	if resSuccess.Usage.InputTokens == nil || *resSuccess.Usage.InputTokens != 120 {
		t.Errorf("expected InputTokens 120, got %v", resSuccess.Usage.InputTokens)
	}
	if resSuccess.Usage.OutputTokens == nil || *resSuccess.Usage.OutputTokens != 15 {
		t.Errorf("expected OutputTokens 15, got %v", resSuccess.Usage.OutputTokens)
	}
	if resSuccess.Usage.CachedTokens == nil || *resSuccess.Usage.CachedTokens != 30 {
		t.Errorf("expected CachedTokens 30, got %v", resSuccess.Usage.CachedTokens)
	}
	if resSuccess.Usage.TotalTokens != nil {
		t.Errorf("expected TotalTokens to be nil for Codex, got %v", *resSuccess.Usage.TotalTokens)
	}
	if resSuccess.UsageSource != backend.ProvenanceExact {
		t.Errorf("expected UsageSource exact, got %s", resSuccess.UsageSource)
	}

	// 2. 失敗路徑 (FAIL_TRIGGER)
	resFail, errFail := adapter.Run(ctx, &backend.ExecutionRequest{
		Role:      backend.RoleStriker,
		Workspace: tmpDir,
		Prompt:    "FAIL_TRIGGER prompt",
	})
	if errFail == nil {
		t.Fatalf("expected error on failure trigger, got nil")
	}
	if resFail == nil {
		t.Fatalf("expected non-nil ExecutionResult even on failure")
	}
	if resFail.Usage == nil {
		t.Fatalf("expected usage to be preserved even on execution failure")
	}
	if resFail.Usage.InputTokens == nil || *resFail.Usage.InputTokens != 555 {
		t.Errorf("expected InputTokens 555 on failure, got %v", resFail.Usage.InputTokens)
	}
	if resFail.Usage.OutputTokens == nil || *resFail.Usage.OutputTokens != 22 {
		t.Errorf("expected OutputTokens 22 on failure, got %v", resFail.Usage.OutputTokens)
	}
	if resFail.Usage.CachedTokens == nil || *resFail.Usage.CachedTokens != 111 {
		t.Errorf("expected CachedTokens 111 on failure, got %v", resFail.Usage.CachedTokens)
	}
	if resFail.UsageSource != backend.ProvenanceExact {
		t.Errorf("expected UsageSource exact on failure, got %s", resFail.UsageSource)
	}
}

