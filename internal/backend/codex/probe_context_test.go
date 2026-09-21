package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// CodexContextCapabilityMatrix 記錄 Codex CLI 在指定版本下的 Context 與 Token 觀測/控制能力
type CodexContextCapabilityMatrix struct {
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
	TokenReportingGranularity   string            `json:"token_reporting_granularity"` // per_turn
	UsagePreservedOnToolFailure bool              `json:"usage_preserved_on_tool_failure"`
	PreflightModelIdentifiable  bool              `json:"preflight_model_identifiable"` // 自 stdout JSONL 能否識別模型
	UnsupportedCapabilities     map[string]string `json:"unsupported_capabilities"`
}

// GetCodex01551Matrix 回傳針對 Codex CLI 0.155.1 之能力矩陣
func GetCodex01551Matrix() CodexContextCapabilityMatrix {
	return CodexContextCapabilityMatrix{
		BackendID:                 "codex",
		TargetVersion:             "0.155.1",
		ProbeDate:                 "2026-09-21",
		Status:                    "verified_installed",
		HasTokenUsage:             true,
		TokenFields:               []string{"input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens"},
		MissingTokenFields:        []string{"total_tokens"}, // Codex CLI 0.155.1 明确不提供 total_tokens
		HasCachedTokens:           true,
		HasReasoningTokens:        true,
		TokenReportingGranularity: "per_turn",
		UsagePreservedOnToolFailure: true,
		PreflightModelIdentifiable:  false,
		UnsupportedCapabilities: map[string]string{
			"tool_output_interception": "Codex CLI exec 模式沙箱由內部 bwrap/環境控制，無法在送入模型前攔截過濾工具輸出",
			"history_dynamic_rewrite":  "單次 CLI invocation 無法動態替換歷史上下文",
			"context_limit_query":      "CLI 未提供查詢目前模型 Context Window 上限之指令",
			"stdout_model_identity":    "stdout JSONL 事件流未包含使用的具體 model 名稱",
		},
	}
}

// GetCodex01550Matrix 回傳針對 Codex CLI 0.155.0 之能力矩陣（標記未安裝驗證）
func GetCodex01550Matrix() CodexContextCapabilityMatrix {
	return CodexContextCapabilityMatrix{
		BackendID:                 "codex",
		TargetVersion:             "0.155.0",
		ProbeDate:                 "2026-09-21",
		Status:                    "unverified_not_installed",
		HasTokenUsage:             true,
		TokenFields:               []string{"input_tokens", "cached_input_tokens", "output_tokens"},
		MissingTokenFields:        []string{"total_tokens"},
		HasCachedTokens:           true,
		HasReasoningTokens:        false,
		TokenReportingGranularity: "per_turn",
		UsagePreservedOnToolFailure: true,
		PreflightModelIdentifiable:  false,
		UnsupportedCapabilities: map[string]string{
			"tool_output_interception": "Codex 0.155.0 無法在送入模型前攔截過濾工具輸出",
			"history_dynamic_rewrite":  "0.155.0 無動態歷史替換 API",
			"context_limit_query":      "0.155.0 CLI 未提供查詢 Context Window 上限指令",
			"stdout_model_identity":    "0.155.0 JSONL 事件未包含 model 欄位",
		},
	}
}

// 靜態去敏樣本：Codex 0.155.1 單回合成功
const CodexFixture_0_155_1_SingleTurn = `{"type":"thread.started","thread_id":"01a0c294-a4d4-7181-b605-5672057326e3"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"PONG"}}
{"type":"turn.completed","usage":{"input_tokens":17223,"cached_input_tokens":12288,"cache_write_input_tokens":0,"output_tokens":6,"reasoning_output_tokens":0}}`

// 靜態去敏樣本：Codex 0.155.1 工具失敗但仍包含用量（exit 42）
const CodexFixture_0_155_1_ToolFailure = `{"type":"thread.started","thread_id":"01a0c296-25a8-7260-8e83-52fca3fc7219"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Running command"}}
{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"/bin/bash -lc 'exit 42'","aggregated_output":"","exit_code":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"/bin/bash -lc 'exit 42'","aggregated_output":"","exit_code":42,"status":"failed"}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"Error: exit 42 failed"}}
{"type":"turn.completed","usage":{"input_tokens":34359,"cached_input_tokens":29184,"cache_write_input_tokens":0,"output_tokens":70,"reasoning_output_tokens":0}}`

// 靜態去敏樣本：Codex 0.155.0 歷史樣本
const CodexFixture_0_155_0_Legacy = `{"type":"thread.started","thread_id":"mock-thread-001"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Legacy PONG"}}
{"type":"turn.completed","usage":{"input_tokens":15000,"cached_input_tokens":10000,"output_tokens":15}}`

// TestCodexProbe_HermeticFixtures 驗證 Codex 雙版本之去敏樣本解析與能力矩陣（離線可重現）
func TestCodexProbe_HermeticFixtures(t *testing.T) {
	// 1. 驗證 0.155.1 單回合樣本
	s1 := bufio.NewScanner(strings.NewReader(CodexFixture_0_155_1_SingleTurn))
	var usageFound map[string]interface{}
	for s1.Scan() {
		evt, err := ParseJSONLLine(s1.Text())
		if err != nil {
			t.Fatalf("parse error on 0.155.1 fixture: %v", err)
		}
		if evt != nil && evt.Type == "turn.completed" {
			usageFound = evt.Usage
		}
	}
	if usageFound == nil {
		t.Fatalf("expected usage in 0.155.1 single turn fixture")
	}
	if inTok, ok := usageFound["input_tokens"].(float64); !ok || inTok != 17223 {
		t.Errorf("expected input_tokens 17223, got %v", usageFound["input_tokens"])
	}
	if outTok, ok := usageFound["output_tokens"].(float64); !ok || outTok != 6 {
		t.Errorf("expected output_tokens 6, got %v", usageFound["output_tokens"])
	}
	// 驗證 total_tokens 缺失（nil/absent）
	if _, hasTotal := usageFound["total_tokens"]; hasTotal {
		t.Errorf("expected total_tokens to be absent in codex usage object")
	}

	// 2. 驗證 0.155.1 工具失敗後依然保留用量
	s2 := bufio.NewScanner(strings.NewReader(CodexFixture_0_155_1_ToolFailure))
	var failUsage map[string]interface{}
	for s2.Scan() {
		evt, _ := ParseJSONLLine(s2.Text())
		if evt != nil && evt.Type == "turn.completed" {
			failUsage = evt.Usage
		}
	}
	if failUsage == nil {
		t.Fatalf("expected usage in tool failure fixture")
	}
	if inTok, ok := failUsage["input_tokens"].(float64); !ok || inTok != 34359 {
		t.Errorf("expected input_tokens 34359 on tool failure, got %v", failUsage["input_tokens"])
	}

	// 3. 驗證能力矩陣之 missing fields 與 unsupported 項目
	m1551 := GetCodex01551Matrix()
	if len(m1551.MissingTokenFields) == 0 || m1551.MissingTokenFields[0] != "total_tokens" {
		t.Errorf("expected total_tokens in MissingTokenFields")
	}
	if m1551.PreflightModelIdentifiable {
		t.Errorf("expected preflight model to be unidentifiable from stdout")
	}

	// 4. 驗證 0.155.0 未安裝狀態明確標註
	m1550 := GetCodex01550Matrix()
	if m1550.Status != "unverified_not_installed" {
		t.Errorf("expected 0.155.0 to be marked unverified_not_installed, got %q", m1550.Status)
	}
}

// TestCodexProbe_Live 針對本機已安裝之 Codex 進行即時探測
func TestCodexProbe_Live(t *testing.T) {
	p, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex binary not found; skipping live probe")
	}

	out, err := exec.Command(p, "--version").Output()
	if err != nil {
		t.Skipf("codex --version failed: %v", err)
	}
	verStr := strings.TrimSpace(string(out))
	t.Logf("installed codex version: %s", verStr)

	// 若非 0.155.1，記錄版本差異
	if !strings.Contains(verStr, "0.155.1") {
		t.Logf("installed version %q differs from target 0.155.1; skipping live assertion", verStr)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, p, "exec",
		"--json",
		"--ephemeral",
		"--skip-git-repo-check",
		"-C", "/tmp",
		"Respond with exactly PONG",
	)
	cmdOutput, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("live codex exec failed (credential/network missing): %v, out: %s", err, string(cmdOutput))
	}

	scanner := bufio.NewScanner(strings.NewReader(string(cmdOutput)))
	foundTurnCompleted := false
	foundUsage := false
	hasTotalTokens := false
	modelFoundInJSONL := false

	for scanner.Scan() {
		line := scanner.Text()
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err == nil {
			if typ, ok := raw["type"].(string); ok && typ == "turn.completed" {
				foundTurnCompleted = true
				if usage, ok := raw["usage"].(map[string]interface{}); ok {
					foundUsage = true
					if _, hasTotal := usage["total_tokens"]; hasTotal {
						hasTotalTokens = true
					}
				}
			}
			if _, hasModel := raw["model"]; hasModel {
				modelFoundInJSONL = true
			}
		}
	}

	if !foundTurnCompleted {
		t.Errorf("expected turn.completed in live codex output")
	}
	if !foundUsage {
		t.Errorf("expected usage object in turn.completed")
	}
	if hasTotalTokens {
		t.Errorf("unexpected total_tokens in codex usage; expected only input/output/cached")
	}
	if modelFoundInJSONL {
		t.Errorf("unexpected model field found in top-level JSONL; check if CLI contract changed")
	}
}
