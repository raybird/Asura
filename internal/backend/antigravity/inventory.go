package antigravity

import (
	"asura/internal/backend"
	"asura/internal/backend/clisandbox"
	"context"
	"path/filepath"
)

// 2026-10-04：不讀取或驗證 token，僅驗必要本機條件。
func (a *Adapter) Probe(ctx context.Context) (string, error) {
	if err := clisandbox.VerifyProbeFile(ctx, a.cfg.BinaryPath, cliHash, backend.ExecutableNotFound); err != nil {
		return "", err
	}
	if err := clisandbox.ProbeSandboxDependency(ctx); err != nil {
		return "", err
	}
	return Version, nil
}

// ProbeExecutable 回報 Probe 驗證的 agy 路徑（建構時已解析 symlink）；找不到時為空字串。
// 找不到時建構結果為 EvalSymlinks("") 的 "."，因此只回報絕對路徑。
func (a *Adapter) ProbeExecutable() string {
	if filepath.IsAbs(a.cfg.BinaryPath) {
		return a.cfg.BinaryPath
	}
	return ""
}
