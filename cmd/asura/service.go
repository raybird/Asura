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

func userUnitPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(base) {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", serviceUnit)
}

// systemctl 以獨立 process group 執行 `systemctl --user`，逾時回收整組；回傳 stdout 與結束錯誤。
func systemctl(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
	if _, err := systemctl(10*time.Second, "show-environment"); err != nil {
		fmt.Fprintln(stderr, "error: systemd user bus unavailable; run `asura daemon` standalone")
		return 1
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "error: "+format+"\n", a...)
		return 1
	}
	unit := userUnitPath()
	switch verb {
	case "install":
		if activeConfig == nil {
			return fail("no config found; run `asura setup` first")
		}
		if err := checkManagedUnit(unit); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fail("%v", err)
		}
		binary, err := os.Executable()
		if err != nil {
			return fail("cannot determine the asura binary path")
		}
		configHome := os.Getenv("XDG_CONFIG_HOME")
		if home, _ := os.UserHomeDir(); !filepath.IsAbs(configHome) || configHome == filepath.Join(home, ".config") {
			configHome = ""
		}
		if err := writeUnit(unit, renderUserUnit(binary, configHome)); err != nil {
			return fail("cannot write %s", unit)
		}
		if _, err := systemctl(30*time.Second, "daemon-reload"); err != nil {
			return fail("systemctl --user daemon-reload failed")
		}
		if _, err := systemctl(30*time.Second, "enable", serviceUnit); err != nil {
			return fail("systemctl --user enable %s failed", serviceUnit)
		}
		fmt.Fprintf(stdout, "Installed %s and enabled %s; start it with `asura service start`.\n", unit, serviceUnit)
	case "start", "stop", "restart":
		// stop／restart 最多等待 unit 的 60 秒停止期限。
		if _, err := systemctl(90*time.Second, verb, serviceUnit); err != nil {
			return fail("systemctl --user %s %s failed", verb, serviceUnit)
		}
		fmt.Fprintf(stdout, "%s: %s requested\n", serviceUnit, verb)
	case "status":
		state, _ := systemctl(10*time.Second, "is-active", serviceUnit)
		enabled, _ := systemctl(10*time.Second, "is-enabled", serviceUnit)
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
		if _, err := systemctl(90*time.Second, "disable", "--now", serviceUnit); err != nil {
			return fail("systemctl --user disable --now %s failed", serviceUnit)
		}
		if err := os.Remove(unit); err != nil {
			return fail("cannot remove %s", unit)
		}
		if _, err := systemctl(30*time.Second, "daemon-reload"); err != nil {
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
