package omp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"asura/internal/backend"
	"asura/internal/backend/verification"
)

// 2026-10-21：issue-73，非內建的 OMP 需要本機驗證紀錄；身分為套件版本加上 cli.js 與 Bun 的雜湊，
// 執行時另以紀錄中的相依樹雜湊固定 node_modules。
func TestOMPIdentityPolicy(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "node_modules", "@oh-my-pi", "pi-coding-agent")
	cli := filepath.Join(pkg, "dist", "cli.js")
	bun := filepath.Join(root, "bun")
	records := filepath.Join(root, "data", "runtime-verification.json")
	for path, content := range map[string]string{filepath.Join(pkg, "package.json"): `{"version":"19.0.0"}`, cli: "cli-v1", bun: "bun"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	a := NewAdapter(AdapterConfig{BinaryPath: cli, BunPath: bun, Home: root, VerificationPath: records})
	inventory := func() backend.RuntimeInventoryEntry {
		s, err := backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "omp", Backend: a}})
		if err != nil {
			t.Fatal(err)
		}
		return s.Entries[0]
	}
	if got := inventory().ReasonCode; got != backend.VersionUnverified {
		t.Fatalf("unverified OMP: reason=%s", got)
	}

	cliSum, _ := verification.FileDigest(cli)
	bunSum, _ := verification.FileDigest(bun)
	rec := verification.Record{Identity: verification.Identity{Runtime: "omp", Version: "19.0.0", Digest: verification.CombineDigests(cliSum, bunSum)},
		Tree: "tree-at-verification", VerifiedAt: time.Now()}
	if err := verification.Add(records, rec); err != nil {
		t.Fatal(err)
	}
	if e := inventory(); e.ReasonCode != backend.ProbeOK || e.Descriptor == nil || e.Descriptor.Version != "omp/19.0.0" {
		t.Fatalf("verified OMP: %+v", e)
	}
	version, files, trees, err := a.pins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version != "omp/19.0.0" || len(files) != 2 || files[0].SHA256 != bunSum || files[1].SHA256 != cliSum ||
		len(trees) != 1 || trees[0].SHA256 != "tree-at-verification" {
		t.Fatalf("pins: version=%s files=%+v trees=%+v", version, files, trees)
	}

	if err := os.WriteFile(cli, []byte("cli-v2"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := inventory().ReasonCode; got != backend.VersionUnverified {
		t.Fatalf("replaced cli.js: reason=%s", got)
	}
}
