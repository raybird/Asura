package release

// 2026-10-09：issue-45 Task 1.2，release archive 契約：命名、archive root、checksum 與 version metadata 注入。

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const (
	tag       = "v9.9.9-test"
	commitSHA = "deadbeefcafe"
)

func TestBuildArchivesContract(t *testing.T) {
	out := t.TempDir()
	cmd := exec.Command("sh", "scripts/release/build-archives.sh", tag, commitSHA, out)
	cmd.Dir = "../.."
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build-archives: %v\n%s", err, b)
	}
	var files []string
	entries, _ := os.ReadDir(out)
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	want := []string{"asura_v9.9.9-test_checksums.txt", "asura_v9.9.9-test_linux_amd64.tar.gz", "asura_v9.9.9-test_linux_arm64.tar.gz"}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Fatalf("assets=%v", files)
	}
	check := exec.Command("sha256sum", "-c", "asura_v9.9.9-test_checksums.txt")
	check.Dir = out
	if b, err := check.CombinedOutput(); err != nil || strings.Count(string(b), ": OK") != 2 {
		t.Fatalf("checksums: %v %s", err, b)
	}
	for arch, machine := range map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64} {
		root := "asura_v9.9.9-test_linux_" + arch + "/"
		names, bin := extract(t, filepath.Join(out, "asura_v9.9.9-test_linux_"+arch+".tar.gz"), t.TempDir())
		if got := strings.Join(names, ","); got != root+","+root+"CHANGELOG.md,"+root+"LICENSE,"+root+"README.md,"+root+"asura" {
			t.Fatalf("%s archive entries=%s", arch, got)
		}
		f, err := elf.Open(bin)
		if err != nil {
			t.Fatal(err)
		}
		if f.Machine != machine {
			t.Fatalf("%s machine=%v", arch, f.Machine)
		}
		f.Close()
		// 非原生架構無法執行，以 binary 內含注入字面值確認 ldflags 生效。
		if b, _ := os.ReadFile(bin); !bytes.Contains(b, []byte(tag)) || !bytes.Contains(b, []byte(commitSHA)) {
			t.Fatalf("%s binary lacks injected version metadata", arch)
		}
		if arch == runtime.GOARCH {
			raw, err := exec.Command(bin, "version", "--json").Output()
			var got map[string]string
			if err != nil || json.Unmarshal(raw, &got) != nil || got["version"] != tag || got["commit"] != commitSHA || got["os"] != "linux" || got["arch"] != arch {
				t.Fatalf("%s version=%q err=%v", arch, raw, err)
			}
		}
	}
}

// release.yml 必須使用同一支腳本建置，且上傳不得覆寫已發佈 asset。
func TestReleaseWorkflowUsesContract(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	wf := string(raw)
	if !strings.Contains(wf, `sh scripts/release/build-archives.sh "${TAG}" "$(git rev-parse HEAD)" dist`) {
		t.Fatal("release.yml does not build via scripts/release/build-archives.sh")
	}
	if strings.Contains(wf, "--clobber") {
		t.Fatal("release.yml still overwrites published assets")
	}
}

func extract(t *testing.T, archive, dir string) ([]string, string) {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	var bin string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
		if strings.HasSuffix(h.Name, "/asura") && h.Typeflag == tar.TypeReg {
			bin = filepath.Join(dir, "asura")
			b, _ := io.ReadAll(tr)
			if err := os.WriteFile(bin, b, 0755); err != nil {
				t.Fatal(err)
			}
		}
	}
	sort.Strings(names)
	return names, bin
}
