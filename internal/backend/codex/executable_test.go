package codex

import (
	"asura/internal/backend"
	"os"
	"path/filepath"
	"testing"
)

// 2026-10-09：issue-45 Task 4.1，回報 probe 使用的 codex executable。
func TestProbeExecutable(t *testing.T) {
	var _ backend.ExecutableReporter = (*Adapter)(nil)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if got := NewAdapter(AdapterConfig{}).ProbeExecutable(); got != "" {
		t.Fatalf("missing codex reported %q", got)
	}
	onPath := filepath.Join(bin, "codex")
	os.WriteFile(onPath, []byte("#!/bin/sh\necho codex-cli 0.155.0\n"), 0700)
	if got := NewAdapter(AdapterConfig{}).ProbeExecutable(); got != onPath {
		t.Fatalf("PATH codex: %q", got)
	}
	explicit := filepath.Join(t.TempDir(), "codex")
	if got := NewAdapter(AdapterConfig{BinaryPath: explicit}).ProbeExecutable(); got != explicit {
		t.Fatalf("explicit codex: %q", got)
	}
}
