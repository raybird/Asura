package codex

import (
	"asura/internal/backend"
	"asura/internal/backend/verification"
	"context"
	"errors"
	"os/exec"
)

// 2026-10-04：admission 版本政策與 execution 相同，不讀取登入憑證。
func (a *Adapter) Probe(ctx context.Context) (string, error) {
	path := a.ProbeExecutable()
	if path == "" {
		return "", &backend.ProbeError{Reason: backend.ExecutableNotFound}
	}
	version, err := backend.ProbeVersionCommand(ctx, path)
	if err != nil {
		return "", err
	}
	if err := a.admit(version, path); err != nil {
		if errors.Is(err, verification.ErrUnverified) {
			return "", &backend.ProbeError{Reason: backend.VersionUnverified}
		}
		return "", &backend.ProbeError{Reason: backend.VersionFailed}
	}
	return version, nil
}

// ProbeExecutable 回報 Probe 會執行的 codex binary；找不到時為空字串。
func (a *Adapter) ProbeExecutable() string {
	if a.cfg.BinaryPath != "" {
		return a.cfg.BinaryPath
	}
	path, _ := exec.LookPath("codex")
	return path
}
