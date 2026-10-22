package omp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"asura/internal/backend"
	"asura/internal/backend/clisandbox"
	"asura/internal/backend/verification"
)

// 2026-10-22：issue-73 Task 3.1，紀錄的樹雜湊必須等於執行時 SnapshotTree 的結果；
// 憑證邊界 probe 指向 OMP 的憑證目錄。
func TestOMPVerificationSpec(t *testing.T) {
	root := t.TempDir()
	modules := filepath.Join(root, "node_modules")
	pkg := filepath.Join(modules, "@oh-my-pi", "pi-coding-agent")
	cli := filepath.Join(pkg, "dist", "cli.js")
	bun := filepath.Join(root, "bun")
	for path, content := range map[string]string{filepath.Join(pkg, "package.json"): `{"version":"19.0.0"}`, cli: "cli-v1", bun: "bun"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	newBackend := func(records string) backend.Backend {
		return NewAdapter(AdapterConfig{BinaryPath: cli, BunPath: bun, Home: root, VerificationPath: records})
	}
	b, spec, err := verification.Prepare(context.Background(), t.TempDir(), newBackend)
	if err != nil {
		t.Fatal(err)
	}
	cliSum, _ := verification.FileDigest(cli)
	bunSum, _ := verification.FileDigest(bun)
	snapshot, err := clisandbox.SnapshotTree(modules, filepath.Join(t.TempDir(), "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	want := verification.Identity{Runtime: "omp", Version: "19.0.0", Digest: verification.CombineDigests(cliSum, bunSum)}
	agent := filepath.Join(root, ".omp", "agent")
	if spec.Builtin || spec.Record.Identity != want || spec.Record.Tree != snapshot || spec.Options.CredentialDir != agent ||
		len(spec.Protected) != 1 || spec.Protected[0] != filepath.Join(agent, "agent.db") || spec.Options.ReadOnlyRoles {
		t.Fatalf("spec = %+v, want identity %+v, tree %s, credential dir %s", spec, want, snapshot, agent)
	}
	if _, _, trees, err := b.(*Adapter).pins(context.Background()); err != nil || trees[0].SHA256 != snapshot {
		t.Errorf("pins with temporary records: trees=%+v err=%v", trees, err)
	}
	if _, err := newBackend("").Version(context.Background()); err == nil {
		t.Error("candidate admitted without records")
	}
}
