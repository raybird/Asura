package codex

import (
	"asura/internal/backend"
	"context"
	"os/exec"
)

// 2026-10-04：admission 版本政策與 execution 相同，不讀取登入憑證。
func (a *Adapter) Probe(ctx context.Context) (string, error) {
	path := a.cfg.BinaryPath
	if path == "" {
		var err error
		path, err = exec.LookPath("codex")
		if err != nil {
			return "", &backend.ProbeError{Reason: backend.ExecutableNotFound}
		}
	}
	version, err := backend.ProbeVersionCommand(ctx, path)
	if err != nil {
		return "", err
	}
	if NormalizeCodexVersion(version) != NormalizeCodexVersion(a.cfg.ExpectedVersion) {
		return "", &backend.ProbeError{Reason: backend.VersionFailed}
	}
	return version, nil
}
