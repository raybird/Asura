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
	if err := ctx.Err(); err != nil {
		return pinned{}, err
	}
	if _, err := os.Stat(a.cfg.BinaryPath); err != nil {
		return pinned{}, &backend.ProbeError{Reason: backend.ExecutableNotFound}
	}
	modules := a.modules()
	if filepath.Base(modules) != "node_modules" || filepath.Join(modules, "@oh-my-pi", "pi-coding-agent", "dist", "cli.js") != a.cfg.BinaryPath {
		return pinned{}, &backend.ProbeError{Reason: backend.ConfigurationInvalid}
	}
	cli, err := clisandbox.ProbeFileDigest(ctx, a.cfg.BinaryPath, backend.ExecutableNotFound)
	if err != nil {
		return pinned{}, err
	}
	bun, err := clisandbox.ProbeFileDigest(ctx, a.cfg.BunPath, backend.DependencyNotFound)
	if err != nil {
		return pinned{}, err
	}
	digest := verification.CombineDigests(cli, bun)
	builtin := verification.Identity{Runtime: "omp", Version: Version, Digest: verification.CombineDigests(cliHash, bunHash)}
	if digest == builtin.Digest {
		return pinned{Version, cli, bun, treeHash}, nil
	}
	version, err := packageVersion(filepath.Join(filepath.Dir(filepath.Dir(a.cfg.BinaryPath)), "package.json"))
	if err != nil {
		return pinned{}, &backend.ProbeError{Reason: backend.VersionFailed}
	}
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
