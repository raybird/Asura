package clisandbox

import (
	"asura/internal/backend"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// 2026-10-04：RTINV-001/002/013，context-aware hash helper 不改原檔。
func TestInventoryPinnedFileProbe(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "runtime")
	data := []byte("known fixture")
	os.WriteFile(path, data, 0700)
	want := fmt.Sprintf("%x", sha256.Sum256(data))
	if got, err := ProbeFileDigest(context.Background(), path, backend.ExecutableNotFound); err != nil || got != want {
		t.Fatalf("digest=%q err=%v", got, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(data) {
		t.Fatal("probe wrote file")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ProbeFileDigest(ctx, path, backend.ExecutableNotFound); err != context.Canceled {
		t.Fatal("cancel ignored")
	}
	t.Setenv("PATH", root)
	if err := ProbeSandboxDependency(context.Background()); err == nil {
		t.Fatal("missing dependency admitted")
	}
}
