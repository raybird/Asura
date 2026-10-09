package main

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// 2026-10-09：issue-45 Task 0.1，刻畫 v1.0 無 config 時的 DB／socket 預設與本機 fallback。
// 期望值皆為字面路徑；引入 config 後無 config 的行為必須維持本檔結果（DIST-009／017）。

func TestNoConfigDefaultSocketPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ASURA_SOCKET", "")
	if got, want := DefaultSocketPath(), home+"/.asura/asura.sock"; got != want {
		t.Fatalf("socket=%q want %q", got, want)
	}
	if info, err := os.Stat(home + "/.asura"); err != nil || !info.IsDir() {
		t.Fatalf("~/.asura not created: %v", err)
	}
	t.Setenv("ASURA_SOCKET", "/run/custom/asura.sock")
	if got := DefaultSocketPath(); got != "/run/custom/asura.sock" {
		t.Fatalf("env socket=%q", got)
	}
	tmp := t.TempDir()
	t.Setenv("ASURA_SOCKET", "")
	t.Setenv("HOME", "")
	t.Setenv("TMPDIR", tmp)
	if got, want := DefaultSocketPath(), tmp+"/asura.sock"; got != want {
		t.Fatalf("no-home socket=%q want %q", got, want)
	}
}

func TestNoConfigFlagDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defaultSock := home + "/.asura/asura.sock"
	cases := []struct {
		env  string
		args []string
		want []string
	}{
		{"", []string{"daemon"}, []string{`-socket string` + "\n    \tpath to daemon unix socket (default \"" + defaultSock + "\")", `(default "asura.db")`, "-workers int\n    \tmaximum concurrent workers (default 2)", "-queue int\n    \tmaximum queue capacity (default 10)"}},
		{"", []string{"run"}, []string{`(default "asura.db")`}},
		{"", []string{"status"}, []string{`(default "asura.db")`}},
		{"", []string{"cancel"}, []string{`(default "asura.db")`}},
		{"", []string{"audit"}, []string{`(default "asura.db")`}},
		{"", []string{"ask"}, []string{`(default "` + defaultSock + `")`}},
		{"", []string{"web"}, []string{`(default "` + defaultSock + `")`, `(default "127.0.0.1:8080")`}},
		{"", []string{"cron", "list"}, []string{`(default "` + defaultSock + `")`}},
		{"", []string{"memory", "list"}, []string{`(default "` + defaultSock + `")`}},
		{"/env/asura.sock", []string{"ask"}, []string{`(default "/env/asura.sock")`}},
		{"/env/asura.sock", []string{"status"}, []string{`(default "/env/asura.sock")`}},
		{"/env/asura.sock", []string{"runtime", "inventory"}, []string{`(default "/env/asura.sock")`}},
	}
	for _, c := range cases {
		t.Setenv("ASURA_SOCKET", c.env)
		var out, stderr bytes.Buffer
		RunCLI(append(c.args, "-h"), &out, &stderr)
		for _, w := range c.want {
			if !strings.Contains(stderr.String(), w) {
				t.Errorf("%v env=%q missing %q in:\n%s", c.args, c.env, w, stderr.String())
			}
		}
	}
	// status／cancel／audit／inventory 未設 ASURA_SOCKET 時沒有 socket 預設，直接走本機 DB。
	t.Setenv("ASURA_SOCKET", "")
	for _, args := range [][]string{{"status"}, {"cancel"}, {"audit"}, {"runtime", "inventory"}} {
		var out, stderr bytes.Buffer
		RunCLI(append(args, "-h"), &out, &stderr)
		if !strings.Contains(stderr.String(), "-socket string") || strings.Contains(stderr.String(), ".asura/asura.sock") {
			t.Errorf("%v socket default changed:\n%s", args, stderr.String())
		}
	}
}

func TestNoConfigLocalFallbackUsesCwdDB(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	for _, socket := range []string{"", filepath.Join(t.TempDir(), "missing.sock")} {
		for _, args := range [][]string{{"status", "missing-task"}, {"cancel", "missing-task"}, {"audit", "--id", "missing-task"}} {
			cwd := t.TempDir()
			t.Chdir(cwd)
			t.Setenv("ASURA_SOCKET", socket)
			var out, stderr bytes.Buffer
			if code := RunCLI(args, &out, &stderr); code == 0 {
				t.Fatalf("%v socket=%q unexpectedly succeeded", args, socket)
			}
			if _, err := os.Stat(filepath.Join(cwd, "asura.db")); err != nil {
				t.Fatalf("%v socket=%q did not fall back to ./asura.db: %v (%s)", args, socket, err, stderr.String())
			}
		}
		t.Setenv("ASURA_SOCKET", socket)
		var out, stderr bytes.Buffer
		if code := RunCLI([]string{"runtime", "inventory"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "Runtime Inventory") {
			t.Fatalf("inventory socket=%q local fallback: code=%d %s", socket, code, stderr.String())
		}
	}
}

func TestNoConfigRunDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	cwd, ws, other := t.TempDir(), t.TempDir(), t.TempDir()
	t.Chdir(cwd)
	var out, stderr bytes.Buffer
	handleRun([]string{"--workspace", ws, "--id", "r1", "probe"}, &out, &stderr)
	if _, err := os.Stat(filepath.Join(cwd, "asura.db")); err != nil {
		t.Fatalf("run did not use ./asura.db: %v", err)
	}
	// run 未指定 allowed roots 時以 workspace 為唯一 root；指定其他 root 時同一 workspace 被拒絕。
	if strings.Contains(stderr.String(), "not in allowed roots") {
		t.Fatalf("default root rejected workspace: %s", stderr.String())
	}
	stderr.Reset()
	handleRun([]string{"--workspace", ws, "--allowed-roots", other, "--id", "r2", "probe"}, &out, &stderr)
	if !strings.Contains(stderr.String(), "not in allowed roots") {
		t.Fatalf("explicit root not enforced: %s", stderr.String())
	}
}

func TestNoConfigEnvValidation(t *testing.T) {
	t.Setenv("ASURA_MEMORY_UTILITY_POLICY", "bogus")
	var out, stderr bytes.Buffer
	if code := handleDaemon([]string{"--db", filepath.Join(t.TempDir(), "db")}, &out, &stderr); code != 1 || !strings.Contains(stderr.String(), "invalid utility policy") {
		t.Fatalf("utility policy: code=%d %s", code, stderr.String())
	}
}

func TestNoConfigDaemonDefaults(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "asura")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home, ws, outside, path := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	// ENV 空值與未設定同義：空的 ASURA_SOCKET／ASURA_RUNTIME_PRIORITY／ASURA_CONTEXT_LIMIT 不改變預設。
	env := []string{"HOME=" + home, "PATH=" + path, "ASURA_SOCKET=", "ASURA_RUNTIME_PRIORITY=", "ASURA_CONTEXT_LIMIT=", "ASURA_MEMORY_UTILITY_POLICY="}
	cmd := exec.Command(bin, "daemon")
	cmd.Dir, cmd.Env = ws, env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		lines <- line
	}()
	select {
	case line := <-lines:
		want := "Daemon started on socket " + home + "/.asura/asura.sock (workers: 2, queue: 10)\n"
		if line != want {
			t.Fatalf("start line=%q want %q (stderr %s)", line, want, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("daemon did not start: %s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(ws, "asura.db")); err != nil {
		t.Fatalf("daemon did not use ./asura.db: %v", err)
	}
	ask := func(workspace string) (string, error) {
		c := exec.Command(bin, "ask", "--workspace", workspace, "probe")
		c.Dir, c.Env = outside, env
		out, err := c.CombinedOutput()
		return string(out), err
	}
	// daemon 未指定 allowed roots 時以啟動目錄為唯一 root。
	if out, err := ask(outside); err == nil || !strings.Contains(out, "not in allowed roots") {
		t.Fatalf("outside workspace accepted: %v %s", err, out)
	}
	// cwd 內的 workspace 通過 root 檢查；fixture 無 runtime，因此停在 admission 而非 root。
	if out, _ := ask(ws); strings.Contains(out, "not in allowed roots") || !strings.Contains(out, "runtime admission rejected") {
		t.Fatalf("cwd workspace: %s", out)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon exit: %v %s", err, stderr.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatal("daemon did not stop")
	}
}
