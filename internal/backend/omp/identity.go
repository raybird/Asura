package omp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"asura/internal/backend"
	"asura/internal/backend/clisandbox"
	"asura/internal/backend/verification"
)

const treeHash = "e8b948577c8d65212f00068a6dc55d293724786d698d8d18438f67d6545f5b4b"

type pinned struct{ version, cli, bun, tree string }

// admit 2026-10-21：issue-73，身分為套件版本加上 cli.js 與 Bun 的雜湊；內建身分沿用既有固定 hash。
// node_modules 樹太大，不在清冊時計算：執行時以內建或驗證紀錄中的樹雜湊固定快照內容。
func (a *Adapter) admit(ctx context.Context) (pinned, error) {
	version, cli, bun, err := a.installed(ctx)
	if err != nil {
		return pinned{}, err
	}
	builtin := verification.Identity{Runtime: "omp", Version: Version, Digest: verification.CombineDigests(cliHash, bunHash)}
	if version == "" {
		return pinned{Version, cli, bun, treeHash}, nil
	}
	digest := verification.CombineDigests(cli, bun)
	rec, err := verification.Admit("omp", version, func() (string, error) { return digest, nil },
		[]verification.Identity{builtin}, a.cfg.VerificationPath)
	if errors.Is(err, verification.ErrUnverified) {
		return pinned{}, &backend.ProbeError{Reason: backend.VersionUnverified}
	}
	if err != nil {
		return pinned{}, &backend.ProbeError{Reason: backend.VersionFailed}
	}
	return pinned{version, cli, bun, rec.Tree}, nil
}

// pins 回傳本次執行要固定的版本、檔案與相依樹雜湊。
func (a *Adapter) pins(ctx context.Context) (string, []clisandbox.PinnedFile, []clisandbox.PinnedFile, error) {
	p, err := a.admit(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	return "omp/" + p.version,
		[]clisandbox.PinnedFile{{Path: a.cfg.BunPath, SHA256: p.bun}, {Path: a.cfg.BinaryPath, SHA256: p.cli}},
		[]clisandbox.PinnedFile{{Path: a.modules(), SHA256: p.tree}}, nil
}

func (a *Adapter) modules() string {
	return filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(a.cfg.BinaryPath))))
}

func packageVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(data, &pkg); err != nil || pkg.Version == "" {
		return "", errors.New("package version missing")
	}
	return pkg.Version, nil
}

// installed 計算目前安裝的 cli.js 與 Bun 雜湊；兩者為內建 hash 時 version 為空字串，否則為套件版本。
func (a *Adapter) installed(ctx context.Context) (version, cli, bun string, err error) {
	if err := ctx.Err(); err != nil {
		return "", "", "", err
	}
	if _, err := os.Stat(a.cfg.BinaryPath); err != nil {
		return "", "", "", &backend.ProbeError{Reason: backend.ExecutableNotFound}
	}
	modules := a.modules()
	if filepath.Base(modules) != "node_modules" || filepath.Join(modules, "@oh-my-pi", "pi-coding-agent", "dist", "cli.js") != a.cfg.BinaryPath {
		return "", "", "", &backend.ProbeError{Reason: backend.ConfigurationInvalid}
	}
	if cli, err = clisandbox.ProbeFileDigest(ctx, a.cfg.BinaryPath, backend.ExecutableNotFound); err != nil {
		return "", "", "", err
	}
	if bun, err = clisandbox.ProbeFileDigest(ctx, a.cfg.BunPath, backend.DependencyNotFound); err != nil {
		return "", "", "", err
	}
	if cli == cliHash && bun == bunHash {
		return "", cli, bun, nil
	}
	if version, err = packageVersion(filepath.Join(filepath.Dir(filepath.Dir(a.cfg.BinaryPath)), "package.json")); err != nil {
		return "", "", "", &backend.ProbeError{Reason: backend.VersionFailed}
	}
	return version, cli, bun, nil
}

// VerificationSpec 2026-10-22：issue-73，紀錄的樹雜湊以 clisandbox.TreeHash 計算，與執行時 SnapshotTree 相同；
// OMP 跑憑證邊界 probe，憑證檔在全部 probe 前後不得改變。
func (a *Adapter) VerificationSpec(ctx context.Context) (verification.Spec, error) {
	version, cli, bun, err := a.installed(ctx)
	if err != nil {
		return verification.Spec{}, err
	}
	agent := filepath.Join(a.cfg.Home, ".omp", "agent")
	spec := verification.Spec{Options: verification.Options{CredentialDir: agent}, Protected: []string{filepath.Join(agent, "agent.db")}}
	spec.Record.Runtime, spec.Record.Version, spec.Record.Digest = "omp", version, verification.CombineDigests(cli, bun)
	if version == "" {
		spec.Builtin, spec.Record.Version, spec.Record.Tree = true, Version, treeHash
		return spec, nil
	}
	spec.Record.Tree, err = clisandbox.TreeHash(a.modules())
	return spec, err
}
