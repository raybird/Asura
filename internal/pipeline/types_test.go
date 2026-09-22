package pipeline_test

import (
	"encoding/json"
	"testing"

	"asura/internal/backend"
	"asura/internal/contextwin"
	"asura/internal/pipeline"
)

func TestPipelineTrace_SerializationWithAdmissionAndUsage(t *testing.T) {
	inTokens := int64(1000)
	outTokens := int64(0) // 真實零值
	trace := pipeline.PipelineTrace{
		Mode:      pipeline.ModeThreeFace,
		BackendID: "opencode",
		Stages: []pipeline.StageTrace{
			{
				Name:           "oracle",
				Status:         pipeline.StageCompleted,
				DurationMs:     250,
				BackendID:      "opencode",
				BackendVersion: "1.18.31",
				Admission: &contextwin.StageAdmission{
					Stage:       "oracle",
					Verdict:     contextwin.VerdictAdmitted,
					Policy:      "observe",
					Model:       "gpt-4o",
					MaxTokens:   128000,
					LimitSource: contextwin.LimitSourceCatalog,
					Estimate: contextwin.StageEstimate{
						Stage:                 "oracle",
						EstimatedPromptTokens: 800,
						ReservedOutputTokens:  160,
						TotalEstimatedTokens:  960,
						Provenance:            backend.ProvenanceEstimated,
						EstimationMethod:      "heuristic-char-ratio-v1",
					},
				},
				Usage: &backend.TokenUsage{
					InputTokens:  &inTokens,
					OutputTokens: &outTokens,
					TotalTokens:  nil, // 缺失
				},
				UsageSource: backend.ProvenanceExact,
			},
		},
	}

	data, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded pipeline.PipelineTrace
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(decoded.Stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(decoded.Stages))
	}
	st := decoded.Stages[0]
	if st.Admission == nil {
		t.Fatal("expected admission to be non-nil")
	}
	if st.Admission.Verdict != contextwin.VerdictAdmitted {
		t.Errorf("expected verdict admitted, got %s", st.Admission.Verdict)
	}
	if st.Usage == nil {
		t.Fatal("expected usage to be non-nil")
	}
	if st.Usage.InputTokens == nil || *st.Usage.InputTokens != 1000 {
		t.Errorf("expected InputTokens 1000, got %v", st.Usage.InputTokens)
	}
	if st.Usage.OutputTokens == nil || *st.Usage.OutputTokens != 0 {
		t.Errorf("expected OutputTokens 0 (explicit zero preserved), got %v", st.Usage.OutputTokens)
	}
	if st.Usage.TotalTokens != nil {
		t.Errorf("expected TotalTokens nil (unknown preserved), got %v", *st.Usage.TotalTokens)
	}
	if st.UsageSource != backend.ProvenanceExact {
		t.Errorf("expected UsageSource exact, got %s", st.UsageSource)
	}
}
