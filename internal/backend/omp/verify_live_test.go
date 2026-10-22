package omp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"asura/internal/backend"
	"asura/internal/backend/verification"
)

// 2026-10-21：issue-73 Phase 1；2026-10-22 改走 verification.Verify，以 daemon 相同的 adapter 設定在 Asura 的 bwrap 內執行，
// 未驗證的已安裝版本只透過暫時紀錄放行；登入憑證在全部 probe 前後必須逐 byte 相同。
func runLive(t *testing.T, only ...string) []verification.Result {
	t.Helper()
	if !verification.LiveEnabled() {
		t.Skip("set ASURA_VERIFY_LIVE=1 to run model-calling probes")
	}
	if _, err := exec.LookPath("omp"); err != nil {
		t.Skip("omp not installed")
	}
	spec, results, err := verification.Verify(context.Background(), func(records string, onStarted func(context.Context, int)) backend.Backend {
		return NewAdapter(AdapterConfig{VerificationPath: records, OnProcessStarted: onStarted})
	}, only...)
	if err != nil {
		t.Fatal(err)
	}
	summary, _ := verification.Summary(results)
	t.Logf("identity %+v builtin=%v\nresults:\n%s", spec.Record.Identity, spec.Builtin, summary)
	return results
}

func credentialDir(t *testing.T) string {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".omp", "agent")
}

func TestVerifyLive_Omp(t *testing.T) {
	if _, ok := verification.Summary(runLive(t)); !ok {
		t.Error("omp probes failed")
	}
}

// 失效對照組：包裝過的 bwrap 把 bind 以可寫方式掛進沙箱，probe 必須回報 security violation。
func control(t *testing.T, bind, probe string) {
	t.Helper()
	dir, err := verification.BwrapControl(t.TempDir(), bind)
	if err != nil {
		t.Skip(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, r := range runLive(t, probe) {
		if r.Name == probe && (r.Err == nil || !strings.Contains(r.Err.Error(), "security violation")) {
			t.Errorf("control did not detect the degraded sandbox: %v", r.Err)
		}
	}
}

// probe 根目錄可寫：workspace 外的寫入必須被抓到。
func TestVerifyLive_OmpControl(t *testing.T) {
	root, err := verification.Root()
	if err != nil {
		t.Fatal(err)
	}
	control(t, root, verification.ProbeWorkspace)
}

// 真實的憑證目錄可寫：憑證邊界 probe 必須失敗。
func TestVerifyLive_OmpCredentialControl(t *testing.T) {
	control(t, credentialDir(t), verification.ProbeCredential)
}
