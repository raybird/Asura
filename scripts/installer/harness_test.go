package installer

// 2026-10-09：issue-45 Task 0.2，installer 測試 harness。
// 以 PATH 前置的 curl／wget／uname stub 把 URL 映射到本地 fixture，驅動真實 sh；
// installer 本身不加任何測試專用參數。

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	latestURL   = "https://api.github.com/repos/raybird/Asura/releases/latest"
	downloadURL = "https://github.com/raybird/Asura/releases/download/"
)

// fetchStub 同時作為 curl 與 wget：-o／-O 指定輸出檔，其餘參數中以 http 開頭者為 URL。
// routes.tsv 每行為「URL<TAB>ok|404|partial<TAB>fixture 路徑」；未登錄的 URL 視為 404。
const fetchStub = `#!/bin/sh
out=-
url=
while [ $# -gt 0 ]; do
  case "$1" in
    -o|-O) out="$2"; shift ;;
    -qO) out="$2"; shift ;;
    http*) url="$1" ;;
  esac
  shift
done
printf '%s %s\n' "$(basename "$0")" "$url" >> "$STUB_LOG"
route=$(awk -F '\t' -v u="$url" '$1 == u { print $2 "\t" $3; exit }' "$STUB_ROUTES")
kind=${route%%	*}
file=${route#*	}
case "$kind" in
  ok) if [ "$out" = - ]; then cat "$file"; else cat "$file" > "$out"; fi; exit 0 ;;
  partial) if [ "$out" = - ]; then head -c 64 "$file"; else head -c 64 "$file" > "$out"; fi; exit 18 ;;
  *) echo "stub: 404 $url" >&2; exit 22 ;;
esac
`

const unameStub = `#!/bin/sh
printf 'uname %s\n' "$*" >> "$STUB_LOG"
case "$1" in
  -m) echo "$STUB_UNAME_M" ;;
  *) echo "$STUB_UNAME_S" ;;
esac
`

// sudoStub 只記錄呼叫並失敗；installer 不得呼叫 sudo 或 systemctl。
const sudoStub = `#!/bin/sh
printf '%s %s\n' "$(basename "$0")" "$*" >> "$STUB_LOG"
exit 1
`

// 2026-10-15：issue-47，gh stub。STUB_GH 決定行為：ok／fail 為驗證結果，old 沒有 attestation 指令，
// no-source-ref 模擬缺 --source-ref 的舊版。驗證呼叫的參數逐行寫入 STUB_GH_ARGS，
// 受驗 archive 與 --bundle 檔內容複製到 STUB_GH_SUBJECT／STUB_GH_BUNDLE。
const ghStub = `#!/bin/sh
if [ "$*" = "attestation verify --help" ]; then
  echo gh-help >> "$STUB_LOG"
  case "$STUB_GH" in
    old) echo 'unknown command "attestation" for "gh"' >&2; exit 1 ;;
    no-source-ref) printf '  -b, --bundle string\n      --signer-workflow string\n'; exit 0 ;;
    *) printf '  -b, --bundle string\n      --signer-workflow string\n      --source-ref string\n'; exit 0 ;;
  esac
fi
echo gh-verify >> "$STUB_LOG"
printf '%s\n' "$@" > "$STUB_GH_ARGS"
cp "$3" "$STUB_GH_SUBJECT"
while [ $# -gt 0 ]; do
  if [ "$1" = --bundle ]; then cp "$2" "$STUB_GH_BUNDLE"; fi
  shift
done
case "$STUB_GH" in
  ok) exit 0 ;;
  *) echo 'Error: verifying with issuer "sigstore.dev"' >&2; exit 1 ;;
esac
`

// ghCall 是 gh stub 最後一次驗證呼叫的紀錄。
type ghCall struct {
	args, subject, bundle string
}

// useGh 在 PATH 放入 gh stub；mode 見 ghStub。
func (h *harness) useGh(mode string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.stubs, "gh"), []byte(ghStub), 0755); err != nil {
		h.t.Fatal(err)
	}
	h.ExtraEnv = append(h.ExtraEnv, "STUB_GH="+mode, "STUB_GH_ARGS="+filepath.Join(h.root, "gh-args"),
		"STUB_GH_SUBJECT="+filepath.Join(h.root, "gh-subject"), "STUB_GH_BUNDLE="+filepath.Join(h.root, "gh-bundle"))
}

func (h *harness) ghVerifyCall() ghCall {
	h.t.Helper()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(h.root, name))
		if err != nil {
			h.t.Fatalf("gh verify not called: %v", err)
		}
		return string(b)
	}
	return ghCall{args: read("gh-args"), subject: read("gh-subject"), bundle: read("gh-bundle")}
}

// drop 讓 stub 或系統工具在 PATH 中消失，用於缺工具情境。
func (h *harness) drop(tools ...string) {
	for _, tool := range tools {
		os.Remove(filepath.Join(h.stubs, tool))
		os.Remove(filepath.Join(h.sysbin, tool))
	}
}

// fixtureBinary 是放進 archive 的假 asura：只回應 version --json。
func fixtureBinary(tag, arch string) []byte {
	return []byte(fmt.Sprintf("#!/bin/sh\nif [ \"$1 $2\" = \"version --json\" ]; then echo '{\"version\":\"%s\",\"commit\":\"fixture\",\"os\":\"linux\",\"arch\":\"%s\"}'; exit 0; fi\nexit 1\n", tag, arch))
}

type entry struct {
	name     string
	body     []byte
	mode     int64
	typeflag byte
	link     string
}

func assetName(tag, arch string) string { return fmt.Sprintf("asura_%s_linux_%s.tar.gz", tag, arch) }
func manifestName(tag string) string    { return fmt.Sprintf("asura_%s_checksums.txt", tag) }
func attestationName(tag string) string {
	return fmt.Sprintf("asura_%s_attestation.sigstore.json", tag)
}

// standardEntries 與 release.yml 的 archive root 及內容一致。
func standardEntries(tag, arch string) []entry {
	root := fmt.Sprintf("asura_%s_linux_%s/", tag, arch)
	return []entry{
		{name: root + "asura", body: fixtureBinary(tag, arch), mode: 0755},
		{name: root + "LICENSE", body: []byte("license\n"), mode: 0644},
		{name: root + "README.md", body: []byte("readme\n"), mode: 0644},
		{name: root + "CHANGELOG.md", body: []byte("changelog\n"), mode: 0644},
	}
}

func buildArchive(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: e.typeflag, Linkname: e.link}
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write(e.body)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// release 描述一個 fixture Release；故障情境以修改欄位表達，再由 publish 寫成 routes。
type release struct {
	tag      string
	assets   map[string][]byte // asset 檔名 → 內容
	manifest []string          // checksum manifest 行；nil 表示依 assets 自動產生
	missing  map[string]bool   // 回 404 的 asset
	partial  map[string]bool   // 下載中斷的 asset
	isLatest bool
	// 2026-10-15：issue-47，驗證檔內容；nil 表示 Release 沒有驗證檔（v1.1.0 即如此）。
	attestation []byte
}

func newRelease(t *testing.T, tag string) *release {
	r := &release{tag: tag, assets: map[string][]byte{}, missing: map[string]bool{}, partial: map[string]bool{}}
	for _, arch := range []string{"amd64", "arm64"} {
		r.assets[assetName(tag, arch)] = buildArchive(t, standardEntries(tag, arch))
	}
	if tag != "v1.1.0" {
		r.attestation = []byte(`{"fixture":"attestation ` + tag + `"}`)
	}
	return r
}

func (r *release) manifestLines() []string {
	if r.manifest != nil {
		return r.manifest
	}
	var names []string
	for n := range r.assets {
		names = append(names, n)
	}
	sort.Strings(names)
	var lines []string
	for _, n := range names {
		lines = append(lines, sha(r.assets[n])+"  "+n)
	}
	return lines
}

// systemTools 是 harness PATH 中唯一可見的系統工具；installer 只能依賴這些基本工具與 stub。
var systemTools = []string{"sh", "tar", "gzip", "sha256sum", "shasum", "mktemp", "awk", "sed", "grep", "mkdir", "cp", "chmod", "mv", "rm", "cat", "head", "wc", "tr", "basename", "dirname", "ls", "ln", "id"}

type harness struct {
	t                                  *testing.T
	root, stubs, sysbin, fixtures, log string
	routes                             *os.File
	Home, InstallDir, TmpDir           string
	UnameS, UnameM                     string
	ExtraEnv                           []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	h := &harness{t: t, root: root, stubs: filepath.Join(root, "stubs"), sysbin: filepath.Join(root, "sysbin"), fixtures: filepath.Join(root, "fixtures"),
		log: filepath.Join(root, "requests.log"), Home: filepath.Join(root, "home"), TmpDir: filepath.Join(root, "tmp"),
		UnameS: "Linux", UnameM: "x86_64"}
	h.InstallDir = filepath.Join(h.Home, ".local", "bin")
	for _, d := range []string{h.stubs, h.sysbin, h.fixtures, h.Home, h.TmpDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"curl": fetchStub, "wget": fetchStub, "uname": unameStub, "sudo": sudoStub, "systemctl": sudoStub} {
		if err := os.WriteFile(filepath.Join(h.stubs, name), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range systemTools {
		if path, err := exec.LookPath(tool); err == nil {
			if err := os.Symlink(path, filepath.Join(h.sysbin, tool)); err != nil {
				t.Fatal(err)
			}
		}
	}
	f, err := os.Create(filepath.Join(root, "routes.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	h.routes = f
	return h
}

func (h *harness) route(url, kind string, body []byte) {
	h.t.Helper()
	path := filepath.Join(h.fixtures, sha([]byte(url)))
	if err := os.WriteFile(path, body, 0600); err != nil {
		h.t.Fatal(err)
	}
	fmt.Fprintf(h.routes, "%s\t%s\t%s\n", url, kind, path)
}

// publish 將 Release 寫成 routes：latest API、各 asset 與 checksum manifest。
func (h *harness) publish(r *release) {
	if r.isLatest {
		h.route(latestURL, "ok", []byte(fmt.Sprintf(`{"tag_name":"%s","prerelease":false,"draft":false}`, r.tag)))
	}
	for name, body := range r.assets {
		kind := "ok"
		if r.missing[name] {
			continue
		}
		if r.partial[name] {
			kind = "partial"
		}
		h.route(downloadURL+r.tag+"/"+name, kind, body)
	}
	h.route(downloadURL+r.tag+"/"+manifestName(r.tag), "ok", []byte(strings.Join(r.manifestLines(), "\n")+"\n"))
	if r.attestation != nil {
		h.route(downloadURL+r.tag+"/"+attestationName(r.tag), "ok", r.attestation)
	}
}

type result struct {
	Code           int
	Stdout, Stderr string
	Requests       []string
}

// run 以真實 sh 執行 script，PATH 以 stub 目錄優先，其餘工具使用系統版本。
func (h *harness) run(script string, args ...string) result {
	h.t.Helper()
	cmd := exec.Command(filepath.Join(h.sysbin, "sh"), append([]string{script}, args...)...)
	cmd.Dir = h.root
	cmd.Env = append([]string{
		"HOME=" + h.Home, "TMPDIR=" + h.TmpDir, "PATH=" + h.stubs + ":" + h.sysbin,
		"STUB_LOG=" + h.log, "STUB_ROUTES=" + h.routes.Name(), "STUB_UNAME_S=" + h.UnameS, "STUB_UNAME_M=" + h.UnameM,
	}, h.ExtraEnv...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		h.t.Fatal(err)
	}
	logged, _ := os.ReadFile(h.log)
	os.Remove(h.log)
	return result{Code: code, Stdout: out.String(), Stderr: errb.String(), Requests: strings.Fields(strings.ReplaceAll(string(logged), "\n", " "))}
}

// sentinel 是 installer 故障判準：既有 binary 的內容、權限與 install-dir／TMPDIR 殘留。
type sentinel struct {
	path    string
	content []byte
	mode    os.FileMode
	dirList []string
}

var sentinelBody = []byte("#!/bin/sh\necho sentinel-asura-v0\n")

func (h *harness) installSentinel() *sentinel {
	h.t.Helper()
	if err := os.MkdirAll(h.InstallDir, 0755); err != nil {
		h.t.Fatal(err)
	}
	path := filepath.Join(h.InstallDir, "asura")
	if err := os.WriteFile(path, sentinelBody, 0755); err != nil {
		h.t.Fatal(err)
	}
	return &sentinel{path: path, content: sentinelBody, mode: 0755, dirList: h.list(h.InstallDir)}
}

func (h *harness) list(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// violations 回傳判準違反清單；空清單代表既有安裝完整且無殘留。
func (h *harness) violations(s *sentinel) []string {
	var v []string
	info, err := os.Lstat(s.path)
	switch {
	case err != nil:
		v = append(v, "sentinel missing: "+err.Error())
	case !info.Mode().IsRegular():
		v = append(v, "sentinel type changed: "+info.Mode().String())
	default:
		if got, _ := os.ReadFile(s.path); !bytes.Equal(got, s.content) {
			v = append(v, "sentinel content changed: "+sha(got))
		}
		if info.Mode().Perm() != s.mode {
			v = append(v, "sentinel mode changed: "+info.Mode().Perm().String())
		}
	}
	if got := strings.Join(h.list(h.InstallDir), ","); got != strings.Join(s.dirList, ",") {
		v = append(v, "install-dir residue: "+got)
	}
	if left := h.list(h.TmpDir); len(left) > 0 {
		v = append(v, "TMPDIR residue: "+strings.Join(left, ","))
	}
	return v
}

func (h *harness) script(name, body string) string {
	h.t.Helper()
	path := filepath.Join(h.root, name)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		h.t.Fatal(err)
	}
	return path
}
