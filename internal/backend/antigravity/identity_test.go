package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"asura/internal/backend"
	"asura/internal/backend/verification"
)

// 2026-10-21：issue-73，hash 不是內建時以 agy --version 取版本，需要本機驗證紀錄才可用。
func TestAntigravityIdentityPolicy(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "agy")
	records := filepath.Join(root, "data", "runtime-verification.json")
	write := func(extra string) {
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n# "+extra+"\necho 1.4.4\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	a := NewAdapter(AdapterConfig{BinaryPath: bin, Home: root, VerificationPath: records})
	inventory := func() backend.RuntimeInventoryEntry {
		s, err := backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "antigravity", Backend: a}})
		if err != nil {
			t.Fatal(err)
		}
		return s.Entries[0]
	}
	write("v1")
	if got := inventory().ReasonCode; got != backend.VersionUnverified {
		t.Fatalf("unverified antigravity: reason=%s", got)
	}
	sum, _ := verification.FileDigest(bin)
	if err := verification.Add(records, verification.Record{Identity: verification.Identity{Runtime: "antigravity", Version: "1.4.4", Digest: sum}, VerifiedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if e := inventory(); e.ReasonCode != backend.ProbeOK || e.Descriptor == nil || e.Descriptor.Version != "1.4.4" {
		t.Fatalf("verified antigravity: %+v", e)
	}
	write("v2")
	if got := inventory().ReasonCode; got != backend.VersionUnverified {
		t.Fatalf("replaced agy: reason=%s", got)
	}
}
