package main

// 2026-10-09：issue-45，asura service：以 systemd user service 管理 daemon。
// 只管理帶有 Asura 標記的 unit；不設定 linger、不寫入 secret、不建立其他 supervisor。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	serviceUnit       = "asura.service"
	serviceUnitMarker = "# Managed by asura service install; do not edit."
	serviceUsage      = "Usage: asura service <install|start|stop|restart|status|uninstall>"
)

// systemd 引號字串：反斜線與引號跳脫，% 為 specifier，ExecStart 的 $ 為變數展開。
var (
	execStartEscaper   = strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`)
	environmentEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`)
)

// renderUserUnit 產生 user unit；configHome 非空時固定 XDG_CONFIG_HOME，讓 service 讀到同一份 config。
func renderUserUnit(binary, configHome string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n[Unit]\nDescription=Asura daemon\nStartLimitIntervalSec=60s\nStartLimitBurst=3\n\n", serviceUnitMarker)
	fmt.Fprintf(&b, "[Service]\nType=simple\nExecStart=\"%s\" daemon\n", execStartEscaper.Replace(binary))
	if configHome != "" {
		fmt.Fprintf(&b, "Environment=\"XDG_CONFIG_HOME=%s\"\n", environmentEscaper.Replace(configHome))
	}
	b.WriteString("KillMode=mixed\nTimeoutStopSec=60s\nRestart=on-failure\nRestartSec=5s\n\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

var errUserBus = errors.New("systemd user bus unavailable; run `asura daemon` standalone")

// managerConfigHome 依 user manager 自身環境（`systemctl --user show-environment` 輸出）決定其 config 目錄；
// manager 只從這裡搜尋 unit，與 CLI 行程的 XDG_CONFIG_HOME 無關。
func managerConfigHome(environment string) (string, error) {
	vars := map[string]string{}
	for _, line := range strings.Split(environment, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			vars[k] = v
		}
	}
	if dir := vars["XDG_CONFIG_HOME"]; filepath.IsAbs(dir) {
		return dir, nil
	}
	home := vars["HOME"]
	if !filepath.IsAbs(home) {
		home = os.Getenv("HOME")
	}
	if !filepath.IsAbs(home) {
		return "", errors.New("cannot determine the systemd user config directory (HOME is not absolute)")
	}
	return filepath.Join(home, ".config"), nil
}

// managerUnitDir 查詢 user manager 並回傳其 config 目錄；同時作為 user bus 可用性檢查。
func managerUnitDir(ctx context.Context) (string, error) {
	environment, err := systemctl(ctx, 10*time.Second, "show-environment")
	if err != nil {
		return "", errUserBus
	}
	return managerConfigHome(environment)
}

func unitPathIn(configHome string) string {
	return filepath.Join(configHome, "systemd", "user", serviceUnit)
}

// systemctl 以獨立 process group 執行 `systemctl --user`，逾時回收整組；回傳 stdout 與結束錯誤。
func systemctl(parent context.Context, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return strings.TrimSpace(string(out)), err
}

func handleService(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, serviceUsage)
		return 2
	}
	verb := args[0]
	switch verb {
	case "install", "start", "stop", "restart", "status", "uninstall":
	default:
		fmt.Fprintln(stderr, serviceUsage)
		return 2
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		fmt.Fprintln(stderr, "error: systemd user services are unavailable: systemctl not found; run `asura daemon` standalone")
		return 1
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "error: "+format+"\n", a...)
		return 1
	}
	ctx := context.Background()
	managerHome, err := managerUnitDir(ctx)
	if err != nil {
		return fail("%v", err)
	}
	unit := unitPathIn(managerHome)
	switch verb {
	case "install":
		if activeConfig == nil {
			return fail("no config found; run `asura setup` first")
		}
		// 沒有 allowed_roots 時 daemon 會以 service 工作目錄（HOME）為 root，拒絕安裝。
		if activeConfig.cfg.AllowedRoots == nil {
			return fail("config %s has no allowed_roots; add it before installing the service", activeConfig.paths.ConfigFile)
		}
		if err := checkManagedUnit(unit); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fail("%v", err)
		}
		binary, err := os.Executable()
		if err != nil {
			return fail("cannot determine the asura binary path")
		}
		// CLI 讀取的 config 目錄與 manager 不同時，才固定 XDG_CONFIG_HOME 讓 service 讀到同一份 config。
		configHome := filepath.Dir(filepath.Dir(activeConfig.paths.ConfigFile))
		if configHome == managerHome {
			configHome = ""
		}
		if err := writeUnit(unit, renderUserUnit(binary, configHome)); err != nil {
			return fail("cannot write %s", unit)
		}
		if _, err := systemctl(ctx, 30*time.Second, "daemon-reload"); err != nil {
			return fail("systemctl --user daemon-reload failed")
		}
		if _, err := systemctl(ctx, 30*time.Second, "enable", serviceUnit); err != nil {
			return fail("systemctl --user enable %s failed", serviceUnit)
		}
		fmt.Fprintf(stdout, "Installed %s and enabled %s; start it with `asura service start`.\n", unit, serviceUnit)
	case "start", "stop", "restart":
		// 只操作 Asura 管理的 unit；stop／restart 最多等待 unit 的 60 秒停止期限。
		if err := checkManagedUnit(unit); errors.Is(err, fs.ErrNotExist) {
			return fail("%s is not installed; run `asura service install` first", serviceUnit)
		} else if err != nil {
			return fail("%v", err)
		}
		if _, err := systemctl(ctx, 90*time.Second, verb, serviceUnit); err != nil {
			return fail("systemctl --user %s %s failed", verb, serviceUnit)
		}
		fmt.Fprintf(stdout, "%s: %s requested\n", serviceUnit, verb)
	case "status":
		state, _ := systemctl(ctx, 10*time.Second, "is-active", serviceUnit)
		enabled, _ := systemctl(ctx, 10*time.Second, "is-enabled", serviceUnit)
		fmt.Fprintf(stdout, "%s: %s (%s)\n", serviceUnit, firstWord(state, "unknown"), firstWord(enabled, "unknown"))
	case "uninstall":
		err := checkManagedUnit(unit)
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(stdout, "%s: not installed\n", serviceUnit)
			return 0
		}
		if err != nil {
			return fail("%v", err)
		}
		if _, err := systemctl(ctx, 90*time.Second, "disable", "--now", serviceUnit); err != nil {
			return fail("systemctl --user disable --now %s failed", serviceUnit)
		}
		if err := os.Remove(unit); err != nil {
			return fail("cannot remove %s", unit)
		}
		if _, err := systemctl(ctx, 30*time.Second, "daemon-reload"); err != nil {
			return fail("systemctl --user daemon-reload failed")
		}
		fmt.Fprintf(stdout, "Removed %s; config and data were kept.\n", unit)
	}
	return 0
}

// checkManagedUnit 回傳 fs.ErrNotExist（未安裝）、未受管理錯誤，或 nil（Asura 管理的 unit）。
func checkManagedUnit(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(b), serviceUnitMarker+"\n") {
		return fmt.Errorf("%s is not managed by asura; remove or rename it manually", path)
	}
	return nil
}

// writeUnit 以同目錄暫存檔加 rename 寫入，避免留下半份 unit。
func writeUnit(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".asura.service.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// firstWord 只取 systemctl 輸出的第一個字，不轉述其餘內容。
func firstWord(s, fallback string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return fallback
}
