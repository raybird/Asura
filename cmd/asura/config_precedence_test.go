package main

// 2026-10-09：issue-45 Task 3.2，config 接線與逐設定 precedence（DIST-009），bad config fail-closed（DIST-010）。

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"asura/internal/storage"
)

// xdgEnv 是一組隔離的 HOME／XDG 目錄。
type xdgEnv struct {
	root, home, configHome, dataHome, runtimeDir string
}

func newXDGEnv(t *testing.T) xdgEnv {
	t.Helper()
	root := t.TempDir()
	e := xdgEnv{root: root, home: filepath.Join(root, "home"), configHome: filepath.Join(root, "xc"), dataHome: filepath.Join(root, "xd"), runtimeDir: filepath.Join(root, "run")}
	for _, d := range []string{e.home, e.configHome, e.dataHome, e.runtimeDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// writeConfig 以目錄 0700、檔案 0600 寫入 config.toml。
func (e xdgEnv) writeConfig(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(e.configHome, "asura")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func (e xdgEnv) vars() []string {
	return []string{"HOME=" + e.home, "XDG_CONFIG_HOME=" + e.configHome, "XDG_DATA_HOME=" + e.dataHome, "XDG_RUNTIME_DIR=" + e.runtimeDir}
}

func (e xdgEnv) setenv(t *testing.T) {
	for _, kv := range e.vars() {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	t.Setenv("ASURA_SOCKET", "")
}

func buildCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "asura")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// startDaemon 啟動 daemon 子行程並回傳第一行啟動訊息；測試結束時以 SIGTERM 停止。
func startDaemon(t *testing.T, bin, cwd string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"daemon"}, args...)...)
	cmd.Dir, cmd.Env = cwd, env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
	})
	line := make(chan string, 1)
	go func() {
		l, _ := bufio.NewReader(stdout).ReadString('\n')
		line <- l
	}()
	select {
	case l := <-line:
		if l == "" {
			t.Fatalf("daemon exited: %s", stderr.String())
		}
		return l
	case <-time.After(30 * time.Second):
		t.Fatalf("daemon did not start: %s", stderr.String())
	}
	return ""
}

func askRoot(t *testing.T, bin string, env []string, workspace string, args ...string) string {
	t.Helper()
	c := exec.Command(bin, append(append([]string{"ask"}, args...), "--workspace", workspace, "probe")...)
	c.Env = env
	out, _ := c.CombinedOutput()
	return string(out)
}

func TestConfigDaemonPrecedence(t *testing.T) {
	bin := buildCLI(t)
	full := func(e xdgEnv) string {
		return `schema_version = 1
socket = "` + filepath.Join(e.root, "cfg.sock") + `"
db = "` + filepath.Join(e.root, "cfgdata", "cfg.db") + `"
allowed_roots = ["` + filepath.Join(e.root, "cfgroot") + `"]
workers = 5
queue = 7
`
	}

	t.Run("config-over-builtin", func(t *testing.T) {
		e := newXDGEnv(t)
		e.writeConfig(t, full(e))
		cwd, cfgRoot := t.TempDir(), filepath.Join(e.root, "cfgroot")
		os.Mkdir(cfgRoot, 0700)
		env := append(e.vars(), "PATH="+t.TempDir())
		line := startDaemon(t, bin, cwd, env)
		if want := "Daemon started on socket " + filepath.Join(e.root, "cfg.sock") + " (workers: 5, queue: 7)\n"; line != want {
			t.Fatalf("line=%q want %q", line, want)
		}
		if _, err := os.Stat(filepath.Join(e.root, "cfgdata", "cfg.db")); err != nil {
			t.Fatalf("config db not used: %v", err)
		}
		for _, p := range []string{filepath.Join(cwd, "asura.db"), filepath.Join(e.home, ".asura")} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Fatalf("legacy path touched: %s", p)
			}
		}
		if out := askRoot(t, bin, env, cwd); !strings.Contains(out, "not in allowed roots") {
			t.Fatalf("cwd accepted despite config roots: %s", out)
		}
		if out := askRoot(t, bin, env, cfgRoot); strings.Contains(out, "not in allowed roots") || !strings.Contains(out, "runtime admission rejected") {
			t.Fatalf("config root: %s", out)
		}
	})

	t.Run("env-over-config", func(t *testing.T) {
		e := newXDGEnv(t)
		e.writeConfig(t, full(e))
		envSock := filepath.Join(e.root, "env.sock")
		line := startDaemon(t, bin, t.TempDir(), append(e.vars(), "PATH="+t.TempDir(), "ASURA_SOCKET="+envSock))
		if want := "Daemon started on socket " + envSock + " (workers: 5, queue: 7)\n"; line != want {
			t.Fatalf("line=%q want %q", line, want)
		}
	})

	t.Run("cli-over-env-and-config", func(t *testing.T) {
		e := newXDGEnv(t)
		e.writeConfig(t, full(e))
		cwd, cfgRoot := t.TempDir(), filepath.Join(e.root, "cfgroot")
		os.Mkdir(cfgRoot, 0700)
		cliSock, cliDB := filepath.Join(e.root, "cli.sock"), filepath.Join(e.root, "cli.db")
		env := append(e.vars(), "PATH="+t.TempDir(), "ASURA_SOCKET="+filepath.Join(e.root, "env.sock"))
		// 顯式值等於內建預設（workers 2）或為空（allowed roots）也必須勝過 config。
		line := startDaemon(t, bin, cwd, env, "--socket", cliSock, "--db", cliDB, "--workers", "2", "--allowed-roots", "")
		if want := "Daemon started on socket " + cliSock + " (workers: 2, queue: 7)\n"; line != want {
			t.Fatalf("line=%q want %q", line, want)
		}
		if _, err := os.Stat(cliDB); err != nil {
			t.Fatalf("cli db not used: %v", err)
		}
		if _, err := os.Stat(filepath.Join(e.root, "cfgdata", "cfg.db")); !os.IsNotExist(err) {
			t.Fatal("config db opened despite --db")
		}
		if out := askRoot(t, bin, env, cfgRoot, "--socket", cliSock); !strings.Contains(out, "not in allowed roots") {
			t.Fatalf("config roots applied despite explicit empty --allowed-roots: %s", out)
		}
		if out := askRoot(t, bin, env, cwd, "--socket", cliSock); strings.Contains(out, "not in allowed roots") {
			t.Fatalf("cwd root missing: %s", out)
		}
	})

	t.Run("xdg-when-config-omits-paths", func(t *testing.T) {
		e := newXDGEnv(t)
		e.writeConfig(t, "schema_version = 1\n")
		line := startDaemon(t, bin, t.TempDir(), append(e.vars(), "PATH="+t.TempDir()))
		if want := "Daemon started on socket " + filepath.Join(e.runtimeDir, "asura", "asura.sock") + " (workers: 2, queue: 10)\n"; line != want {
			t.Fatalf("line=%q want %q", line, want)
		}
		if _, err := os.Stat(filepath.Join(e.dataHome, "asura", "asura.db")); err != nil {
			t.Fatalf("XDG db not used: %v", err)
		}
	})
}

func TestConfigLocalFallbackUsesConfigDB(t *testing.T) {
	e := newXDGEnv(t)
	cfgDB := filepath.Join(e.root, "cfg.db")
	e.writeConfig(t, "schema_version = 1\nsocket = \""+filepath.Join(e.root, "missing.sock")+"\"\ndb = \""+cfgDB+"\"\n")
	e.setenv(t)
	t.Setenv("PATH", t.TempDir())
	cwd := t.TempDir()
	t.Chdir(cwd)
	for _, args := range [][]string{{"status", "x"}, {"cancel", "x"}, {"audit", "--id", "x"}} {
		os.Remove(cfgDB)
		var out, stderr bytes.Buffer
		RunCLI(args, &out, &stderr)
		if _, err := os.Stat(cfgDB); err != nil {
			t.Fatalf("%v did not fall back to config db: %v (%s)", args, err, stderr.String())
		}
		cliDB := filepath.Join(t.TempDir(), "cli.db")
		RunCLI(append([]string{args[0], "--db", cliDB}, args[1:]...), &out, &stderr)
		if _, err := os.Stat(cliDB); err != nil {
			t.Fatalf("%v ignored --db: %v", args, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cwd, "asura.db")); !os.IsNotExist(err) {
		t.Fatal("legacy cwd db used despite config")
	}
	var out, stderr bytes.Buffer
	if code := RunCLI([]string{"runtime", "inventory"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "Runtime Inventory") {
		t.Fatalf("inventory fallback: %d %s", code, stderr.String())
	}
}

func TestConfigRuntimePriorityAndContext(t *testing.T) {
	e := newXDGEnv(t)
	e.writeConfig(t, "schema_version = 1\nruntime_priority = [\"codex\", \"opencode\"]\n")
	e.setenv(t)
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\necho 1.18.23\n"), 0700)
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'codex-cli 0.155.0'; exit 0; fi\nprintf '%s\\n' '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"ok\"}}'\n"), 0700)
	t.Setenv("PATH", bin)
	ws := t.TempDir()
	backendOf := func(id string) string {
		db := filepath.Join(t.TempDir(), "tasks.db")
		var out, stderr bytes.Buffer
		RunCLI([]string{"run", "--db", db, "--workspace", ws, "--id", id, "--backend", "auto", "probe"}, &out, &stderr)
		store, err := storage.NewSQLiteStore(db)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		task, err := store.GetTask(context.Background(), id)
		if err != nil {
			t.Fatalf("%s: %v %s", id, err, stderr.String())
		}
		return task.Backend
	}
	t.Setenv("ASURA_RUNTIME_PRIORITY", "")
	if got := backendOf("cfg"); got != "codex" {
		t.Fatalf("config priority ignored: %s", got)
	}
	t.Setenv("ASURA_RUNTIME_PRIORITY", "opencode,codex")
	if got := backendOf("env"); got != "opencode" {
		t.Fatalf("env priority did not win: %s", got)
	}

	e.writeConfig(t, "schema_version = 1\n[context]\nlimit = 5\npolicy = \"reject\"\n")
	run := func() (int, string) {
		var out, stderr bytes.Buffer
		code := RunCLI([]string{"run", "--db", filepath.Join(t.TempDir(), "c.db"), "--workspace", ws, "--backend", "codex", "this prompt definitely exceeds 5 tokens limit"}, &out, &stderr)
		return code, out.String() + stderr.String()
	}
	if code, out := run(); code != 1 || !strings.Contains(out, "context limit exceeded") {
		t.Fatalf("config context ignored: %d %s", code, out)
	}
	t.Setenv("ASURA_CONTEXT_POLICY", "observe")
	if _, out := run(); strings.Contains(out, "context limit exceeded") {
		t.Fatalf("env context policy did not win: %s", out)
	}
}

func TestConfigMemoryUtilityPolicy(t *testing.T) {
	e := newXDGEnv(t)
	e.writeConfig(t, "schema_version = 1\nmemory_utility_policy = \"apply\"\n")
	e.setenv(t)
	cc, err := loadCLIConfig()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASURA_MEMORY_UTILITY_POLICY", "")
	if got := cc.memoryUtilityPolicy(); got != "apply" {
		t.Fatalf("config utility policy ignored: %q", got)
	}
	t.Setenv("ASURA_MEMORY_UTILITY_POLICY", "observe")
	if got := cc.memoryUtilityPolicy(); got != "observe" {
		t.Fatalf("env utility policy did not win: %q", got)
	}
	t.Setenv("ASURA_MEMORY_UTILITY_POLICY", "")
	if got := (*cliConfig)(nil).memoryUtilityPolicy(); got != "observe" {
		t.Fatalf("builtin utility policy: %q", got)
	}
}

func TestConfigInvalidFailsClosed(t *testing.T) {
	bin := buildCLI(t)
	// 子行程執行並設逾時：若未 fail-closed，daemon／web 會啟動並持續執行。
	run := func(e xdgEnv, extra []string, args ...string) (int, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		c := exec.CommandContext(ctx, bin, args...)
		c.Dir = t.TempDir()
		c.Env = append(append(e.vars(), "PATH="+t.TempDir()), extra...)
		var stderr bytes.Buffer
		c.Stderr = &stderr
		err := c.Run()
		if ctx.Err() != nil {
			return -1, "timed out (command kept running): " + stderr.String()
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), stderr.String()
		}
		return 0, stderr.String()
	}
	for name, body := range map[string]string{
		"unknown-field": "schema_version = 1\napi_token = \"SECRET-SENTINEL-c0de\"\n",
		"bad-schema":    "schema_version = 7\n",
	} {
		t.Run(name, func(t *testing.T) {
			e := newXDGEnv(t)
			e.writeConfig(t, body)
			for _, args := range [][]string{{"daemon"}, {"run", "probe"}, {"ask", "probe"}, {"status", "x"}, {"cancel", "x"}, {"audit", "--id", "x"}, {"runtime", "inventory"}, {"web", "--listen", "127.0.0.1:0"}, {"cron", "list"}, {"memory", "list", "--workspace", e.root}} {
				if code, stderr := run(e, nil, args...); code != 1 || !strings.Contains(stderr, "config") || strings.Contains(stderr, "SECRET-SENTINEL") {
					t.Fatalf("%v: code=%d stderr=%s", args, code, stderr)
				}
			}
			if _, err := os.Stat(filepath.Join(e.runtimeDir, "asura", "asura.sock")); !os.IsNotExist(err) {
				t.Fatal("daemon socket created with invalid config")
			}
			if code, stderr := run(e, nil, "version"); code != 0 {
				t.Fatalf("version requires config: %s", stderr)
			}
		})
	}
	t.Run("unsafe-permissions", func(t *testing.T) {
		e := newXDGEnv(t)
		e.writeConfig(t, "schema_version = 1\n")
		os.Chmod(filepath.Join(e.configHome, "asura", "config.toml"), 0666)
		if code, stderr := run(e, nil, "daemon"); code != 1 || !strings.Contains(stderr, "writable by group or others") {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
	})
	t.Run("socket-too-long", func(t *testing.T) {
		e := newXDGEnv(t)
		e.writeConfig(t, "schema_version = 1\n")
		long := []string{"XDG_RUNTIME_DIR=" + filepath.Join(e.runtimeDir, strings.Repeat("r", 100))}
		if code, stderr := run(e, long, "daemon"); code != 1 || !strings.Contains(stderr, "Unix socket limit") {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
	})
}
