package main

// 2026-10-09：issue-45 Task 5.1，asura service 子命令與 user unit（DIST-014／015，支援 DIST-013）。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"asura/internal/storage"
)

func TestRenderUserUnit(t *testing.T) {
	got := renderUserUnit(`/opt/my apps/a"b%c$d\e/asura`, "")
	want := `# Managed by asura service install; do not edit.
[Unit]
Description=Asura daemon
StartLimitIntervalSec=60s
StartLimitBurst=3

[Service]
Type=simple
ExecStart="/opt/my apps/a\"b%%c$$d\\e/asura" daemon
KillMode=mixed
TimeoutStopSec=60s
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=default.target
`
	if got != want {
		t.Fatalf("unit:\n%s\nwant:\n%s", got, want)
	}
	withEnv := renderUserUnit("/usr/local/bin/asura", "/srv/conf dir")
	if !strings.Contains(withEnv, "ExecStart=\"/usr/local/bin/asura\" daemon\nEnvironment=\"XDG_CONFIG_HOME=/srv/conf dir\"\n") {
		t.Fatalf("environment line:\n%s", withEnv)
	}
}

// serviceEnv 準備有效 config、DB 與記錄 argv 的 systemctl stub。
func serviceEnv(t *testing.T, withSystemctl bool) (xdgEnv, string) {
	t.Helper()
	e := newXDGEnv(t)
	e.setenv(t)
	root := filepath.Join(e.root, "work")
	os.Mkdir(root, 0700)
	e.writeConfig(t, "schema_version = 1\nallowed_roots = [\""+root+"\"]\n")
	bin := t.TempDir()
	log := filepath.Join(e.root, "systemctl.log")
	if withSystemctl {
		stub := `#!/bin/sh
echo "$*" >> ` + log + `
[ "$SYSTEMCTL_BUS" = down ] && exit 1
case "$2" in
  is-active) echo "${SYSTEMCTL_STATE:-inactive}" ;;
  is-enabled) echo enabled ;;
esac
exit 0
`
		os.WriteFile(filepath.Join(bin, "systemctl"), []byte(stub), 0700)
	}
	t.Setenv("PATH", bin)
	t.Setenv("SYSTEMCTL_BUS", "")
	t.Chdir(t.TempDir())
	return e, log
}

func runService(args ...string) (int, string) {
	var out, stderr bytes.Buffer
	code := RunCLI(append([]string{"service"}, args...), &out, &stderr)
	return code, out.String() + stderr.String()
}

func calls(t *testing.T, log string) []string {
	b, _ := os.ReadFile(log)
	os.Remove(log)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestServiceInstallAndLifecycle(t *testing.T) {
	e, log := serviceEnv(t, true)
	unit := filepath.Join(e.configHome, "systemd", "user", "asura.service")
	exe, _ := os.Executable()
	code, out := runService("install")
	if code != 0 {
		t.Fatalf("install: %d %s", code, out)
	}
	b, err := os.ReadFile(unit)
	if err != nil || string(b) != renderUserUnit(exe, e.configHome) {
		t.Fatalf("unit content:\n%s", b)
	}
	if info, _ := os.Stat(unit); info.Mode().Perm() != 0644 {
		t.Fatalf("unit mode %v", info.Mode().Perm())
	}
	if got := strings.Join(calls(t, log), "|"); got != "--user show-environment|--user daemon-reload|--user enable asura.service" {
		t.Fatalf("install argv: %s", got)
	}
	if strings.Contains(string(b), "linger") || strings.Contains(string(b), "SECRET") {
		t.Fatal("unit contains linger or secret")
	}
	// 重跑不產生重複 unit。
	if code, out := runService("install"); code != 0 {
		t.Fatalf("reinstall: %s", out)
	}
	if entries, _ := os.ReadDir(filepath.Dir(unit)); len(entries) != 1 {
		t.Fatalf("unit dir entries: %v", entries)
	}
	calls(t, log)
	for _, verb := range []string{"start", "stop", "restart"} {
		if code, out := runService(verb); code != 0 {
			t.Fatalf("%s: %s", verb, out)
		}
		if got := strings.Join(calls(t, log), "|"); got != "--user show-environment|--user "+verb+" asura.service" {
			t.Fatalf("%s argv: %s", verb, got)
		}
	}
	t.Setenv("SYSTEMCTL_STATE", "active")
	if code, out := runService("status"); code != 0 || !strings.Contains(out, "asura.service: active (enabled)") {
		t.Fatalf("status: %d %s", code, out)
	}
}

func TestServiceDefaultConfigHomeOmitsEnvironment(t *testing.T) {
	e, _ := serviceEnv(t, true)
	t.Setenv("XDG_CONFIG_HOME", "")
	home := filepath.Join(e.home, ".config")
	os.MkdirAll(filepath.Join(home, "asura"), 0700)
	cfg, _ := os.ReadFile(filepath.Join(e.configHome, "asura", "config.toml"))
	os.WriteFile(filepath.Join(home, "asura", "config.toml"), cfg, 0600)
	if code, out := runService("install"); code != 0 {
		t.Fatalf("install: %s", out)
	}
	b, _ := os.ReadFile(filepath.Join(home, "systemd", "user", "asura.service"))
	if strings.Contains(string(b), "Environment=") {
		t.Fatalf("default config home pinned:\n%s", b)
	}
}

func TestServiceRefusesUnmanagedUnit(t *testing.T) {
	e, log := serviceEnv(t, true)
	unit := filepath.Join(e.configHome, "systemd", "user", "asura.service")
	os.MkdirAll(filepath.Dir(unit), 0700)
	foreign := "[Service]\nExecStart=/usr/bin/other\n"
	os.WriteFile(unit, []byte(foreign), 0644)
	for _, verb := range []string{"install", "uninstall"} {
		code, out := runService(verb)
		if code != 1 || !strings.Contains(out, "is not managed by asura") {
			t.Fatalf("%s: %d %s", verb, code, out)
		}
		if b, _ := os.ReadFile(unit); string(b) != foreign {
			t.Fatalf("%s modified unmanaged unit", verb)
		}
	}
	for _, c := range calls(t, log) {
		if strings.Contains(c, "enable") || strings.Contains(c, "disable") {
			t.Fatalf("systemctl changed state of unmanaged unit: %s", c)
		}
	}
}

func TestServiceInstallRequiresConfig(t *testing.T) {
	e, _ := serviceEnv(t, true)
	os.Remove(filepath.Join(e.configHome, "asura", "config.toml"))
	if code, out := runService("install"); code != 1 || !strings.Contains(out, "asura setup") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestServiceUninstallKeepsData(t *testing.T) {
	e, log := serviceEnv(t, true)
	db := filepath.Join(e.dataHome, "asura", "asura.db")
	os.MkdirAll(filepath.Dir(db), 0700)
	store, err := storage.NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.CreateTask(ctx, &storage.Task{ID: "kept-task", Prompt: "p", Workspace: e.root, Status: storage.StatusCompleted}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateMemory(ctx, storage.Memory{ID: "kept-memory", Workspace: e.root, Content: "c", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	cfgPath := filepath.Join(e.configHome, "asura", "config.toml")
	hash := func(p string) [32]byte { b, _ := os.ReadFile(p); return sha256.Sum256(b) }
	cfgBefore, dbBefore := hash(cfgPath), hash(db)
	if code, out := runService("install"); code != 0 {
		t.Fatalf("install: %s", out)
	}
	calls(t, log)
	if code, out := runService("uninstall"); code != 0 {
		t.Fatalf("uninstall: %d %s", code, out)
	}
	if got := strings.Join(calls(t, log), "|"); got != "--user show-environment|--user disable --now asura.service|--user daemon-reload" {
		t.Fatalf("uninstall argv: %s", got)
	}
	if _, err := os.Stat(filepath.Join(e.configHome, "systemd", "user", "asura.service")); !os.IsNotExist(err) {
		t.Fatal("unit not removed")
	}
	if hash(cfgPath) != cfgBefore || hash(db) != dbBefore {
		t.Fatal("uninstall changed config or database")
	}
	store, err = storage.NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if task, err := store.GetTask(ctx, "kept-task"); err != nil || task.ID != "kept-task" {
		t.Fatalf("task lost: %v", err)
	}
	if mems, err := store.ListMemories(ctx, e.root); err != nil || len(mems) != 1 {
		t.Fatalf("memory lost: %v %v", mems, err)
	}
	if code, out := runService("uninstall"); code != 0 || !strings.Contains(out, "not installed") {
		t.Fatalf("second uninstall: %d %s", code, out)
	}
}

func TestServiceWithoutSystemd(t *testing.T) {
	t.Run("no-systemctl", func(t *testing.T) {
		serviceEnv(t, false)
		for _, verb := range []string{"install", "start", "stop", "restart", "status", "uninstall"} {
			if code, out := runService(verb); code != 1 || !strings.Contains(out, "systemctl not found; run `asura daemon` standalone") {
				t.Fatalf("%s: %d %s", verb, code, out)
			}
		}
	})
	t.Run("no-user-bus", func(t *testing.T) {
		e, _ := serviceEnv(t, true)
		t.Setenv("SYSTEMCTL_BUS", "down")
		if code, out := runService("install"); code != 1 || !strings.Contains(out, "systemd user bus unavailable") {
			t.Fatalf("%d %s", code, out)
		}
		if _, err := os.Stat(filepath.Join(e.configHome, "systemd")); !os.IsNotExist(err) {
			t.Fatal("unit written without a user bus")
		}
	})
}

func TestServiceUsage(t *testing.T) {
	serviceEnv(t, true)
	for _, args := range [][]string{{}, {"bogus"}, {"start", "extra"}} {
		if code, _ := runService(args...); code != 2 {
			t.Fatalf("%v code=%d", args, code)
		}
	}
}
