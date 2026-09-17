package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"asura/internal/engine"
)

func TestOracleStage_Success(t *testing.T) {
	jsonOutput := `{
		"goal": "Refactor auth",
		"constraints": ["No breaking change"],
		"steps": ["Step 1", "Step 2"],
		"expected_outcome": "Pass all tests"
	}`

	mockRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		if workspace != "/test/ws" {
			t.Errorf("expected workspace /test/ws, got %s", workspace)
		}
		return jsonOutput, nil
	}

	stage := NewOracleStage(mockRunner)
	execCtx := &engine.ExecutionContext{
		OriginalPrompt: "Please refactor auth",
		Workspace:      "/test/ws",
	}

	plan, err := stage.Execute(context.Background(), execCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan.Goal != "Refactor auth" {
		t.Errorf("expected goal 'Refactor auth', got %s", plan.Goal)
	}
	if len(plan.Steps) != 2 || plan.Steps[0] != "Step 1" {
		t.Errorf("unexpected steps: %v", plan.Steps)
	}
}

func TestOracleStage_MarkdownJSON(t *testing.T) {
	mdOutput := "Here is the plan:\n```json\n{\n  \"goal\": \"Add logging\",\n  \"steps\": [\"Log req\", \"Log resp\"]\n}\n```\nGood luck!"

	mockRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		return mdOutput, nil
	}

	stage := NewOracleStage(mockRunner)
	execCtx := &engine.ExecutionContext{OriginalPrompt: "Add logging"}

	plan, err := stage.Execute(context.Background(), execCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Goal != "Add logging" {
		t.Errorf("expected goal 'Add logging', got %s", plan.Goal)
	}
	if len(plan.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(plan.Steps))
	}
}

func TestOracleStage_RunnerError(t *testing.T) {
	expectedErr := errors.New("model offline")
	mockRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		return "", expectedErr
	}

	stage := NewOracleStage(mockRunner)
	execCtx := &engine.ExecutionContext{OriginalPrompt: "test"}

	_, err := stage.Execute(context.Background(), execCtx)
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}
}

func TestCriticStage_Success(t *testing.T) {
	jsonOutput := `{
		"verdict": "pass",
		"findings": ["Clean code", "Tests pass"],
		"summary": "Looks great"
	}`

	mockRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		return jsonOutput, nil
	}

	stage := NewCriticStage(mockRunner)
	execCtx := &engine.ExecutionContext{OriginalPrompt: "Fix bug"}
	plan := &OraclePlan{Goal: "Fix bug"}

	review, err := stage.Execute(context.Background(), execCtx, plan, "bug fixed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if review.Verdict != VerdictPass {
		t.Errorf("expected verdict pass, got %s", review.Verdict)
	}
	if len(review.Findings) != 2 {
		t.Errorf("expected 2 findings, got %d", len(review.Findings))
	}
	if review.Summary != "Looks great" {
		t.Errorf("expected summary 'Looks great', got %s", review.Summary)
	}
}

func TestCriticStage_MarkdownJSON(t *testing.T) {
	mdOutput := "Review result:\n```json\n{\n  \"verdict\": \"needs_attention\",\n  \"findings\": [\"Missing nil check\"]\n}\n```"

	mockRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		return mdOutput, nil
	}

	stage := NewCriticStage(mockRunner)
	review, err := stage.Execute(context.Background(), &engine.ExecutionContext{}, &OraclePlan{}, "done")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if review.Verdict != VerdictNeedsAttention {
		t.Errorf("expected needs_attention, got %s", review.Verdict)
	}
	if len(review.Findings) != 1 || review.Findings[0] != "Missing nil check" {
		t.Errorf("unexpected findings: %v", review.Findings)
	}
}

func TestCriticStage_RunnerError(t *testing.T) {
	expectedErr := errors.New("critic timeout")
	mockRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		return "", expectedErr
	}

	stage := NewCriticStage(mockRunner)
	_, err := stage.Execute(context.Background(), &engine.ExecutionContext{}, &OraclePlan{}, "done")
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}
}

func TestOracleStage_InvalidJSON_FailClosed(t *testing.T) {
	mockRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		return "This is plain text without valid JSON plan", nil
	}

	stage := NewOracleStage(mockRunner)
	_, err := stage.Execute(context.Background(), &engine.ExecutionContext{OriginalPrompt: "test"})
	if err == nil {
		t.Fatalf("expected error for invalid json, got nil")
	}
}

func TestOracleStage_SemanticValidation_FailClosed(t *testing.T) {
	// Case 1: empty goal
	stage1 := NewOracleStage(func(ctx context.Context, ws, p string) (string, error) {
		return `{"goal": "", "steps": ["step 1"]}`, nil
	})
	if _, err := stage1.Execute(context.Background(), &engine.ExecutionContext{}); err == nil {
		t.Errorf("expected error for empty goal, got nil")
	}

	// Case 2: empty steps
	stage2 := NewOracleStage(func(ctx context.Context, ws, p string) (string, error) {
		return `{"goal": "my goal", "steps": []}`, nil
	})
	if _, err := stage2.Execute(context.Background(), &engine.ExecutionContext{}); err == nil {
		t.Errorf("expected error for empty steps, got nil")
	}

	// Case 3: blank step
	stage3 := NewOracleStage(func(ctx context.Context, ws, p string) (string, error) {
		return `{"goal": "my goal", "steps": ["  "]}`, nil
	})
	if _, err := stage3.Execute(context.Background(), &engine.ExecutionContext{}); err == nil {
		t.Errorf("expected error for blank step, got nil")
	}
}

func TestCriticStage_InvalidJSON_FailClosed(t *testing.T) {
	mockRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		return "I am a critic but not returning JSON", nil
	}

	stage := NewCriticStage(mockRunner)
	_, err := stage.Execute(context.Background(), &engine.ExecutionContext{}, &OraclePlan{}, "done")
	if err == nil {
		t.Fatalf("expected error for invalid json, got nil")
	}
}

func TestCriticStage_InvalidVerdict_FailClosed(t *testing.T) {
	mockRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		return `{"verdict": "unknown_verdict", "summary": "hmm"}`, nil
	}

	stage := NewCriticStage(mockRunner)
	_, err := stage.Execute(context.Background(), &engine.ExecutionContext{}, &OraclePlan{}, "done")
	if err == nil {
		t.Fatalf("expected error for invalid verdict, got nil")
	}
}

func TestCriticStage_ComposePromptContext(t *testing.T) {
	var capturedPrompt string
	mockRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		capturedPrompt = prompt
		return `{"verdict": "pass", "summary": "ok"}`, nil
	}

	stage := NewCriticStage(mockRunner)
	execCtx := &engine.ExecutionContext{
		OriginalPrompt: "Write code",
		ProjectMemories: []engine.ProjectMemorySnapshot{
			{ID: "mem-1", Content: "No plain text passwords"},
		},
	}
	_, err := stage.Execute(context.Background(), execCtx, &OraclePlan{Goal: "Write code"}, "done")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(capturedPrompt, "mem-1") || !strings.Contains(capturedPrompt, "No plain text passwords") {
		t.Errorf("expected critic prompt to contain sealed project memories, got: %s", capturedPrompt)
	}
}
