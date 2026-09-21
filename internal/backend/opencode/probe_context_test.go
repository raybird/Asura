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

// 靜態去敏樣本：OpenCode 1.18.23 真實探測單回合成功
const OpenCodeFixture_1_18_23_SingleTurn = `{"type":"step_start","timestamp":1789975378850,"sessionID":"ses_f3d275f9dffeipJ50b2PMUtxtH","part":{"id":"prt_0c2d8bb9e001SQvJ4Qe8z85jMN","type":"step-start"}}
{"type":"text","timestamp":1789975379110,"sessionID":"ses_f3d275f9dffeipJ50b2PMUtxtH","part":{"id":"prt_0c2d8bba0001arCQ8hziy9vxsS","type":"text","text":"PONG"}}
{"type":"step_finish","timestamp":1789975379297,"sessionID":"ses_f3d275f9dffeipJ50b2PMUtxtH","part":{"id":"prt_0c2d8bd5c001T8X98ST01OuoCs","reason":"stop","type":"step-finish","tokens":{"total":24496,"input":24490,"output":6,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0}}`

// 靜態去敏樣本：OpenCode 1.18.23 真實探測工具執行失敗（仍完整回報 token 用量）
const OpenCodeFixture_1_18_23_ToolFailure = `{"type":"step_start","timestamp":1789975394080,"sessionID":"ses_f3d272b01ffeEIl4Vc5Y2DIblz","part":{"id":"prt_0c2d8f71b001bWoEMgDuCZ1IFM","type":"step-start"}}
{"type":"tool_use","timestamp":1789975398069,"sessionID":"ses_f3d272b01ffeEIl4Vc5Y2DIblz","part":{"type":"tool","tool":"bash","state":{"status":"completed","output":"(no output)","metadata":{"exit":1}},"title":"false"}}
{"type":"step_finish","timestamp":1789975398222,"sessionID":"ses_f3d272b01ffeEIl4Vc5Y2DIblz","part":{"id":"prt_0c2d90749001lN37FsCt3h5WGy","reason":"tool-calls","type":"step-finish","tokens":{"total":24559,"input":24493,"output":36,"reasoning":30,"cache":{"write":0,"read":0}},"cost":0}}
{"type":"step_start","timestamp":1789975401146,"sessionID":"ses_f3d272b01ffeEIl4Vc5Y2DIblz","part":{"id":"prt_0c2d912b2001ojVEXtiXgiVjSM","type":"step-start"}}
{"type":"text","timestamp":1789975401870,"sessionID":"ses_f3d272b01ffeEIl4Vc5Y2DIblz","part":{"id":"prt_0c2d912b6001OiqcxMdp6DNiNu","type":"text","text":"failed with exit code 1"}}
{"type":"step_finish","timestamp":1789975402036,"sessionID":"ses_f3d272b01ffeEIl4Vc5Y2DIblz","part":{"id":"prt_0c2d9162f001sR2hfxhQsAhi0T","reason":"stop","type":"step-finish","tokens":{"total":24603,"input":253,"output":30,"reasoning":0,"cache":{"write":0,"read":24320}},"cost":0}}`

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
		if evt != nil && evt.Tokens != nil && evt.Tokens.Total != nil && *evt.Tokens.Total > 0 {
			lastTokens = evt.Tokens
		}
	}
	if lastTokens == nil {
		t.Fatalf("expected tokens in 1.18.31 single turn fixture")
	}
	if *lastTokens.Input != 21516 || *lastTokens.Output != 6 || *lastTokens.Total != 21522 {
		t.Errorf("unexpected token counts: %+v", *lastTokens)
	}

	// 2. 驗證 1.18.31 工具失敗後依然保留用量
	s2 := bufio.NewScanner(strings.NewReader(OpenCodeFixture_1_18_31_ToolFailure))
	finishCount := 0
	for s2.Scan() {
		evt, _ := ParseEventLine(s2.Text())
		if evt != nil && evt.Tokens != nil && evt.Tokens.Total != nil && *evt.Tokens.Total > 0 {
			finishCount++
		}
	}
	if finishCount != 2 {
		t.Errorf("expected 2 step_finish events with tokens, got %d", finishCount)
	}

	// 3. 驗證 1.18.23 真實 probe 樣本解析
	s3 := bufio.NewScanner(strings.NewReader(OpenCodeFixture_1_18_23_SingleTurn))
	var t23 *TokenUsage
	for s3.Scan() {
		evt, err := ParseEventLine(s3.Text())
		if err != nil {
			t.Fatalf("parse error on 1.18.23 fixture: %v", err)
		}
		if evt != nil && evt.Tokens != nil && evt.Tokens.Total != nil && *evt.Tokens.Total > 0 {
			t23 = evt.Tokens
		}
	}
	if t23 == nil || *t23.Input != 24490 || *t23.Output != 6 || *t23.Total != 24496 {
		t.Errorf("unexpected token counts on 1.18.23: %+v", t23)
	}

	// 4. 驗證能力矩陣之 unsupported 項目非空且有理由
	m31 := GetOpenCode11831Matrix()
	if len(m31.UnsupportedCapabilities) == 0 {
		t.Errorf("expected unsupported capabilities in 1.18.31 matrix")
	}
	if m31.PreflightModelIdentifiable {
		t.Errorf("expected preflight model to be unidentifiable from stdout")
	}

	// 5. 驗證 1.18.23 探測狀態與矩陣
	m23 := GetOpenCode11823Matrix()
	if m23.Status != "verified_probe_artifact" {
		t.Errorf("expected 1.18.23 to be marked verified_probe_artifact, got %q", m23.Status)
	}
	if !m23.HasTokenUsage || !m23.HasCachedTokens {
		t.Errorf("expected 1.18.23 to have token usage and cache")
	}
}

// TestOpenCodeProbe_Live 針對環境已安裝之 OpenCode 進行即時探測
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

	// 支援驗證 1.18.23 (pinned) 與 1.18.31
	if !strings.Contains(verStr, "1.18.23") && !strings.Contains(verStr, "1.18.31") {
		t.Skipf("installed version %q is neither 1.18.23 nor 1.18.31; skipping live assertion", verStr)
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
