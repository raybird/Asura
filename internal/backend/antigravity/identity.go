package antigravity

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"asura/internal/backend"
	"asura/internal/backend/clisandbox"
	"asura/internal/backend/verification"
)

// admit 2026-10-21：issue-73，hash 為內建時不執行 agy；其他 hash 以 agy --version 取版本，需要本機驗證紀錄。
func (a *Adapter) admit(ctx context.Context) (version, digest string, err error) {
	digest, err = clisandbox.ProbeFileDigest(ctx, a.cfg.BinaryPath, backend.ExecutableNotFound)
	if err != nil {
		return "", "", err
	}
	if digest == cliHash {
		return Version, digest, nil
	}
	if version, err = backend.ProbeVersionCommand(ctx, a.cfg.BinaryPath); err != nil {
		return "", "", err
	}
	_, err = verification.Admit("antigravity", version, func() (string, error) { return digest, nil },
		[]verification.Identity{{Runtime: "antigravity", Version: Version, Digest: cliHash}}, a.cfg.VerificationPath)
	if errors.Is(err, verification.ErrUnverified) {
		return "", "", &backend.ProbeError{Reason: backend.VersionUnverified}
	}
	if err != nil {
		return "", "", &backend.ProbeError{Reason: backend.VersionFailed}
	}
	return version, digest, nil
}

// VerificationSpec 2026-10-22：issue-73，Antigravity 跑憑證邊界 probe；token 檔存在時才列入前後不變檢查
// （keyring 登入時可能沒有這個檔案）。
func (a *Adapter) VerificationSpec(ctx context.Context) (verification.Spec, error) {
	digest, err := clisandbox.ProbeFileDigest(ctx, a.cfg.BinaryPath, backend.ExecutableNotFound)
	if err != nil {
		return verification.Spec{}, err
	}
	token := filepath.Join(a.cfg.Home, ".gemini/antigravity-cli/antigravity-oauth-token")
	spec := verification.Spec{Builtin: digest == cliHash, Options: verification.Options{CredentialDir: filepath.Dir(token)}}
	if _, err := os.Lstat(token); err == nil {
		spec.Protected = []string{token}
	}
	spec.Record.Runtime, spec.Record.Version, spec.Record.Digest = "antigravity", Version, digest
	if !spec.Builtin {
		spec.Record.Version, err = backend.ProbeVersionCommand(ctx, a.cfg.BinaryPath)
	}
	return spec, err
}
