package antigravity

import (
	"context"
	"os"
	"path/filepath"

	"asura/internal/backend"
	"asura/internal/backend/clisandbox"
	"asura/internal/backend/verification"
)

// Builtin 是免驗證即可使用的 Antigravity 身分（2026-10-22 issue-73）。
var Builtin = verification.Identity{Runtime: "antigravity", Version: Version, Digest: cliHash}

// admit 2026-10-21：issue-73，hash 為內建時不執行 agy；其他 hash 需要本機驗證紀錄。
// 2026-10-27：PR #77 審查 Q18，只以雜湊查紀錄、版本取自紀錄，判定時不在 host 執行未驗證的 agy。
func (a *Adapter) admit(ctx context.Context) (version, digest string, err error) {
	digest, err = clisandbox.ProbeFileDigest(ctx, a.cfg.BinaryPath, backend.ExecutableNotFound)
	if err != nil {
		return "", "", err
	}
	if digest == cliHash {
		return Version, digest, nil
	}
	// AdmitDigest 的錯誤一律是 ErrUnverified（含讀不到紀錄檔）。
	rec, err := verification.AdmitDigest("antigravity", digest, a.cfg.VerificationPath)
	if err != nil {
		return "", "", &backend.ProbeError{Reason: backend.VersionUnverified}
	}
	return rec.Version, digest, nil
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
