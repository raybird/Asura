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

// 靜態去敏樣本：Codex 0.155.0 真實探測單回合成功
const CodexFixture_0_155_0_SingleTurn = `{"type":"thread.started","thread_id":"01a0c2d9-5b16-7de3-a30e-41cec6b1319d"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"PONG"}}
{"type":"turn.completed","usage":{"input_tokens":17224,"cached_input_tokens":7424,"cache_write_input_tokens":0,"output_tokens":6,"reasoning_output_tokens":0}}`

// 靜態去敏樣本：Codex 0.155.0 真實探測工具失敗（仍完整回報 token 用量）
const CodexFixture_0_155_0_ToolFailure = `{"type":"thread.started","thread_id":"01a0c2d9-8a88-7990-adc8-b38ebf8f6eb4"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"I’ll run false."}}
{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"/bin/bash -lc false"}}
{"type":"item.completed","item":{"id":"item_1","type":"command_execution","exit_code":1,"status":"failed"}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"false failed"}}
{"type":"turn.completed","usage":{"input_tokens":34352,"cached_input_tokens":33792,"cache_write_input_tokens":0,"output_tokens":68,"reasoning_output_tokens":0}}`

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
		t.Fatalf("expected usage on 0.155.1 tool failure fixture")
	}
	if inTok, ok := failUsage["input_tokens"].(float64); !ok || inTok != 34359 {
		t.Errorf("expected input_tokens 34359 on tool failure, got %v", failUsage["input_tokens"])
	}

	// 3. 驗證 0.155.0 真實 probe 樣本解析
	s3 := bufio.NewScanner(strings.NewReader(CodexFixture_0_155_0_SingleTurn))
	var u0 map[string]interface{}
	for s3.Scan() {
		evt, err := ParseJSONLLine(s3.Text())
		if err != nil {
			t.Fatalf("parse error on 0.155.0 fixture: %v", err)
		}
		if evt != nil && evt.Type == "turn.completed" {
			u0 = evt.Usage
		}
	}
	if u0 == nil || u0["input_tokens"].(float64) != 17224 || u0["output_tokens"].(float64) != 6 {
		t.Errorf("unexpected usage on 0.155.0: %+v", u0)
	}

	// 4. 驗證能力矩陣之 missing fields 與 unsupported 項目
	m1551 := GetCodex01551Matrix()
	if len(m1551.MissingTokenFields) == 0 || m1551.MissingTokenFields[0] != "total_tokens" {
		t.Errorf("expected total_tokens in MissingTokenFields")
	}
	if m1551.PreflightModelIdentifiable {
		t.Errorf("expected preflight model to be unidentifiable from stdout")
	}

	// 5. 驗證 0.155.0 探測狀態與矩陣
	m1550 := GetCodex01550Matrix()
	if m1550.Status != "verified_probe_artifact" {
		t.Errorf("expected 0.155.0 to be marked verified_probe_artifact, got %q", m1550.Status)
	}
	if len(m1550.MissingTokenFields) == 0 || m1550.MissingTokenFields[0] != "total_tokens" {
		t.Errorf("expected total_tokens to be missing in 0.155.0")
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

	// 支援驗證 0.155.0 (pinned) 與 0.155.1
	if !strings.Contains(verStr, "0.155.0") && !strings.Contains(verStr, "0.155.1") {
		t.Skipf("installed version %q is neither 0.155.0 nor 0.155.1; skipping live assertion", verStr)
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
