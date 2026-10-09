package opencode

import (
	"asura/internal/backend"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// 2026-10-04：只探測明確的本機版本／配置，不初始化 workspace 或登入。
func (a *Adapter) Probe(ctx context.Context) (string, error) {
	if a.cfg.Isolated {
		if a.cfg.ConfigDir == "" || !a.cfg.DisableAuto || !a.cfg.Pure || !a.cfg.DisableProjectConfig {
			return "", &backend.ProbeError{Reason: backend.ConfigurationInvalid}
		}
		info, err := os.Stat(filepath.Join(a.cfg.ConfigDir, "opencode", "opencode.json"))
		if err != nil || !info.Mode().IsRegular() {
			return "", &backend.ProbeError{Reason: backend.ConfigurationInvalid}
		}
	}
	path := a.cfg.BinaryPath
	if path == "" {
		if home, err := os.UserHomeDir(); err == nil {
			candidate := filepath.Join(home, ".cache/asura-probe-issue-2/install/node_modules/.bin/opencode")
			if version, err := backend.ProbeVersionCommand(ctx, candidate); err == nil && strings.HasPrefix(version, a.cfg.ExpectedVersion) {
				a.probedExecutable.Store(&candidate)
				return version, nil
			}
		}
		var err error
		path, err = exec.LookPath("opencode")
		if err != nil {
			a.probedExecutable.Store(&path)
			return "", &backend.ProbeError{Reason: backend.ExecutableNotFound}
		}
	}
	a.probedExecutable.Store(&path)
	version, err := backend.ProbeVersionCommand(ctx, path)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(version, a.cfg.ExpectedVersion) {
		return "", &backend.ProbeError{Reason: backend.VersionFailed}
	}
	return version, nil
}
func (b *RoleRoutingBackend) Probe(ctx context.Context) (string, error) {
	if b.readOnly == nil || b.striker == nil {
		return "", &backend.ProbeError{Reason: backend.ConfigurationInvalid}
	}
	versions := []string{}
	for _, adapter := range []backend.Backend{b.readOnly, b.striker} {
		var version string
		var err error
		if p, ok := adapter.(backend.RuntimeProber); ok {
			version, err = p.Probe(ctx)
		} else {
			version, err = adapter.Version(ctx)
		}
		if err != nil {
			return "", err
		}
		versions = append(versions, version)
	}
	if versions[0] != versions[1] {
		return "", &backend.ProbeError{Reason: backend.ConfigurationInvalid}
	}
	return versions[1], nil
}

// ProbeExecutable 回報最近一次 Probe 執行的 binary；尚未 probe 或找不到時為空字串。
func (a *Adapter) ProbeExecutable() string {
	if p := a.probedExecutable.Load(); p != nil {
		return *p
	}
	return ""
}

// ProbeExecutable 回報最近一次 probe 到的 binary；Probe 先探唯讀角色，失敗時 striker 不會被探測。
func (b *RoleRoutingBackend) ProbeExecutable() string {
	for _, adapter := range []backend.Backend{b.striker, b.readOnly} {
		if r, ok := adapter.(backend.ExecutableReporter); ok {
			if path := r.ProbeExecutable(); path != "" {
				return path
			}
		}
	}
	return ""
}
