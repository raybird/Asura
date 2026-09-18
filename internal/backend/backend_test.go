package backend

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExecutionRequest_JSON(t *testing.T) {
	req := ExecutionRequest{
		Role:      RoleOracle,
		Workspace: "/test/workspace",
		Prompt:    "Plan something",
		Metadata: map[string]string{
			"task_id": "task-123",
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal ExecutionRequest: %v", err)
	}

	var parsed ExecutionRequest
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal ExecutionRequest: %v", err)
	}

	if parsed.Role != RoleOracle {
		t.Errorf("expected role %s, got %s", RoleOracle, parsed.Role)
	}
	if parsed.Workspace != "/test/workspace" {
		t.Errorf("expected workspace /test/workspace, got %s", parsed.Workspace)
	}
	if parsed.Metadata["task_id"] != "task-123" {
		t.Errorf("expected metadata task_id = task-123, got %v", parsed.Metadata)
	}
}

func TestExecutionResult_JSON(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	res := ExecutionResult{
		BackendID:      "codex",
		BackendVersion: "0.155.0",
		Output:         "Finished successfully",
		PartialOutput:  "Intermediate output",
		Events: []Event{
			NewTextEvent("hello"),
			NewToolEvent("bash", "completed"),
		},
		FailureClass: FailureNone,
		StartedAt:    now,
		CompletedAt:  now.Add(time.Second),
	}

	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("failed to marshal ExecutionResult: %v", err)
	}

	var parsed ExecutionResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal ExecutionResult: %v", err)
	}

	if parsed.BackendID != "codex" || parsed.BackendVersion != "0.155.0" {
		t.Errorf("expected backend id codex v0.155.0, got %s %s", parsed.BackendID, parsed.BackendVersion)
	}
	if len(parsed.Events) != 2 {
		t.Errorf("expected 2 events, got %d", len(parsed.Events))
	}
}

func TestExecutionError(t *testing.T) {
	baseErr := errors.New("underlying network failure")
	execErr := NewExecutionError("codex", FailureRateLimit, baseErr, "partial log content")

	if !errors.Is(execErr, baseErr) {
		t.Errorf("expected execErr to unwrap to baseErr")
	}

	errStr := execErr.Error()
	if !strings.Contains(errStr, "rate_limited") || !strings.Contains(errStr, "codex") {
		t.Errorf("unexpected error string: %s", errStr)
	}
	if !strings.Contains(errStr, "partial output preserved") {
		t.Errorf("expected error string to mention partial output, got: %s", errStr)
	}
}
