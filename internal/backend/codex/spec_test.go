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

// 2026-10-27：PR #77 審查 Q17，唯讀沙箱 probe 以 codex sandbox 直接執行命令，不經模型：
// 唯讀用 :read-only，正向對照用 :workspace，工作目錄是 probe 的 workspace。
func TestCodexReadOnlySandboxRunner(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'codex-cli 0.163.0'; exit 0; fi\necho \"$*\" >> " + args + "\necho ran\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	spec, err := NewAdapter(AdapterConfig{BinaryPath: bin}).VerificationSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if spec.Options.ReadOnlySandbox == nil {
		t.Fatal("codex spec has no read-only sandbox runner")
	}
	ws := filepath.Join(dir, "ws")
	for _, readOnly := range []bool{true, false} {
		if out, err := spec.Options.ReadOnlySandbox(context.Background(), ws, readOnly, "sh", "-c", "true"); err != nil || out != "ran\n" {
			t.Errorf("readOnly=%v: out=%q err=%v", readOnly, out, err)
		}
	}
	data, _ := os.ReadFile(args)
	if want := "sandbox -P :read-only -C " + ws + " -- sh -c true\nsandbox -P :workspace -C " + ws + " -- sh -c true\n"; string(data) != want {
		t.Errorf("codex sandbox args:\n%s\nwant:\n%s", data, want)
	}
}
