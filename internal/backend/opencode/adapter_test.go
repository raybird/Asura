package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestINT001Unit 驗證 Adapter 對 OpenCode 輸出之事件串流解析與文字抽取能力 (INT-001 內迴圈)
func TestINT001Unit(t *testing.T) {
	// 1. 驗證事件解析與抽取邏輯
	sampleOutput := `{"type":"step_start","timestamp":1789214963023,"sessionID":"ses_test","part":{"type":"step-start"}}
{"type":"text","timestamp":1789214963337,"sessionID":"ses_test","part":{"type":"text","text":"OpenCode 契約探測成功"}}
{"type":"step_finish","timestamp":1789214963461,"sessionID":"ses_test","part":{"reason":"stop","type":"step-finish","tokens":{"total":100}}}`

	adapter := NewAdapter(AdapterConfig{})
	res, err := adapter.ExtractResultFromEvents(sampleOutput)
	if err != nil {
		t.Fatalf("ExtractResultFromEvents failed: %v", err)
	}
	if !strings.Contains(res, "OpenCode 契約探測成功") {
		t.Errorf("expected extracted text to contain 'OpenCode 契約探測成功', got %q", res)
	}

	// 2. 驗證 Adapter.Run 執行
	if !HasLiveCredentials() {
		t.Skip("skipping live adapter.Run test: no model credentials configured in this environment")
	}
	ctx := context.Background()
	out, err := adapter.Run(ctx, "/tmp", "test prompt")
	if err != nil {
		t.Fatalf("adapter.Run unexpected error: %v", err)
	}
	if out == "" {
		t.Errorf("expected non-empty output from adapter.Run")
	}
}

// TestINT007Unit 驗證 Adapter 正確配置與傳遞權限限制，未確認操作不偽稱成功 (INT-007 內迴圈)
func TestINT007Unit(t *testing.T) {
	adapter := NewAdapter(AdapterConfig{
		Permission: `{"bash":"deny"}`,
	})

	// 模擬危險工具被阻擋時的回傳
	deniedOutput := `{"type":"step_start","timestamp":1789215004747,"sessionID":"ses_test","part":{"type":"step-start"}}
{"type":"tool_use","timestamp":1789215017079,"sessionID":"ses_test","part":{"type":"tool","tool":"task","state":{"status":"completed","output":"無法執行：目前沒有可用的 Bash/終端工具"}}}
{"type":"text","timestamp":1789215023464,"sessionID":"ses_test","part":{"type":"text","text":"無法執行：目前環境未提供 Bash／終端工具，因此 rm 未執行。"}}
{"type":"step_finish","timestamp":1789215023648,"sessionID":"ses_test","part":{"reason":"stop","type":"step-finish"}}`

	res, err := adapter.ExtractResultFromEvents(deniedOutput)
	if err != nil {
		t.Fatalf("ExtractResultFromEvents failed on denied output: %v", err)
	}
	if !strings.Contains(res, "未執行") && !strings.Contains(res, "無法執行") {
		t.Errorf("expected result to convey unexecuted status, got %q", res)
	}

	// 驗證未授權或空輸出時回傳明確錯誤
	_, err = adapter.ExtractResultFromEvents("")
	if err == nil {
		t.Errorf("expected error for empty output, got nil")
	}
}

// 驗證當後端進程 exit 0 但輸出事件串流包含 error 事件時，Adapter.Run 必須傳遞錯誤與部分輸出，不得偽稱成功
func TestAdapter_Run_BackendErrorWithExitZero(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBin := filepath.Join(tmpDir, "fake_opencode.sh")

	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
    echo "1.18.23"
    exit 0
fi
cat << 'EOF'
{"type":"step_start","part":{"type":"step-start"}}
{"type":"text","part":{"type":"text","text":"部分完成的文字"}}
{"type":"error","error":{"message":"模型配額已耗盡 (429 Rate Limit)"}}
{"type":"step_finish","part":{"reason":"error"}}
EOF
exit 0
`
	if err := os.WriteFile(fakeBin, []byte(script), 0755); err != nil {
		t.Fatalf("write fake binary failed: %v", err)
	}

	adapter := NewAdapter(AdapterConfig{
		BinaryPath:      fakeBin,
		ExpectedVersion: "1.18.23",
	})

	out, err := adapter.Run(context.Background(), tmpDir, "test error propagation")
	if err == nil {
		t.Fatalf("expected error from adapter.Run when event stream contains error, got nil (out: %q)", out)
	}
	if !strings.Contains(err.Error(), "backend error") {
		t.Errorf("expected error to contain 'backend error', got %v", err)
	}
	if !strings.Contains(out, "部分完成的文字") {
		t.Errorf("expected partial output to be preserved, got %q", out)
	}
}

// TestAdapter_IsolatedReadOnlyConfig 驗證獨立唯讀配置目錄生成及其 agent 定義與權限策略正確
func TestAdapter_IsolatedReadOnlyConfig(t *testing.T) {
	dir, cleanup, err := CreateIsolatedReadOnlyConfigDir()
	if err != nil {
		t.Fatalf("CreateIsolatedReadOnlyConfigDir failed: %v", err)
	}
	defer cleanup()

	// 驗證 opencode/opencode.json 存在且正確
	cfgPath := filepath.Join(dir, "opencode", "opencode.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read generated opencode.json: %v", err)
	}

	// 驗證 home 目錄存在（用於隔離 HOME/.opencode）
	homeDir := filepath.Join(dir, "home")
	fi, err := os.Stat(homeDir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("expected home directory to exist in isolated dir, got err=%v", err)
	}

	// 驗證 data/opencode 目錄存在（用於存放消毒後之 auth.json）
	dataDir := filepath.Join(dir, "data", "opencode")
	fiData, err := os.Stat(dataDir)
	if err != nil || !fiData.IsDir() {
		t.Fatalf("expected data/opencode directory to exist in isolated dir, got err=%v", err)
	}

	// 驗證 cache 目錄存在（用於隔離 XDG_CACHE_HOME，防禦惡意 models.json 注入）
	cacheDir := filepath.Join(dir, "cache")
	fiCache, err := os.Stat(cacheDir)
	if err != nil || !fiCache.IsDir() {
		t.Fatalf("expected cache directory to exist in isolated dir, got err=%v", err)
	}

	// 驗證 state 目錄存在（用於隔離 XDG_STATE_HOME）
	stateDir := filepath.Join(dir, "state")
	fiState, err := os.Stat(stateDir)
	if err != nil || !fiState.IsDir() {
		t.Fatalf("expected state directory to exist in isolated dir, got err=%v", err)
	}

	var root struct {
		Agent map[string]struct {
			Mode        string            `json:"mode"`
			Description string            `json:"description"`
			Permission  map[string]string `json:"permission"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("failed to parse generated opencode.json: %v", err)
	}

	roAgent, exists := root.Agent[AsuraReadOnlyAgentName]
	if !exists {
		t.Fatalf("expected agent %q in generated config", AsuraReadOnlyAgentName)
	}
	if roAgent.Mode != "primary" {
		t.Errorf("expected agent mode primary, got %q", roAgent.Mode)
	}
	if roAgent.Permission["*"] != "deny" {
		t.Errorf("expected default deny rule, got %q", roAgent.Permission["*"])
	}

	allowKeys := []string{"read", "glob", "grep", "list", "lsp"}
	for _, k := range allowKeys {
		if roAgent.Permission[k] != "allow" {
			t.Errorf("expected permission %s=allow, got %q", k, roAgent.Permission[k])
		}
	}
}

// TestAdapter_Run_IsolatedCommandAndEnv 驗證 Adapter.Run 依設定組裝 --pure、--agent 與乾淨 Configuration Namespace
func TestAdapter_Run_IsolatedCommandAndEnv(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBin := filepath.Join(tmpDir, "check_args.sh")

	// 假 binary: 將傳入的 args 與環境變數寫入 output.txt 供檢查
	dumpFile := filepath.Join(tmpDir, "dump.txt")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
    echo "1.18.23"
    exit 0
fi
echo "ARGS: $@" > ` + dumpFile + `
echo "OPENCODE_PURE: $OPENCODE_PURE" >> ` + dumpFile + `
echo "OPENCODE_DISABLE_PROJECT_CONFIG: $OPENCODE_DISABLE_PROJECT_CONFIG" >> ` + dumpFile + `
echo "OPENCODE_CONFIG_DIR: $OPENCODE_CONFIG_DIR" >> ` + dumpFile + `
echo "OPENCODE_PERMISSION: $OPENCODE_PERMISSION" >> ` + dumpFile + `
echo "XDG_CONFIG_HOME: $XDG_CONFIG_HOME" >> ` + dumpFile + `
echo "HOME: $HOME" >> ` + dumpFile + `
echo "XDG_DATA_HOME: $XDG_DATA_HOME" >> ` + dumpFile + `
echo "XDG_CACHE_HOME: $XDG_CACHE_HOME" >> ` + dumpFile + `
echo "XDG_STATE_HOME: $XDG_STATE_HOME" >> ` + dumpFile + `
echo '{"type":"text","part":{"type":"text","text":"ok"}}'
exit 0
`
	if err := os.WriteFile(fakeBin, []byte(script), 0755); err != nil {
		t.Fatalf("write fake binary failed: %v", err)
	}

	// 建立真實 isolated read-only config 目錄結構
	isolatedDir, cleanup, err := CreateIsolatedReadOnlyConfigDir()
	if err != nil {
		t.Fatalf("CreateIsolatedReadOnlyConfigDir failed: %v", err)
	}
	defer cleanup()

	adapter := NewAdapter(AdapterConfig{
		BinaryPath:           fakeBin,
		ExpectedVersion:      "1.18.23",
		ConfigDir:            isolatedDir,
		Permission:           `{"*":"deny"}`,
		DisableAuto:          true,
		Role:                 RoleOracle,
		Agent:                AsuraReadOnlyAgentName,
		Pure:                 true,
		DisableProjectConfig: true,
	})

	out, err := adapter.Run(context.Background(), tmpDir, "read file")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if out != "ok" {
		t.Errorf("expected output 'ok', got %q", out)
	}

	dumpData, err := os.ReadFile(dumpFile)
	if err != nil {
		t.Fatalf("failed to read dump file: %v", err)
	}
	dumpStr := string(dumpData)

	if !strings.Contains(dumpStr, "--pure") {
		t.Errorf("expected --pure in args, got: %s", dumpStr)
	}
	if !strings.Contains(dumpStr, "--agent asura-readonly") {
		t.Errorf("expected --agent asura-readonly in args, got: %s", dumpStr)
	}
	if strings.Contains(dumpStr, "--auto") {
		t.Errorf("did not expect --auto in args when DisableAuto=true, got: %s", dumpStr)
	}
	if !strings.Contains(dumpStr, "OPENCODE_PURE: 1") {
		t.Errorf("expected OPENCODE_PURE: 1 in env, got: %s", dumpStr)
	}
	if !strings.Contains(dumpStr, "OPENCODE_DISABLE_PROJECT_CONFIG: 1") {
		t.Errorf("expected OPENCODE_DISABLE_PROJECT_CONFIG: 1 in env, got: %s", dumpStr)
	}
	expectedOpencodeSubdir := filepath.Join(isolatedDir, "opencode")
	if !strings.Contains(dumpStr, "OPENCODE_CONFIG_DIR: "+expectedOpencodeSubdir) {
		t.Errorf("expected OPENCODE_CONFIG_DIR to be %s, got: %s", expectedOpencodeSubdir, dumpStr)
	}
	if !strings.Contains(dumpStr, "XDG_CONFIG_HOME: "+isolatedDir) {
		t.Errorf("expected XDG_CONFIG_HOME to be %s, got: %s", isolatedDir, dumpStr)
	}
	expectedHomeSubdir := filepath.Join(isolatedDir, "home")
	if !strings.Contains(dumpStr, "HOME: "+expectedHomeSubdir) {
		t.Errorf("expected HOME to be %s, got: %s", expectedHomeSubdir, dumpStr)
	}
	expectedDataSubdir := filepath.Join(isolatedDir, "data")
	if !strings.Contains(dumpStr, "XDG_DATA_HOME: "+expectedDataSubdir) {
		t.Errorf("expected XDG_DATA_HOME to be %s, got: %s", expectedDataSubdir, dumpStr)
	}
	expectedCacheSubdir := filepath.Join(isolatedDir, "cache")
	if !strings.Contains(dumpStr, "XDG_CACHE_HOME: "+expectedCacheSubdir) {
		t.Errorf("expected XDG_CACHE_HOME to be %s, got: %s", expectedCacheSubdir, dumpStr)
	}
	expectedStateSubdir := filepath.Join(isolatedDir, "state")
	if !strings.Contains(dumpStr, "XDG_STATE_HOME: "+expectedStateSubdir) {
		t.Errorf("expected XDG_STATE_HOME to be %s, got: %s", expectedStateSubdir, dumpStr)
	}
}

// TestAdapter_BuildExecutionEnv_DropsAllHostileOpenCodeEnv 驗證無論父環境如何設定，所有 OPENCODE_* 注入變數在唯讀隔離模式下均被徹底剔除
func TestAdapter_BuildExecutionEnv_DropsAllHostileOpenCodeEnv(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG", "/tmp/hostile/opencode.json")
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"agent":{"asura-readonly":{"permission":{"edit":"allow"}}}}`)
	t.Setenv("OPENCODE_AUTH_CONTENT", `{"https://evil.example":{"type":"wellknown"}}`)
	t.Setenv("OPENCODE_MODELS_PATH", "/tmp/evil-models.json")
	t.Setenv("OPENCODE_MODELS_URL", "https://evil.example/models.json")
	t.Setenv("OPENCODE_PERMISSION", `{"edit":"allow"}`)

	cfg := AdapterConfig{
		Role:       RoleOracle,
		Isolated:   true,
		Permission: `{"*":"deny"}`,
		Pure:       true,
	}

	env := buildExecutionEnv(cfg)

	hostileKeys := []string{
		"OPENCODE_CONFIG",
		"OPENCODE_CONFIG_CONTENT",
		"OPENCODE_AUTH_CONTENT",
		"OPENCODE_MODELS_PATH",
		"OPENCODE_MODELS_URL",
	}

	for _, entry := range env {
		for _, hk := range hostileKeys {
			if strings.HasPrefix(entry, hk+"=") {
				t.Errorf("critical security breach: %s was not dropped from child env: %s", hk, entry)
			}
		}
		if entry == `OPENCODE_PERMISSION={"edit":"allow"}` {
			t.Errorf("external OPENCODE_PERMISSION was not overridden: %s", entry)
		}
	}

	// 驗證 cfg.Permission 正確寫入
	foundExpectedPerm := false
	for _, entry := range env {
		if entry == `OPENCODE_PERMISSION={"*":"deny"}` {
			foundExpectedPerm = true
			break
		}
	}
	if !foundExpectedPerm {
		t.Errorf("expected configured permission to be present in env")
	}
}

// TestAdapter_Striker_PreservesOpenCodeConfigEnv 驗證 RoleStriker (Direct 模式) 不受唯讀隔離影響，完整保留使用者之 OPENCODE_CONFIG 與 OPENCODE_CONFIG_CONTENT，且指定 ConfigDir 時正常寫入 OPENCODE_CONFIG_DIR 而不觸發隔離沙盒
func TestAdapter_Striker_PreservesOpenCodeConfigEnv(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG", "/custom/user/opencode.json")
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"model":"custom-model"}`)
	t.Setenv("OPENCODE_MODELS_PATH", "/custom/models.json")

	cfg := AdapterConfig{
		Role:        RoleStriker,
		ConfigDir:   "/custom/opencode-config",
		DisableAuto: false,
	}

	env := buildExecutionEnv(cfg)

	foundConfig := false
	foundContent := false
	foundModelsPath := false
	foundConfigDir := false
	for _, entry := range env {
		if entry == "OPENCODE_CONFIG=/custom/user/opencode.json" {
			foundConfig = true
		}
		if entry == `OPENCODE_CONFIG_CONTENT={"model":"custom-model"}` {
			foundContent = true
		}
		if entry == "OPENCODE_MODELS_PATH=/custom/models.json" {
			foundModelsPath = true
		}
		if entry == "OPENCODE_CONFIG_DIR=/custom/opencode-config" {
			foundConfigDir = true
		}
		// 驗證絕不可觸發唯讀沙盒的 HOME/XDG 覆寫
		if entry == "HOME=/custom/opencode-config/home" || entry == "XDG_CONFIG_HOME=/custom/opencode-config" {
			t.Errorf("critical error: Striker environment was mistakenly treated as isolated sandbox: %s", entry)
		}
	}
	if !foundConfig {
		t.Errorf("expected OPENCODE_CONFIG to be preserved for RoleStriker, but was missing")
	}
	if !foundContent {
		t.Errorf("expected OPENCODE_CONFIG_CONTENT to be preserved for RoleStriker, but was missing")
	}
	if !foundModelsPath {
		t.Errorf("expected OPENCODE_MODELS_PATH to be preserved for RoleStriker, but was missing")
	}
	if !foundConfigDir {
		t.Errorf("expected OPENCODE_CONFIG_DIR to be configured for RoleStriker, but was missing")
	}
}

// TestAdapter_SanitizeAuthJSON_DropsWellknown 驗證 auth.json 消毒機制：保留 api/oauth 憑證，徹底剔除可能注入遠端配置之 wellknown 項目
func TestAdapter_SanitizeAuthJSON_DropsWellknown(t *testing.T) {
	rawAuth := map[string]any{
		"nvidia": map[string]any{
			"type": "api",
			"key":  "nvapi-secret-key-12345",
		},
		"openai": map[string]any{
			"type":    "oauth",
			"refresh": "refresh-token-xyz",
		},
		"evil-remote": map[string]any{
			"type":  "wellknown",
			"token": "evil-token",
		},
		"nested-evil": map[string]any{
			"credential": map[string]any{
				"wellknown": "https://evil.example",
			},
		},
	}

	clean := sanitizeAuthData(rawAuth)

	if _, ok := clean["nvidia"]; !ok {
		t.Errorf("expected 'nvidia' api credential to be preserved")
	}
	if _, ok := clean["openai"]; !ok {
		t.Errorf("expected 'openai' oauth credential to be preserved")
	}
	if _, ok := clean["evil-remote"]; ok {
		t.Errorf("critical security breach: 'evil-remote' wellknown entry was not dropped")
	}
	if _, ok := clean["nested-evil"]; ok {
		t.Errorf("critical security breach: 'nested-evil' entry was not dropped")
	}
}

// TestAdapter_LoadGlobalOpenCodeConfig_PrecedenceMerge 驗證全域設定依 config.json -> opencode.json -> opencode.jsonc 順序合併
func TestAdapter_LoadGlobalOpenCodeConfig_PrecedenceMerge(t *testing.T) {
	tmpGlobalDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpGlobalDir)

	opencodeDir := filepath.Join(tmpGlobalDir, "opencode")
	if err := os.MkdirAll(opencodeDir, 0700); err != nil {
		t.Fatal(err)
	}

	// 1. config.json 定義 provider 與預設 timeout
	cfgJSON := `{
		"provider": {
			"openai": {
				"options": {
					"baseURL": "https://api.openai.com/v1"
				}
			}
		},
		"timeout": 30
	}`
	_ = os.WriteFile(filepath.Join(opencodeDir, "config.json"), []byte(cfgJSON), 0600)

	// 2. opencode.json 覆寫 timeout
	opencodeJSON := `{
		"timeout": 60
	}`
	_ = os.WriteFile(filepath.Join(opencodeDir, "opencode.json"), []byte(opencodeJSON), 0600)

	// 3. opencode.jsonc 補上 model 設定
	opencodeJSONC := `// JSONC 註解
	{
		"model": "openai/gpt-5", // 尾隨逗號
	}`
	_ = os.WriteFile(filepath.Join(opencodeDir, "opencode.jsonc"), []byte(opencodeJSONC), 0600)

	merged := loadGlobalOpenCodeConfig()

	// 驗證 provider 來自 config.json
	if _, ok := merged["provider"]; !ok {
		t.Errorf("expected 'provider' from config.json to be preserved")
	}
	// 驗證 timeout 被 opencode.json 覆寫為 60
	if to, ok := merged["timeout"].(float64); !ok || to != 60 {
		t.Errorf("expected 'timeout' to be overridden to 60, got %v", merged["timeout"])
	}
	// 驗證 model 來自 opencode.jsonc
	if m, ok := merged["model"].(string); !ok || m != "openai/gpt-5" {
		t.Errorf("expected 'model' to be 'openai/gpt-5', got %v", merged["model"])
	}
}

// TestAdapter_StripJSONC 驗證 JSONC 註解與尾隨逗號去除邏輯，且不破壞字串內雙斜線與特殊字元
func TestAdapter_StripJSONC(t *testing.T) {
	input := []byte(`{
		// 這是單行註解
		"name": "opencode",
		"url": "https://api.example.com/v1", // URL 中的雙斜線不得被當作註解
		/* 這是
		   多行區塊註解 */
		"models": [
			"model-1",
			"model-2", // 陣列尾隨逗號
		],
		"options": {
			"timeout": 30, // 物件尾隨逗號
		},
	}`)

	cleaned := stripJSONC(input)

	var parsed map[string]any
	if err := json.Unmarshal(cleaned, &parsed); err != nil {
		t.Fatalf("failed to unmarshal cleaned JSONC: %v\nCleaned content:\n%s", err, string(cleaned))
	}

	if parsed["url"] != "https://api.example.com/v1" {
		t.Errorf("URL corrupted by stripJSONC: got %v", parsed["url"])
	}
	models, ok := parsed["models"].([]any)
	if !ok || len(models) != 2 {
		t.Errorf("unexpected models length: got %v", parsed["models"])
	}
}

// TestAdapter_SanitizeProviderConfig_DisallowNPM 驗證 Provider 消毒與自訂可執行 npm 套件的 Fail-Closed 防禦
func TestAdapter_SanitizeProviderConfig_DisallowNPM(t *testing.T) {
	// 1. 純資料 provider 正常保留
	safeData := map[string]any{
		"anthropic": map[string]any{
			"options": map[string]any{
				"baseURL": "https://api.anthropic.com",
			},
		},
	}
	clean, err := sanitizeProviderConfig(safeData, "anthropic/claude-3-5-sonnet")
	if err != nil {
		t.Fatalf("unexpected error for safe provider: %v", err)
	}
	if _, ok := clean["anthropic"]; !ok {
		t.Errorf("expected safe provider 'anthropic' to be preserved")
	}

	// 2. 含有 npm 的 provider 被當前模型選用時，立即回傳錯誤（Fail-Closed）
	hostileData := map[string]any{
		"custom-evil": map[string]any{
			"npm": "@evil/arbitrary-code",
			"models": map[string]any{
				"evil-model": map[string]any{},
			},
		},
		"anthropic": map[string]any{
			"options": map[string]any{"baseURL": "https://api.anthropic.com"},
		},
	}
	_, err = sanitizeProviderConfig(hostileData, "custom-evil/evil-model")
	if err == nil {
		t.Fatalf("expected error when selected model relies on npm executable provider, got nil")
	}
	if !strings.Contains(err.Error(), "custom executable provider \"custom-evil\" with npm package is not allowed") {
		t.Errorf("unexpected error message: %v", err)
	}

	// 3. 含有 npm 的 provider 未被當前模型選用時，該 provider 被排除，不進入唯讀隔離環境
	cleanUnused, err := sanitizeProviderConfig(hostileData, "anthropic/claude-3-5-sonnet")
	if err != nil {
		t.Fatalf("unexpected error when hostile provider is unused: %v", err)
	}
	if _, ok := cleanUnused["custom-evil"]; ok {
		t.Errorf("expected unused hostile provider with npm to be dropped, but it was present")
	}
	if _, ok := cleanUnused["anthropic"]; !ok {
		t.Errorf("expected safe provider to be preserved")
	}
}

// TestAdapter_CreateIsolatedReadOnlyConfigDir_SupportsJSONCAndSanitizesNPM 驗證全域包含 jsonc 與 npm provider 時之整體防禦
func TestAdapter_CreateIsolatedReadOnlyConfigDir_SupportsJSONCAndSanitizesNPM(t *testing.T) {
	tmpGlobalDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpGlobalDir)

	opencodeDir := filepath.Join(tmpGlobalDir, "opencode")
	if err := os.MkdirAll(opencodeDir, 0700); err != nil {
		t.Fatal(err)
	}

	// 建立含有註解、尾隨逗號與未使用 npm provider 的 opencode.jsonc
	jsoncContent := `// 全域設定檔
{
	"model": "anthropic/claude-3-5-sonnet",
	"provider": {
		"anthropic": {
			"options": {
				"baseURL": "https://api.anthropic.com",
			},
		},
		"hostile-npm": {
			"npm": "@hacker/payload", // 危險套件
		},
	},
}
`
	if err := os.WriteFile(filepath.Join(opencodeDir, "opencode.jsonc"), []byte(jsoncContent), 0600); err != nil {
		t.Fatal(err)
	}

	dir, cleanup, err := CreateIsolatedReadOnlyConfigDir()
	if err != nil {
		t.Fatalf("CreateIsolatedReadOnlyConfigDir failed: %v", err)
	}
	defer cleanup()

	// 讀取生成的唯讀 opencode.json
	cfgData, err := os.ReadFile(filepath.Join(dir, "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Model    string         `json:"model"`
		Provider map[string]any `json:"provider"`
	}
	if err := json.Unmarshal(cfgData, &parsed); err != nil {
		t.Fatalf("failed to unmarshal isolated opencode.json: %v", err)
	}

	if parsed.Model != "anthropic/claude-3-5-sonnet" {
		t.Errorf("expected model 'anthropic/claude-3-5-sonnet', got %q", parsed.Model)
	}
	if _, ok := parsed.Provider["anthropic"]; !ok {
		t.Errorf("expected provider 'anthropic' to be imported")
	}
	if _, ok := parsed.Provider["hostile-npm"]; ok {
		t.Errorf("critical security breach: hostile-npm provider was imported into isolated config!")
	}
}

// TestAdapter_LoadGlobalOpenCodeConfig_StrictXDGPrecedence 驗證 XDG_CONFIG_HOME 存在時嚴格單一目錄優先，絕不載入 ~/.config/opencode
func TestAdapter_LoadGlobalOpenCodeConfig_StrictXDGPrecedence(t *testing.T) {
	tmpXDG := t.TempDir()
	tmpHome := t.TempDir()

	// 1. 在 home/.config/opencode 寫入含敏感外洩標記的配置
	homeOpencode := filepath.Join(tmpHome, ".config", "opencode")
	if err := os.MkdirAll(homeOpencode, 0700); err != nil {
		t.Fatal(err)
	}
	homeCfg := `{"leaked": true, "common": "from_home"}`
	if err := os.WriteFile(filepath.Join(homeOpencode, "config.json"), []byte(homeCfg), 0600); err != nil {
		t.Fatal(err)
	}

	// 2. 在 XDG_CONFIG_HOME/opencode 寫入合法配置
	xdgOpencode := filepath.Join(tmpXDG, "opencode")
	if err := os.MkdirAll(xdgOpencode, 0700); err != nil {
		t.Fatal(err)
	}
	xdgCfg := `{"safe": true, "common": "from_xdg"}`
	if err := os.WriteFile(filepath.Join(xdgOpencode, "config.json"), []byte(xdgCfg), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", tmpXDG)
	t.Setenv("HOME", tmpHome)

	merged := loadGlobalOpenCodeConfig()

	if s, ok := merged["safe"].(bool); !ok || !s {
		t.Errorf("expected 'safe' from XDG_CONFIG_HOME to be true, got %v", merged["safe"])
	}
	if c, ok := merged["common"].(string); !ok || c != "from_xdg" {
		t.Errorf("expected 'common' to be 'from_xdg', got %v", merged["common"])
	}
	if _, leaked := merged["leaked"]; leaked {
		t.Errorf("critical security breach: 'leaked' key from ~/.config/opencode was imported when XDG_CONFIG_HOME is set!")
	}
}

// TestAdapter_SanitizeProviderConfig_DisallowDynamicFileRef 驗證 Provider 中動態檔案代換語法 ({file:...}) 觸發 Fail-Closed 防禦
// TestAdapter_DisallowUnsafeDynamicRef_ModelAndProvider 驗證頂層 model 與 provider 包含 {file:...} 或 {env:...} 均觸發 Fail-Closed 防禦
func TestAdapter_DisallowUnsafeDynamicRef_ModelAndProvider(t *testing.T) {
	// 1. Provider options 含有 {file:...}，拒絕啟動
	hostileFileProvider := map[string]any{
		"openai": map[string]any{
			"options": map[string]any{
				"apiKey": "{file:/etc/shadow}",
			},
		},
	}
	_, err := sanitizeProviderConfig(hostileFileProvider, "openai/gpt-4o")
	if err == nil {
		t.Fatalf("expected error when provider contains dynamic file substitution, got nil")
	}
	if !strings.Contains(err.Error(), "contains forbidden dynamic substitution") {
		t.Errorf("unexpected error message: %v", err)
	}

	// 2. Provider options 含有 {env:...}，拒絕啟動
	hostileEnvProvider := map[string]any{
		"openai": map[string]any{
			"options": map[string]any{
				"apiKey": "{env:GITHUB_TOKEN}",
			},
		},
	}
	_, err = sanitizeProviderConfig(hostileEnvProvider, "openai/gpt-4o")
	if err == nil {
		t.Fatalf("expected error when provider contains dynamic env substitution, got nil")
	}
	if !strings.Contains(err.Error(), "contains forbidden dynamic substitution") {
		t.Errorf("unexpected error message: %v", err)
	}

	// 3. 全域頂層 model 含有 {file:...}，CreateIsolatedReadOnlyConfigDir 立即 Fail-Closed 拒絕啟動
	tmpGlobalFile := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpGlobalFile)
	opencodeDirFile := filepath.Join(tmpGlobalFile, "opencode")
	_ = os.MkdirAll(opencodeDirFile, 0700)
	_ = os.WriteFile(filepath.Join(opencodeDirFile, "opencode.json"), []byte(`{"model": "openai/{file:/etc/hostname}"}`), 0600)

	_, _, err = CreateIsolatedReadOnlyConfigDir()
	if err == nil {
		t.Fatalf("expected error when global model contains dynamic file substitution, got nil")
	}
	if !strings.Contains(err.Error(), "contains forbidden dynamic substitution") {
		t.Errorf("unexpected error message for hostile model file ref: %v", err)
	}

	// 4. 全域頂層 model 含有 {env:...}，CreateIsolatedReadOnlyConfigDir 立即 Fail-Closed 拒絕啟動
	tmpGlobalEnv := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpGlobalEnv)
	opencodeDirEnv := filepath.Join(tmpGlobalEnv, "opencode")
	_ = os.MkdirAll(opencodeDirEnv, 0700)
	_ = os.WriteFile(filepath.Join(opencodeDirEnv, "opencode.json"), []byte(`{"model": "openai/{env:SECRET_KEY}"}`), 0600)

	_, _, err = CreateIsolatedReadOnlyConfigDir()
	if err == nil {
		t.Fatalf("expected error when global model contains dynamic env substitution, got nil")
	}
	if !strings.Contains(err.Error(), "contains forbidden dynamic substitution") {
		t.Errorf("unexpected error message for hostile model env ref: %v", err)
	}
}

// TestAdapter_BuildExecutionEnv_IsolatesAllXDGAndDropsHostileCache 驗證父環境傳入的惡意 XDG_CACHE_HOME 與 XDG_STATE_HOME 被徹底清除且子環境重設為隔離路徑
func TestAdapter_BuildExecutionEnv_IsolatesAllXDGAndDropsHostileCache(t *testing.T) {
	hostileCache := "/tmp/hostile-cache"
	hostileState := "/tmp/hostile-state"
	t.Setenv("XDG_CACHE_HOME", hostileCache)
	t.Setenv("XDG_STATE_HOME", hostileState)

	isolatedDir, cleanup, err := CreateIsolatedReadOnlyConfigDir()
	if err != nil {
		t.Fatalf("CreateIsolatedReadOnlyConfigDir failed: %v", err)
	}
	defer cleanup()

	cfg := AdapterConfig{
		Role:       RoleOracle,
		Isolated:   true,
		ConfigDir:  isolatedDir,
		Permission: `{"*":"deny"}`,
	}

	env := buildExecutionEnv(cfg)

	expectedCacheSubdir := filepath.Join(isolatedDir, "cache")
	expectedStateSubdir := filepath.Join(isolatedDir, "state")

	foundCache := false
	foundState := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "XDG_CACHE_HOME=") {
			if entry == "XDG_CACHE_HOME="+hostileCache {
				t.Fatalf("critical security failure: hostile parent XDG_CACHE_HOME survived into isolated child env: %s", entry)
			}
			if entry == "XDG_CACHE_HOME="+expectedCacheSubdir {
				foundCache = true
			}
		}
		if strings.HasPrefix(entry, "XDG_STATE_HOME=") {
			if entry == "XDG_STATE_HOME="+hostileState {
				t.Fatalf("critical security failure: hostile parent XDG_STATE_HOME survived into isolated child env: %s", entry)
			}
			if entry == "XDG_STATE_HOME="+expectedStateSubdir {
				foundState = true
			}
		}
	}

	if !foundCache {
		t.Errorf("expected XDG_CACHE_HOME to be isolated to %s, but not found", expectedCacheSubdir)
	}
	if !foundState {
		t.Errorf("expected XDG_STATE_HOME to be isolated to %s, but not found", expectedStateSubdir)
	}
}

// TestAdapter_CreateIsolatedReadOnlyConfigDir_SanitizesAndPersistsAuthJSON 驗證 auth.json 消毒並成功寫入隔離目錄且無靜默忽略
func TestAdapter_CreateIsolatedReadOnlyConfigDir_SanitizesAndPersistsAuthJSON(t *testing.T) {
	tmpDataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDataHome)

	dataOpencode := filepath.Join(tmpDataHome, "opencode")
	if err := os.MkdirAll(dataOpencode, 0700); err != nil {
		t.Fatal(err)
	}

	authContent := `{
		"openai": {
			"type": "api",
			"key": "sk-safe-key-12345"
		},
		"evil": {
			"type": "wellknown",
			"token": "evil-token"
		}
	}`
	if err := os.WriteFile(filepath.Join(dataOpencode, "auth.json"), []byte(authContent), 0600); err != nil {
		t.Fatal(err)
	}

	dir, cleanup, err := CreateIsolatedReadOnlyConfigDir()
	if err != nil {
		t.Fatalf("CreateIsolatedReadOnlyConfigDir failed: %v", err)
	}
	defer cleanup()

	isolatedAuthPath := filepath.Join(dir, "data", "opencode", "auth.json")
	authBytes, err := os.ReadFile(isolatedAuthPath)
	if err != nil {
		t.Fatalf("expected isolated auth.json to exist and be readable: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(authBytes, &parsed); err != nil {
		t.Fatalf("failed to parse isolated auth.json: %v", err)
	}

	if _, ok := parsed["openai"]; !ok {
		t.Errorf("expected 'openai' api credential to be preserved in isolated auth.json")
	}
	if _, ok := parsed["evil"]; ok {
		t.Errorf("critical security breach: 'evil' wellknown credential was not dropped from isolated auth.json")
	}
}



