package installer

// 2026-10-09：issue-45 Task 2.3，以 release 腳本建出的真實 archive 安裝，核對真實 binary 的 version 輸出。

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallRealReleaseArchive(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("real archive execution covers the native amd64 host only")
	}
	const tag = "v1.1.0-real"
	dist := t.TempDir()
	build := exec.Command("sh", "scripts/release/build-archives.sh", tag, "0123456789ab", dist)
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	h := newHarness(t)
	r := &release{tag: tag, assets: map[string][]byte{}, missing: map[string]bool{}, partial: map[string]bool{}}
	for _, arch := range []string{"amd64", "arm64"} {
		b, err := os.ReadFile(filepath.Join(dist, assetName(tag, arch)))
		if err != nil {
			t.Fatal(err)
		}
		r.assets[assetName(tag, arch)] = b
	}
	h.publish(r)
	res := h.run(installScript(t), "--version", tag)
	if res.Code != 0 {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	out, err := exec.Command(filepath.Join(h.InstallDir, "asura"), "version").Output()
	if err != nil || string(out) != "asura v1.1.0-real (commit 0123456789ab, linux/amd64)\n" {
		t.Fatalf("installed version=%q err=%v", out, err)
	}
}
