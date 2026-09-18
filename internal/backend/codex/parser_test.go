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
{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":10}}`

	out, partial, events, failureClass, err := ExtractResultAndEvents(stdout, "")
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
}

func TestExtractResultAndEvents_RateLimit(t *testing.T) {
	stdout := `{"type":"item.completed","item":{"id":"1","type":"agent_message","text":"Some partial thought"}}
{"type":"error","message":"429 Too Many Requests: rate limit exceeded"}`

	out, partial, _, failureClass, err := ExtractResultAndEvents(stdout, "")
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
}

func TestExtractResultAndEvents_AuthFailure(t *testing.T) {
	stdout := `{"type":"error","message":"unauthorized: invalid api key"}`

	_, _, _, failureClass, err := ExtractResultAndEvents(stdout, "")
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

	_, _, _, failureClass, err := ExtractResultAndEvents(stdout, "")
	if err == nil {
		t.Fatalf("expected error for empty result")
	}
	if failureClass != backend.FailureEmptyResult {
		t.Errorf("expected FailureEmptyResult, got %s", failureClass)
	}
}
