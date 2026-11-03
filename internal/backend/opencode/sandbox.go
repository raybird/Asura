package opencode

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// sandboxArgs 2026-11-03：issue-75，回傳寫入角色在 Asura 的 bwrap 內執行的參數（到 "--" 為止）。
// 寫回主機的只有 workspace 與 OpenCode 的 data、state 目錄（登入更新、session、log）；
// HOME 其他部分是暫時 overlay，沙箱內可寫、結束即丟，讓 MCP server 與工具快取照常運作（Q5）；
// /tmp 與 /run 換成 tmpfs，沙箱內連不到主機 /run 底下的 socket，例如 docker 與 session bus（Q6）；HOME 以外唯讀。
// 執行檔本身唯讀掛回，執行檔位在 /tmp 底下時仍可執行；只掛檔案，避免目錄是 HOME 或 /tmp 時蓋掉 overlay 與遮蔽（PR #86 審查 S2）。
// --new-session 讓沙箱內的命令沒有控制終端，無法以 TIOCSTI 注入按鍵（審查 S4）。
// --die-with-parent 放在掛載參數之後，與 clisandbox 相同；失效對照組（verification.BwrapControl）在它前面插入可寫的 bind。
// errWorkspaceCoversMask：workspace 等於或包含 HOME 與遮蔽目錄時，workspace 的可寫掛載會蓋掉它們（Q8）。
var errWorkspaceCoversMask = errors.New("workspace overlaps HOME or a masked directory")

func sandboxArgs(env []string, workspace, bin string) ([]string, error) {
	home := envValue(env, "HOME")
	if home == "" {
		return nil, errors.New("HOME is not set")
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	if workspace, err = filepath.EvalSymlinks(workspace); err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	for _, masked := range []string{home, "/tmp", "/var/tmp", "/run"} {
		if rel, err := filepath.Rel(workspace, masked); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, errWorkspaceCoversMask
		}
	}
	data := xdgDir(env, "XDG_DATA_HOME", home, ".local/share")
	state := xdgDir(env, "XDG_STATE_HOME", home, ".local/state")
	for _, dir := range []string{data, state} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	bin, err = filepath.EvalSymlinks(bin)
	if err != nil {
		return nil, err
	}
	args := []string{"--unshare-pid", "--unshare-ipc", "--new-session", "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp", "--tmpfs", "/var/tmp", "--tmpfs", "/run"}
	// Ubuntu 的 /etc/resolv.conf 指向 /run/systemd/resolve 底下的檔案，不掛回就無法解析名稱；只掛檔案，不帶同目錄的 varlink socket。
	if resolv, err := filepath.EvalSymlinks("/etc/resolv.conf"); err == nil && strings.HasPrefix(resolv, "/run/") {
		args = append(args, "--ro-bind", resolv, resolv)
	}
	args = append(args, "--overlay-src", home, "--tmp-overlay", home, "--bind", data, data, "--bind", state, state, "--ro-bind", bin, bin, "--bind", workspace, workspace, "--die-with-parent", "--chdir", workspace, "--")
	return args, nil
}

// xdgDir 依 OpenCode 的 XDG 規則回傳它的目錄：key 有值時用 <key>/opencode，否則用 <home>/<fallback>/opencode。
func xdgDir(env []string, key, home, fallback string) string {
	if base := envValue(env, key); base != "" {
		return filepath.Join(base, "opencode")
	}
	return filepath.Join(home, fallback, "opencode")
}

// envValue 回傳 env 中最後一個 key 的值；與 exec 傳給子行程的值相同。
func envValue(env []string, key string) string {
	value := ""
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, key+"="); ok {
			value = v
		}
	}
	return value
}

// bwrapSupportsOverlay 回報 `bwrap --version` 是否為 0.10 以上；--overlay-src 與 --tmp-overlay 自 0.10 起才有（Q7）。
func bwrapSupportsOverlay(ctx context.Context, bwrap string) bool {
	out, err := exec.CommandContext(ctx, bwrap, "--version").Output()
	if err != nil {
		return false
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || fields[0] != "bubblewrap" {
		return false
	}
	parts := strings.SplitN(fields[1], ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	return err1 == nil && err2 == nil && (major > 0 || minor >= 10)
}
