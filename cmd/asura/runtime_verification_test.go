package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"asura/internal/backend/verification"
)

// 2026-10-21：issue-73 Task 2.4，SCN-001／002／006／008。同一個已安裝的 runtime，
// runtime inventory（本機與 daemon）、doctor、本機 run 與 daemon ask 的判定必須一致，
// 驗證紀錄固定在 $XDG_DATA_HOME/asura/，daemon 執行中寫入或移除紀錄也立即生效。
func TestRuntimeVerificationAcrossPaths(t *testing.T) {
	bin := buildCLI(t)
	e := newXDGEnv(t)
	ws := filepath.Join(e.root, "ws")
	sock := filepath.Join(e.root, "asura.sock")
	if err := os.Mkdir(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	e.writeConfig(t, "schema_version = 1\nsocket = \""+sock+"\"\nallowed_roots = [\""+ws+"\"]\n")
	runtimes := t.TempDir()
	codex := filepath.Join(runtimes, "codex")
	calls := filepath.Join(e.root, "calls.log")
	writeCodex := func(extra string) {
		script := "#!/bin/sh\n# " + extra + "\necho \"$*\" >> " + calls + "\n" +
			"if [ \"$1\" = --version ]; then echo 'codex-cli 0.163.0'; exit 0; fi\n" +
			"printf '%s\\n' '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"ok\"}}'\n"
		if err := os.WriteFile(codex, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeCodex("v1")
	env := append(e.vars(), "PATH="+runtimes+":/usr/bin:/bin")
	startDaemon(t, bin, t.TempDir(), env)

	run := func(args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Env, c.Dir = env, ws
		out, err := c.CombinedOutput()
		return string(out), err
	}
	modelCalls := func() int {
		data, _ := os.ReadFile(calls)
		n := 0
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if strings.HasPrefix(line, "exec ") {
				n++
			}
		}
		return n
	}
	const hint = "asura runtime verify codex"
	expectUnverified := func(stage string) {
		t.Helper()
		before := modelCalls()
		want := `codex registration=registered availability=unavailable version="" reason=version_unverified`
		for _, socket := range []string{"", sock} {
			if out, _ := run("runtime", "inventory", "--socket", socket); !strings.Contains(out, want) {
				t.Errorf("%s: inventory (socket %q):\n%s", stage, socket, out)
			}
		}
		if out, _ := run("doctor"); !strings.Contains(out, "[warn] codex: unavailable reason=version_unverified") || !strings.Contains(out, hint) {
			t.Errorf("%s: doctor:\n%s", stage, out)
		}
		out, err := run("run", "--db", filepath.Join(t.TempDir(), "run.db"), "--workspace", ws, "--mode", "three-face", "--backend", "auto", "probe")
		if err == nil || !strings.Contains(out, "codex:version_unverified") || !strings.Contains(out, hint) {
			t.Errorf("%s: local run err=%v:\n%s", stage, err, out)
		}
		if out, _ := run("ask", "--backend", "codex", "--workspace", ws, "probe"); !strings.Contains(out, "codex:version_unverified") || !strings.Contains(out, hint) {
			t.Errorf("%s: daemon ask:\n%s", stage, out)
		}
		if after := modelCalls(); after != before {
			t.Errorf("%s: unverified codex was executed %d times", stage, after-before)
		}
	}
	expectAvailable := func(stage string) {
		t.Helper()
		want := `codex registration=registered availability=available version="codex-cli 0.163.0"`
		for _, socket := range []string{"", sock} {
			if out, _ := run("runtime", "inventory", "--socket", socket); !strings.Contains(out, want) {
				t.Errorf("%s: inventory (socket %q):\n%s", stage, socket, out)
			}
		}
		if out, _ := run("doctor"); !strings.Contains(out, "[ok]   codex: available version=codex-cli 0.163.0") {
			t.Errorf("%s: doctor:\n%s", stage, out)
		}
		before := modelCalls()
		if out, _ := run("run", "--db", filepath.Join(t.TempDir(), "run.db"), "--workspace", ws, "--mode", "three-face", "--backend", "auto", "probe"); strings.Contains(out, "admission rejected") {
			t.Errorf("%s: local run:\n%s", stage, out)
		}
		if modelCalls() == before {
			t.Errorf("%s: local run did not execute the verified codex", stage)
		}
		before = modelCalls()
		if out, _ := run("ask", "--backend", "codex", "--workspace", ws, "--wait", "probe"); strings.Contains(out, "admission rejected") {
			t.Errorf("%s: daemon ask:\n%s", stage, out)
		}
		if modelCalls() == before {
			t.Errorf("%s: daemon ask did not execute the verified codex", stage)
		}
	}

	expectUnverified("before verification")

	records := filepath.Join(e.dataHome, "asura", "runtime-verification.json")
	digest, err := verification.FileDigest(codex)
	if err != nil {
		t.Fatal(err)
	}
	id := verification.Identity{Runtime: "codex", Version: "0.163.0", Digest: digest}
	if err := verification.Add(records, verification.Record{Identity: id, VerifiedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	expectAvailable("after verification")

	writeCodex("v2: same version, different binary")
	expectUnverified("after the binary changed")

	writeCodex("v1")
	expectAvailable("binary restored")
	if err := verification.Remove(records, id, nil); err != nil {
		t.Fatal(err)
	}
	expectUnverified("after the record was removed")
}

// 2026-10-21：issue-73 Task 2.5，SCN-009。以已驗證身分接受的任務，驗證紀錄在後續 stage 執行前被移除：
// 該 stage 的執行前檢查必須拒絕，不再呼叫模型，也不改選其他可用的 runtime。
func TestRuntimeVerificationRemovedBeforeStage(t *testing.T) {
	bin := buildCLI(t)
	e := newXDGEnv(t)
	ws := filepath.Join(e.root, "ws")
	sock := filepath.Join(e.root, "asura.sock")
	if err := os.Mkdir(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	e.writeConfig(t, "schema_version = 1\nsocket = \""+sock+"\"\nallowed_roots = [\""+ws+"\"]\nruntime_priority = [\"codex\", \"opencode\"]\n")
	records := filepath.Join(e.dataHome, "asura", "runtime-verification.json")
	runtimes := t.TempDir()
	calls := filepath.Join(e.root, "calls.log")
	// 假 codex 第一次被呼叫執行任務時就刪掉驗證紀錄，模擬使用者在 oracle 與 striker 之間執行 --remove。
	codex := filepath.Join(runtimes, "codex")
	if err := os.WriteFile(codex, []byte("#!/bin/sh\necho \"codex $*\" >> "+calls+"\n"+
		"if [ \"$1\" = --version ]; then echo 'codex-cli 0.163.0'; exit 0; fi\n"+
		"rm -f "+records+"\n"+
		`printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"goal\":\"g\",\"steps\":[\"s\"]}"}}'`+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimes, "opencode"), []byte("#!/bin/sh\necho \"opencode $*\" >> "+calls+"\n"+
		"if [ \"$1\" = --version ]; then echo 1.18.23; exit 0; fi\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := verification.FileDigest(codex)
	if err != nil {
		t.Fatal(err)
	}
	if err := verification.Add(records, verification.Record{Identity: verification.Identity{Runtime: "codex", Version: "0.163.0", Digest: digest}, VerifiedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	env := append(e.vars(), "PATH="+runtimes+":/usr/bin:/bin")
	startDaemon(t, bin, t.TempDir(), env)

	c := exec.Command(bin, "ask", "--backend", "auto", "--mode", "three-face", "--workspace", ws, "--wait", "probe")
	c.Env, c.Dir = env, ws
	out, _ := c.CombinedOutput()
	t.Logf("ask:\n%s", out)

	data, _ := os.ReadFile(calls)
	var codexRuns, opencodeRuns int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		switch {
		case strings.HasPrefix(line, "codex exec "):
			codexRuns++
		case strings.HasPrefix(line, "opencode ") && !strings.HasSuffix(line, " --version"):
			opencodeRuns++
		}
	}
	if codexRuns != 1 {
		t.Errorf("codex ran %d times, want 1 (oracle only)", codexRuns)
	}
	if opencodeRuns != 0 {
		t.Errorf("opencode ran %d times after codex was rejected; the stage must not switch runtime", opencodeRuns)
	}
	if !strings.Contains(string(out), "codex:version_unverified") {
		t.Errorf("task did not fail on the unverified codex:\n%s", out)
	}
}
