package omp

import (
	"context"
	"os"
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
		t.Skipf("omp not usable: %v", err)
	}
	base, err := verification.NewBase()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("probe base: %s", base)
	results := verification.Run(context.Background(), a, base, probes(started), []string{filepath.Join(home, ".omp/agent/agent.db")})
	summary, _ := verification.Summary(results)
	t.Logf("results:\n%s", summary)
	return results
}

func TestVerifyLive_Omp(t *testing.T) {
	results := runLive(t, func(started chan int) []verification.Probe {
		return verification.Probes(verification.Options{Started: started})
	})
	if _, ok := verification.Summary(results); !ok {
		t.Error("omp probes failed")
	}
}

// 失效對照組：包裝過的 bwrap 把 probe 根目錄以可寫方式掛進沙箱，workspace 外的寫入必須被抓到。
func TestVerifyLive_OmpControl(t *testing.T) {
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
