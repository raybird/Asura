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

// TestAdapter_VersionMismatch_FailClosed 驗證版本不符時 fail-closed 拒絕執行且 0 execution process (MUST FIX 4)
func TestAdapter_VersionMismatch_FailClosed(t *testing.T) {
	tmpDir := t.TempDir()
	fakeScript := filepath.Join(tmpDir, "fake-codex.sh")
	scriptContent := `#!/bin/sh
if [ "$1" = "--version" ]; then
    echo "codex-cli 0.154.0"
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
