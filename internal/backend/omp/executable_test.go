package omp

import (
	"asura/internal/backend"
	"path/filepath"
	"testing"
)

// 2026-10-09：issue-45 Task 4.1，回報 probe 驗證的 OMP CLI 路徑。
func TestProbeExecutable(t *testing.T) {
	var _ backend.ExecutableReporter = (*Adapter)(nil)
	path := filepath.Join(t.TempDir(), "node_modules", "@oh-my-pi", "pi-coding-agent", "dist", "cli.js")
	if got := NewAdapter(AdapterConfig{BinaryPath: path, BunPath: "/bun"}).ProbeExecutable(); got != path {
		t.Fatalf("got %q", got)
	}
	t.Setenv("PATH", t.TempDir())
	if got := NewAdapter(AdapterConfig{}).ProbeExecutable(); got != "" {
		t.Fatalf("missing omp reported %q", got)
	}
}
