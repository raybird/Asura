package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"asura/internal/backend"
	"asura/internal/backend/verification"
)

// 2026-10-21：issue-73 Phase 1，以 daemon 相同的 adapter 設定在 Asura 的 bwrap 內執行共用 probe；
// 登入憑證在全部 probe 前後必須逐 byte 相同。
func runLive(t *testing.T, probes func(chan int) []verification.Probe) []verification.Result {
	t.Helper()
	if !verification.LiveEnabled() {
		t.Skip("set ASURA_VERIFY_LIVE=1 to run model-calling probes")
	}
	// snap 版 go 會讓測試行程帶著 SNAP_NAME 等變數，libsecret 因而查不到使用者 keyring，
	// adapter 會改讀過期的 token 檔（2026-10-22）。正式的 asura 執行檔沒有這些變數。
	for _, kv := range os.Environ() {
		if k, v, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "SNAP") {
			os.Unsetenv(k)
			t.Cleanup(func() { os.Setenv(k, v) })
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan int, 4)
	onStarted := func(_ context.Context, pgid int) {
		select {
		case started <- pgid:
		default:
		}
	}
	a := NewAdapter(AdapterConfig{OnProcessStarted: onStarted})
	// 已安裝版本不是內建身分時，以實際安裝的身分寫一份暫時紀錄，才能對新版本執行 probe。
	if version, err := backend.ProbeVersionCommand(context.Background(), a.cfg.BinaryPath); err == nil {
		digest, err := verification.FileDigest(a.cfg.BinaryPath)
		if err != nil {
			t.Fatal(err)
		}
		records, err := verification.TempRecords(t.TempDir(), verification.Record{Identity: verification.Identity{Runtime: "antigravity", Version: version, Digest: digest}})
		if err != nil {
			t.Fatal(err)
		}
		a.cfg.VerificationPath = records
	}
	if _, err := a.Version(context.Background()); err != nil {
		t.Skipf("antigravity not usable: %v", err)
	}
	base, err := verification.NewBase()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("probe base: %s", base)
	results := verification.Run(context.Background(), a, base, probes(started), []string{filepath.Join(home, credentialFile)})
	summary, _ := verification.Summary(results)
	t.Logf("results:\n%s", summary)
	return results
}

const credentialFile = ".gemini/antigravity-cli/antigravity-oauth-token"

func credentialDir(t *testing.T) string {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Join(home, credentialFile))
}

func TestVerifyLive_Antigravity(t *testing.T) {
	results := runLive(t, func(started chan int) []verification.Probe {
		return verification.Probes(verification.Options{Started: started, CredentialDir: credentialDir(t)})
	})
	if _, ok := verification.Summary(results); !ok {
		t.Error("antigravity probes failed")
	}
}

// 失效對照組：包裝過的 bwrap 把 probe 根目錄以可寫方式掛進沙箱，workspace 外的寫入必須被抓到。
func TestVerifyLive_AntigravityControl(t *testing.T) {
	root, err := verification.Root()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := verification.BwrapControl(t.TempDir(), root)
	if err != nil {
		t.Skip(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	results := runLive(t, func(chan int) []verification.Probe {
		for _, p := range verification.Probes(verification.Options{}) {
			if p.Name == verification.ProbeWorkspace {
				return []verification.Probe{p}
			}
		}
		return nil
	})
	for _, r := range results {
		if r.Name == verification.ProbeWorkspace && (r.Err == nil || !strings.Contains(r.Err.Error(), "security violation")) {
			t.Errorf("control did not detect the degraded sandbox: %v", r.Err)
		}
	}
}

// 失效對照組：包裝過的 bwrap 把真實的憑證目錄以可寫方式掛進沙箱，憑證邊界 probe 必須失敗。
func TestVerifyLive_AntigravityCredentialControl(t *testing.T) {
	dir, err := verification.BwrapControl(t.TempDir(), credentialDir(t))
	if err != nil {
		t.Skip(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	results := runLive(t, func(chan int) []verification.Probe {
		for _, p := range verification.Probes(verification.Options{CredentialDir: credentialDir(t)}) {
			if p.Name == verification.ProbeCredential {
				return []verification.Probe{p}
			}
		}
		return nil
	})
	for _, r := range results {
		if r.Name == verification.ProbeCredential && (r.Err == nil || !strings.Contains(r.Err.Error(), "security violation")) {
			t.Errorf("control did not detect the writable credential directory: %v", r.Err)
		}
	}
}
