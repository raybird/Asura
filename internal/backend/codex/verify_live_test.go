package codex

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"asura/internal/backend"
	"asura/internal/backend/verification"
)

// 2026-10-21：issue-73 Phase 1；2026-10-22 改走 verification.Verify，與 asura runtime verify 相同的放行路徑：
// 未驗證的已安裝版本只透過暫時紀錄放行。binary 非空時改用該執行檔（失效對照組的 shim）。
func liveCodex(t *testing.T, binary string, only ...string) []verification.Result {
	t.Helper()
	if !verification.LiveEnabled() {
		t.Skip("set ASURA_VERIFY_LIVE=1 to run model-calling probes")
	}
	if binary == "" {
		real, err := codexBinary()
		if err != nil {
			t.Skip("codex not installed")
		}
		binary = real
	}
	spec, results, err := verification.Verify(context.Background(), func(records string, onStarted func(context.Context, int)) backend.Backend {
		return NewAdapter(AdapterConfig{BinaryPath: binary, VerificationPath: records, OnProcessStarted: onStarted})
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

// ASURA_VERIFY_CODEX_BIN 指定另一份未驗證的安裝做對照（內建身分不執行 probe）。
func codexBinary() (string, error) {
	if p := os.Getenv("ASURA_VERIFY_CODEX_BIN"); p != "" {
		return p, nil
	}
	return exec.LookPath("codex")
}

func TestVerifyLive_Codex(t *testing.T) {
	if _, ok := verification.Summary(liveCodex(t, "")); !ok {
		t.Error("codex probes failed")
	}
}

// 失效對照組：shim 把 read-only 換成 workspace-write、把 --approve-for-me 換成完全不設沙箱，
// 對應的 probe 必須失敗，證明 probe 抓得到沙箱失效。
func TestVerifyLive_CodexControls(t *testing.T) {
	if !verification.LiveEnabled() {
		t.Skip("set ASURA_VERIFY_LIVE=1 to run model-calling probes")
	}
	real, err := codexBinary()
	if err != nil {
		t.Skip("codex not installed")
	}
	shim, err := verification.WriteShim(t.TempDir(), "codex", real, `for a; do shift
  case "$a" in
    read-only) set -- "$@" workspace-write ;;
    --approve-for-me) set -- "$@" --dangerously-bypass-approvals-and-sandbox ;;
    *) set -- "$@" "$a" ;;
  esac
done
exec "$REAL" "$@"`)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range liveCodex(t, shim, verification.ProbeReadOnly, verification.ProbeWorkspace) {
		if r.Err == nil || !strings.Contains(r.Err.Error(), "security violation") {
			t.Errorf("control %s did not detect the degraded sandbox: %v", r.Name, r.Err)
		}
	}
}
