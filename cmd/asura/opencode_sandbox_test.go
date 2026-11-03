package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 2026-11-03：issue-75 SCN-004，PATH 找不到 bwrap 時，inventory、doctor 都把 OpenCode 判為 dependency_not_found；
// auto 不選它，daemon 明確指定時帶 reason 拒絕，假 opencode 只被查版本。
func TestOpenCodeWithoutBwrapFailsClosed(t *testing.T) {
	bin := buildCLI(t)
	e := newXDGEnv(t)
	ws := filepath.Join(e.root, "ws")
	if err := os.Mkdir(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(e.root, "asura.sock")
	e.writeConfig(t, "schema_version = 1\nsocket = \""+sock+"\"\nallowed_roots = [\""+ws+"\"]\n")
	runtimes := t.TempDir()
	calls := filepath.Join(ws, "calls.log")
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\nif [ \"$1\" = --version ]; then echo 1.18.23; exit 0; fi\n"
	if err := os.WriteFile(filepath.Join(runtimes, "opencode"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// PATH 只放假 opencode；/usr/bin 與 /bin 都有 bwrap，不能放進來。
	env := append(e.vars(), "PATH="+runtimes)
	startDaemon(t, bin, t.TempDir(), env)
	run := func(args ...string) string {
		c := exec.Command(bin, args...)
		c.Env, c.Dir = env, ws
		out, _ := c.CombinedOutput()
		return string(out)
	}
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"local inventory", []string{"runtime", "inventory", "--socket", ""}, `opencode registration=registered availability=unavailable version="" reason=dependency_not_found`},
		{"daemon inventory", []string{"runtime", "inventory", "--socket", sock}, `opencode registration=registered availability=unavailable version="" reason=dependency_not_found`},
		{"doctor", []string{"doctor"}, "[warn] opencode: unavailable reason=dependency_not_found"},
		{"auto run", []string{"run", "--db", filepath.Join(t.TempDir(), "run.db"), "--workspace", ws, "--mode", "three-face", "--backend", "auto", "probe"}, "opencode:dependency_not_found"},
		{"daemon ask", []string{"ask", "--backend", "opencode", "--workspace", ws, "probe"}, "opencode:dependency_not_found"},
	} {
		if out := run(c.args...); !strings.Contains(out, c.want) {
			t.Errorf("%s: output lacks %q:\n%s", c.name, c.want, out)
		}
	}
	data, _ := os.ReadFile(calls)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" && line != "--version" {
			t.Errorf("opencode was executed beyond the version check: %s", line)
		}
	}
}

// linkBwrap 把主機的 bwrap 連進 dir：OpenCode 寫入角色只在 bwrap 內執行，PATH 只放假 runtime 的測試也要找得到它（issue-75）。
func linkBwrap(t *testing.T, dir string) {
	t.Helper()
	path, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap unavailable")
	}
	if err := os.Symlink(path, filepath.Join(dir, "bwrap")); err != nil {
		t.Fatal(err)
	}
}
