package main

// 2026-10-09：issue-45 Task 5.2，asura setup 互動／腳本模式與冪等（DIST-011／012）。

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"asura/internal/config"
)

// setupEnv 建立隔離 HOME／XDG（尚無 config）、一個 allowed root 與 systemctl／codex stub。
func setupEnv(t *testing.T) (xdgEnv, string, string) {
	t.Helper()
	e := newXDGEnv(t)
	e.setenv(t)
	root := filepath.Join(e.root, "project")
	os.Mkdir(root, 0700)
	bin := t.TempDir()
	log := filepath.Join(e.root, "systemctl.log")
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte(`#!/bin/sh
echo "$*" >> `+log+`
[ "$2" = enable ] && [ "$SYSTEMCTL_ENABLE" = fail ] && exit 1
case "$2" in is-active) echo active ;; is-enabled) echo enabled ;; esac
exit 0
`), 0700)
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho 'codex-cli 0.155.0'\n"), 0700)
	t.Setenv("PATH", bin)
	t.Setenv("SYSTEMCTL_ENABLE", "")
	t.Chdir(t.TempDir())
	return e, root, log
}

func runSetup(args ...string) (int, string) {
	var out, stderr bytes.Buffer
	code := RunCLI(append([]string{"setup"}, args...), &out, &stderr)
	return code, out.String() + stderr.String()
}

func TestSetupFreshNonInteractive(t *testing.T) {
	e, root, log := setupEnv(t)
	cred := filepath.Join(e.home, ".codex", "auth.json")
	os.MkdirAll(filepath.Dir(cred), 0700)
	os.WriteFile(cred, []byte("credential"), 0600)
	before := tree(t, e.root)

	code, out := runSetup("--allowed-roots", root)
	if code != 0 || !strings.Contains(out, "Setup complete.") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	cfgPath := filepath.Join(e.configHome, "asura", "config.toml")
	cfg, err := config.Load(cfgPath)
	// 第 18 題：新建 config 固定建立當下解析的絕對 db／socket。
	if err != nil || cfg == nil || strings.Join(cfg.AllowedRoots, ",") != root || cfg.DB == nil || *cfg.DB != filepath.Join(e.dataHome, "asura", "asura.db") ||
		cfg.Socket == nil || *cfg.Socket != filepath.Join(e.runtimeDir, "asura", "asura.sock") {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
	for path, mode := range map[string]os.FileMode{cfgPath: 0600, filepath.Dir(cfgPath): 0700, filepath.Join(e.dataHome, "asura"): 0700} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s mode: %v %v", path, info, err)
		}
	}
	for _, want := range []string{"Asura doctor:", "codex: available", "User service not enabled", "asura daemon"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	// 只新增 config 與 data 目錄；不建 DB、unit、shell rc，也不動 credential。
	var created []string
	for _, p := range diffTree(before, tree(t, e.root)) {
		// systemctl.log 是 stub 自己的呼叫紀錄（doctor 會查詢 service 狀態），不屬於使用者狀態。
		if !strings.HasPrefix(p, filepath.Join(e.configHome, "asura")) && p != filepath.Join(e.dataHome, "asura") && p != log {
			created = append(created, p)
		}
	}
	if len(created) > 0 {
		t.Fatalf("unexpected changes: %v", created)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "enable") || strings.Contains(string(b), "start") {
		t.Fatalf("service touched without --service: %s", b)
	}
}

func TestSetupNonInteractiveRequiresRoots(t *testing.T) {
	e, _, _ := setupEnv(t)
	code, out := runSetup()
	if code != 1 || !strings.Contains(out, "--allowed-roots is required") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(e.configHome, "asura")); !os.IsNotExist(err) {
		t.Fatal("config created without roots")
	}
	if code, out := runSetup("--allowed-roots", filepath.Join(e.root, "missing")); code != 1 || !strings.Contains(out, "is not a directory") {
		t.Fatalf("missing root: %d %s", code, out)
	}
}

func TestSetupServiceAndIdempotency(t *testing.T) {
	e, root, log := setupEnv(t)
	if code, out := runSetup("--allowed-roots", root, "--service"); code != 0 || !strings.Contains(out, "Setup complete.") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "--user enable asura.service") || !strings.Contains(string(b), "--user start asura.service") {
		t.Fatalf("service not installed and started: %s", b)
	}
	db := filepath.Join(e.dataHome, "asura", "asura.db")
	os.WriteFile(db, []byte("existing-data"), 0600)
	cfgPath := filepath.Join(e.configHome, "asura", "config.toml")
	custom, _ := os.ReadFile(cfgPath)
	custom = append(custom, []byte("workers = 4\n")...)
	os.WriteFile(cfgPath, custom, 0600)
	before := tree(t, e.root)

	if code, out := runSetup("--allowed-roots", root, "--service"); code != 0 {
		t.Fatalf("rerun: %d\n%s", code, out)
	}
	for _, p := range diffTree(before, tree(t, e.root)) {
		if p != log {
			t.Fatalf("rerun changed %s", p)
		}
	}
	units, _ := os.ReadDir(filepath.Join(e.home, ".config", "systemd", "user"))
	if len(units) != 1 {
		t.Fatalf("units=%v", units)
	}

	other := filepath.Join(e.root, "other")
	os.Mkdir(other, 0700)
	code, out := runSetup("--allowed-roots", other)
	if code != 1 || !strings.Contains(out, "conflicts with existing config") || !strings.Contains(out, cfgPath) {
		t.Fatalf("conflict: %d\n%s", code, out)
	}
	if after, _ := os.ReadFile(cfgPath); !bytes.Equal(after, custom) {
		t.Fatal("conflicting setup rewrote config")
	}
}

func TestSetupPartialFailureIsResumable(t *testing.T) {
	e, root, _ := setupEnv(t)
	t.Setenv("SYSTEMCTL_ENABLE", "fail")
	code, out := runSetup("--allowed-roots", root, "--service")
	if code != 1 || strings.Contains(out, "Setup complete.") || !strings.Contains(out, "Setup incomplete") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	t.Setenv("SYSTEMCTL_ENABLE", "")
	if code, out := runSetup("--allowed-roots", root, "--service"); code != 0 || !strings.Contains(out, "Setup complete.") {
		t.Fatalf("resume: %d\n%s", code, out)
	}
	if units, _ := os.ReadDir(filepath.Join(e.home, ".config", "systemd", "user")); len(units) != 1 {
		t.Fatalf("units=%v", units)
	}
}

// 2026-10-09：review M1／第 17 題，既有 config 缺 allowed_roots 時失敗、不改寫、不宣稱完成，也不安裝 service。
func TestSetupRejectsConfigWithoutAllowedRoots(t *testing.T) {
	e, _, log := setupEnv(t)
	e.writeConfig(t, "schema_version = 1\nworkers = 3\n")
	cfgPath := filepath.Join(e.configHome, "asura", "config.toml")
	before, _ := os.ReadFile(cfgPath)
	for _, args := range [][]string{nil, {"--service"}} {
		code, out := runSetup(args...)
		if code != 1 || !strings.Contains(out, cfgPath+" has no allowed_roots") || strings.Contains(out, "Setup complete.") {
			t.Fatalf("%v: code=%d\n%s", args, code, out)
		}
	}
	if after, _ := os.ReadFile(cfgPath); !bytes.Equal(before, after) {
		t.Fatal("config rewritten")
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "enable") || strings.Contains(string(b), "start") {
		t.Fatalf("service touched: %s", b)
	}
}

func TestSetupReportsLegacyDatabase(t *testing.T) {
	_, root, _ := setupEnv(t)
	cwd, _ := os.Getwd()
	os.WriteFile(filepath.Join(cwd, "asura.db"), []byte("legacy"), 0600)
	code, out := runSetup("--allowed-roots", root)
	if code != 0 || !strings.Contains(out, "Found existing database "+filepath.Join(cwd, "asura.db")) || !strings.Contains(out, "was not moved") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(cwd, "asura.db")); string(b) != "legacy" {
		t.Fatal("legacy database changed")
	}
}

func TestSetupInteractive(t *testing.T) {
	scriptPath, err := exec.LookPath("script")
	if err != nil {
		t.Skip("util-linux script is required to provide a terminal")
	}
	bin := buildCLI(t)
	e, root, log := setupEnv(t)
	cmd := exec.Command(scriptPath, "-qec", bin+" setup", "/dev/null")
	cmd.Env = append(e.vars(), "PATH="+os.Getenv("PATH"), "TERM=dumb")
	cmd.Stdin = strings.NewReader(root + "\nn\n")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Allowed root directories") || !strings.Contains(string(out), "Install and start the user service? [y/N]") || !strings.Contains(string(out), "Setup complete.") {
		t.Fatalf("err=%v\n%s", err, out)
	}
	cfg, err := config.Load(filepath.Join(e.configHome, "asura", "config.toml"))
	if err != nil || cfg == nil || strings.Join(cfg.AllowedRoots, ",") != root {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "enable") {
		t.Fatalf("service installed after declining: %s", b)
	}
}
