package omp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"asura/internal/backend/clisandbox"
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
	// 已安裝版本不是內建身分時，以實際安裝的身分寫一份暫時紀錄，才能對新版本執行 probe。
	if version, err := packageVersion(filepath.Join(filepath.Dir(filepath.Dir(a.cfg.BinaryPath)), "package.json")); err == nil {
		cli, cliErr := verification.FileDigest(a.cfg.BinaryPath)
		bun, bunErr := verification.FileDigest(a.cfg.BunPath)
		tree, treeErr := clisandbox.TreeHash(a.modules())
		if err := errors.Join(cliErr, bunErr, treeErr); err != nil {
			t.Fatal(err)
		}
		records, err := verification.TempRecords(t.TempDir(), verification.Record{Identity: verification.Identity{Runtime: "omp", Version: version, Digest: verification.CombineDigests(cli, bun)}, Tree: tree})
		if err != nil {
			t.Fatal(err)
		}
		a.cfg.VerificationPath = records
	}
	if _, err := a.Version(context.Background()); err != nil {
		t.Skipf("omp not usable: %v", err)
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

const credentialFile = ".omp/agent/agent.db"

func credentialDir(t *testing.T) string {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Join(home, credentialFile))
}

func TestVerifyLive_Omp(t *testing.T) {
	results := runLive(t, func(started chan int) []verification.Probe {
		return verification.Probes(verification.Options{Started: started, CredentialDir: credentialDir(t)})
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

// 失效對照組：包裝過的 bwrap 把真實的憑證目錄以可寫方式掛進沙箱，憑證邊界 probe 必須失敗。
func TestVerifyLive_OmpCredentialControl(t *testing.T) {
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
