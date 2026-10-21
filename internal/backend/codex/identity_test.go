package codex

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

// 2026-10-21：issue-73，內建版本免驗證；其他版本需要身分（版本 + SHA-256）相符的本機驗證紀錄。
func TestCodexIdentityPolicy(t *testing.T) {
	dir := t.TempDir()
	records := filepath.Join(dir, "data", "runtime-verification.json")
	bin := filepath.Join(dir, "codex")
	marker := filepath.Join(dir, "executed")
	write := func(version, extra string) {
		script := "#!/bin/sh\n# " + extra + "\n[ \"$1\" = --version ] && { echo codex-cli " + version + "; exit 0; }\ntouch " + marker + "\n"
		if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	a := NewAdapter(AdapterConfig{BinaryPath: bin, VerificationPath: records})
	reason := func() backend.ProbeReason {
		s, err := backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "codex", Backend: a}})
		if err != nil {
			t.Fatal(err)
		}
		return s.Entries[0].ReasonCode
	}

	write("0.155.0", "builtin")
	if got := reason(); got != backend.ProbeOK {
		t.Fatalf("builtin version: reason=%s", got)
	}

	write("0.163.0", "new")
	if got := reason(); got != backend.VersionUnverified {
		t.Fatalf("unverified version: reason=%s", got)
	}
	res, err := a.Run(context.Background(), &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: dir, Prompt: "x"})
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
	if err := verification.Add(records, verification.Record{Identity: verification.Identity{Runtime: "codex", Version: "0.163.0", Digest: digest}, VerifiedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if got := reason(); got != backend.ProbeOK {
		t.Fatalf("verified version: reason=%s", got)
	}
	if err := a.ValidateVersion(context.Background()); err != nil {
		t.Fatalf("verified ValidateVersion: %v", err)
	}

	write("0.163.0", "replaced binary, same version")
	if got := reason(); got != backend.VersionUnverified {
		t.Fatalf("replaced binary: reason=%s", got)
	}
	if err := a.ValidateVersion(context.Background()); err == nil {
		t.Fatal("replaced binary passed ValidateVersion")
	}
}

// npm 安裝的 codex 只是 Node 包裝腳本，真正執行的是平台套件裡的原生檔與它附帶的 bwrap；
// 這些檔案換掉時，即使版本字串不變也要重新驗證。
func TestCodexIdentityCoversNpmPlatformPackage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "node_modules", "@openai", "codex")
	native := filepath.Join(root, "node_modules", "@openai", "codex-linux-x64", "vendor", "x86_64-unknown-linux-musl", "bin", "codex")
	wrapper := filepath.Join(root, "bin", "codex.js")
	for path, content := range map[string]string{
		filepath.Join(root, "package.json"): `{"name":"@openai/codex"}`,
		wrapper:                             "#!/bin/sh\necho codex-cli 0.163.0\n",
		native:                              "native-v1",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	before, err := codexDigest(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte("native-v2"), 0o700); err != nil {
		t.Fatal(err)
	}
	after, err := codexDigest(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("replacing the native binary did not change the codex identity")
	}
}
