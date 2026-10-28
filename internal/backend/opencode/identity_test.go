package opencode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"asura/internal/backend"
	"asura/internal/backend/verification"
)

// 2026-10-21：issue-73，OpenCode 統一精確比對：內建 1.18.23 免驗證，其他版本（含 1.18.x）需要本機驗證紀錄。
func TestOpenCodeIdentityPolicy(t *testing.T) {
	dir := t.TempDir()
	records := filepath.Join(dir, "data", "runtime-verification.json")
	bin := filepath.Join(dir, "opencode")
	marker := filepath.Join(dir, "executed")
	config := filepath.Join(dir, "config")
	if err := os.MkdirAll(filepath.Join(config, "opencode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "opencode", "opencode.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(version, extra string) {
		script := "#!/bin/sh\n# " + extra + "\n[ \"$1\" = --version ] && { echo " + version + "; exit 0; }\ntouch " + marker + "\n"
		if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	read := NewAdapter(AdapterConfig{BinaryPath: bin, VerificationPath: records, ConfigDir: config, Isolated: true, DisableAuto: true, Pure: true, DisableProjectConfig: true})
	striker := NewAdapter(AdapterConfig{BinaryPath: bin, VerificationPath: records})
	logical := NewRoleRoutingBackend(read, striker)
	reason := func() backend.ProbeReason {
		s, err := backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "opencode", Backend: logical}})
		if err != nil {
			t.Fatal(err)
		}
		return s.Entries[0].ReasonCode
	}

	write("1.18.23", "builtin")
	if got := reason(); got != backend.ProbeOK {
		t.Fatalf("builtin version: reason=%s", got)
	}

	write("1.18.31", "new")
	if got := reason(); got != backend.VersionUnverified {
		t.Fatalf("unverified 1.18.x: reason=%s", got)
	}
	res, err := logical.Run(context.Background(), &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: dir, Prompt: "x"})
	if err == nil || res == nil || res.FailureClass != backend.FailureUnavailable {
		t.Fatalf("unverified Run: res=%+v err=%v", res, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unverified binary was executed for a task")
	}

	digest, err := verification.FileDigest(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := verification.Add(records, verification.Record{Identity: verification.Identity{Runtime: "opencode", Version: "1.18.31", Digest: digest}, VerifiedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if got := reason(); got != backend.ProbeOK {
		t.Fatalf("verified version: reason=%s", got)
	}

	write("1.18.31", "replaced binary, same version")
	if got := reason(); got != backend.VersionUnverified {
		t.Fatalf("replaced binary: reason=%s", got)
	}
}
