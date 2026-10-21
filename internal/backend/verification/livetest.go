package verification

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LiveEnabled 2026-10-21：會呼叫模型的 probe 只在明確設定 ASURA_VERIFY_LIVE=1 時執行。
func LiveEnabled() bool { return os.Getenv("ASURA_VERIFY_LIVE") == "1" }

// NewBase 在使用者快取目錄下建立 probe 根目錄；刻意避開 /tmp，
// 因為 Codex workspace-write 預設可寫 /tmp、bwrap 會把 /tmp 換成 tmpfs，兩者都會讓 workspace 外的寫入檢查失準。
func NewBase() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(cache, "asura-verify"), 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(filepath.Join(cache, "asura-verify"), "run-")
}

// WriteShim 寫出包裝 real 的 shell 腳本，供失效對照組刻意削弱沙箱。
func WriteShim(dir, name, real, body string) (string, error) {
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\nREAL=" + shellQuote(real) + "\n" + body + "\n"
	return path, os.WriteFile(path, []byte(script), 0o700)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// BwrapControl 讓 clisandbox 改用包裝過的 bwrap：在每個 --die-with-parent 前多掛可寫的 bind，
// 模擬 workspace 外的路徑漏進沙箱。回傳應放在 PATH 最前面的目錄。
func BwrapControl(dir, bind string) (string, error) {
	real, err := exec.LookPath("bwrap")
	if err != nil {
		return "", err
	}
	_, err = WriteShim(dir, "bwrap", real, `BIND=`+shellQuote(bind)+`
for a; do shift
  [ "$a" = --die-with-parent ] && set -- "$@" --bind "$BIND" "$BIND"
  set -- "$@" "$a"
done
exec "$REAL" "$@"`)
	return dir, err
}

// Root 回傳 NewBase 建立 probe 目錄的上層。
func Root() (string, error) {
	cache, err := os.UserCacheDir()
	return filepath.Join(cache, "asura-verify"), err
}
