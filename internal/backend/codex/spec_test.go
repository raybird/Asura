package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"asura/internal/backend"
	"asura/internal/backend/verification"
)

func reason(t *testing.T, b backend.Backend) backend.ProbeReason {
	t.Helper()
	s, err := backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: b.ID(), Backend: b}})
	if err != nil {
		t.Fatal(err)
	}
	return s.Entries[0].ReasonCode
}

// 2026-10-22：issue-73 Task 3.1，驗證身分與 admission 計算方式相同，未驗證的版本只透過暫時紀錄放行。
func TestCodexVerificationSpec(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "codex")
	write := func(version string) {
		if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'codex-cli "+version+"'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	newBackend := func(records string) backend.Backend {
		return NewAdapter(AdapterConfig{BinaryPath: bin, VerificationPath: records})
	}
	write("0.163.0")
	b, spec, err := verification.Prepare(context.Background(), t.TempDir(), newBackend)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := verification.FileDigest(bin)
	if want := (verification.Identity{Runtime: "codex", Version: "0.163.0", Digest: digest}); spec.Builtin || spec.Record.Identity != want || !spec.Options.ReadOnlyRoles {
		t.Fatalf("spec = %+v, want identity %+v with read-only probes", spec, want)
	}
	if got := reason(t, b); got != backend.ProbeOK {
		t.Errorf("candidate with temporary records: reason=%s", got)
	}
	if got := reason(t, newBackend("")); got != backend.VersionUnverified {
		t.Errorf("candidate without records: reason=%s", got)
	}
	write(BuiltinVersion)
	if _, spec, err := verification.Prepare(context.Background(), t.TempDir(), newBackend); err != nil || !spec.Builtin {
		t.Errorf("builtin %s: spec = %+v, err = %v", BuiltinVersion, spec, err)
	}
}
