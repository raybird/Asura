package opencode_test

import (
	"testing"

	"asura/internal/backend/opencode"
)

func TestParseEventLine_Text(t *testing.T) {
	line := `{"type":"text","timestamp":1789109089572,"sessionID":"ses_123","part":{"id":"prt_1","type":"text","text":"hello world"}}`
	event, err := opencode.ParseEventLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatalf("expected event, got nil")
	}
	if event.Type != "text" {
		t.Fatalf("expected type text, got %s", event.Type)
	}
	if event.Text != "hello world" {
		t.Fatalf("expected text 'hello world', got %s", event.Text)
	}
	if event.SessionID != "ses_123" {
		t.Fatalf("expected sessionID ses_123, got %s", event.SessionID)
	}
}

func TestParseEventLine_ToolUse(t *testing.T) {
	line := `{"type":"tool_use","timestamp":1789109104570,"sessionID":"ses_123","part":{"type":"tool","tool":"apply_patch","callID":"call_abc","state":{"status":"completed","output":"Success"}}}`
	event, err := opencode.ParseEventLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != "tool_use" {
		t.Fatalf("expected type tool_use, got %s", event.Type)
	}
	if event.Tool != "apply_patch" {
		t.Fatalf("expected tool apply_patch, got %s", event.Tool)
	}
	if event.ToolStatus != "completed" {
		t.Fatalf("expected tool status completed, got %s", event.ToolStatus)
	}
}

func TestParseEventLine_StepFinish(t *testing.T) {
	line := `{"type":"step_finish","timestamp":1789109089718,"sessionID":"ses_123","part":{"id":"prt_2","reason":"stop","type":"step-finish","tokens":{"total":24185,"input":24180,"output":5}}}`
	event, err := opencode.ParseEventLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != "step_finish" {
		t.Fatalf("expected type step_finish, got %s", event.Type)
	}
	if event.Tokens.Total != 24185 || event.Tokens.Input != 24180 || event.Tokens.Output != 5 {
		t.Fatalf("unexpected token counts: %+v", event.Tokens)
	}
}

func TestParseEventLine_RateLimit429(t *testing.T) {
	line := `{"type":"error","timestamp":1789109089718,"sessionID":"ses_123","error":{"message":"Rate limit exceeded","statusCode":429}}`
	event, err := opencode.ParseEventLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !event.IsRateLimit {
		t.Fatalf("expected IsRateLimit=true, got false")
	}
}

func TestParseEventLine_Invalid(t *testing.T) {
	line := `non-json line from stdout or stderr`
	event, err := opencode.ParseEventLine(line)
	if err != nil {
		t.Fatalf("should not return error for non-json, got %v", err)
	}
	if event != nil {
		t.Fatalf("expected nil event for non-json line, got %+v", event)
	}
}
