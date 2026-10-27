package antigravity

import (
	"asura/internal/backend"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// 2026-10-04：RTINV-002，缺 binary 或 hash 不符皆 fail-closed，不讀憑證。
func TestInventoryAntigravityProbe(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agy")
	a := NewAdapter(AdapterConfig{BinaryPath: path, Home: root})
	s, err := backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "antigravity", Backend: a}})
	if err != nil || s.Entries[0].ReasonCode != backend.ExecutableNotFound {
		t.Fatalf("missing=%+v %v", s, err)
	}
	// 2026-10-27：issue-73 Q18，未知 hash 不執行 agy 取版本，直接判為 version_unverified。
	os.WriteFile(path, []byte("untrusted"), 0700)
	s, err = backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "antigravity", Backend: a}})
	if err != nil || s.Entries[0].ReasonCode != backend.VersionUnverified {
		t.Fatal("unverified CLI admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Probe(ctx); err != context.Canceled {
		t.Fatal("cancel ignored")
	}
}
