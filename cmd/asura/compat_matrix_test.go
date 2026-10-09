package main

// 2026-10-09：issue-45 Task 6.3，有 config 時 cron／memory／web 經 config socket 連線，CLI --socket 仍優先（DIST-017）。

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompatClientsUseConfigSocket(t *testing.T) {
	bin := buildCLI(t)
	e := newXDGEnv(t)
	root := filepath.Join(e.root, "project")
	os.Mkdir(root, 0700)
	sock := filepath.Join(e.root, "cfg.sock")
	e.writeConfig(t, "schema_version = 1\nsocket = \""+sock+"\"\nallowed_roots = [\""+root+"\"]\n")
	env := append(e.vars(), "PATH="+t.TempDir())
	if line := startDaemon(t, bin, t.TempDir(), env); !strings.HasPrefix(line, "Daemon started on socket "+sock+" ") {
		t.Fatalf("daemon socket not from config: %q", line)
	}

	run := func(args ...string) (int, string) {
		c := exec.Command(bin, args...)
		c.Dir, c.Env = t.TempDir(), env
		out, err := c.CombinedOutput()
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), string(out)
		}
		return 0, string(out)
	}
	for _, args := range [][]string{{"cron", "list"}, {"memory", "list", "--workspace", root}} {
		if code, out := run(args...); code != 0 {
			t.Fatalf("%v via config socket: %d %s", args, code, out)
		}
		override := append(append([]string{}, args...), "--socket", filepath.Join(e.root, "missing.sock"))
		if code, _ := run(override...); code == 0 {
			t.Fatalf("%v ignored --socket override", override)
		}
	}

	e.setenv(t)
	var out, stderr bytes.Buffer
	RunCLI([]string{"web", "-h"}, &out, &stderr)
	if !strings.Contains(stderr.String(), `(default "`+sock+`")`) {
		t.Fatalf("web default socket not from config:\n%s", stderr.String())
	}
}
