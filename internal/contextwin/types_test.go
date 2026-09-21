package contextwin_test

import (
	"encoding/json"
	"testing"

	"asura/internal/backend"
	"asura/internal/contextwin"
)

func TestTypes_JSONSerialization(t *testing.T) {
	adm := contextwin.StageAdmission{
		Stage:       "striker",
		Verdict:     contextwin.VerdictRejected,
		Policy:      string(contextwin.PolicyReject),
		Model:       "gpt-4o",
		MaxTokens:   128000,
		LimitSource: contextwin.LimitSourceCatalog,
		Estimate: contextwin.StageEstimate{
			Stage:                 "striker",
			EstimatedPromptTokens: 110000,
			ReservedOutputTokens:  20000,
			TotalEstimatedTokens:  130000,
			Provenance:            backend.ProvenanceEstimated,
			EstimationMethod:      "heuristic-char-ratio-v1",
		},
		Reason: "estimated tokens 130000 exceeds limit 128000",
	}

	data, err := json.Marshal(adm)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded contextwin.StageAdmission
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if decoded.Stage != adm.Stage {
		t.Errorf("Stage mismatch: got %s, want %s", decoded.Stage, adm.Stage)
	}
	if decoded.Verdict != adm.Verdict {
		t.Errorf("Verdict mismatch: got %s, want %s", decoded.Verdict, adm.Verdict)
	}
	if decoded.Estimate.Provenance != backend.ProvenanceEstimated {
		t.Errorf("Provenance mismatch: got %s, want %s", decoded.Estimate.Provenance, backend.ProvenanceEstimated)
	}
	if decoded.Estimate.TotalEstimatedTokens != 130000 {
		t.Errorf("TotalEstimatedTokens mismatch: got %d, want 130000", decoded.Estimate.TotalEstimatedTokens)
	}
}

func TestTypes_BypassedOmitEmpty(t *testing.T) {
	adm := contextwin.StageAdmission{
		Stage:       "oracle",
		Verdict:     contextwin.VerdictBypassed,
		Policy:      string(contextwin.PolicyObserve),
		LimitSource: contextwin.LimitSourceUnknown,
		Estimate: contextwin.StageEstimate{
			Stage:                 "oracle",
			EstimatedPromptTokens: 500,
			ReservedOutputTokens:  100,
			TotalEstimatedTokens:  600,
			Provenance:            backend.ProvenanceEstimated,
			EstimationMethod:      "heuristic-char-ratio-v1",
		},
	}

	data, err := json.Marshal(adm)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if _, ok := raw["model"]; ok {
		t.Errorf("expected model to be omitted when empty, got %v", raw["model"])
	}
	if _, ok := raw["max_tokens"]; ok {
		t.Errorf("expected max_tokens to be omitted when empty, got %v", raw["max_tokens"])
	}
	if _, ok := raw["reason"]; ok {
		t.Errorf("expected reason to be omitted when empty, got %v", raw["reason"])
	}
}
