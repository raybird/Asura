package opencode

import (
	"asura/internal/backend"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// 2026-10-09：issue-45 Task 4.1，doctor 顯示的 executable 必須是 probe 實際執行的檔案。
func TestProbeExecutableReportsProbedBinary(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	script := []byte("#!/bin/sh\necho 1.18.23\n")
	onPath := filepath.Join(bin, "opencode")
	os.WriteFile(onPath, script, 0700)
	cache := filepath.Join(home, ".cache/asura-probe-issue-2/install/node_modules/.bin/opencode")
	os.MkdirAll(filepath.Dir(cache), 0700)
	os.WriteFile(cache, script, 0700)

	var _ backend.ExecutableReporter = (*Adapter)(nil)
	var _ backend.ExecutableReporter = (*RoleRoutingBackend)(nil)
	a := NewAdapter(AdapterConfig{})
	if got := a.ProbeExecutable(); got != "" {
		t.Fatalf("unprobed adapter reported %q", got)
	}
	if _, err := a.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := a.ProbeExecutable(); got != cache {
		t.Fatalf("cache fallback: got %q want %q", got, cache)
	}
	os.Remove(cache)
	if _, err := a.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := a.ProbeExecutable(); got != onPath {
		t.Fatalf("PATH binary: got %q want %q", got, onPath)
	}
	os.Remove(onPath)
	a.Probe(context.Background())
	if got := a.ProbeExecutable(); got != "" {
		t.Fatalf("missing binary reported %q", got)
	}
	explicit := filepath.Join(t.TempDir(), "opencode")
	os.WriteFile(explicit, []byte("#!/bin/sh\necho 9.9.9\n"), 0700)
	pinned := NewAdapter(AdapterConfig{BinaryPath: explicit})
	pinned.Probe(context.Background())
	if got := pinned.ProbeExecutable(); got != explicit {
		t.Fatalf("version-mismatched binary must still be reported: %q", got)
	}
	logical := NewRoleRoutingBackend(NewAdapter(AdapterConfig{BinaryPath: explicit}), pinned)
	if got := logical.ProbeExecutable(); got != explicit {
		t.Fatalf("role routing: %q", got)
	}
}
