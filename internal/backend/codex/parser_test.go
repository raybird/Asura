package codex

import (
	"testing"

	"asura/internal/backend"
)

func TestExtractResultAndEvents_Success(t *testing.T) {
	stdout := `{"type":"thread.started","thread_id":"t-123"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"1","type":"agent_message","text":"Hello World"}}
{"type":"item.completed","item":{"id":"2","type":"command_execution","command":"ls -la","status":"completed"}}
{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":10,"cached_input_tokens":50}}`

	out, partial, events, failureClass, usage, err := ExtractResultAndEvents(stdout, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "Hello World" {
		t.Errorf("expected output 'Hello World', got %q", out)
	}
	if partial != "Hello World" {
		t.Errorf("expected partial 'Hello World', got %q", partial)
	}
	if failureClass != backend.FailureNone {
		t.Errorf("expected failure class None, got %s", failureClass)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
	if usage == nil {
		t.Fatalf("expected usage to be parsed")
	}
	if usage.InputTokens == nil || *usage.InputTokens != 100 {
		t.Errorf("expected InputTokens 100, got %v", usage.InputTokens)
	}
	if usage.OutputTokens == nil || *usage.OutputTokens != 10 {
		t.Errorf("expected OutputTokens 10, got %v", usage.OutputTokens)
	}
	if usage.CachedTokens == nil || *usage.CachedTokens != 50 {
		t.Errorf("expected CachedTokens 50, got %v", usage.CachedTokens)
	}
	// 驗證 total_tokens 缺失為 nil (符合 probe 發現)
	if usage.TotalTokens != nil {
		t.Errorf("expected TotalTokens to be nil for Codex, got %v", *usage.TotalTokens)
	}
}

func TestExtractResultAndEvents_RateLimit(t *testing.T) {
	stdout := `{"type":"item.completed","item":{"id":"1","type":"agent_message","text":"Some partial thought"}}
{"type":"error","message":"429 Too Many Requests: rate limit exceeded"}`

	out, partial, _, failureClass, usage, err := ExtractResultAndEvents(stdout, "")
	if err == nil {
		t.Fatalf("expected error for rate limit")
	}
	if failureClass != backend.FailureRateLimit {
		t.Errorf("expected FailureRateLimit, got %s", failureClass)
	}
	if partial != "Some partial thought" {
		t.Errorf("expected partial output preserved, got %q", partial)
	}
	if out != "" {
		t.Errorf("expected empty final output on error, got %q", out)
	}
	if usage != nil {
		t.Errorf("expected nil usage when no turn.completed event, got %+v", usage)
	}
}

func TestExtractResultAndEvents_AuthFailure(t *testing.T) {
	stdout := `{"type":"error","message":"unauthorized: invalid api key"}`

	_, _, _, failureClass, _, err := ExtractResultAndEvents(stdout, "")
	if err == nil {
		t.Fatalf("expected error for auth failure")
	}
	if failureClass != backend.FailureAuth {
		t.Errorf("expected FailureAuth, got %s", failureClass)
	}
}

func TestExtractResultAndEvents_EmptyResult(t *testing.T) {
	stdout := `{"type":"thread.started","thread_id":"t-123"}
{"type":"turn.completed"}`

	_, _, _, failureClass, _, err := ExtractResultAndEvents(stdout, "")
	if err == nil {
		t.Fatalf("expected error for empty result")
	}
	if failureClass != backend.FailureEmptyResult {
		t.Errorf("expected FailureEmptyResult, got %s", failureClass)
	}
}

func TestExtractResultAndEvents_FailurePreservesUsage(t *testing.T) {
	// 即使最後執行失敗或有非零 exit code，只要有 turn.completed 事件，usage 依然保留
	stdout := `{"type":"thread.started","thread_id":"t-123"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"1","type":"agent_message","text":"Failed step"}}
{"type":"error","message":"command execution exited with 42"}
{"type":"turn.completed","usage":{"input_tokens":500,"output_tokens":25}}`

	_, partial, _, failureClass, usage, err := ExtractResultAndEvents(stdout, "")
	if err == nil {
		t.Fatalf("expected error")
	}
	if failureClass != backend.FailureExecution {
		t.Errorf("expected FailureExecution, got %s", failureClass)
	}
	if partial != "Failed step" {
		t.Errorf("expected partial preserved, got %q", partial)
	}
	if usage == nil {
		t.Fatalf("expected usage to be preserved despite error")
	}
	if usage.InputTokens == nil || *usage.InputTokens != 500 {
		t.Errorf("expected InputTokens 500, got %v", usage.InputTokens)
	}
	if usage.OutputTokens == nil || *usage.OutputTokens != 25 {
		t.Errorf("expected OutputTokens 25, got %v", usage.OutputTokens)
	}
}
