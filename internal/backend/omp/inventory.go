package omp

import (
	"asura/internal/backend"
	"asura/internal/backend/clisandbox"
	"context"
	"os"
	"path/filepath"
)

// 2026-10-04：僅檢查 package layout、固定 CLI/Bun 與 sandbox dependency。
func (a *Adapter) Probe(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := os.Stat(a.cfg.BinaryPath); err != nil {
		return "", &backend.ProbeError{Reason: backend.ExecutableNotFound}
	}
	modules := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(a.cfg.BinaryPath))))
	if filepath.Base(modules) != "node_modules" || filepath.Join(modules, "@oh-my-pi", "pi-coding-agent", "dist", "cli.js") != a.cfg.BinaryPath {
		return "", &backend.ProbeError{Reason: backend.ConfigurationInvalid}
	}
	if err := clisandbox.VerifyProbeFile(ctx, a.cfg.BinaryPath, cliHash, backend.ExecutableNotFound); err != nil {
		return "", err
	}
	if err := clisandbox.VerifyProbeFile(ctx, a.cfg.BunPath, bunHash, backend.DependencyNotFound); err != nil {
		return "", err
	}
	if err := clisandbox.ProbeSandboxDependency(ctx); err != nil {
		return "", err
	}
	return "omp/" + Version, nil
}
