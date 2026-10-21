package omp

import (
	"asura/internal/backend/clisandbox"
	"context"
	"path/filepath"
)

// 2026-10-04：僅檢查 package layout、固定 CLI/Bun 與 sandbox dependency。
// 2026-10-21：CLI/Bun 改為內建或已驗證的身分（issue-73）。
func (a *Adapter) Probe(ctx context.Context) (string, error) {
	p, err := a.admit(ctx)
	if err != nil {
		return "", err
	}
	if err := clisandbox.ProbeSandboxDependency(ctx); err != nil {
		return "", err
	}
	return "omp/" + p.version, nil
}

// ProbeExecutable 回報 Probe 驗證的 OMP CLI 路徑（建構時已解析 symlink）；找不到時為空字串。
// 找不到時建構結果為 EvalSymlinks("") 的 "."，因此只回報絕對路徑。
func (a *Adapter) ProbeExecutable() string {
	if filepath.IsAbs(a.cfg.BinaryPath) {
		return a.cfg.BinaryPath
	}
	return ""
}
