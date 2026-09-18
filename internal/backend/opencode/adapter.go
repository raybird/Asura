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

	// 若使用者全域環境存在 opencode.jsonc / opencode.json / config.json，安全提取 provider 與 model 設定以確保模型調用能力，
	// 但嚴格過濾、排除任何 agent、permission、mcp、plugin 污染，並杜絕帶有 npm package 之 custom executable provider
	globalCfgPath := findGlobalOpenCodeConfigPath()
	if globalCfgPath != "" {
		if rawGlobal, err := os.ReadFile(globalCfgPath); err == nil {
			cleanJSON := stripJSONC(rawGlobal)
			var globalData map[string]any
			if err := json.Unmarshal(cleanJSON, &globalData); err == nil {
				var baseData map[string]any
				if err := json.Unmarshal([]byte(AsuraReadOnlyConfigJSON), &baseData); err == nil {
					var selectedModel string
					if m, ok := globalData["model"].(string); ok {
						selectedModel = m
						baseData["model"] = m
					}

					if p, ok := globalData["provider"]; ok {
						cleanProv, err := sanitizeProviderConfig(p, selectedModel)
						if err != nil {
							_ = os.RemoveAll(dir)
							return "", nil, fmt.Errorf("sanitize provider config: %w", err)
						}
						if len(cleanProv) > 0 {
							baseData["provider"] = cleanProv
						}
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

// HasLiveCredentials 檢查當前執行環境是否已配置真實模型連線憑證（環境變數或 auth.json）
func HasLiveCredentials() bool {
	if os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("GEMINI_API_KEY") != "" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	authPath := filepath.Join(dataHome, "opencode", "auth.json")
	if fi, err := os.Stat(authPath); err == nil && !fi.IsDir() {
		return true
	}
	return false
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

	// 配置環境變數，落實權限控制、Configuration Namespace 隔離並徹底消除外部配置注入
	cmd.Env = buildExecutionEnv(a.cfg)

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

// DisallowedReadOnlyEnvKeys 定義在唯讀/隔離執行時嚴格禁止繼承的環境變數鍵值（防止外部配置與權限注入）
var DisallowedReadOnlyEnvKeys = []string{
	"OPENCODE_CONFIG",
	"OPENCODE_CONFIG_CONTENT",
}

// removeEnv 從環境變數切片中移除特定鍵（不區分大小寫匹配 key= 前綴）
func removeEnv(env []string, keys ...string) []string {
	if len(keys) == 0 {
		return env
	}
	keyMap := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keyMap[strings.ToUpper(k)] = struct{}{}
	}

	result := make([]string, 0, len(env))
	for _, entry := range env {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) > 0 {
			k := strings.ToUpper(parts[0])
			if _, drop := keyMap[k]; drop {
				continue
			}
		}
		result = append(result, entry)
	}
	return result
}

// buildExecutionEnv 根據 AdapterConfig 構建乾淨、無未授權配置注入的子進程環境變數
func buildExecutionEnv(cfg AdapterConfig) []string {
	env := os.Environ()

	// 1. 強制清除任何外部配置注入標記，杜絕 OPENCODE_CONFIG 與 OPENCODE_CONFIG_CONTENT 越權
	env = removeEnv(env, DisallowedReadOnlyEnvKeys...)
	env = removeEnv(env, "OPENCODE_PERMISSION")

	// 2. 若指定了隔離配置目錄，重設 XDG 與 HOME 空間，切斷未受管配置
	if cfg.ConfigDir != "" {
		opencodeSubdir := filepath.Join(cfg.ConfigDir, "opencode")
		homeSubdir := filepath.Join(cfg.ConfigDir, "home")
		if _, err := os.Stat(opencodeSubdir); err == nil {
			env = setOrUpdateEnv(env, "XDG_CONFIG_HOME", cfg.ConfigDir)
			env = setOrUpdateEnv(env, "HOME", homeSubdir)
			env = setOrUpdateEnv(env, "OPENCODE_CONFIG_DIR", opencodeSubdir)
			realHome, _ := os.UserHomeDir()
			dataHome := os.Getenv("XDG_DATA_HOME")
			if dataHome == "" && realHome != "" {
				dataHome = filepath.Join(realHome, ".local", "share")
			}
			if dataHome != "" {
				env = setOrUpdateEnv(env, "XDG_DATA_HOME", dataHome)
			}
		} else {
			env = setOrUpdateEnv(env, "OPENCODE_CONFIG_DIR", cfg.ConfigDir)
		}
	}

	// 3. 設定核心保護標記
	env = setOrUpdateEnv(env, "OPENCODE_DISABLE_AUTOUPDATE", "1")
	if cfg.Pure {
		env = setOrUpdateEnv(env, "OPENCODE_PURE", "1")
	}
	if cfg.DisableProjectConfig {
		env = setOrUpdateEnv(env, "OPENCODE_DISABLE_PROJECT_CONFIG", "1")
	}
	if cfg.Permission != "" {
		env = setOrUpdateEnv(env, "OPENCODE_PERMISSION", cfg.Permission)
	}

	// 4. 最後防線：再次過濾禁止繼承的環境變數，確保絕對不殘留
	return removeEnv(env, DisallowedReadOnlyEnvKeys...)
}

// findGlobalOpenCodeConfigPath 搜尋全域配置路徑，優先順序：opencode.jsonc -> opencode.json -> config.json
func findGlobalOpenCodeConfigPath() string {
	userHome, _ := os.UserHomeDir()
	xdgConfig := os.Getenv("XDG_CONFIG_HOME")

	var configDirs []string
	if xdgConfig != "" {
		configDirs = append(configDirs, filepath.Join(xdgConfig, "opencode"))
	}
	if userHome != "" {
		configDirs = append(configDirs, filepath.Join(userHome, ".config", "opencode"))
	}

	fileNames := []string{"opencode.jsonc", "opencode.json", "config.json"}

	for _, dir := range configDirs {
		for _, fn := range fileNames {
			p := filepath.Join(dir, fn)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p
			}
		}
	}
	return ""
}

// stripJSONC 清除 JSONC 註解（// ... 與 /* ... */）及尾隨逗號（trailing comma before } or ]），
// 同時保留字串內部引號、跳脫符號與 URL 中的雙斜線
func stripJSONC(src []byte) []byte {
	var out bytes.Buffer
	inString := false
	escaped := false
	i := 0
	n := len(src)

	for i < n {
		c := src[i]

		if inString {
			out.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			i++
			continue
		}

		if c == '"' {
			inString = true
			escaped = false
			out.WriteByte(c)
			i++
			continue
		}

		// 處理單行註解 //
		if c == '/' && i+1 < n && src[i+1] == '/' {
			i += 2
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}

		// 處理區塊註解 /* ... */
		if c == '/' && i+1 < n && src[i+1] == '*' {
			i += 2
			for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			if i+1 < n {
				i += 2
			}
			continue
		}

		// 處理尾隨逗號 (trailing comma before } or ])
		if c == ',' {
			j := i + 1
			isTrailing := false
			for j < n {
				b := src[j]
				if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
					j++
					continue
				}
				if b == '/' && j+1 < n && src[j+1] == '/' {
					j += 2
					for j < n && src[j] != '\n' {
						j++
					}
					continue
				}
				if b == '/' && j+1 < n && src[j+1] == '*' {
					j += 2
					for j+1 < n && !(src[j] == '*' && src[j+1] == '/') {
						j++
					}
					if j+1 < n {
						j += 2
					}
					continue
				}
				if b == '}' || b == ']' {
					isTrailing = true
				}
				break
			}
			if isTrailing {
				i++
				continue
			}
		}

		out.WriteByte(c)
		i++
	}

	return out.Bytes()
}

// hasNPMField 遞迴檢查資料結構是否包含任意 "npm" 欄位宣告（表示外部可執行 package）
func hasNPMField(v any) bool {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			if strings.EqualFold(k, "npm") {
				return true
			}
			if hasNPMField(child) {
				return true
			}
		}
	case []any:
		for _, item := range val {
			if hasNPMField(item) {
				return true
			}
		}
	}
	return false
}

// isModelUsingProvider 檢查指定模型是否依賴特定 provider 名稱
func isModelUsingProvider(selectedModel, providerName string) bool {
	if selectedModel == "" || providerName == "" {
		return false
	}
	if selectedModel == providerName {
		return true
	}
	return strings.HasPrefix(selectedModel, providerName+"/")
}

// sanitizeProviderConfig 檢查並清洗 provider 設定：
// 1. 若 provider 含有 "npm"，代表為 custom executable provider。
// 2. 若 selectedModel 依賴該 executable provider，遵循 Fail-Closed 原則，拒絕啟動並回傳錯誤。
// 3. 若 selectedModel 未使用該 provider，則從配置中排除，不複製進唯讀隔離環境。
// 4. 確保複製進唯讀配置之 provider 僅為純資料屬性，杜絕任意代碼執行風險。
func sanitizeProviderConfig(rawProvider any, selectedModel string) (map[string]any, error) {
	if rawProvider == nil {
		return nil, nil
	}
	provMap, ok := rawProvider.(map[string]any)
	if !ok {
		return nil, nil
	}

	cleanProviders := make(map[string]any)

	for name, v := range provMap {
		if hasNPMField(v) {
			if isModelUsingProvider(selectedModel, name) {
				return nil, fmt.Errorf("custom executable provider %q with npm package is not allowed in read-only runtime", name)
			}
			// 未使用的 custom executable provider，不複製進隔離配置
			continue
		}
		cleanProviders[name] = v
	}

	return cleanProviders, nil
}

