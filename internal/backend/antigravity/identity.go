package antigravity

import (
	"context"
	"errors"

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
