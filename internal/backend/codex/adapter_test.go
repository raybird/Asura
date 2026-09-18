package codex

import (
	"context"
	"errors"
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
			t.Errorf("expected version to contain 0.155.0, got %s", ver)
		}
	}
}

func TestAdapter_Run_Live(t *testing.T) {
	adapter := NewAdapter(AdapterConfig{})
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

func TestAdapter_Run_Cancellation(t *testing.T) {
	adapter := NewAdapter(AdapterConfig{})
	tmpDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())

	started := make(chan struct{})
	adapter.cfg.OnProcessStarted = func(c context.Context, pgid int) {
		close(started)
	}

	req := &backend.ExecutionRequest{
		Role:      backend.RoleStriker,
		Workspace: tmpDir,
		Prompt:    "Run a bash command that sleeps for 60 seconds",
	}

	go func() {
		<-started
		time.Sleep(100 * time.Millisecond)
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
