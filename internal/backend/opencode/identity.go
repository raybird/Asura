package opencode

import (
	"context"
	"os/exec"
	"path/filepath"

	"asura/internal/backend"

	"asura/internal/backend/verification"
)

// admitOpenCode 2026-10-21：issue-73，內建 expected 版本精確比對；其他版本需要本機驗證紀錄。
func admitOpenCode(version, bin, expected, recordsPath string) error {
	_, err := verification.Admit("opencode", version, func() (string, error) {
		real, err := filepath.EvalSymlinks(bin)
		if err != nil {
			return "", err
		}
		return verification.FileDigest(real)
	}, []verification.Identity{{Runtime: "opencode", Version: expected}}, recordsPath)
	return err
}

// VerificationSpec 2026-10-22：issue-73，候選執行檔與 resolveOpenCodeBinary 的順序相同：
// 隔離安裝的內建版本優先，否則 PATH；OpenCode 跑唯讀角色 probe。
func (a *Adapter) VerificationSpec(ctx context.Context) (verification.Spec, error) {
	bin := a.cfg.BinaryPath
	if bin == "" {
		path, err := resolveOpenCodeBinary(ctx, "", a.cfg.ExpectedVersion, "")
		if err != nil {
			if path, err = exec.LookPath("opencode"); err != nil {
				return verification.Spec{}, ErrNoBinary
			}
		}
		bin = path
	}
	version, err := backend.ProbeVersionCommand(ctx, bin)
	if err != nil {
		return verification.Spec{}, err
	}
	spec := verification.Spec{Options: verification.Options{ReadOnlyRoles: true}}
	spec.Record.Runtime, spec.Record.Version = "opencode", version
	if version == a.cfg.ExpectedVersion {
		spec.Builtin = true
		return spec, nil
	}
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return verification.Spec{}, err
	}
	spec.Record.Digest, err = verification.FileDigest(real)
	return spec, err
}
