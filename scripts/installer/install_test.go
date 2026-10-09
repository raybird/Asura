package installer

// 2026-10-09：issue-45 Task 2.1，install.sh 主流程、平台判定與前置工具檢查（DIST-001／005）。

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const stableTag = "v1.1.0"

func installScript(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// latestRelease 建立 harness 並發佈一個 latest stable fixture Release。
func latestRelease(t *testing.T) (*harness, *release) {
	h := newHarness(t)
	r := newRelease(t, stableTag)
	r.isLatest = true
	h.publish(r)
	return h, r
}

func requested(res result, url string) bool {
	for _, r := range res.Requests {
		if r == url {
			return true
		}
	}
	return false
}

func assertInstalled(t *testing.T, path, tag, arch string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("binary missing: %v", err)
	}
	if !bytes.Equal(got, fixtureBinary(tag, arch)) {
		t.Fatalf("installed binary is not the %s %s fixture", tag, arch)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0755 {
		t.Fatalf("mode=%v", info.Mode().Perm())
	}
}

func assertNothingInstalled(t *testing.T, h *harness, res result) {
	t.Helper()
	if res.Code == 0 {
		t.Fatalf("expected failure: %s", res.Stdout)
	}
	if _, err := os.Lstat(h.InstallDir); !os.IsNotExist(err) {
		t.Fatalf("install-dir touched: %v", err)
	}
	if left := h.list(h.TmpDir); len(left) > 0 {
		t.Fatalf("TMPDIR residue: %v", left)
	}
	for _, r := range res.Requests {
		if strings.HasPrefix(r, "http") {
			t.Fatalf("downloaded before failing: %v", res.Requests)
		}
	}
}

func TestInstallFreshAmd64(t *testing.T) {
	h, _ := latestRelease(t)
	res := h.run(installScript(t))
	if res.Code != 0 {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	assertInstalled(t, filepath.Join(h.InstallDir, "asura"), stableTag, "amd64")
	for _, url := range []string{latestURL, downloadURL + "v1.1.0/asura_v1.1.0_linux_amd64.tar.gz", downloadURL + "v1.1.0/asura_v1.1.0_checksums.txt"} {
		if !requested(res, url) {
			t.Fatalf("missing request %s: %v", url, res.Requests)
		}
	}
	if requested(res, downloadURL+"v1.1.0/asura_v1.1.0_linux_arm64.tar.gz") {
		t.Fatal("downloaded arm64 asset on amd64")
	}
	for _, r := range res.Requests {
		if r == "sudo" || r == "systemctl" {
			t.Fatalf("installer called %s", r)
		}
	}
	if got := strings.Join(h.list(h.InstallDir), ","); got != "asura" {
		t.Fatalf("install-dir=%s", got)
	}
	if left := h.list(h.TmpDir); len(left) > 0 {
		t.Fatalf("TMPDIR residue: %v", left)
	}
	if !strings.Contains(res.Stdout, h.InstallDir+" is not in PATH") {
		t.Fatalf("missing PATH hint: %s", res.Stdout)
	}
	if _, err := os.Stat(filepath.Join(h.Home, ".profile")); !os.IsNotExist(err) {
		t.Fatal("shell rc modified")
	}
}

func TestInstallExplicitDirWithSpace(t *testing.T) {
	h, _ := latestRelease(t)
	dir := filepath.Join(h.Home, "my tools", "bin")
	res := h.run(installScript(t), "--install-dir", dir)
	if res.Code != 0 {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	assertInstalled(t, filepath.Join(dir, "asura"), stableTag, "amd64")
	if _, err := os.Stat(h.InstallDir); !os.IsNotExist(err) {
		t.Fatal("default install-dir created")
	}
}

func TestInstallUnsupportedPlatform(t *testing.T) {
	for _, c := range []struct{ s, m string }{{"Darwin", "x86_64"}, {"Linux", "riscv64"}, {"Linux", "i686"}} {
		t.Run(c.s+"-"+c.m, func(t *testing.T) {
			h, _ := latestRelease(t)
			h.UnameS, h.UnameM = c.s, c.m
			res := h.run(installScript(t))
			assertNothingInstalled(t, h, res)
			if !strings.Contains(res.Stderr, "asura-install: error: unsupported platform: "+c.s+"/"+c.m) {
				t.Fatalf("stderr=%s", res.Stderr)
			}
		})
	}
}

func TestInstallMissingTools(t *testing.T) {
	for _, c := range []struct {
		drop []string
		msg  string
	}{
		{[]string{"curl", "wget"}, "curl or wget"},
		{[]string{"tar"}, "tar"},
		{[]string{"sha256sum", "shasum"}, "sha256sum or shasum"},
		{[]string{"uname"}, "uname"},
	} {
		t.Run(strings.Join(c.drop, "+"), func(t *testing.T) {
			h, _ := latestRelease(t)
			h.drop(c.drop...)
			res := h.run(installScript(t))
			assertNothingInstalled(t, h, res)
			if !strings.Contains(res.Stderr, "asura-install: error: missing required tool: "+c.msg+"\n") {
				t.Fatalf("stderr=%s", res.Stderr)
			}
		})
	}
}

func TestInstallToolFallbacks(t *testing.T) {
	for _, c := range []struct{ drop, want string }{{"curl", "wget"}} {
		t.Run("without-"+c.drop, func(t *testing.T) {
			h, _ := latestRelease(t)
			h.drop(c.drop)
			res := h.run(installScript(t))
			if res.Code != 0 {
				t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
			}
			assertInstalled(t, filepath.Join(h.InstallDir, "asura"), stableTag, "amd64")
			if c.want != "" && !strings.Contains(strings.Join(res.Requests, " "), c.want+" ") {
				t.Fatalf("did not use %s: %v", c.want, res.Requests)
			}
		})
	}
}

func TestInstallUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"--version"}, {"--install-dir"}} {
		h, _ := latestRelease(t)
		res := h.run(installScript(t), args...)
		if res.Code != 2 || !strings.Contains(res.Stderr, "Usage: install.sh [--version vX.Y.Z] [--install-dir PATH]") {
			t.Fatalf("%v code=%d stderr=%s", args, res.Code, res.Stderr)
		}
		assertNothingInstalled(t, h, res)
	}
}

// 2026-10-09：issue-45 Task 2.2，arm64 主機選用 arm64 asset（DIST-002）。
func TestInstallArm64Asset(t *testing.T) {
	for _, machine := range []string{"aarch64", "arm64"} {
		t.Run(machine, func(t *testing.T) {
			h, _ := latestRelease(t)
			h.UnameM = machine
			res := h.run(installScript(t))
			if res.Code != 0 {
				t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
			}
			assertInstalled(t, filepath.Join(h.InstallDir, "asura"), stableTag, "arm64")
			if !requested(res, downloadURL+"v1.1.0/asura_v1.1.0_linux_arm64.tar.gz") || requested(res, downloadURL+"v1.1.0/asura_v1.1.0_linux_amd64.tar.gz") {
				t.Fatalf("requests=%v", res.Requests)
			}
		})
	}
}
