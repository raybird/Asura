package omp

import (
	"asura/internal/backend"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// 2026-10-04：RTINV-002，OMP 缺 binary 與不符 pin 的 package 不可 admission。
func TestInventoryOMPProbe(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "node_modules", "@oh-my-pi", "pi-coding-agent", "dist", "cli.js")
	a := NewAdapter(AdapterConfig{BinaryPath: path, BunPath: filepath.Join(root, "bun"), Home: root})
	s, err := backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "omp", Backend: a}})
	if err != nil || s.Entries[0].ReasonCode != backend.ExecutableNotFound {
		t.Fatalf("missing=%+v %v", s, err)
	}
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, []byte("untrusted"), 0600)
	os.WriteFile(filepath.Join(root, "bun"), []byte("bun"), 0700)
	s, err = backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "omp", Backend: a}})
	if err != nil || s.Entries[0].AvailabilityState != backend.Unavailable || s.Entries[0].ReasonCode != backend.VersionFailed {
		t.Fatal("unverified CLI admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Probe(ctx); err != context.Canceled {
		t.Fatal("cancel ignored")
	}
}
