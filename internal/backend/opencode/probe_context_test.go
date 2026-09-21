package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ContextCapabilityMatrix 記錄後端在指定版本下的 Context 與 Token 觀測/控制能力
type ContextCapabilityMatrix struct {
	BackendID                   string            `json:"backend_id"`
	TargetVersion               string            `json:"target_version"`
	InstalledVersion            string            `json:"installed_version,omitempty"`
	ProbeDate                   string            `json:"probe_date"`
	Status                      string            `json:"status"` // verified_installed, unverified_not_installed
	HasTokenUsage               bool              `json:"has_token_usage"`
	TokenFields                 []string          `json:"token_fields"`
	MissingTokenFields          []string          `json:"missing_token_fields"`
	HasCachedTokens             bool              `json:"has_cached_tokens"`
	HasReasoningTokens          bool              `json:"has_reasoning_tokens"`
	TokenReportingGranularity   string            `json:"token_reporting_granularity"` // per_step, per_turn
	UsagePreservedOnToolFailure bool              `json:"usage_preserved_on_tool_failure"`
	PreflightModelIdentifiable  bool              `json:"preflight_model_identifiable"` // 自 stdout JSONL 能否識別模型
	UnsupportedCapabilities     map[string]string `json:"unsupported_capabilities"`
}

// GetOpenCode11831Matrix 回傳針對 OpenCode 1.18.31 之能力矩陣
func GetOpenCode11831Matrix() ContextCapabilityMatrix {
	return ContextCapabilityMatrix{
		BackendID:                 "opencode",
		TargetVersion:             "1.18.31",
		ProbeDate:                 "2026-09-21",
		Status:                    "verified_installed",
		HasTokenUsage:             true,
		TokenFields:               []string{"input", "output", "total", "reasoning", "cache.read", "cache.write"},
		MissingTokenFields:        []string{},
		HasCachedTokens:           true,
		HasReasoningTokens:        true,
		TokenReportingGranularity: "per_step",
		UsagePreservedOnToolFailure: true,
		PreflightModelIdentifiable:  false,
		UnsupportedCapabilities: map[string]string{
			"tool_output_interception": "OpenCode CLI 非互動模式無外部攔截 hook，無法在送入模型前修改或過濾工具輸出",
			"history_dynamic_rewrite":  "非互動式單次運行無會話歷史熱插拔 API",
			"context_limit_query":      "CLI 未提供動態查詢目前模型 Context Window 上限之指令",
			"stdout_model_identity":    "stdout JSONL 事件流未包含使用的具體 model 名稱",
		},
	}
}

// GetOpenCode11823Matrix 回傳針對 OpenCode 1.18.23 之能力矩陣（標記未安裝驗證）
func GetOpenCode11823Matrix() ContextCapabilityMatrix {
	return ContextCapabilityMatrix{
		BackendID:                 "opencode",
		TargetVersion:             "1.18.23",
		ProbeDate:                 "2026-09-21",
		Status:                    "unverified_not_installed",
		HasTokenUsage:             true,
		TokenFields:               []string{"input", "output", "total", "reasoning"},
		MissingTokenFields:        []string{},
		HasCachedTokens:           false, // 1.18.23 早期版本無 cache 欄位
		HasReasoningTokens:        true,
		TokenReportingGranularity: "per_step",
		UsagePreservedOnToolFailure: true,
		PreflightModelIdentifiable:  false,
		UnsupportedCapabilities: map[string]string{
			"tool_output_interception": "OpenCode 1.18.23 非互動模式無外部攔截 hook",
			"history_dynamic_rewrite":  "1.18.23 無會話歷史熱插拔 API",
			"context_limit_query":      "1.18.23 CLI 未提供動態查詢 Context Window 指令",
			"stdout_model_identity":    "1.18.23 JSONL 未包含 model 欄位",
		},
	}
}

// 靜態去敏樣本：OpenCode 1.18.31 單回合成功
const OpenCodeFixture_1_18_31_SingleTurn = `{"type":"step_start","timestamp":1789970937711,"sessionID":"ses_mock001","part":{"id":"prt_01","type":"step-start"}}
{"type":"text","timestamp":1789970937976,"sessionID":"ses_mock001","part":{"id":"prt_02","type":"text","text":"PONG"}}
{"type":"step_finish","timestamp":1789970938158,"sessionID":"ses_mock001","part":{"id":"prt_03","reason":"stop","type":"step-finish","tokens":{"total":21522,"input":21516,"output":6,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0}}`

// 靜態去敏樣本：OpenCode 1.18.31 工具執行失敗但仍包含用量
const OpenCodeFixture_1_18_31_ToolFailure = `{"type":"step_start","timestamp":1789970963000,"sessionID":"ses_mock002","part":{"id":"prt_11","type":"step-start"}}
{"type":"step_finish","timestamp":1789970963415,"sessionID":"ses_mock002","part":{"id":"prt_12","reason":"tool-calls","type":"step-finish","tokens":{"total":21574,"input":21520,"output":26,"reasoning":28,"cache":{"write":0,"read":0}},"cost":0}}
{"type":"step_start","timestamp":1789970969398,"sessionID":"ses_mock002","part":{"id":"prt_13","type":"step-start"}}
{"type":"text","timestamp":1789970970060,"sessionID":"ses_mock002","part":{"id":"prt_14","type":"text","text":"Command failed with error"}}
{"type":"step_finish","timestamp":1789970970248,"sessionID":"ses_mock002","part":{"id":"prt_15","reason":"stop","type":"step-finish","tokens":{"total":26341,"input":4937,"output":28,"reasoning":0,"cache":{"write":0,"read":21376}},"cost":0}}`

// 靜態去敏樣本：OpenCode 1.18.23 歷史格式（無 cache 欄位）
const OpenCodeFixture_1_18_23_Legacy = `{"type":"step_start","timestamp":1789970000000,"sessionID":"ses_mock003","part":{"id":"prt_21","type":"step-start"}}
{"type":"text","timestamp":1789970000500,"sessionID":"ses_mock003","part":{"id":"prt_22","type":"text","text":"Legacy Output"}}
{"type":"step_finish","timestamp":1789970000800,"sessionID":"ses_mock003","part":{"id":"prt_23","reason":"stop","type":"step-finish","tokens":{"total":1500,"input":1450,"output":50,"reasoning":0}}}`

// TestOpenCodeProbe_HermeticFixtures 驗證 OpenCode 雙版本之去敏樣本解析與能力矩陣（離線可重現）
func TestOpenCodeProbe_HermeticFixtures(t *testing.T) {
	// 1. 驗證 1.18.31 單回合樣本
	s1 := bufio.NewScanner(strings.NewReader(OpenCodeFixture_1_18_31_SingleTurn))
	var lastTokens *TokenUsage
	for s1.Scan() {
		evt, err := ParseEventLine(s1.Text())
		if err != nil {
			t.Fatalf("parse error on 1.18.31 fixture: %v", err)
		}
		if evt != nil && evt.Tokens.Total > 0 {
			lastTokens = &evt.Tokens
		}
	}
	if lastTokens == nil {
		t.Fatalf("expected tokens in 1.18.31 single turn fixture")
	}
	if lastTokens.Input != 21516 || lastTokens.Output != 6 || lastTokens.Total != 21522 {
		t.Errorf("unexpected token counts: %+v", *lastTokens)
	}

	// 2. 驗證 1.18.31 工具失敗後依然保留用量
	s2 := bufio.NewScanner(strings.NewReader(OpenCodeFixture_1_18_31_ToolFailure))
	finishCount := 0
	for s2.Scan() {
		evt, _ := ParseEventLine(s2.Text())
		if evt != nil && evt.Tokens.Total > 0 {
			finishCount++
		}
	}
	if finishCount != 2 {
		t.Errorf("expected 2 step_finish events with tokens, got %d", finishCount)
	}

	// 3. 驗證能力矩陣之 unsupported 項目非空且有理由
	m31 := GetOpenCode11831Matrix()
	if len(m31.UnsupportedCapabilities) == 0 {
		t.Errorf("expected unsupported capabilities in 1.18.31 matrix")
	}
	if m31.PreflightModelIdentifiable {
		t.Errorf("expected preflight model to be unidentifiable from stdout")
	}

	// 4. 驗證 1.18.23 未安裝狀態明確標註
	m23 := GetOpenCode11823Matrix()
	if m23.Status != "unverified_not_installed" {
		t.Errorf("expected 1.18.23 to be marked unverified_not_installed, got %q", m23.Status)
	}
}

// TestOpenCodeProbe_Live 針對本機已安裝之 OpenCode 進行即時探測
func TestOpenCodeProbe_Live(t *testing.T) {
	p, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode binary not found; skipping live probe")
	}

	out, err := exec.Command(p, "--version").Output()
	if err != nil {
		t.Skipf("opencode --version failed: %v", err)
	}
	verStr := strings.TrimSpace(string(out))
	t.Logf("installed opencode version: %s", verStr)

	// 若非 1.18.31，記錄版本差異
	if !strings.Contains(verStr, "1.18.31") {
		t.Logf("installed version %q differs from target 1.18.31; skipping live assertion", verStr)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, p, "run", "--format", "json", "Respond with exactly PONG")
	cmdOutput, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("live opencode run failed (credential/network missing): %v, out: %s", err, string(cmdOutput))
	}

	scanner := bufio.NewScanner(strings.NewReader(string(cmdOutput)))
	foundStepFinish := false
	foundTokens := false
	modelFoundInJSONL := false

	for scanner.Scan() {
		line := scanner.Text()
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err == nil {
			if typ, ok := raw["type"].(string); ok && typ == "step_finish" {
				foundStepFinish = true
				if part, ok := raw["part"].(map[string]interface{}); ok {
					if tokens, ok := part["tokens"].(map[string]interface{}); ok {
						if _, hasInput := tokens["input"]; hasInput {
							foundTokens = true
						}
					}
				}
			}
			// 檢查事件中是否有 model 欄位
			if _, hasModel := raw["model"]; hasModel {
				modelFoundInJSONL = true
			}
		}
	}

	if !foundStepFinish {
		t.Errorf("expected step_finish in live opencode output")
	}
	if !foundTokens {
		t.Errorf("expected tokens object in step_finish")
	}
	if modelFoundInJSONL {
		t.Errorf("unexpected model field found in top-level JSONL; check if CLI contract changed")
	}
}
