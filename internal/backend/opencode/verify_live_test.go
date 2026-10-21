package opencode_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"asura/internal/backend"
	"asura/internal/backend/opencode"
	"asura/internal/backend/verification"
	"asura/internal/daemon"
)

// 2026-10-21：issue-73 Phase 1，以 daemon 相同的唯讀與寫入 adapter 設定對已安裝的 OpenCode 執行共用 probe。
// ASURA_VERIFY_OPENCODE_BIN 指定另一份安裝做對照；readOnlyBin／strikerBin 非空時改用失效對照組的 shim。
func liveOpenCode(t *testing.T, readOnlyBin, strikerBin string) (backend.Backend, chan int) {
	t.Helper()
	if !verification.LiveEnabled() {
		t.Skip("set ASURA_VERIFY_LIVE=1 to run model-calling probes")
	}
	real := openCodeBinary(t)
	out, err := exec.Command(real, "--version").Output()
	if err != nil {
		t.Fatalf("opencode --version: %v", err)
	}
	version := strings.TrimSpace(string(out))
	if readOnlyBin == "" {
		readOnlyBin = real
	}
	if strikerBin == "" {
		strikerBin = real
	}
	dir, cleanup, err := opencode.CreateIsolatedReadOnlyConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	started := make(chan int, 4)
	onStarted := func(_ context.Context, pgid int) {
		select {
		case started <- pgid:
		default:
		}
	}
	readOnly := opencode.NewAdapter(opencode.AdapterConfig{BinaryPath: readOnlyBin, ExpectedVersion: version, ConfigDir: dir,
		Permission: daemon.DefaultReadOnlyPermissionPolicy, DisableAuto: true, Role: opencode.RoleOracle,
		Agent: opencode.AsuraReadOnlyAgentName, Pure: true, DisableProjectConfig: true, Isolated: true, OnProcessStarted: onStarted})
	striker := opencode.NewAdapter(opencode.AdapterConfig{BinaryPath: strikerBin, ExpectedVersion: version,
		Permission: daemon.DefaultApprovedPermissionPolicy, Role: opencode.RoleStriker, OnProcessStarted: onStarted})
	return opencode.NewRoleRoutingBackend(readOnly, striker), started
}

func openCodeBinary(t *testing.T) string {
	if p := os.Getenv("ASURA_VERIFY_OPENCODE_BIN"); p != "" {
		return p
	}
	p, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode not installed")
	}
	return p
}

func runOpenCodeProbes(t *testing.T, b backend.Backend, probes []verification.Probe) []verification.Result {
	t.Helper()
	base, err := verification.NewBase()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("probe base: %s", base)
	results := verification.Run(context.Background(), b, base, probes, nil)
	summary, _ := verification.Summary(results)
	t.Logf("results:\n%s", summary)
	return results
}

func TestVerifyLive_OpenCode(t *testing.T) {
	b, started := liveOpenCode(t, "", "")
	if _, ok := verification.Summary(runOpenCodeProbes(t, b, verification.Probes(verification.Options{ReadOnlyRoles: true, Started: started}))); !ok {
		t.Error("opencode probes failed")
	}
}

// 失效對照組：唯讀 shim 拿掉 asura-readonly agent 並放行所有權限；寫入 shim 放行所有權限。
func TestVerifyLive_OpenCodeControls(t *testing.T) {
	if !verification.LiveEnabled() {
		t.Skip("set ASURA_VERIFY_LIVE=1 to run model-calling probes")
	}
	real, dir := openCodeBinary(t), t.TempDir()
	readOnlyShim, err := verification.WriteShim(dir, "opencode-ro", real, `skip=
for a; do shift
  if [ -n "$skip" ]; then skip=; continue; fi
  case "$a" in --agent) skip=1 ;; *) set -- "$@" "$a" ;; esac
done
OPENCODE_PERMISSION='{"*":"allow"}' exec "$REAL" "$@"`)
	if err != nil {
		t.Fatal(err)
	}
	strikerShim, err := verification.WriteShim(dir, "opencode-rw", real, `OPENCODE_PERMISSION='{"*":"allow"}' exec "$REAL" "$@"`)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := liveOpenCode(t, readOnlyShim, strikerShim)
	var probes []verification.Probe
	for _, p := range verification.Probes(verification.Options{ReadOnlyRoles: true}) {
		if p.Name == verification.ProbeReadOnly || p.Name == verification.ProbeWorkspace {
			probes = append(probes, p)
		}
	}
	for _, r := range runOpenCodeProbes(t, b, probes) {
		if r.Err == nil || !strings.Contains(r.Err.Error(), "security violation") {
			t.Errorf("control %s did not detect the degraded sandbox: %v", r.Name, r.Err)
		}
	}
}
