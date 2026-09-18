package pipeline

import (
	"encoding/json"
	"testing"
)

func TestExecutionMode(t *testing.T) {
	if ModeDirect != "direct" {
		t.Fatalf("expected ModeDirect to be 'direct', got %s", ModeDirect)
	}
	if ModeThreeFace != "three-face" {
		t.Fatalf("expected ModeThreeFace to be 'three-face', got %s", ModeThreeFace)
	}

	if !IsValidExecutionMode(ModeDirect) {
		t.Fatalf("expected ModeDirect to be valid")
	}
	if !IsValidExecutionMode(ModeThreeFace) {
		t.Fatalf("expected ModeThreeFace to be valid")
	}
	if IsValidExecutionMode("invalid-mode") {
		t.Fatalf("expected invalid-mode to be invalid")
	}
}

func TestOraclePlanSerialization(t *testing.T) {
	plan := OraclePlan{
		Goal:            "Refactor authentication module",
		Constraints:     []string{"No breaking changes", "Keep backward compatibility"},
		Steps:           []string{"Create interface", "Implement adapter", "Run regression tests"},
		ExpectedOutcome: "All tests pass without regression",
		Metadata: map[string]string{
			"model": "oracle-v1",
		},
	}

	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("failed to marshal OraclePlan: %v", err)
	}

	var decoded OraclePlan
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal OraclePlan: %v", err)
	}

	if decoded.Goal != plan.Goal {
		t.Errorf("expected Goal %s, got %s", plan.Goal, decoded.Goal)
	}
	if len(decoded.Constraints) != 2 || decoded.Constraints[0] != plan.Constraints[0] {
		t.Errorf("constraints mismatch: %v", decoded.Constraints)
	}
	if len(decoded.Steps) != 3 || decoded.Steps[1] != plan.Steps[1] {
		t.Errorf("steps mismatch: %v", decoded.Steps)
	}
	if decoded.ExpectedOutcome != plan.ExpectedOutcome {
		t.Errorf("expected ExpectedOutcome %s, got %s", plan.ExpectedOutcome, decoded.ExpectedOutcome)
	}
	if decoded.Metadata["model"] != "oracle-v1" {
		t.Errorf("expected metadata model oracle-v1, got %v", decoded.Metadata)
	}
}

func TestCriticReviewSerialization(t *testing.T) {
	review := CriticReview{
		Verdict:  VerdictPass,
		Findings: []string{"All requirements met", "Code follows guidelines"},
		Summary:  "High quality execution",
		Metadata: map[string]string{
			"reviewer": "critic-v1",
		},
	}

	data, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("failed to marshal CriticReview: %v", err)
	}

	var decoded CriticReview
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CriticReview: %v", err)
	}

	if decoded.Verdict != VerdictPass {
		t.Errorf("expected Verdict %s, got %s", VerdictPass, decoded.Verdict)
	}
	if len(decoded.Findings) != 2 {
		t.Errorf("expected 2 findings, got %d", len(decoded.Findings))
	}
	if decoded.Summary != review.Summary {
		t.Errorf("expected Summary %s, got %s", review.Summary, decoded.Summary)
	}
}

func TestPipelineTraceSerialization(t *testing.T) {
	trace := PipelineTrace{
		Mode: ModeThreeFace,
		OraclePlan: &OraclePlan{
			Goal: "Goal 1",
		},
		CriticReview: &CriticReview{
			Verdict: VerdictPass,
		},
		Stages: []StageTrace{
			{Name: "oracle", Status: StageCompleted, DurationMs: 120, Output: "plan generated"},
			{Name: "striker", Status: StageCompleted, DurationMs: 500, Output: "code updated"},
			{Name: "critic", Status: StageCompleted, DurationMs: 200, Output: "verdict pass"},
		},
		ModelCalls:  3,
		DurationMs:  820,
		FinalResult: "code updated",
	}

	data, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("failed to marshal PipelineTrace: %v", err)
	}

	var decoded PipelineTrace
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal PipelineTrace: %v", err)
	}

	if decoded.Mode != ModeThreeFace {
		t.Errorf("expected Mode %s, got %s", ModeThreeFace, decoded.Mode)
	}
	if decoded.ModelCalls != 3 {
		t.Errorf("expected 3 ModelCalls, got %d", decoded.ModelCalls)
	}
	if decoded.DurationMs != 820 {
		t.Errorf("expected DurationMs 820, got %d", decoded.DurationMs)
	}
	if len(decoded.Stages) != 3 {
		t.Errorf("expected 3 stages, got %d", len(decoded.Stages))
	}
	if decoded.Stages[1].Status != StageCompleted {
		t.Errorf("expected striker stage completed, got %s", decoded.Stages[1].Status)
	}
	if decoded.FinalResult != "code updated" {
		t.Errorf("expected FinalResult 'code updated', got %s", decoded.FinalResult)
	}
}
