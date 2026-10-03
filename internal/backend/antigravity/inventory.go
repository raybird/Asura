package antigravity

import (
	"asura/internal/backend"
	"asura/internal/backend/clisandbox"
	"context"
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
