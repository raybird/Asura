package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"asura/internal/backend/antigravity"
	"asura/internal/backend/omp"
	"asura/internal/backend/verification"
)

// verifyCodexScript 是會回應全部 verify probe 的假 codex。confine 為 true 時模擬 workspace-write 沙箱：
// 只執行目標在 -C workspace 內的重導向；read-only 模式從不執行命令，只讀出 nonce。
// auth 為 true 時每次執行都回報未登入。
func verifyCodexScript(calls, version string, confine, auth bool) string {
	s := "#!/bin/sh\necho \"$*\" >> " + calls + "\n" +
		"if [ \"$1\" = --version ]; then echo 'codex-cli " + version + "'; exit 0; fi\n" +
		"say() { printf '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"%s\"}}\\n' \"$1\"; }\n"
	// codex sandbox -P <profile> -C <ws> -- <command>：confine 時 :read-only 擋下寫入（命令仍會執行並印出輸出）。
	s += "if [ \"$1\" = sandbox ]; then profile=$3; while [ \"$1\" != -- ]; do shift; done; shift\n"
	if confine {
		s += "  [ \"$profile\" = :read-only ] && { echo ASURA_SANDBOX_RAN; echo 'sh: Read-only file system'; exit 2; }\n"
	}
	s += "  exec \"$@\"\nfi\n"
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
	// 2026-10-27：PR #77 審查，紀錄檔寫不進去時，在呼叫模型之前就失敗並指出紀錄檔，不先跑完全部 probe。
	if err := os.Chmod(records, 0o666); err != nil {
		t.Fatal(err)
	}
	n := execCalls()
	expectRejected("unsafe records file", "codex", fullPath, "verification records", records)
	if execCalls() != n {
		t.Errorf("unsafe records file: probes ran the model %d times", execCalls()-n)
	}
	if err := os.Chmod(records, 0o600); err != nil {
		t.Fatal(err)
	}
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
	n = execCalls()
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

// 2026-10-22：issue-73 Task 3.3，SCN-007。--list 列出內建身分與每筆紀錄；--remove 移除一筆後該身分回到 version_unverified；
// 內建身分不能移除，失敗時紀錄逐 byte 不變。
func TestRuntimeVerifyListRemove(t *testing.T) {
	e := newXDGEnv(t)
	runtimes := t.TempDir()
	for k, v := range map[string]string{"HOME": e.home, "XDG_CONFIG_HOME": e.configHome, "XDG_DATA_HOME": e.dataHome, "XDG_RUNTIME_DIR": e.runtimeDir, "PATH": runtimes + ":/usr/bin:/bin"} {
		t.Setenv(k, v)
	}
	codex := filepath.Join(runtimes, "codex")
	if err := os.WriteFile(codex, []byte("#!/bin/sh\necho 'codex-cli 0.163.0'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := verification.FileDigest(codex)
	if err != nil {
		t.Fatal(err)
	}
	records := filepath.Join(e.dataHome, "asura", "runtime-verification.json")
	at := time.Date(2026, 10, 20, 1, 2, 3, 0, time.UTC)
	for _, r := range []verification.Record{
		{Identity: verification.Identity{Runtime: "codex", Version: "0.163.0", Digest: digest}, VerifiedAt: at, AsuraVersion: "v1.2.3"},
		{Identity: verification.Identity{Runtime: "omp", Version: "19.0.0", Digest: "e1"}, VerifiedAt: at, AsuraVersion: "dev"},
		{Identity: verification.Identity{Runtime: "omp", Version: "19.0.0", Digest: "f2"}, VerifiedAt: at, AsuraVersion: "dev"},
	} {
		if err := verification.Add(records, r); err != nil {
			t.Fatal(err)
		}
	}
	cli := func(args ...string) (string, string, int) {
		t.Helper()
		var out, errOut bytes.Buffer
		code := RunCLI(append([]string{"runtime"}, args...), &out, &errOut)
		return out.String(), errOut.String(), code
	}
	codexLine := func() string {
		out, _, _ := cli("inventory", "--socket", "")
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "codex ") {
				return line
			}
		}
		return out
	}
	builtins := "opencode version=\"1.18.23\" sha256=any built-in\n" +
		"codex version=\"0.155.0\" sha256=any built-in\n" +
		"omp version=\"18.4.4\" sha256=" + omp.Builtin.Digest + " built-in\n" +
		"antigravity version=\"1.2.13\" sha256=" + antigravity.Builtin.Digest + " built-in\n"
	ompRows := "omp version=\"19.0.0\" sha256=e1 verified_at=2026-10-20T01:02:03Z asura_version=dev\n" +
		"omp version=\"19.0.0\" sha256=f2 verified_at=2026-10-20T01:02:03Z asura_version=dev\n"
	if out, errOut, code := cli("verify", "--list"); code != 0 || errOut != "" ||
		out != builtins+"codex version=\"0.163.0\" sha256="+digest+" verified_at=2026-10-20T01:02:03Z asura_version=v1.2.3\n"+ompRows {
		t.Errorf("--list: exit %d, stderr %q:\n%s", code, errOut, out)
	}
	if line := codexLine(); !strings.Contains(line, "availability=available") {
		t.Errorf("before remove: %s", line)
	}

	seeded, _ := os.ReadFile(records)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"codex", "0.155.0"}, "error: codex 0.155.0 is a built-in verified identity: built-in verified identities cannot be removed\n"},
		{[]string{"omp", "18.4.4"}, "error: omp 18.4.4 is a built-in verified identity: built-in verified identities cannot be removed\n"},
		{[]string{"omp", "18.4.4", omp.Builtin.Digest}, "error: omp 18.4.4 is a built-in verified identity: built-in verified identities cannot be removed\n"},
		{[]string{"codex", "9.9.9"}, "error: codex 9.9.9: no verification record for this identity\n"},
		{[]string{"omp", "19.0.0"}, "error: omp 19.0.0 has 2 verification records; specify the sha256\n"},
	} {
		if out, errOut, code := cli(append([]string{"verify", "--remove"}, c.args...)...); code == 0 || out != "" || errOut != c.want {
			t.Errorf("--remove %v: exit %d, stdout %q, stderr %q, want %q", c.args, code, out, errOut, c.want)
		}
	}
	if data, _ := os.ReadFile(records); !bytes.Equal(data, seeded) {
		t.Errorf("failed removals changed the records:\n%s", data)
	}

	if out, errOut, code := cli("verify", "--remove", "codex", "0.163.0"); code != 0 || errOut != "" || out != "removed codex 0.163.0 (sha256 "+digest+")\n" {
		t.Errorf("--remove codex: exit %d, stderr %q:\n%s", code, errOut, out)
	}
	if out, errOut, code := cli("verify", "--remove", "omp", "19.0.0", "f2"); code != 0 || errOut != "" || out != "removed omp 19.0.0 (sha256 f2)\n" {
		t.Errorf("--remove omp f2: exit %d, stderr %q:\n%s", code, errOut, out)
	}
	if out, _, code := cli("verify", "--list"); code != 0 || out != builtins+strings.SplitAfter(ompRows, "\n")[0] {
		t.Errorf("--list after remove:\n%s", out)
	}
	if line := codexLine(); !strings.Contains(line, "availability=unavailable") || !strings.HasSuffix(line, "reason=version_unverified") {
		t.Errorf("after remove: %s", line)
	}
	for _, args := range [][]string{{"--list", "codex"}, {"--remove", "codex"}, {"--remove", "nope", "1.0"}, {"--list", "--remove", "codex", "1.0"}} {
		if _, errOut, code := cli(append([]string{"verify"}, args...)...); code == 0 || !strings.Contains(errOut, "Usage: asura runtime verify") {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
}

// 2026-10-22：issue-73 Task 3.4，SCN-010。未驗證的 Codex 與 OpenCode：daemon 啟動、runtime inventory（本機與 daemon）、
// doctor、本機 run 與 daemon ask 都只查版本，不執行任何 probe；只回報 version_unverified 與 verify 提示。
func TestRuntimeVerificationNotAutomatic(t *testing.T) {
	bin := buildCLI(t)
	e := newXDGEnv(t)
	ws := filepath.Join(e.root, "ws")
	sock := filepath.Join(e.root, "asura.sock")
	if err := os.Mkdir(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	e.writeConfig(t, "schema_version = 1\nsocket = \""+sock+"\"\nallowed_roots = [\""+ws+"\"]\n")
	runtimes := t.TempDir()
	calls := filepath.Join(e.root, "calls.log")
	for name, version := range map[string]string{"codex": "codex-cli 0.163.0", "opencode": "1.18.31"} {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> " + calls + "\nif [ \"$1\" = --version ]; then echo '" + version + "'; exit 0; fi\n"
		if err := os.WriteFile(filepath.Join(runtimes, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	env := append(e.vars(), "PATH="+runtimes+":/usr/bin:/bin")
	startDaemon(t, bin, t.TempDir(), env)
	run := func(args ...string) string {
		c := exec.Command(bin, args...)
		c.Env, c.Dir = env, ws
		out, _ := c.CombinedOutput()
		return string(out)
	}
	for _, c := range []struct {
		name  string
		args  []string
		wants []string
	}{
		{"local inventory", []string{"runtime", "inventory", "--socket", ""}, []string{
			`codex registration=registered availability=unavailable version="" reason=version_unverified`,
			`opencode registration=registered availability=unavailable version="" reason=version_unverified`}},
		{"daemon inventory", []string{"runtime", "inventory", "--socket", sock}, []string{
			`codex registration=registered availability=unavailable version="" reason=version_unverified`,
			`opencode registration=registered availability=unavailable version="" reason=version_unverified`}},
		{"doctor", []string{"doctor"}, []string{
			"[warn] codex: unavailable reason=version_unverified", "asura runtime verify codex",
			"[warn] opencode: unavailable reason=version_unverified", "asura runtime verify opencode"}},
		{"local run", []string{"run", "--db", filepath.Join(t.TempDir(), "run.db"), "--workspace", ws, "--mode", "three-face", "--backend", "auto", "probe"}, []string{
			"codex:version_unverified", "asura runtime verify codex", "opencode:version_unverified", "asura runtime verify opencode"}},
		{"daemon ask", []string{"ask", "--backend", "auto", "--mode", "three-face", "--workspace", ws, "probe"}, []string{
			"codex:version_unverified", "asura runtime verify codex", "opencode:version_unverified", "asura runtime verify opencode"}},
	} {
		out := run(c.args...)
		for _, want := range c.wants {
			if !strings.Contains(out, want) {
				t.Errorf("%s: output lacks %q:\n%s", c.name, want, out)
			}
		}
	}

	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		name, args, _ := strings.Cut(line, " ")
		if args != "--version" {
			t.Errorf("unverified runtime was executed beyond the version check: %s", line)
		}
		seen[name] = true
	}
	if !seen["codex"] || !seen["opencode"] {
		t.Errorf("the fake runtimes were never consulted:\n%s", data)
	}
}
