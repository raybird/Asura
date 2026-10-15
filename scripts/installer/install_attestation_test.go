package installer

// 2026-10-15：issue-47，發布者身分驗證（SCN-001～SCN-005）。gh 以 stub 取代，
// 本檔只驗 installer 如何呼叫 gh 與處理結果；簽章本身的有效性由真實 Release 驗證。

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const attTag = "v1.2.0"

func attestedRelease(t *testing.T, ghMode string) (*harness, *release) {
	h := newHarness(t)
	if ghMode != "" {
		h.useGh(ghMode)
	}
	r := newRelease(t, attTag)
	r.isLatest = true
	h.publish(r)
	return h, r
}

func logged(res result, token string) bool {
	for _, r := range res.Requests {
		if r == token {
			return true
		}
	}
	return false
}

func TestAttestationVerifiedInstall(t *testing.T) {
	h, r := attestedRelease(t, "ok")
	res := h.run(installScript(t))
	if res.Code != 0 {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	assertInstalled(t, filepath.Join(h.InstallDir, "asura"), attTag, "amd64")
	call := h.ghVerifyCall()
	args := strings.Split(strings.TrimSuffix(call.args, "\n"), "\n")
	if len(args) != 12 || args[0] != "attestation" || args[1] != "verify" || filepath.Base(args[2]) != "asura_v1.2.0_linux_amd64.tar.gz" || args[3] != "--bundle" {
		t.Fatalf("gh args=%q", args)
	}
	if got := strings.Join(args[5:], " "); got != "--repo raybird/Asura --signer-workflow raybird/Asura/.github/workflows/release.yml --source-ref refs/tags/v1.2.0 --deny-self-hosted-runners" {
		t.Fatalf("gh identity pins=%q", got)
	}
	if call.subject != string(r.assets["asura_v1.2.0_linux_amd64.tar.gz"]) || call.bundle != string(r.attestation) {
		t.Fatal("gh did not receive the published archive and attestation")
	}
	if !requested(res, downloadURL+"v1.2.0/asura_v1.2.0_attestation.sigstore.json") {
		t.Fatalf("attestation not downloaded: %v", res.Requests)
	}
	if !strings.Contains(res.Stdout, "Verified publisher attestation for asura_v1.2.0_linux_amd64.tar.gz\n") || strings.Contains(res.Stderr, "warning") {
		t.Fatalf("stdout=%s stderr=%s", res.Stdout, res.Stderr)
	}
}

// 驗證在 checksum 之後：checksum 不符時不呼叫 gh。
func TestAttestationAfterChecksum(t *testing.T) {
	h := newHarness(t)
	h.useGh("ok")
	r := newRelease(t, attTag)
	r.isLatest = true
	r.manifest = []string{strings.Repeat("0", 64) + "  asura_v1.2.0_linux_amd64.tar.gz"}
	h.publish(r)
	res := h.run(installScript(t))
	if res.Code == 0 || !strings.Contains(res.Stderr, "checksum mismatch") || logged(res, "gh-verify") {
		t.Fatalf("code=%d stderr=%s requests=%v", res.Code, res.Stderr, res.Requests)
	}
}

func TestAttestationFailureKeepsExistingBinary(t *testing.T) {
	for _, c := range []struct {
		name, ghMode string
		missing      bool
		want         string
	}{
		{"gh-rejects", "fail", false, "publisher attestation verification failed for asura_v1.2.0_linux_amd64.tar.gz: Error: verifying with issuer \"sigstore.dev\""},
		{"attestation-missing", "ok", true, "cannot download publisher attestation for v1.2.0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.useGh(c.ghMode)
			r := newRelease(t, attTag)
			r.isLatest = true
			if c.missing {
				r.attestation = nil
			}
			h.publish(r)
			s := h.installSentinel()
			res := h.run(installScript(t))
			if res.Code == 0 || !strings.Contains(res.Stderr, "asura-install: error: "+c.want) || strings.Contains(res.Stdout, "Installed") {
				t.Fatalf("code=%d stdout=%s stderr=%s", res.Code, res.Stdout, res.Stderr)
			}
			if v := h.violations(s); len(v) > 0 {
				t.Fatalf("violations: %v", v)
			}
			if c.missing && logged(res, "gh-verify") {
				t.Fatal("gh verify called without an attestation")
			}
		})
	}
}

func TestAttestationUnavailableGhWarns(t *testing.T) {
	for _, c := range []struct{ ghMode, reason string }{
		{"", "gh not found"},
		{"old", "gh does not support attestation verification (gh 2.68.0+ required)"},
		{"no-source-ref", "gh does not support attestation verification (gh 2.68.0+ required)"},
	} {
		t.Run("gh="+c.ghMode, func(t *testing.T) {
			h, _ := attestedRelease(t, c.ghMode)
			res := h.run(installScript(t))
			if res.Code != 0 {
				t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
			}
			assertInstalled(t, filepath.Join(h.InstallDir, "asura"), attTag, "amd64")
			if !strings.Contains(res.Stderr, "asura-install: warning: publisher not verified ("+c.reason+"); only the SHA-256 checksum was checked\n") {
				t.Fatalf("stderr=%s", res.Stderr)
			}
			if logged(res, "gh-verify") || requested(res, downloadURL+"v1.2.0/asura_v1.2.0_attestation.sigstore.json") {
				t.Fatalf("requests=%v", res.Requests)
			}
		})
	}
}

func TestAttestationRequiredWithoutGh(t *testing.T) {
	for _, c := range []struct{ ghMode, reason string }{
		{"", "gh not found"},
		{"old", "gh does not support attestation verification (gh 2.68.0+ required)"},
	} {
		t.Run("gh="+c.ghMode, func(t *testing.T) {
			h, _ := attestedRelease(t, c.ghMode)
			res := h.run(installScript(t), "--require-attestation")
			assertNothingInstalled(t, h, res)
			if !strings.Contains(res.Stderr, "asura-install: error: --require-attestation: "+c.reason+"\n") {
				t.Fatalf("stderr=%s", res.Stderr)
			}
		})
	}
}

// v1.1.0 是唯一免驗的版本；v1.1.0 的 prerelease 不免驗。
func TestAttestationV110(t *testing.T) {
	const notice = "asura-install: warning: publisher not verified (v1.1.0 has no publisher attestation); only the SHA-256 checksum was checked\n"
	for _, c := range []struct{ name, ghMode string }{{"with-gh", "ok"}, {"without-gh", ""}} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			if c.ghMode != "" {
				h.useGh(c.ghMode)
			}
			r := newRelease(t, stableTag)
			r.isLatest = true
			h.publish(r)
			res := h.run(installScript(t))
			if res.Code != 0 {
				t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
			}
			assertInstalled(t, filepath.Join(h.InstallDir, "asura"), stableTag, "amd64")
			if !strings.Contains(res.Stderr, notice) || logged(res, "gh-verify") || requested(res, downloadURL+"v1.1.0/asura_v1.1.0_attestation.sigstore.json") {
				t.Fatalf("stderr=%s requests=%v", res.Stderr, res.Requests)
			}
		})
	}
	t.Run("require-latest", func(t *testing.T) {
		h := newHarness(t)
		h.useGh("ok")
		r := newRelease(t, stableTag)
		r.isLatest = true
		h.publish(r)
		res := h.run(installScript(t), "--require-attestation")
		if res.Code == 0 || !strings.Contains(res.Stderr, "asura-install: error: --require-attestation: v1.1.0 has no publisher attestation\n") {
			t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
		}
		var http []string
		for _, req := range res.Requests {
			if strings.HasPrefix(req, "http") {
				http = append(http, req)
			}
		}
		if strings.Join(http, " ") != latestURL {
			t.Fatalf("requests beyond latest API: %v", http)
		}
		if _, err := os.Lstat(h.InstallDir); !os.IsNotExist(err) {
			t.Fatalf("install-dir touched: %v", err)
		}
	})
	t.Run("require-explicit", func(t *testing.T) {
		h := newHarness(t)
		h.useGh("ok")
		h.publish(newRelease(t, stableTag))
		res := h.run(installScript(t), "--version", "v1.1.0", "--require-attestation")
		assertNothingInstalled(t, h, res)
		if !strings.Contains(res.Stderr, "asura-install: error: --require-attestation: v1.1.0 has no publisher attestation\n") {
			t.Fatalf("stderr=%s", res.Stderr)
		}
	})
	t.Run("prerelease-not-exempt", func(t *testing.T) {
		h := newHarness(t)
		h.useGh("ok")
		r := newRelease(t, "v1.1.0-rc.1")
		h.publish(r)
		res := h.run(installScript(t), "--version", "v1.1.0-rc.1")
		if res.Code != 0 {
			t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
		}
		if call := h.ghVerifyCall(); !strings.Contains(call.args, "--source-ref\nrefs/tags/v1.1.0-rc.1\n") || !bytes.Equal([]byte(call.bundle), r.attestation) {
			t.Fatalf("gh args=%q", call.args)
		}
	})
}
