package codex

import (
	"asura/internal/backend"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// 2026-10-04：RTINV-001/002/012，真版本 fixture；admission 必須檢查 ExpectedVersion。
func TestInventoryCodexVersionPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex")
	os.WriteFile(path, []byte("#!/bin/sh\necho codex-cli 0.155.1\n"), 0700)
	a := NewAdapter(AdapterConfig{BinaryPath: path, ExpectedVersion: "0.155.0"})
	s, err := backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "codex", Backend: a}})
	if err != nil || s.Entries[0].AvailabilityState != backend.Unavailable {
		t.Fatalf("version mismatch admitted: %+v %v", s, err)
	}
}
