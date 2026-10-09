package installer

// 2026-10-09：issue-45 Task 2.3，安裝失敗保留既有 binary、重裝與升降版（DIST-003／004）。

import (
	"archive/tar"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// userState 是 installer 不得觸碰的 config 與資料 fixture。
func userState(t *testing.T, h *harness) func() {
	t.Helper()
	files := map[string][]byte{
		filepath.Join(h.Home, ".config", "asura", "config.toml"):      []byte("schema_version = 1\n"),
		filepath.Join(h.Home, ".local", "share", "asura", "asura.db"): []byte("sqlite-fixture"),
		filepath.Join(h.Home, "work", "asura.db"):                     []byte("legacy-db-fixture"),
	}
	for path, body := range files {
		os.MkdirAll(filepath.Dir(path), 0700)
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		t.Helper()
		for path, body := range files {
			if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, body) {
				t.Fatalf("user state changed: %s", path)
			}
		}
	}
}

func assertNoServiceCalls(t *testing.T, res result) {
	t.Helper()
	for _, r := range res.Requests {
		if r == "systemctl" || r == "sudo" {
			t.Fatalf("installer called %s", r)
		}
	}
}

func TestInstallFailuresPreserveExistingBinary(t *testing.T) {
	tag := stableTag
	amd := assetName(tag, "amd64")
	root := "asura_v1.1.0_linux_amd64/"
	withEntries := func(entries ...entry) func(t *testing.T, h *harness, r *release) {
		return func(t *testing.T, h *harness, r *release) {
			r.assets[amd] = buildArchive(t, entries)
		}
	}
	cases := []struct {
		name   string
		args   []string
		setup  func(t *testing.T, h *harness, r *release)
		stderr string
	}{
		{"checksum-mismatch", nil, func(t *testing.T, h *harness, r *release) {
			r.manifest = r.manifestLines()
			r.assets[amd] = buildArchive(t, append(standardEntries(tag, "amd64"), entry{name: root + "NOTICE", body: []byte("tampered"), mode: 0644}))
		}, "checksum mismatch"},
		{"checksum-mismatch-shasum", nil, func(t *testing.T, h *harness, r *release) {
			h.drop("sha256sum")
			r.manifest = r.manifestLines()
			r.assets[amd] = buildArchive(t, append(standardEntries(tag, "amd64"), entry{name: root + "NOTICE", body: []byte("tampered"), mode: 0644}))
		}, "checksum mismatch"},
		{"missing-asset", nil, func(t *testing.T, h *harness, r *release) { r.missing[amd] = true }, "cannot download " + amd},
		{"manifest-missing-entry", nil, func(t *testing.T, h *harness, r *release) {
			r.manifest = []string{sha(r.assets[assetName(tag, "arm64")]) + "  " + assetName(tag, "arm64")}
		}, "checksum manifest has 0 entries for " + amd},
		{"manifest-duplicate-entry", nil, func(t *testing.T, h *harness, r *release) {
			r.manifest = append(r.manifestLines(), sha(r.assets[amd])+"  "+amd)
		}, "checksum manifest has 2 entries for " + amd},
		{"interrupted-download", nil, func(t *testing.T, h *harness, r *release) { r.partial[amd] = true }, "cannot download " + amd},
		{"path-traversal", nil, withEntries(append(standardEntries(tag, "amd64"), entry{name: root + "../evil", body: []byte("x"), mode: 0644})...), "unsafe archive"},
		{"outside-root", nil, withEntries(append(standardEntries(tag, "amd64"), entry{name: "evil", body: []byte("x"), mode: 0644})...), "unsafe archive"},
		{"absolute-path", nil, withEntries(append(standardEntries(tag, "amd64"), entry{name: "/tmp/evil", body: []byte("x"), mode: 0644})...), "unsafe archive"},
		{"symlink-binary", nil, withEntries(entry{name: root + "asura", typeflag: tar.TypeSymlink, link: "/bin/sh"}), "unsafe archive"},
		{"missing-binary", nil, withEntries(entry{name: root + "LICENSE", body: []byte("x"), mode: 0644}), "does not contain " + root + "asura"},
		{"version-mismatch", nil, withEntries(entry{name: root + "asura", body: fixtureBinary("v9.0.0", "amd64"), mode: 0755}), "staged binary reports"},
		{"arch-mismatch", nil, withEntries(entry{name: root + "asura", body: fixtureBinary(tag, "arm64"), mode: 0755}), "staged binary reports"},
		{"replace-failure", nil, func(t *testing.T, h *harness, r *release) {
			os.Chmod(h.InstallDir, 0555)
			t.Cleanup(func() { os.Chmod(h.InstallDir, 0755) })
		}, "cannot write to " + "INSTALL_DIR"},
		{"v1.0.0-without-metadata", []string{"--version", "v1.0.0"}, nil, "v1.0.0 predates installer support"},
		{"invalid-version", []string{"--version", "v1.1.0/../x"}, nil, "invalid version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			s := h.installSentinel()
			unchanged := userState(t, h)
			r := newRelease(t, tag)
			r.isLatest = true
			if c.setup != nil {
				c.setup(t, h, r)
			}
			h.publish(r)
			res := h.run(installScript(t), c.args...)
			if res.Code == 0 {
				t.Fatalf("expected failure: %s", res.Stdout)
			}
			want := strings.ReplaceAll(c.stderr, "INSTALL_DIR", h.InstallDir)
			if !strings.Contains(res.Stderr, "asura-install: error: ") || !strings.Contains(res.Stderr, want) {
				t.Fatalf("stderr=%q want %q", res.Stderr, want)
			}
			os.Chmod(h.InstallDir, 0755)
			if v := h.violations(s); len(v) > 0 {
				t.Fatalf("existing install damaged: %v", v)
			}
			unchanged()
			assertNoServiceCalls(t, res)
		})
	}
}

func TestInstallRefusesNonRegularTarget(t *testing.T) {
	for _, kind := range []string{"symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			h, _ := latestRelease(t)
			os.MkdirAll(h.InstallDir, 0755)
			target := filepath.Join(h.InstallDir, "asura")
			dev := filepath.Join(h.Home, "src", "asura")
			os.MkdirAll(filepath.Dir(dev), 0755)
			os.WriteFile(dev, sentinelBody, 0755)
			if kind == "symlink" {
				os.Symlink(dev, target)
			} else {
				os.Mkdir(target, 0755)
			}
			res := h.run(installScript(t))
			if res.Code == 0 || !strings.Contains(res.Stderr, "asura-install: error: "+target+" is not a regular file") || !strings.Contains(res.Stderr, "--install-dir") {
				t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
			}
			info, err := os.Lstat(target)
			if kind == "symlink" {
				link, _ := os.Readlink(target)
				if err != nil || info.Mode()&os.ModeSymlink == 0 || link != dev {
					t.Fatal("symlink changed")
				}
			} else if err != nil || !info.IsDir() {
				t.Fatal("directory changed")
			}
			if got, _ := os.ReadFile(dev); !bytes.Equal(got, sentinelBody) {
				t.Fatal("symlink target written")
			}
			if left := h.list(h.InstallDir); strings.Join(left, ",") != "asura" {
				t.Fatalf("install-dir residue: %v", left)
			}
		})
	}
}

func TestInstallReinstallUpgradeDowngrade(t *testing.T) {
	h := newHarness(t)
	unchanged := userState(t, h)
	old, latest := newRelease(t, "v1.1.0"), newRelease(t, "v1.2.0")
	latest.isLatest = true
	pre := newRelease(t, "v1.3.0-rc.1")
	h.publish(old)
	h.publish(latest)
	h.publish(pre)
	bin := filepath.Join(h.InstallDir, "asura")
	h.installSentinel()
	steps := []struct {
		name string
		args []string
		tag  string
	}{
		{"upgrade-to-latest", nil, "v1.2.0"},
		{"same-version-reinstall", nil, "v1.2.0"},
		{"explicit-downgrade", []string{"--version", "v1.1.0"}, "v1.1.0"},
		{"explicit-prerelease", []string{"--version", "v1.3.0-rc.1"}, "v1.3.0-rc.1"},
	}
	for _, s := range steps {
		res := h.run(installScript(t), s.args...)
		if res.Code != 0 {
			t.Fatalf("%s: code=%d stderr=%s", s.name, res.Code, res.Stderr)
		}
		assertInstalled(t, bin, s.tag, "amd64")
		if !requested(res, downloadURL+s.tag+"/"+assetName(s.tag, "amd64")) || !requested(res, downloadURL+s.tag+"/"+manifestName(s.tag)) {
			t.Fatalf("%s did not download and verify: %v", s.name, res.Requests)
		}
		if s.args != nil && requested(res, latestURL) {
			t.Fatalf("%s queried latest despite --version", s.name)
		}
		unchanged()
		assertNoServiceCalls(t, res)
		if got := strings.Join(h.list(h.InstallDir), ","); got != "asura" {
			t.Fatalf("%s install-dir=%s", s.name, got)
		}
	}
}

// 2026-10-09：issue-45 review S1／N1，hardlink 不得把 archive 外的檔案帶進安裝（GNU 與 busybox tar），版本字串不得含換行。
func TestInstallRejectsHardlinkAndMultilineVersion(t *testing.T) {
	for _, impl := range []string{"gnu", "busybox"} {
		t.Run("hardlink-"+impl, func(t *testing.T) {
			h := newHarness(t)
			if impl == "busybox" {
				bb, err := exec.LookPath("busybox")
				if err != nil {
					t.Skip("busybox not installed")
				}
				os.Remove(filepath.Join(h.sysbin, "tar"))
				if err := os.Symlink(bb, filepath.Join(h.sysbin, "tar")); err != nil {
					t.Fatal(err)
				}
			}
			s := h.installSentinel()
			// archive 外的檔案內容恰好能通過版本核對，若被連結進來就會被安裝。
			outside := filepath.Join(h.Home, "outside-asura")
			os.WriteFile(outside, fixtureBinary(stableTag, "amd64"), 0700)
			r := newRelease(t, stableTag)
			r.isLatest = true
			r.assets[assetName(stableTag, "amd64")] = buildArchive(t, []entry{{name: "asura_v1.1.0_linux_amd64/asura", typeflag: tar.TypeLink, link: outside}})
			h.publish(r)
			res := h.run(installScript(t))
			if res.Code == 0 {
				t.Fatalf("hardlink archive installed: %s", res.Stdout)
			}
			if v := h.violations(s); len(v) > 0 {
				t.Fatalf("existing install damaged: %v", v)
			}
		})
	}
	t.Run("multiline-version", func(t *testing.T) {
		h, _ := latestRelease(t)
		res := h.run(installScript(t), "--version", "v1.1.0\n../x")
		if res.Code == 0 || !strings.Contains(res.Stderr, "invalid version") {
			t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
		}
		for _, r := range res.Requests {
			if strings.HasPrefix(r, "http") {
				t.Fatalf("downloaded with invalid version: %v", res.Requests)
			}
		}
	})
}
