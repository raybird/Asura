package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"asura/internal/engine"
)

func TestStrikerStage_DirectMode_WithoutPlan(t *testing.T) {
	var capturedPrompt string
	mockRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		capturedPrompt = prompt
		return "execution done", nil
	}

	striker := NewStrikerStage(mockRunner)
	execCtx := &engine.ExecutionContext{
		OriginalPrompt: "Run unit tests",
		Workspace:      "/repo",
		ProjectMemories: []engine.ProjectMemorySnapshot{
			{ID: "mem-1", Content: "Always use go test"},
		},
	}

	out, err := striker.Execute(context.Background(), execCtx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "execution done" {
		t.Errorf("expected 'execution done', got %s", out)
	}

	// Direct 模式下不應含有 Oracle Execution Plan
	if strings.Contains(capturedPrompt, "### Oracle Execution Plan") {
		t.Errorf("prompt should NOT contain Oracle Plan in direct mode")
	}
	if !strings.Contains(capturedPrompt, "Run unit tests") {
		t.Errorf("prompt should contain original prompt")
	}
	if !strings.Contains(capturedPrompt, "[mem-1]: Always use go test") {
		t.Errorf("prompt should contain project memory")
	}
}

func TestStrikerStage_ThreeFace_WithPlan(t *testing.T) {
	var capturedPrompt string
	mockRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		capturedPrompt = prompt
		return "plan executed", nil
	}

	striker := NewStrikerStage(mockRunner)
	execCtx := &engine.ExecutionContext{
		OriginalPrompt: "Implement feature X",
		Workspace:      "/repo",
	}
	plan := &OraclePlan{
		Goal:            "Feature X Implementation",
		Constraints:     []string{"No breaking changes"},
		Steps:           []string{"Create model", "Add route"},
		ExpectedOutcome: "Route works and tested",
	}

	out, err := striker.Execute(context.Background(), execCtx, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "plan executed" {
		t.Errorf("expected 'plan executed', got %s", out)
	}

	// Three-Face 模式下應包含 Oracle Plan
	if !strings.Contains(capturedPrompt, "### Oracle Execution Plan") {
		t.Errorf("prompt must contain Oracle Execution Plan block")
	}
	if !strings.Contains(capturedPrompt, "Goal: Feature X Implementation") {
		t.Errorf("prompt must contain goal")
	}
	if !strings.Contains(capturedPrompt, "- No breaking changes") {
		t.Errorf("prompt must contain constraints")
	}
	if !strings.Contains(capturedPrompt, "1. Create model") {
		t.Errorf("prompt must contain steps")
	}
	if !strings.Contains(capturedPrompt, "Implement feature X") {
		t.Errorf("prompt must contain original prompt")
	}
}

func TestStrikerStage_RunnerError(t *testing.T) {
	expectedErr := errors.New("command failed with exit code 1")
	mockRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		return "partial log", expectedErr
	}

	striker := NewStrikerStage(mockRunner)
	execCtx := &engine.ExecutionContext{OriginalPrompt: "Build"}

	out, err := striker.Execute(context.Background(), execCtx, nil)
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}
	if out != "partial log" {
		t.Errorf("expected partial output, got %s", out)
	}
}
