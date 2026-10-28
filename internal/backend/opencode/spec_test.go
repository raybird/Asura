package opencode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"asura/internal/backend"
	"asura/internal/backend/verification"
)

func fakeOpenCode(t *testing.T, path, version string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho "+version+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

// 2026-10-22：issue-73 Task 3.1，候選身分與 Run 解析到的執行檔相同（先隔離安裝的內建版本，再 PATH），
// 兩個角色必須是同一份安裝，未驗證的版本只透過暫時紀錄放行。
func TestOpenCodeVerificationSpec(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	fakeOpenCode(t, filepath.Join(bin, "opencode"), "1.18.31")
	newBackend := func(records string) backend.Backend {
		return NewRoleRoutingBackend(NewAdapter(AdapterConfig{Role: RoleOracle, VerificationPath: records}),
			NewAdapter(AdapterConfig{Role: RoleStriker, VerificationPath: records}))
	}
	reason := func(b backend.Backend) backend.ProbeReason {
		s, err := backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "opencode", Backend: b}})
		if err != nil {
			t.Fatal(err)
		}
		return s.Entries[0].ReasonCode
	}
	b, spec, err := verification.Prepare(context.Background(), t.TempDir(), newBackend)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := verification.FileDigest(filepath.Join(bin, "opencode"))
	if want := (verification.Identity{Runtime: "opencode", Version: "1.18.31", Digest: digest}); spec.Builtin || spec.Record.Identity != want || !spec.Options.ReadOnlyRoles {
		t.Fatalf("spec = %+v, want identity %+v with read-only probes", spec, want)
	}
	if got := reason(b); got != backend.ProbeOK {
		t.Errorf("candidate with temporary records: reason=%s", got)
	}
	if got := reason(newBackend("")); got != backend.VersionUnverified {
		t.Errorf("candidate without records: reason=%s", got)
	}

	other := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(other, []byte("#!/bin/sh\n# another install\necho 1.18.31\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	mixed := func(string) backend.Backend {
		return NewRoleRoutingBackend(NewAdapter(AdapterConfig{BinaryPath: other}), NewAdapter(AdapterConfig{Role: RoleStriker}))
	}
	if _, _, err := verification.Prepare(context.Background(), t.TempDir(), mixed); err == nil {
		t.Error("roles resolving to different installations accepted")
	}

	fakeOpenCode(t, filepath.Join(home, ".cache/asura-probe-issue-2/install/node_modules/.bin/opencode"), BuiltinVersion)
	if _, spec, err := verification.Prepare(context.Background(), t.TempDir(), newBackend); err != nil || !spec.Builtin {
		t.Errorf("isolated builtin install: spec = %+v, err = %v", spec, err)
	}
}
