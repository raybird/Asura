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

// 2026-10-21：issue-73 Phase 1；2026-10-22 改走 verification.Verify，以 daemon 相同的唯讀與寫入 adapter 設定執行，
// 未驗證的已安裝版本只透過暫時紀錄放行。binary 非空時兩個角色都改用該執行檔（失效對照組的 shim）。
func liveOpenCode(t *testing.T, binary string, only ...string) []verification.Result {
	t.Helper()
	if !verification.LiveEnabled() {
		t.Skip("set ASURA_VERIFY_LIVE=1 to run model-calling probes")
	}
	if binary == "" {
		binary = openCodeBinary(t)
	}
	dir, cleanup, err := opencode.CreateIsolatedReadOnlyConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	spec, results, err := verification.Verify(context.Background(), func(records string, onStarted func(context.Context, int)) backend.Backend {
		readOnly := opencode.NewAdapter(opencode.AdapterConfig{BinaryPath: binary, VerificationPath: records, ConfigDir: dir,
			Permission: daemon.DefaultReadOnlyPermissionPolicy, DisableAuto: true, Role: opencode.RoleOracle,
			Agent: opencode.AsuraReadOnlyAgentName, Pure: true, DisableProjectConfig: true, Isolated: true, OnProcessStarted: onStarted})
		striker := opencode.NewAdapter(opencode.AdapterConfig{BinaryPath: binary, VerificationPath: records,
			Permission: daemon.DefaultApprovedPermissionPolicy, Role: opencode.RoleStriker, OnProcessStarted: onStarted})
		return opencode.NewRoleRoutingBackend(readOnly, striker)
	}, only...)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Builtin {
		t.Skipf("%s %s is a built-in verified identity; Verify runs no probes", spec.Record.Runtime, spec.Record.Version)
	}
	summary, _ := verification.Summary(results)
	t.Logf("identity %+v builtin=%v\nresults:\n%s", spec.Record.Identity, spec.Builtin, summary)
	return results
}

// ASURA_VERIFY_OPENCODE_BIN 指定另一份未驗證的安裝做對照（內建身分不執行 probe）。
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

func TestVerifyLive_OpenCode(t *testing.T) {
	if _, ok := verification.Summary(liveOpenCode(t, "")); !ok {
		t.Error("opencode probes failed")
	}
}

// 失效對照組：shim 拿掉唯讀角色的 --agent asura-readonly，並對兩個角色放行所有權限。
func TestVerifyLive_OpenCodeControls(t *testing.T) {
	if !verification.LiveEnabled() {
		t.Skip("set ASURA_VERIFY_LIVE=1 to run model-calling probes")
	}
	shim, err := verification.WriteShim(t.TempDir(), "opencode", openCodeBinary(t), `skip=
for a; do shift
  if [ -n "$skip" ]; then skip=; continue; fi
  case "$a" in --agent) skip=1 ;; *) set -- "$@" "$a" ;; esac
done
OPENCODE_PERMISSION='{"*":"allow"}' exec "$REAL" "$@"`)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range liveOpenCode(t, shim, verification.ProbeReadOnly, verification.ProbeWorkspace) {
		if r.Err == nil || !strings.Contains(r.Err.Error(), "security violation") {
			t.Errorf("control %s did not detect the degraded sandbox: %v", r.Name, r.Err)
		}
	}
}
