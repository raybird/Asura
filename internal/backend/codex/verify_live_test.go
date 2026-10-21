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

// 2026-10-21：issue-73 Phase 1，以 daemon 相同的 adapter 設定對已安裝的 Codex 執行共用 probe。
func liveCodex(t *testing.T, binary string) (*Adapter, chan int) {
	t.Helper()
	if !verification.LiveEnabled() {
		t.Skip("set ASURA_VERIFY_LIVE=1 to run model-calling probes")
	}
	real, err := codexBinary()
	if err != nil {
		t.Skip("codex not installed")
	}
	out, err := exec.Command(real, "--version").Output()
	if err != nil {
		t.Fatalf("codex --version: %v", err)
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(out)), "codex-cli ")
	if binary == "" {
		binary = real
	}
	started := make(chan int, 4)
	return NewAdapter(AdapterConfig{BinaryPath: binary, ExpectedVersion: version,
		OnProcessStarted: func(_ context.Context, pgid int) {
			select {
			case started <- pgid:
			default:
			}
		}}), started
}

// ASURA_VERIFY_CODEX_BIN 指定另一份安裝（例如內建版本 0.155.0）做對照。
func codexBinary() (string, error) {
	if p := os.Getenv("ASURA_VERIFY_CODEX_BIN"); p != "" {
		return p, nil
	}
	return exec.LookPath("codex")
}

func runCodexProbes(t *testing.T, b backend.Backend, probes []verification.Probe) []verification.Result {
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

func TestVerifyLive_Codex(t *testing.T) {
	a, started := liveCodex(t, "")
	results := runCodexProbes(t, a, verification.Probes(verification.Options{ReadOnlyRoles: true, Started: started}))
	if _, ok := verification.Summary(results); !ok {
		t.Error("codex probes failed")
	}
}

// 失效對照組：shim 把 read-only 換成 workspace-write、把 --approve-for-me 換成完全不設沙箱，
// 對應的 probe 必須失敗，證明 probe 抓得到沙箱失效。
func TestVerifyLive_CodexControls(t *testing.T) {
	real, _ := codexBinary()
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
	a, _ := liveCodex(t, shim)
	all := verification.Probes(verification.Options{ReadOnlyRoles: true})
	var probes []verification.Probe
	for _, p := range all {
		if p.Name == verification.ProbeReadOnly || p.Name == verification.ProbeWorkspace {
			probes = append(probes, p)
		}
	}
	for _, r := range runCodexProbes(t, a, probes) {
		if r.Err == nil || !strings.Contains(r.Err.Error(), "security violation") {
			t.Errorf("control %s did not detect the degraded sandbox: %v", r.Name, r.Err)
		}
	}
}
