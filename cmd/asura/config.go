package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"asura/internal/config"
	"asura/internal/contextwin"
)

// cliConfig 是本次 CLI 呼叫載入的 operator config 與 XDG 預設路徑。
// 方法皆接受 nil receiver：nil 代表沒有 config，維持 v1.0 的預設行為。
type cliConfig struct {
	cfg   *config.Config
	paths config.Paths
}

// activeConfig 由 RunCLI 在每次呼叫時設定。
var activeConfig *cliConfig

// loadCLIConfig 載入 XDG config；檔案不存在或無法定位 HOME 時回傳 nil（相容模式），壞檔回傳錯誤。
func loadCLIConfig() (*cliConfig, error) {
	paths, err := config.DefaultPaths(os.Getenv)
	if err != nil {
		return nil, nil
	}
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil || cfg == nil {
		return nil, err
	}
	return &cliConfig{cfg: cfg, paths: paths}, nil
}

// DefaultSocketPath 回傳統一預設的 Unix Socket 路徑
// 優先序：環境變數 ASURA_SOCKET -> config socket -> XDG socket；無 config 時 -> ~/.asura/asura.sock -> /tmp/asura.sock
func DefaultSocketPath() string {
	if env := os.Getenv("ASURA_SOCKET"); env != "" {
		return env
	}
	if c := activeConfig; c != nil {
		if c.cfg.Socket != nil {
			return *c.cfg.Socket
		}
		return c.paths.Socket
	}
	home, err := os.UserHomeDir()
	if err == nil {
		asuraDir := filepath.Join(home, ".asura")
		_ = os.MkdirAll(asuraDir, 0755)
		return filepath.Join(asuraDir, "asura.sock")
	}
	return filepath.Join(os.TempDir(), "asura.sock")
}

// localSocketDefault 是 status／cancel／audit／inventory 的 socket 預設；無 config 時只認 ASURA_SOCKET，未設定即走本機 DB。
func localSocketDefault() string {
	if activeConfig == nil {
		return os.Getenv("ASURA_SOCKET")
	}
	return DefaultSocketPath()
}

func (c *cliConfig) dbPath() string {
	if c == nil {
		return "asura.db"
	}
	if c.cfg.DB != nil {
		return *c.cfg.DB
	}
	return c.paths.DB
}

func (c *cliConfig) allowedRoots() string {
	if c == nil {
		return ""
	}
	return strings.Join(c.cfg.AllowedRoots, ",")
}

func (c *cliConfig) workers() int {
	if c == nil || c.cfg.Workers == nil {
		return 2
	}
	return *c.cfg.Workers
}

func (c *cliConfig) queue() int {
	if c == nil || c.cfg.Queue == nil {
		return 10
	}
	return *c.cfg.Queue
}

// runtimePriority 依 ASURA_RUNTIME_PRIORITY > config 取值；空字串代表使用內建順序。
func (c *cliConfig) runtimePriority() string {
	if env := os.Getenv("ASURA_RUNTIME_PRIORITY"); env != "" || c == nil {
		return env
	}
	return strings.Join(c.cfg.RuntimePriority, ",")
}

func (c *cliConfig) memoryUtilityPolicy() string {
	if env := os.Getenv("ASURA_MEMORY_UTILITY_POLICY"); env != "" {
		return env
	}
	if c != nil && c.cfg.MemoryUtilityPolicy != nil {
		return *c.cfg.MemoryUtilityPolicy
	}
	return "observe"
}

// contextConfig 逐欄位採 ENV > config > 內建預設，驗證沿用 contextwin。
func (c *cliConfig) contextConfig() (contextwin.Config, error) {
	return contextwin.LoadConfigWithLookup(func(key string) (string, bool) {
		if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
			return v, true
		}
		if c == nil {
			return "", false
		}
		switch {
		case key == contextwin.EnvContextLimit && c.cfg.ContextLimit != nil:
			return strconv.FormatInt(*c.cfg.ContextLimit, 10), true
		case key == contextwin.EnvContextPolicy && c.cfg.ContextPolicy != nil:
			return *c.cfg.ContextPolicy, true
		case key == contextwin.EnvContextReserveRatio && c.cfg.ContextReserveRatio != nil:
			return strconv.FormatFloat(*c.cfg.ContextReserveRatio, 'g', -1, 64), true
		}
		return "", false
	})
}

// prepareDirs 在有 config 時以 0700 建立 DB／socket 所在目錄；無 config 維持舊行為不建立。
func (c *cliConfig) prepareDirs(paths ...string) error {
	if c == nil {
		return nil
	}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			return fmt.Errorf("cannot create directory for %s: %w", p, err)
		}
	}
	return nil
}
