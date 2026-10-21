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
	"sync/atomic"
	"syscall"
	"time"

	"asura/internal/backend"
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

// BuiltinVersion 是開發時實測過、免驗證即可使用的 OpenCode 版本（2026-10-21 issue-73 起精確比對）
const BuiltinVersion = "1.18.23"

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

// CreateIsolatedReadOnlyConfigDir 建立包含 asura-readonly agent 的獨立配置目錄、乾淨 home 隔離空間與消毒憑證目錄
// 目錄結構包含：
//   - <dir>/opencode/opencode.json (只定義 asura-readonly primary agent，並安全繼承全域 provider 與 model)
//   - <dir>/home (乾淨空目錄，用於隔離 HOME/.opencode)
//   - <dir>/data/opencode/auth.json (消毒後之憑證複本，保留合法 API/OAuth 金鑰，徹底排除可能注入遠端配置之 wellknown 型別)
//
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
	dataOpencodeDir := filepath.Join(dir, "data", "opencode")
	if err := os.MkdirAll(dataOpencodeDir, 0700); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("create isolated data/opencode dir: %w", err)
	}
	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("create isolated cache dir: %w", err)
	}
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("create isolated state dir: %w", err)
	}

	// 1. 消毒並複製使用者真實 auth.json：保留 api/oauth 憑證，徹底剔除 wellknown 遠端注入風險
	realAuthPath := findRealAuthJSONPath()
	if realAuthPath != "" {
		rawAuthBytes, err := os.ReadFile(realAuthPath)
		if err != nil {
			_ = os.RemoveAll(dir)
			return "", nil, fmt.Errorf("read real auth.json: %w", err)
		}
		var rawAuth map[string]any
		if err := json.Unmarshal(stripJSONC(rawAuthBytes), &rawAuth); err != nil {
			_ = os.RemoveAll(dir)
			return "", nil, fmt.Errorf("parse real auth.json: %w", err)
		}
		cleanAuth := sanitizeAuthData(rawAuth)
		cleanAuthBytes, err := json.MarshalIndent(cleanAuth, "", "  ")
		if err != nil {
			_ = os.RemoveAll(dir)
			return "", nil, fmt.Errorf("marshal sanitized auth.json: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dataOpencodeDir, "auth.json"), cleanAuthBytes, 0600); err != nil {
			_ = os.RemoveAll(dir)
			return "", nil, fmt.Errorf("write isolated auth.json: %w", err)
		}
	}

	// 2. 預設純 Asura 唯讀配置
	configContent := []byte(AsuraReadOnlyConfigJSON)

	// 3. 依 OpenCode 順序載入並合併全域配置 (config.json -> opencode.json -> opencode.jsonc)
	globalData := loadGlobalOpenCodeConfig()
	var baseData map[string]any
	if err := json.Unmarshal([]byte(AsuraReadOnlyConfigJSON), &baseData); err == nil {
		var selectedModel string
		if len(globalData) > 0 {
			if m, ok := globalData["model"].(string); ok {
				if hasUnsafeDynamicRef(m) {
					_ = os.RemoveAll(dir)
					return "", nil, fmt.Errorf("model %q contains forbidden dynamic substitution ({file:...} or {env:...})", m)
				}
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
	BeforeStart          func(context.Context) (func(), error)
	BinaryPath           string
	ExpectedVersion      string
	VerificationPath     string
	ConfigDir            string
	Permission           string // JSON string for OPENCODE_PERMISSION
	DisableAuto          bool
	Role                 RuntimeRole
	Agent                string
	Pure                 bool
	DisableProjectConfig bool
	Isolated             bool // 標記此 Adapter 是否為受隔離的唯讀沙盒環境
	OnProcessStarted     func(ctx context.Context, pgid int)
	OnProcessExited      func(ctx context.Context, pgid int)
}

// Adapter 負責調度固定版本 OpenCode CLI 執行 AI 任務並解析事件串流
type Adapter struct {
	cfg AdapterConfig
	// probedExecutable 記錄最近一次 Probe 實際執行的 binary，供 doctor 顯示。
	probedExecutable atomic.Pointer[string]
}

// ValidateOpenCodeVersion 檢查二進位檔版本是否符合預期
func ValidateOpenCodeVersion(binPath string, expectedVersion string) error {
	return validateOpenCodeVersion(context.Background(), binPath, expectedVersion, "")
}

// 2026-10-03：接受前版本預檢沿用 caller deadline，保留既有公開 helper 契約。
// 2026-10-21：issue-73，expectedVersion 為內建版本（精確比對），其他版本需 recordsPath 內的驗證紀錄。
func validateOpenCodeVersion(ctx context.Context, binPath string, expectedVersion string, recordsPath string) error {
	out, err := exec.CommandContext(ctx, binPath, "--version").Output()
	if err != nil {
		return fmt.Errorf("execute opencode --version: %w", err)
	}
	ver := strings.TrimSpace(string(out))
	if err := admitOpenCode(ver, binPath, expectedVersion, recordsPath); err != nil {
		return fmt.Errorf("unsupported opencode version %s: %w", ver, err)
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
	return resolveOpenCodeBinary(context.Background(), preferredPath, expectedVersion, "")
}

func resolveOpenCodeBinary(ctx context.Context, preferredPath string, expectedVersion string, recordsPath string) (string, error) {
	if preferredPath != "" {
		if err := validateOpenCodeVersion(ctx, preferredPath, expectedVersion, recordsPath); err != nil {
			return "", err
		}
		return preferredPath, nil
	}

	// 1. 優先檢查隔離安裝目錄
	home, err := os.UserHomeDir()
	if err == nil {
		isolatedBin := filepath.Join(home, ".cache/asura-probe-issue-2/install/node_modules/.bin/opencode")
		if _, err := os.Stat(isolatedBin); err == nil {
			if vErr := validateOpenCodeVersion(ctx, isolatedBin, expectedVersion, recordsPath); vErr == nil {
				return isolatedBin, nil
			}
		}
	}

	// 2. 檢查 PATH
	pathBin, err := exec.LookPath("opencode")
	if err == nil {
		vErr := validateOpenCodeVersion(ctx, pathBin, expectedVersion, recordsPath)
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
		cfg.ExpectedVersion = BuiltinVersion
	}
	return &Adapter{cfg: cfg}
}

// maxScanCapacity 限制單行 JSONL 之掃描容量（4MB 支援大型代碼/工具事件串流輸出，比照 Codex parser）
const maxScanCapacity = 4 * 1024 * 1024

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
	scanner.Buffer(make([]byte, 64*1024), maxScanCapacity)
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

	if scanErr := scanner.Err(); scanErr != nil && lastErr == "" {
		lastErr = fmt.Sprintf("stdout scanner error: %v", scanErr)
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

// ID 回傳後端唯一標識
func (a *Adapter) ID() string {
	return "opencode"
}

// Version 回傳目前 OpenCode 版本
func (a *Adapter) Version(ctx context.Context) (string, error) {
	expectedVer := a.cfg.ExpectedVersion
	if expectedVer == "" {
		expectedVer = BuiltinVersion
	}
	binPath, err := resolveOpenCodeBinary(ctx, a.cfg.BinaryPath, expectedVer, a.cfg.VerificationPath)
	if err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, binPath, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("execute opencode --version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Capabilities 回傳 OpenCode 支援的能力清單
func (a *Adapter) Capabilities() []backend.Capability {
	return []backend.Capability{
		backend.CapReadOnlySandbox,
		backend.CapWorkspaceWrite,
		backend.CapStructuredEvents,
		backend.CapProcessCancellation,
	}
}

// HasCapability 檢查後端是否具備指定能力
func (a *Adapter) HasCapability(cap backend.Capability) bool {
	for _, c := range a.Capabilities() {
		if c == cap {
			return true
		}
	}
	return false
}

// Run 實作 backend.Backend 介面，執行任務並回傳結構化結果與標準化事件
func (a *Adapter) Run(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	if req == nil {
		return nil, errors.New("execution request cannot be nil")
	}

	startedAt := time.Now()
	cfg := a.cfg
	if cfg.ExpectedVersion == "" {
		cfg.ExpectedVersion = BuiltinVersion
	}

	// 依角色配置安全邊界：Oracle 與 Critic 必須啟用唯讀隔離沙盒 (MBACK-008)
	var cleanupReadOnly func()
	if req.Role == backend.RoleOracle || req.Role == backend.RoleCritic {
		if cfg.Role != RoleOracle && cfg.Role != RoleCritic {
			cfg.Role = RoleOracle
		}
		if cfg.ConfigDir == "" && !cfg.Isolated {
			dir, cleanup, err := CreateIsolatedReadOnlyConfigDir()
			if err != nil {
				return nil, fmt.Errorf("create isolated config for role %s: %w", req.Role, err)
			}
			cfg.ConfigDir = dir
			cfg.Isolated = true
			cleanupReadOnly = cleanup
		}
	} else if req.Role == backend.RoleStriker {
		cfg.Role = RoleStriker
	}

	if cleanupReadOnly != nil {
		defer cleanupReadOnly()
	}

	binPath, err := resolveOpenCodeBinary(ctx, cfg.BinaryPath, cfg.ExpectedVersion, cfg.VerificationPath)
	if err != nil {
		return &backend.ExecutionResult{
			BackendID:    "opencode",
			FailureClass: backend.FailureUnavailable,
			StartedAt:    startedAt,
			CompletedAt:  time.Now(),
		}, backend.NewExecutionError("opencode", backend.FailureUnavailable, err, "")
	}

	args := []string{"run", "--format", "json", "--dir", req.Workspace}
	if cfg.Pure {
		args = append(args, "--pure")
	}
	if cfg.Agent != "" {
		args = append(args, "--agent", cfg.Agent)
	}
	if !cfg.DisableAuto {
		args = append(args, "--auto")
	}
	args = append(args, req.Prompt)

	cmd := exec.CommandContext(ctx, binPath, args...)
	ConfigureProcessGroup(cmd)
	cmd.Env = buildExecutionEnv(cfg)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// 2026-10-04：完成版本/準備後，在真正啟動前握手取消與進程身分鎖。
	var releaseStart func()
	if cfg.BeforeStart != nil {
		var startErr error
		releaseStart, startErr = cfg.BeforeStart(ctx)
		if startErr != nil {
			return nil, startErr
		}
	}
	release := func() {
		if releaseStart != nil {
			releaseStart()
			releaseStart = nil
		}
	}
	defer release()
	if err := cmd.Start(); err != nil {
		return &backend.ExecutionResult{
			BackendID:    "opencode",
			FailureClass: backend.FailureExecution,
			StartedAt:    startedAt,
			CompletedAt:  time.Now(),
		}, backend.NewExecutionError("opencode", backend.FailureExecution, fmt.Errorf("start process: %w", err), "")
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid
	}

	if cfg.OnProcessStarted != nil {
		cfg.OnProcessStarted(ctx, pgid)
	}
	release()

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

	if cfg.OnProcessExited != nil {
		cfg.OnProcessExited(ctx, pgid)
	}

	rawStdout := stdoutBuf.String()
	rawStderr := stderrBuf.String()
	completedAt := time.Now()

	// 解析事件與萃取文字 (MBACK-006, MBACK-011)
	events := make([]backend.Event, 0)
	var sb strings.Builder
	var lastErr string
	type tokenFieldAcc struct {
		sum          int64
		seenCount    int
		missingCount int
	}
	var (
		inputAcc        tokenFieldAcc
		outputAcc       tokenFieldAcc
		totalAcc        tokenFieldAcc
		cachedAcc       tokenFieldAcc
		reasoningAcc    tokenFieldAcc
		stepTokensCount = 0
	)
	hasRateLimit := false
	hasAuthError := false
	hasEvents := false

	scanner := bufio.NewScanner(strings.NewReader(rawStdout))
	scanner.Buffer(make([]byte, 64*1024), maxScanCapacity)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		evt, pErr := ParseEventLine(line)
		if pErr != nil || evt == nil {
			continue
		}
		hasEvents = true

		if evt.Text != "" {
			if sb.Len() > 0 && !strings.HasSuffix(sb.String(), "\n") {
				sb.WriteString("\n")
			}
			sb.WriteString(evt.Text)
			events = append(events, backend.NewTextEvent(evt.Text))
		}
		if evt.Tool != "" {
			events = append(events, backend.NewToolEvent(evt.Tool, evt.ToolStatus))
		}
		if evt.Tokens != nil {
			stepTokensCount++

			if evt.Tokens.Input != nil {
				inputAcc.seenCount++
				inputAcc.sum += *evt.Tokens.Input
			} else {
				inputAcc.missingCount++
			}

			if evt.Tokens.Output != nil {
				outputAcc.seenCount++
				outputAcc.sum += *evt.Tokens.Output
			} else {
				outputAcc.missingCount++
			}

			if evt.Tokens.Total != nil {
				totalAcc.seenCount++
				totalAcc.sum += *evt.Tokens.Total
			} else {
				totalAcc.missingCount++
			}

			if evt.Tokens.Reasoning != nil {
				reasoningAcc.seenCount++
				reasoningAcc.sum += *evt.Tokens.Reasoning
			} else {
				reasoningAcc.missingCount++
			}

			if evt.Tokens.Cache != nil && evt.Tokens.Cache.Read != nil {
				cachedAcc.seenCount++
				cachedAcc.sum += *evt.Tokens.Cache.Read
			} else {
				cachedAcc.missingCount++
			}
		}
		if evt.IsRateLimit {
			hasRateLimit = true
			lastErr = "rate limit exceeded"
			events = append(events, backend.Event{
				Type:      backend.EventTypeRateLimit,
				Error:     "rate limit exceeded",
				Timestamp: time.Now(),
			})
		}
		if evt.Type == "error" && evt.ErrorMessage != "" {
			lastErr = evt.ErrorMessage
			events = append(events, backend.NewErrorEvent(evt.ErrorMessage))
			lowerErr := strings.ToLower(evt.ErrorMessage)
			if strings.Contains(lowerErr, "unauthorized") || strings.Contains(lowerErr, "auth") || strings.Contains(lowerErr, "api key") {
				hasAuthError = true
			}
		}
	}

	if scanErr := scanner.Err(); scanErr != nil && lastErr == "" {
		lastErr = fmt.Sprintf("stdout scanner error: %v", scanErr)
	}

	extractedText := strings.TrimSpace(sb.String())
	if !hasEvents && extractedText == "" {
		extractedText = strings.TrimSpace(rawStdout)
	}

	var usage *backend.TokenUsage
	usageSource := backend.ProvenanceUnknown
	if stepTokensCount > 0 {
		usage = &backend.TokenUsage{}
		usageSource = backend.ProvenanceExact

		if inputAcc.seenCount > 0 && inputAcc.missingCount == 0 {
			usage.InputTokens = backend.Int64Ptr(inputAcc.sum)
		}
		if outputAcc.seenCount > 0 && outputAcc.missingCount == 0 {
			usage.OutputTokens = backend.Int64Ptr(outputAcc.sum)
		}
		if totalAcc.seenCount > 0 && totalAcc.missingCount == 0 {
			usage.TotalTokens = backend.Int64Ptr(totalAcc.sum)
		}
		if cachedAcc.seenCount > 0 && cachedAcc.missingCount == 0 {
			usage.CachedTokens = backend.Int64Ptr(cachedAcc.sum)
		}
		if reasoningAcc.seenCount > 0 && reasoningAcc.missingCount == 0 {
			usage.ReasoningTokens = backend.Int64Ptr(reasoningAcc.sum)
		}
	}

	ver, _ := a.Version(ctx)
	result := &backend.ExecutionResult{
		BackendID:      "opencode",
		BackendVersion: ver,
		Output:         extractedText,
		PartialOutput:  extractedText,
		Events:         events,
		FailureClass:   backend.FailureNone,
		Usage:          usage,
		UsageSource:    usageSource,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
	}

	// 判定錯誤分類
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.FailureClass = backend.FailureCancelled
		return result, backend.NewExecutionError("opencode", backend.FailureCancelled, ctx.Err(), extractedText)
	}

	if hasRateLimit {
		result.FailureClass = backend.FailureRateLimit
		return result, backend.NewExecutionError("opencode", backend.FailureRateLimit, errors.New("backend error: rate limit exceeded"), extractedText)
	}

	if hasAuthError {
		result.FailureClass = backend.FailureAuth
		return result, backend.NewExecutionError("opencode", backend.FailureAuth, errors.New("backend error: authentication failed"), extractedText)
	}

	if waitErr != nil {
		result.FailureClass = backend.FailureExecution
		errMsg := strings.TrimSpace(rawStderr)
		if errMsg == "" {
			errMsg = waitErr.Error()
		}
		return result, backend.NewExecutionError("opencode", backend.FailureExecution, fmt.Errorf("%w: %s", ErrExecutionFailed, errMsg), extractedText)
	}

	if lastErr != "" {
		result.FailureClass = backend.FailureExecution
		return result, backend.NewExecutionError("opencode", backend.FailureExecution, fmt.Errorf("backend error: %s", lastErr), extractedText)
	}

	if strings.TrimSpace(extractedText) == "" {
		result.FailureClass = backend.FailureEmptyResult
		return result, backend.NewExecutionError("opencode", backend.FailureEmptyResult, ErrEmptyResult, "")
	}

	return result, nil
}

// RunPrompt 保持相容舊版 (ctx, workspace, prompt) 呼叫簽名
func (a *Adapter) RunPrompt(ctx context.Context, workspace string, prompt string) (string, error) {
	req := &backend.ExecutionRequest{
		Workspace: workspace,
		Prompt:    prompt,
	}
	res, err := a.Run(ctx, req)
	if err != nil {
		if res != nil && res.PartialOutput != "" {
			return res.PartialOutput, err
		}
		return "", err
	}
	return res.Output, nil
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

// findRealAuthJSONPath 尋找本機既有真實之 auth.json 路徑
func findRealAuthJSONPath() string {
	realHome, _ := os.UserHomeDir()
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" && realHome != "" {
		dataHome = filepath.Join(realHome, ".local", "share")
	}
	if dataHome != "" {
		p := filepath.Join(dataHome, "opencode", "auth.json")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// sanitizeAuthData 過濾 auth 設定，保留合法憑證（api, oauth 等），徹底剔除帶有 "wellknown" 遠端配置注入風險之項目
func sanitizeAuthData(rawAuth map[string]any) map[string]any {
	cleanAuth := make(map[string]any)
	for key, val := range rawAuth {
		valMap, ok := val.(map[string]any)
		if !ok {
			cleanAuth[key] = val
			continue
		}
		if t, ok := valMap["type"].(string); ok && strings.EqualFold(t, "wellknown") {
			continue
		}
		if hasWellknownField(valMap) {
			continue
		}
		cleanAuth[key] = val
	}
	return cleanAuth
}

// hasWellknownField 遞迴檢查是否包含任何 wellknown 型別或欄位
func hasWellknownField(v any) bool {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			if strings.EqualFold(k, "wellknown") {
				return true
			}
			if strings.EqualFold(k, "type") && strings.EqualFold(fmt.Sprintf("%v", child), "wellknown") {
				return true
			}
			if hasWellknownField(child) {
				return true
			}
		}
	case []any:
		for _, item := range val {
			if hasWellknownField(item) {
				return true
			}
		}
	}
	return false
}

// deepMergeMaps 遞迴合併兩個 map，以 src 之鍵值覆寫 dst
func deepMergeMaps(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = make(map[string]any)
	}
	for k, sv := range src {
		if dv, ok := dst[k]; ok {
			dvMap, dvOk := dv.(map[string]any)
			svMap, svOk := sv.(map[string]any)
			if dvOk && svOk {
				dst[k] = deepMergeMaps(dvMap, svMap)
				continue
			}
		}
		dst[k] = sv
	}
	return dst
}

// loadGlobalOpenCodeConfig 依 OpenCode 順序載入並合併全域配置：config.json -> opencode.json -> opencode.jsonc
// 依據 XDG Base Directory 規範，若 XDG_CONFIG_HOME 存在則僅自該目錄讀取，不得回退或併入 ~/.config/opencode
func loadGlobalOpenCodeConfig() map[string]any {
	userHome, _ := os.UserHomeDir()
	xdgConfig := os.Getenv("XDG_CONFIG_HOME")

	var configDir string
	if xdgConfig != "" {
		configDir = filepath.Join(xdgConfig, "opencode")
	} else if userHome != "" {
		configDir = filepath.Join(userHome, ".config", "opencode")
	}
	if configDir == "" {
		return make(map[string]any)
	}

	merged := make(map[string]any)
	fileNames := []string{"config.json", "opencode.json", "opencode.jsonc"}

	for _, fn := range fileNames {
		p := filepath.Join(configDir, fn)
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		cleanJSON := stripJSONC(raw)
		var data map[string]any
		if err := json.Unmarshal(cleanJSON, &data); err == nil {
			merged = deepMergeMaps(merged, data)
		}
	}
	return merged
}

// DisallowedReadOnlyEnvKeys 定義在唯讀/隔離執行時嚴格禁止繼承的環境變數鍵值（相容舊有輔助參考）
var DisallowedReadOnlyEnvKeys = []string{
	"OPENCODE_CONFIG",
	"OPENCODE_CONFIG_CONTENT",
	"OPENCODE_AUTH_CONTENT",
	"OPENCODE_MODELS_PATH",
	"OPENCODE_MODELS_URL",
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

// isReadOnlyRole 判斷角色是否為唯讀角色（Oracle, Critic）
func isReadOnlyRole(role RuntimeRole) bool {
	return role == RoleOracle || role == RoleCritic
}

// isIsolatedRuntime 判定 Adapter 是否運行於隔離沙盒模式
func isIsolatedRuntime(cfg AdapterConfig) bool {
	return cfg.Isolated || isReadOnlyRole(cfg.Role)
}

// buildExecutionEnv 依據執行模式構建子進程環境變數：
// - 唯讀/隔離沙盒（Oracle/Critic）：徹底切斷外部 OPENCODE_* 變數、重設 isolated data/home/config 路徑空間，杜絕配置注入與權限逃逸
// - 一般/核准模式（Striker/Direct）：完整保留既有 OpenCode 環境變數（包含使用者的 OPENCODE_CONFIG / OPENCODE_CONFIG_CONTENT），維持既有語意
func buildExecutionEnv(cfg AdapterConfig) []string {
	if isIsolatedRuntime(cfg) {
		return buildReadOnlyExecutionEnv(cfg)
	}
	return buildStandardExecutionEnv(cfg)
}

// buildStandardExecutionEnv 構建 Striker / Direct 模式之環境變數，維持向後相容
func buildStandardExecutionEnv(cfg AdapterConfig) []string {
	env := os.Environ()
	if cfg.ConfigDir != "" {
		env = setOrUpdateEnv(env, "OPENCODE_CONFIG_DIR", cfg.ConfigDir)
	}
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
	return env
}

// buildReadOnlyExecutionEnv 構建 Oracle / Critic 之隔離沙盒環境變數
func buildReadOnlyExecutionEnv(cfg AdapterConfig) []string {
	rawEnv := os.Environ()
	env := make([]string, 0, len(rawEnv))

	// 1. 白名單式安全過濾：徹底剔除所有外部 OPENCODE_* 變數，杜絕任何未經受管的配置/目錄/模型/憑證注入通道
	//    同時過濾外部 XDG_CONFIG_HOME、HOME、XDG_DATA_HOME、XDG_CACHE_HOME、XDG_STATE_HOME
	for _, entry := range rawEnv {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 0 {
			continue
		}
		k := strings.ToUpper(parts[0])
		if strings.HasPrefix(k, "OPENCODE_") {
			continue // 徹底封鎖外部未受管的 OPENCODE_* 注入（如 OPENCODE_CONFIG, OPENCODE_AUTH_CONTENT, OPENCODE_MODELS_PATH 等）
		}
		if k == "XDG_CONFIG_HOME" || k == "HOME" || k == "XDG_DATA_HOME" || k == "XDG_CACHE_HOME" || k == "XDG_STATE_HOME" {
			continue
		}
		env = append(env, entry)
	}

	// 2. 注入 Asura 隔離沙盒專屬路徑空間
	if cfg.ConfigDir != "" {
		opencodeSubdir := filepath.Join(cfg.ConfigDir, "opencode")
		homeSubdir := filepath.Join(cfg.ConfigDir, "home")
		dataSubdir := filepath.Join(cfg.ConfigDir, "data")
		cacheSubdir := filepath.Join(cfg.ConfigDir, "cache")
		stateSubdir := filepath.Join(cfg.ConfigDir, "state")

		env = setOrUpdateEnv(env, "XDG_CONFIG_HOME", cfg.ConfigDir)
		env = setOrUpdateEnv(env, "HOME", homeSubdir)
		env = setOrUpdateEnv(env, "OPENCODE_CONFIG_DIR", opencodeSubdir)
		if _, err := os.Stat(dataSubdir); err == nil {
			env = setOrUpdateEnv(env, "XDG_DATA_HOME", dataSubdir)
		}
		if _, err := os.Stat(cacheSubdir); err == nil {
			env = setOrUpdateEnv(env, "XDG_CACHE_HOME", cacheSubdir)
		}
		if _, err := os.Stat(stateSubdir); err == nil {
			env = setOrUpdateEnv(env, "XDG_STATE_HOME", stateSubdir)
		}
	}

	// 3. 注入 Asura 明確受控之核心保護標記
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

	return env
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

// hasUnsafeDynamicRef 遞迴檢查資料結構中是否包含 OpenCode 動態代換語法（"{file:" 或 "{env:"）
func hasUnsafeDynamicRef(v any) bool {
	switch val := v.(type) {
	case string:
		if strings.Contains(val, "{file:") || strings.Contains(val, "{env:") {
			return true
		}
	case map[string]any:
		for _, child := range val {
			if hasUnsafeDynamicRef(child) {
				return true
			}
		}
	case []any:
		for _, item := range val {
			if hasUnsafeDynamicRef(item) {
				return true
			}
		}
	}
	return false
}

// sanitizeProviderConfig 檢查並清洗 provider 設定：
// 1. 若 provider 含有動態代換語法 "{file:" 或 "{env:"，遵循 Fail-Closed 原則，拒絕啟動並回傳錯誤。
// 2. 若 provider 含有 "npm"，代表為 custom executable provider。
// 3. 若 selectedModel 依賴該 executable provider，遵循 Fail-Closed 原則，拒絕啟動並回傳錯誤。
// 4. 若 selectedModel 未使用該 provider，則從配置中排除，不複製進唯讀隔離環境。
// 5. 確保複製進唯讀配置之 provider 僅為純資料屬性，杜絕任意代碼執行、外部檔案探測與機密環境變數洩漏風險。
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
		if hasUnsafeDynamicRef(v) {
			return nil, fmt.Errorf("provider %q contains forbidden dynamic substitution ({file:...} or {env:...})", name)
		}
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
