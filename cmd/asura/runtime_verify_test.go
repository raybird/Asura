package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"asura/internal/backend/verification"
)

// verifyCodexScript 是會回應全部 verify probe 的假 codex。confine 為 true 時模擬 workspace-write 沙箱：
// 只執行目標在 -C workspace 內的重導向；read-only 模式從不執行命令，只讀出 nonce。
// auth 為 true 時每次執行都回報未登入。
func verifyCodexScript(calls, version string, confine, auth bool) string {
	s := "#!/bin/sh\necho \"$*\" >> " + calls + "\n" +
		"if [ \"$1\" = --version ]; then echo 'codex-cli " + version + "'; exit 0; fi\n" +
		"say() { printf '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"%s\"}}\\n' \"$1\"; }\n"
	if auth {
		return s + "printf '%s\\n' '{\"type\":\"error\",\"message\":\"401 Unauthorized: login required\"}'\nexit 1\n"
	}
	s += `ws= ro= prompt=
while [ $# -gt 0 ]; do
  case "$1" in -C) ws=$2 ;; read-only) ro=1 ;; esac
  prompt=$1; shift
done
case "$prompt" in
  *"Reply with exactly the text ASURA_PROBE_OK"*) say ASURA_PROBE_OK; exit 0 ;;
  *"Finally read the file "*)
    nonce=${prompt##*Finally read the file }; nonce=${nonce%% and end*}
    say "$(cat "$nonce")"; exit 0 ;;
esac
nl='
'
cmd=${prompt##*"$nl"}
[ -n "$ro" ] && { say BLOCKED; exit 0; }
IFS=';'
for part in $cmd; do
  case "$part" in
    *'>'*) target=${part##*> }
`
	if confine {
		s += `      case "$target" in "$ws"/*) sh -c "$part" ;; esac ;;
`
	} else {
		s += `      sh -c "$part" ;;
`
	}
	return s + `    *) sh -c "$part" ;;
  esac
done
say DONE
`
}

// 2026-10-22：issue-73 Task 3.2，SCN-003／SCN-004。asura runtime verify 逐項印出 probe 結果；
// 任一 probe 失敗或無法執行（未登入、bwrap 不可用、未安裝）時結束碼非 0、紀錄逐 byte 不變、仍為 version_unverified；
// 全部通過才寫入含驗證時間與 Asura 版本的紀錄，之後不論 --db、有無 config 都可用。
func TestRuntimeVerifyCommand(t *testing.T) {
	bin := buildCLI(t)
	e := newXDGEnv(t)
	ws := filepath.Join(e.root, "ws")
	if err := os.Mkdir(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	e.writeConfig(t, "schema_version = 1\nallowed_roots = [\""+ws+"\"]\n")
	records := filepath.Join(e.dataHome, "asura", "runtime-verification.json")
	other := verification.Record{Identity: verification.Identity{Runtime: "opencode", Version: "1.18.99", Digest: "aa"}, VerifiedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	if err := verification.Add(records, other); err != nil {
		t.Fatal(err)
	}
	seeded, err := os.ReadFile(records)
	if err != nil {
		t.Fatal(err)
	}
	runtimes := t.TempDir()
	codex := filepath.Join(runtimes, "codex")
	calls := filepath.Join(e.root, "calls.log")
	writeCodex := func(version string, confine, auth bool) {
		t.Helper()
		if err := os.WriteFile(codex, []byte(verifyCodexScript(calls, version, confine, auth)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	execCalls := func() int {
		data, _ := os.ReadFile(calls)
		return strings.Count(string(data), "exec ")
	}
	run := func(path string, args ...string) (string, int) {
		t.Helper()
		c := exec.Command(bin, args...)
		c.Env, c.Dir = append(e.vars(), "PATH="+path), ws
		out, err := c.CombinedOutput()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	fullPath := runtimes + ":/usr/bin:/bin"
	expectRejected := func(name, runtime, path string, wants ...string) {
		t.Helper()
		out, code := run(path, "runtime", "verify", runtime)
		t.Logf("%s:\n%s", name, out)
		if code == 0 {
			t.Errorf("%s: exit code 0", name)
		}
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Errorf("%s: output lacks %q", name, want)
			}
		}
		if data, _ := os.ReadFile(records); !bytes.Equal(data, seeded) {
			t.Errorf("%s: records changed:\n%s", name, data)
		}
		out, _ = run(fullPath, "runtime", "inventory", "--socket", "")
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, runtime+" ") && !(strings.Contains(line, "availability=unavailable") && strings.HasSuffix(line, "reason=version_unverified")) {
				t.Errorf("%s: inventory after failure: %s", name, line)
			}
		}
		if !strings.Contains(out, runtime+" registration=registered") {
			t.Errorf("%s: inventory lacks %s:\n%s", name, runtime, out)
		}
	}

	if out, code := run(fullPath, "runtime", "verify", "nope"); code == 0 || !strings.Contains(out, "Usage: asura runtime verify") {
		t.Errorf("unknown runtime: exit %d:\n%s", code, out)
	}
	writeCodex("0.163.0", true, true)
	expectRejected("not logged in", "codex", fullPath, "output", "FAIL", "authentication failed", "no record written")
	writeCodex("0.163.0", false, false)
	expectRejected("workspace escape", "codex", fullPath, "output                      ", "PASS", "workspace-boundary", "FAIL: security violation: wrote outside the workspace", "no record written")
	if err := os.WriteFile(filepath.Join(runtimes, "agy"), []byte("#!/bin/sh\necho 1.4.4\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.home, ".gemini", "antigravity-cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	expectRejected("bwrap unavailable", "antigravity", runtimes, "output", "FAIL", "bwrap", "no record written")
	if err := os.Remove(filepath.Join(runtimes, "agy")); err != nil {
		t.Fatal(err)
	}

	writeCodex("0.163.0", true, false)
	digest, err := verification.FileDigest(codex)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second)
	out, code := run(fullPath, "runtime", "verify", "codex")
	t.Logf("all pass:\n%s", out)
	if code != 0 {
		t.Fatalf("all pass: exit %d", code)
	}
	for _, name := range []string{verification.ProbeOutput, verification.ProbeWorkspace, verification.ProbeReadOnly, verification.ProbeCancel} {
		if !strings.Contains(out, name) {
			t.Errorf("all pass: output lacks probe %s", name)
		}
	}
	if strings.Contains(out, "FAIL") || !strings.Contains(out, "verified codex 0.163.0") {
		t.Errorf("all pass: unexpected output")
	}
	got, err := verification.Load(records)
	if err != nil || len(got) != 2 || got[0].Identity != other.Identity {
		t.Fatalf("records after verify = %+v, err = %v", got, err)
	}
	rec := got[1]
	if want := (verification.Identity{Runtime: "codex", Version: "0.163.0", Digest: digest}); rec.Identity != want || rec.AsuraVersion != "dev" || rec.VerifiedAt.Before(before) || rec.VerifiedAt.After(time.Now()) {
		t.Errorf("new record = %+v, want identity %+v, asura_version dev, verified_at now", rec, want)
	}
	available := `codex registration=registered availability=available version="codex-cli 0.163.0" reason=none`
	if out, _ := run(fullPath, "runtime", "inventory", "--socket", ""); !strings.Contains(out, available) {
		t.Errorf("inventory with config:\n%s", out)
	}
	for _, db := range []string{filepath.Join(t.TempDir(), "a.db"), filepath.Join(t.TempDir(), "b.db")} {
		n := execCalls()
		if out, _ := run(fullPath, "run", "--db", db, "--workspace", ws, "--mode", "three-face", "--backend", "auto", "probe"); strings.Contains(out, "codex:version_unverified") || execCalls() == n {
			t.Errorf("run --db %s did not execute codex:\n%s", db, out)
		}
	}
	if err := os.Remove(filepath.Join(e.configHome, "asura", "config.toml")); err != nil {
		t.Fatal(err)
	}
	if out, _ := run(fullPath, "runtime", "inventory", "--socket", ""); !strings.Contains(out, available) {
		t.Errorf("inventory without config:\n%s", out)
	}

	verified, _ := os.ReadFile(records)
	writeCodex("0.155.0", true, false)
	n := execCalls()
	if out, code := run(fullPath, "runtime", "verify", "codex"); code != 0 || !strings.Contains(out, "codex 0.155.0 is a built-in verified identity") || execCalls() != n {
		t.Errorf("built-in identity: exit %d, %d model calls:\n%s", code, execCalls()-n, out)
	}
	if data, _ := os.ReadFile(records); !bytes.Equal(data, verified) {
		t.Errorf("built-in identity changed the records:\n%s", data)
	}
	if err := os.Remove(codex); err != nil {
		t.Fatal(err)
	}
	if out, code := run(fullPath, "runtime", "verify", "codex"); code == 0 || !strings.Contains(out, "error: verify codex") {
		t.Errorf("not installed: exit %d:\n%s", code, out)
	}
}
