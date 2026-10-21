package antigravity

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"asura/internal/backend/verification"
)

// 2026-10-21：issue-73 Phase 1，以 daemon 相同的 adapter 設定在 Asura 的 bwrap 內執行共用 probe；
// 登入憑證在全部 probe 前後必須逐 byte 相同。
func runLive(t *testing.T, probes func(chan int) []verification.Probe) []verification.Result {
	t.Helper()
	if !verification.LiveEnabled() {
		t.Skip("set ASURA_VERIFY_LIVE=1 to run model-calling probes")
	}
	// 已安裝版本與內建 hash 不同時，以實際安裝的 hash 暫時放行，才能對新版本執行 probe。
	if p, err := exec.LookPath("agy"); err == nil {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			if sum, err := fileSHA256(real); err == nil {
				saved := cliHash
				cliHash = sum
				t.Cleanup(func() { cliHash = saved })
			}
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
	if _, err := a.Version(context.Background()); err != nil {
		t.Skipf("antigravity not usable: %v", err)
	}
	base, err := verification.NewBase()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("probe base: %s", base)
	results := verification.Run(context.Background(), a, base, probes(started), []string{filepath.Join(home, ".gemini/antigravity-cli/antigravity-oauth-token")})
	summary, _ := verification.Summary(results)
	t.Logf("results:\n%s", summary)
	return results
}

func TestVerifyLive_Antigravity(t *testing.T) {
	results := runLive(t, func(started chan int) []verification.Probe {
		return verification.Probes(verification.Options{Started: started})
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

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
