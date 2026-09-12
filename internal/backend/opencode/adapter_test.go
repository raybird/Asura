package opencode

import (
	"context"
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
