package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"asura/internal/backend"
	"asura/internal/backend/verification"
)

// 2026-10-22：issue-73 Task 3.1，憑證邊界 probe 指向 agy 的憑證目錄；token 檔存在時才列入前後不變檢查
// （keyring 登入時可能沒有這個檔案）。
func TestAntigravityVerificationSpec(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "agy")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 1.4.4\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	newBackend := func(records string) backend.Backend {
		return NewAdapter(AdapterConfig{BinaryPath: bin, Home: root, VerificationPath: records})
	}
	b, spec, err := verification.Prepare(context.Background(), t.TempDir(), newBackend)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := verification.FileDigest(bin)
	want := verification.Identity{Runtime: "antigravity", Version: "1.4.4", Digest: digest}
	dir := filepath.Join(root, ".gemini", "antigravity-cli")
	if spec.Builtin || spec.Record.Identity != want || spec.Options.CredentialDir != dir || len(spec.Protected) != 0 {
		t.Fatalf("spec = %+v, want identity %+v, credential dir %s, nothing protected", spec, want, dir)
	}
	if _, _, err := b.(*Adapter).admit(context.Background()); err != nil {
		t.Errorf("candidate with temporary records: %v", err)
	}
	if _, _, err := newBackend("").(*Adapter).admit(context.Background()); err == nil {
		t.Error("candidate admitted without records")
	}

	token := filepath.Join(dir, "antigravity-oauth-token")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, spec, err := verification.Prepare(context.Background(), t.TempDir(), newBackend); err != nil || len(spec.Protected) != 1 || spec.Protected[0] != token {
		t.Errorf("with token file: protected = %v, err = %v", spec.Protected, err)
	}
}
