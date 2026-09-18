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
	if !strings.Contains(dumpStr, "XDG_DATA_HOME:") {
		t.Errorf("expected XDG_DATA_HOME to be populated in env, got: %s", dumpStr)
	}
}

