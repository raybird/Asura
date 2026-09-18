package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

var (
	// ErrNoBinary 代表找不到合格的 OpenCode binary
	ErrNoBinary = errors.New("opencode binary not found")
	// ErrExecutionFailed 代表 OpenCode 執行非零退出且無可恢復輸出
	ErrExecutionFailed = errors.New("opencode execution failed")
	// ErrEmptyResult 代表執行未產生任何有效文字
	ErrEmptyResult = errors.New("opencode produced empty result")
)

// RuntimeRole 定義 Three-Face Pipeline 各階段在執行器層級之角色標記
type RuntimeRole string

const (
	RoleOracle  RuntimeRole = "oracle"
	RoleStriker RuntimeRole = "striker"
	RoleCritic  RuntimeRole = "critic"
)

// AsuraReadOnlyAgentName 定義專屬於 Asura 唯讀角色（Oracle, Critic）的 Agent 名稱
const AsuraReadOnlyAgentName = "asura-readonly"

// AsuraReadOnlyConfigJSON 定義 Asura 專屬唯讀 Agent 之獨立設定檔內容
// 透過 primary mode 與明確的 permission 限制，在 OpenCode agent 評估層提供不可覆蓋的唯讀邊界
const AsuraReadOnlyConfigJSON = `{
  "agent": {
    "asura-readonly": {
      "mode": "primary",
      "description": "Asura read-only execution agent for Oracle and Critic",
      "permission": {
        "*": "deny",
        "read": "allow",
        "glob": "allow",
        "grep": "allow",
        "list": "allow",
        "lsp": "allow"
      }
    }
  }
}`

// CreateIsolatedReadOnlyConfigDir 建立包含 asura-readonly agent 的獨立配置目錄與乾淨 home 隔離空間
// 目錄結構包含：
//   - <dir>/opencode/opencode.json (只定義 asura-readonly primary agent)
//   - <dir>/home (乾淨空目錄，用於隔離 HOME/.opencode)
// 回傳目錄路徑與清理函式，供 Oracle 與 Critic 唯讀階段使用，免受外部、全域或專案設定污染
func CreateIsolatedReadOnlyConfigDir() (string, func(), error) {
	dir, err := os.MkdirTemp("", "asura-opencode-readonly-*")
	if err != nil {
		return "", nil, fmt.Errorf("create isolated read-only config dir: %w", err)
	}
	opencodeDir := filepath.Join(dir, "opencode")
	if err := os.MkdirAll(opencodeDir, 0700); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("create isolated opencode dir: %w", err)
	}
	homeDir := filepath.Join(dir, "home")
	if err := os.MkdirAll(homeDir, 0700); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("create isolated home dir: %w", err)
	}
	// 預設純 Asura 唯讀配置
	configContent := []byte(AsuraReadOnlyConfigJSON)

	// 若使用者全域環境存在 opencode.json，安全提取 provider 與 model 設定以確保模型調用能力，
	// 但嚴格過濾、排除任何 agent、permission、mcp、plugin 污染
	userHome, _ := os.UserHomeDir()
	xdgConfig := os.Getenv("XDG_CONFIG_HOME")
	var globalCfgPath string
	if xdgConfig != "" {
		globalCfgPath = filepath.Join(xdgConfig, "opencode", "opencode.json")
	} else if userHome != "" {
		globalCfgPath = filepath.Join(userHome, ".config", "opencode", "opencode.json")
	}

	if globalCfgPath != "" {
		if rawGlobal, err := os.ReadFile(globalCfgPath); err == nil {
			var globalData map[string]any
			if err := json.Unmarshal(rawGlobal, &globalData); err == nil {
				var baseData map[string]any
				if err := json.Unmarshal([]byte(AsuraReadOnlyConfigJSON), &baseData); err == nil {
					if p, ok := globalData["provider"]; ok {
						baseData["provider"] = p
					}
					if m, ok := globalData["model"]; ok {
						baseData["model"] = m
					}
					if mergedJSON, err := json.MarshalIndent(baseData, "", "  "); err == nil {
						configContent = mergedJSON
					}
				}
			}
		}
	}

	cfgPath := filepath.Join(opencodeDir, "opencode.json")
	if err := os.WriteFile(cfgPath, configContent, 0600); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("write isolated read-only opencode.json: %w", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(dir)
	}
	return dir, cleanup, nil
}

// AdapterConfig 定義 OpenCode Adapter 的執行設定
type AdapterConfig struct {
	BinaryPath           string
	ExpectedVersion      string
	ConfigDir            string
	Permission           string // JSON string for OPENCODE_PERMISSION
	DisableAuto          bool
	Role                 RuntimeRole
	Agent                string
	Pure                 bool
	DisableProjectConfig bool
	OnProcessStarted     func(ctx context.Context, pgid int)
	OnProcessExited      func(ctx context.Context, pgid int)
}

// Adapter 負責調度固定版本 OpenCode CLI 執行 AI 任務並解析事件串流
type Adapter struct {
	cfg AdapterConfig
}

// ValidateOpenCodeVersion 檢查二進位檔版本是否符合預期
func ValidateOpenCodeVersion(binPath string, expectedVersion string) error {
	out, err := exec.Command(binPath, "--version").Output()
	if err != nil {
		return fmt.Errorf("execute opencode --version: %w", err)
	}
	ver := strings.TrimSpace(string(out))
	if expectedVersion != "" && !strings.HasPrefix(ver, expectedVersion) {
		return fmt.Errorf("unsupported opencode version: expected %s, got %s", expectedVersion, ver)
	}
	return nil
}

// ResolveOpenCodeBinary 解析並驗證 OpenCode 二進位檔路徑與版本
func ResolveOpenCodeBinary(preferredPath string, expectedVersion string) (string, error) {
	if preferredPath != "" {
		if err := ValidateOpenCodeVersion(preferredPath, expectedVersion); err != nil {
			return "", err
		}
		return preferredPath, nil
	}

	// 1. 優先檢查隔離安裝目錄
	home, err := os.UserHomeDir()
	if err == nil {
		isolatedBin := filepath.Join(home, ".cache/asura-probe-issue-2/install/node_modules/.bin/opencode")
		if _, err := os.Stat(isolatedBin); err == nil {
			if vErr := ValidateOpenCodeVersion(isolatedBin, expectedVersion); vErr == nil {
				return isolatedBin, nil
			}
		}
	}

	// 2. 檢查 PATH
	pathBin, err := exec.LookPath("opencode")
	if err == nil {
		vErr := ValidateOpenCodeVersion(pathBin, expectedVersion)
		if vErr == nil {
			return pathBin, nil
		}
		return "", fmt.Errorf("opencode found in PATH but version invalid: %w", vErr)
	}

	return "", fmt.Errorf("%w: valid opencode %s not found in probe path or PATH", ErrNoBinary, expectedVersion)
}

// NewAdapter 建立新的 OpenCode Adapter 實例
func NewAdapter(cfg AdapterConfig) *Adapter {
	if cfg.ExpectedVersion == "" {
		cfg.ExpectedVersion = "1.18.23"
	}
	if cfg.BinaryPath == "" {
		if resolved, err := ResolveOpenCodeBinary("", cfg.ExpectedVersion); err == nil {
			cfg.BinaryPath = resolved
		}
	}
	return &Adapter{cfg: cfg}
}

// ExtractResultFromEvents 從 JSON 事件串流中解析並萃取最終回答文字或錯誤
func (a *Adapter) ExtractResultFromEvents(output string) (string, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "", ErrEmptyResult
	}

	var sb strings.Builder
	var lastErr string
	hasEvents := false

	scanner := bufio.NewScanner(strings.NewReader(trimmed))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		evt, err := ParseEventLine(line)
		if err != nil || evt == nil {
			continue
		}
		hasEvents = true

		if evt.Text != "" {
			if sb.Len() > 0 && !strings.HasSuffix(sb.String(), "\n") {
				sb.WriteString("\n")
			}
			sb.WriteString(evt.Text)
		}

		if evt.IsRateLimit {
			lastErr = "rate limit exceeded"
		}
		if evt.Type == "error" && evt.ErrorMessage != "" {
			lastErr = evt.ErrorMessage
		}
	}

	result := strings.TrimSpace(sb.String())
	// 遵循審查建議：保留 partial output + terminal error，避免先收到 text、後收到 429/error 時被判成成功
	if lastErr != "" {
		return result, fmt.Errorf("backend error: %s", lastErr)
	}

	if result != "" {
		return result, nil
	}

	if !hasEvents {
		// 非 JSON 格式，保留原始標準輸出
		return trimmed, nil
	}

	return "", ErrEmptyResult
}

// Run 執行自然語言任務，解析事件串流並回傳文字結果 (INT-001, INT-007)
// 自然語言 prompt 直接作為參數傳入，不經過 shell 解譯。
func (a *Adapter) Run(ctx context.Context, workspace string, prompt string) (string, error) {
	expectedVer := a.cfg.ExpectedVersion
	if expectedVer == "" {
		expectedVer = "1.18.23"
	}

	binPath, err := ResolveOpenCodeBinary(a.cfg.BinaryPath, expectedVer)
	if err != nil {
		return "", err
	}

	args := []string{"run", "--format", "json", "--dir", workspace}
	if a.cfg.Pure {
		args = append(args, "--pure")
	}
	if a.cfg.Agent != "" {
		args = append(args, "--agent", a.cfg.Agent)
	}
	if !a.cfg.DisableAuto {
		// 依 Phase 1 探測與核准決策：在核准專案內允許自動修改/建置/測試
		args = append(args, "--auto")
	}
	// 自然語言 prompt 作為獨立參數傳遞，絕對不經由 sh -c
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, binPath, args...)
	ConfigureProcessGroup(cmd)

	// 配置環境變數，包含權限控制與隔離設定
	cmd.Env = os.Environ()
	cmd.Env = setOrUpdateEnv(cmd.Env, "OPENCODE_DISABLE_AUTOUPDATE", "1")
	if a.cfg.Pure {
		cmd.Env = setOrUpdateEnv(cmd.Env, "OPENCODE_PURE", "1")
	}
	if a.cfg.DisableProjectConfig {
		cmd.Env = setOrUpdateEnv(cmd.Env, "OPENCODE_DISABLE_PROJECT_CONFIG", "1")
	}
	if a.cfg.ConfigDir != "" {
		opencodeSubdir := filepath.Join(a.cfg.ConfigDir, "opencode")
		homeSubdir := filepath.Join(a.cfg.ConfigDir, "home")
		if _, err := os.Stat(opencodeSubdir); err == nil {
			// 乾淨 Configuration Namespace：
			// 1. XDG_CONFIG_HOME 指向 a.cfg.ConfigDir，使 OpenCode 的 config 路徑解析為 <ConfigDir>/opencode
			// 2. HOME 指向乾淨的空目錄 homeSubdir，阻斷任何 $HOME/.opencode 的全域設定污染
			// 3. OPENCODE_CONFIG_DIR 指向 opencodeSubdir
			// 4. XDG_DATA_HOME 保留使用者真實資料路徑，確保 auth.json 模型憑證可正常讀取
			cmd.Env = setOrUpdateEnv(cmd.Env, "XDG_CONFIG_HOME", a.cfg.ConfigDir)
			cmd.Env = setOrUpdateEnv(cmd.Env, "HOME", homeSubdir)
			cmd.Env = setOrUpdateEnv(cmd.Env, "OPENCODE_CONFIG_DIR", opencodeSubdir)
			realHome, _ := os.UserHomeDir()
			dataHome := os.Getenv("XDG_DATA_HOME")
			if dataHome == "" && realHome != "" {
				dataHome = filepath.Join(realHome, ".local", "share")
			}
			if dataHome != "" {
				cmd.Env = setOrUpdateEnv(cmd.Env, "XDG_DATA_HOME", dataHome)
			}
		} else {
			cmd.Env = setOrUpdateEnv(cmd.Env, "OPENCODE_CONFIG_DIR", a.cfg.ConfigDir)
		}
	}
	if a.cfg.Permission != "" {
		cmd.Env = setOrUpdateEnv(cmd.Env, "OPENCODE_PERMISSION", a.cfg.Permission)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start opencode process: %w", err)
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid
	}

	if a.cfg.OnProcessStarted != nil {
		a.cfg.OnProcessStarted(ctx, pgid)
	}

	// 支援 Context 取消時發送信號並清理進程組
	doneChan := make(chan struct{})
	var closeOnce sync.Once
	defer closeOnce.Do(func() { close(doneChan) })

	go func() {
		select {
		case <-ctx.Done():
			_ = KillProcessGroup(pgid)
		case <-doneChan:
		}
	}()

	waitErr := cmd.Wait()
	closeOnce.Do(func() { close(doneChan) })

	if a.cfg.OnProcessExited != nil {
		a.cfg.OnProcessExited(ctx, pgid)
	}

	rawStdout := stdoutBuf.String()
	extracted, extractErr := a.ExtractResultFromEvents(rawStdout)

	if waitErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return extracted, ctx.Err()
		}
		// 若已有部分抽取文字，保留之，同時回傳錯誤
		if extracted != "" {
			return extracted, fmt.Errorf("%w: %v", ErrExecutionFailed, waitErr)
		}
		errMsg := strings.TrimSpace(stderrBuf.String())
		if errMsg == "" {
			errMsg = waitErr.Error()
		}
		return "", fmt.Errorf("%w: %s", ErrExecutionFailed, errMsg)
	}

	if extractErr != nil {
		// 若抽取失敗或包含後端錯誤，保留部分抽取文字或原始輸出，但必須回傳錯誤
		if extracted != "" {
			return extracted, extractErr
		}
		if rawStdout != "" {
			return rawStdout, extractErr
		}
		return "", extractErr
	}

	return extracted, nil
}

// setOrUpdateEnv 更新或追加環境變數切片中的鍵值，避免重複設定
func setOrUpdateEnv(env []string, key, val string) []string {
	prefix := key + "="
	entry := prefix + val
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}
