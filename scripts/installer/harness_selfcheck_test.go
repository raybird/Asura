package installer

import (
	"archive/tar"
	"strings"
	"testing"
)

// 2026-10-09：issue-45 Task 0.2，先證明故障判準本身抓得到破壞，再用於 installer 測試。

func TestSentinelOracleDetectsDamage(t *testing.T) {
	cases := []struct {
		name, body string
		want       string // 空字串表示判準必須回報無違反
	}{
		{"noop", "exit 1\n", ""},
		{"overwrite", "printf 'new' > \"$HOME/.local/bin/asura\"\n", "sentinel content changed"},
		{"chmod", "chmod 0700 \"$HOME/.local/bin/asura\"\n", "sentinel mode changed"},
		{"remove", "rm \"$HOME/.local/bin/asura\"\n", "sentinel missing"},
		{"symlink", "rm \"$HOME/.local/bin/asura\" && ln -s /bin/true \"$HOME/.local/bin/asura\"\n", "sentinel type changed"},
		{"stage-residue", "printf 'partial' > \"$HOME/.local/bin/.asura.tmp.1\"\n", "install-dir residue"},
		{"tmp-residue", "mkdir \"$TMPDIR/asura-dl\"\n", "TMPDIR residue"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			s := h.installSentinel()
			h.run(h.script("control.sh", c.body))
			v := strings.Join(h.violations(s), "; ")
			if c.want == "" && v != "" {
				t.Fatalf("clean control flagged: %s", v)
			}
			if c.want != "" && !strings.Contains(v, c.want) {
				t.Fatalf("damage not detected, want %q got %q", c.want, v)
			}
		})
	}
}

// 控制腳本以 harness 的 stub 取得 latest／asset／manifest 並自行核對 checksum，
// 證明 fixture 與 stub 對正常與各故障情境給出可區分的結果。
const fetchControl = `set -e
tag=$(curl -fsSL https://api.github.com/repos/raybird/Asura/releases/latest | sed -n 's/.*"tag_name":"\([^"]*\)".*/\1/p')
arch=$(uname -m)
[ "$arch" = x86_64 ] && arch=amd64
[ "$arch" = aarch64 ] && arch=arm64
name="asura_${tag}_linux_${arch}.tar.gz"
d=$(mktemp -d)
trap 'rm -rf "$d"' EXIT
curl -fsSL -o "$d/$name" "https://github.com/raybird/Asura/releases/download/$tag/$name"
curl -fsSL -o "$d/sums" "https://github.com/raybird/Asura/releases/download/$tag/asura_${tag}_checksums.txt"
n=$(awk -v f="$name" '$2 == f' "$d/sums" | wc -l)
[ "$n" -eq 1 ] || { echo "manifest entries: $n" >&2; exit 3; }
(cd "$d" && awk -v f="$name" '$2 == f' sums | sha256sum -c - >/dev/null) || { echo "checksum mismatch" >&2; exit 4; }
tar -tzf "$d/$name"
`

func TestFixtureFaultsAreDistinguishable(t *testing.T) {
	const tag = "v1.1.0"
	cases := []struct {
		name    string
		arch    string
		mutate  func(t *testing.T, r *release)
		code    int
		stderr  string
		listing string
	}{
		{name: "ok-amd64", arch: "x86_64", code: 0, listing: "asura_v1.1.0_linux_amd64/asura"},
		{name: "ok-arm64", arch: "aarch64", code: 0, listing: "asura_v1.1.0_linux_arm64/asura"},
		{name: "tampered", arch: "x86_64", code: 4, stderr: "checksum mismatch", mutate: func(t *testing.T, r *release) {
			r.manifest = r.manifestLines()
			r.assets[assetName(tag, "amd64")] = buildArchive(t, append(standardEntries(tag, "amd64"), entry{name: "asura_v1.1.0_linux_amd64/extra", body: []byte("x"), mode: 0644}))
		}},
		{name: "missing-asset", arch: "x86_64", code: 22, stderr: "404", mutate: func(t *testing.T, r *release) { r.missing[assetName(tag, "amd64")] = true }},
		{name: "manifest-missing-entry", arch: "x86_64", code: 3, stderr: "manifest entries: 0", mutate: func(t *testing.T, r *release) {
			r.manifest = []string{sha(r.assets[assetName(tag, "arm64")]) + "  " + assetName(tag, "arm64")}
		}},
		{name: "manifest-duplicate-entry", arch: "x86_64", code: 3, stderr: "manifest entries: 2", mutate: func(t *testing.T, r *release) {
			r.manifest = append(r.manifestLines(), sha([]byte("other"))+"  "+assetName(tag, "amd64"))
		}},
		{name: "interrupted", arch: "x86_64", code: 18, mutate: func(t *testing.T, r *release) { r.partial[assetName(tag, "amd64")] = true }},
		{name: "path-traversal", arch: "x86_64", code: 0, listing: "../evil", mutate: func(t *testing.T, r *release) {
			r.assets[assetName(tag, "amd64")] = buildArchive(t, append(standardEntries(tag, "amd64"), entry{name: "../evil", body: []byte("x"), mode: 0644}))
		}},
		{name: "absolute-path", arch: "x86_64", code: 0, listing: "/tmp/evil", mutate: func(t *testing.T, r *release) {
			r.assets[assetName(tag, "amd64")] = buildArchive(t, append(standardEntries(tag, "amd64"), entry{name: "/tmp/evil", body: []byte("x"), mode: 0644}))
		}},
		{name: "symlink-entry", arch: "x86_64", code: 0, listing: "asura_v1.1.0_linux_amd64/asura", mutate: func(t *testing.T, r *release) {
			r.assets[assetName(tag, "amd64")] = buildArchive(t, []entry{{name: "asura_v1.1.0_linux_amd64/asura", typeflag: tar.TypeSymlink, link: "/bin/sh"}})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.UnameM = c.arch
			r := newRelease(t, tag)
			r.isLatest = true
			if c.mutate != nil {
				c.mutate(t, r)
			}
			h.publish(r)
			res := h.run(h.script("fetch.sh", fetchControl))
			if res.Code != c.code || !strings.Contains(res.Stderr, c.stderr) || !strings.Contains(res.Stdout, c.listing) {
				t.Fatalf("code=%d stdout=%q stderr=%q", res.Code, res.Stdout, res.Stderr)
			}
			if len(res.Requests) == 0 || !strings.Contains(strings.Join(res.Requests, " "), "releases/latest") {
				t.Fatalf("stub log missing requests: %v", res.Requests)
			}
		})
	}
}
