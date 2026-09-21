package storage_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"asura/internal/backend"
	"asura/internal/contextwin"
	"asura/internal/pipeline"
	"asura/internal/storage"
)

func TestTaskPipelineTrace_ContextWindowCompatibility(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_compat.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. 歷史無 Usage 任務相容性（向前相容）
	legacyTraceJSON := `{"mode":"direct","model_calls":1,"duration_ms":80,"stages":[{"name":"striker","status":"completed","duration_ms":80,"output":"hello"}]}`
	legacyTask := &storage.Task{
		ID:            "task-legacy-001",
		RequestID:     "req-legacy-001",
		Prompt:        "legacy prompt",
		Workspace:     "/tmp/ws",
		Status:        storage.StatusCompleted,
		ExecutionMode: "direct",
		PipelineTrace: legacyTraceJSON,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := store.CreateTask(ctx, legacyTask); err != nil {
		t.Fatalf("CreateTask legacy failed: %v", err)
	}

	gotLegacy, err := store.GetTask(ctx, legacyTask.ID)
	if err != nil {
		t.Fatalf("GetTask legacy failed: %v", err)
	}
	var decodedLegacy pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(gotLegacy.PipelineTrace), &decodedLegacy); err != nil {
		t.Fatalf("unmarshal legacy trace failed: %v", err)
	}
	if len(decodedLegacy.Stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(decodedLegacy.Stages))
	}
	if decodedLegacy.Stages[0].Admission != nil {
		t.Errorf("expected nil admission for legacy trace, got %+v", decodedLegacy.Stages[0].Admission)
	}
	if decodedLegacy.Stages[0].Usage != nil {
		t.Errorf("expected nil usage for legacy trace, got %+v", decodedLegacy.Stages[0].Usage)
	}

	// 2. 新任務完整保留性（含 Admission、部分缺失與真實零值、失敗後 usage）
	inTokens := int64(300)
	outTokens := int64(0) // 明確零值
	newTrace := pipeline.PipelineTrace{
		Mode:       pipeline.ModeThreeFace,
		BackendID:  "opencode",
		ModelCalls: 1,
		DurationMs: 150,
		Stages: []pipeline.StageTrace{
			{
				Name:           "oracle",
				Status:         pipeline.StageFailed,
				DurationMs:     150,
				BackendID:      "opencode",
				BackendVersion: "1.18.31",
				Admission: &contextwin.StageAdmission{
					Stage:       "oracle",
					Verdict:     contextwin.VerdictRejected,
					Policy:      "reject",
					Model:       "gpt-4o",
					MaxTokens:   100,
					LimitSource: contextwin.LimitSourceConfig,
					Estimate: contextwin.StageEstimate{
						Stage:                 "oracle",
						EstimatedPromptTokens: 120,
						ReservedOutputTokens:  24,
						TotalEstimatedTokens:  144,
						Provenance:            backend.ProvenanceEstimated,
						EstimationMethod:      "heuristic-char-ratio-v1",
					},
					Reason: "estimated tokens 144 exceeds limit 100",
				},
				Usage: &backend.TokenUsage{
					InputTokens:  &inTokens,
					OutputTokens: &outTokens,
					TotalTokens:  nil, // 缺失保持 nil
				},
				UsageSource: backend.ProvenanceExact,
				Error:       "context limit exceeded: estimated tokens 144 exceeds limit 100",
			},
		},
	}
	newTraceBytes, _ := json.Marshal(newTrace)

	newTask := &storage.Task{
		ID:            "task-new-001",
		RequestID:     "req-new-001",
		Prompt:        "new prompt with admission",
		Workspace:     "/tmp/ws",
		Status:        storage.StatusFailed,
		ExecutionMode: "three-face",
		PipelineTrace: string(newTraceBytes),
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := store.CreateTask(ctx, newTask); err != nil {
		t.Fatalf("CreateTask new failed: %v", err)
	}

	gotNew, err := store.GetTask(ctx, newTask.ID)
	if err != nil {
		t.Fatalf("GetTask new failed: %v", err)
	}
	var decodedNew pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(gotNew.PipelineTrace), &decodedNew); err != nil {
		t.Fatalf("unmarshal new trace failed: %v", err)
	}

	st := decodedNew.Stages[0]
	if st.Admission == nil || st.Admission.Verdict != contextwin.VerdictRejected {
		t.Errorf("expected verdict rejected in stored trace, got %+v", st.Admission)
	}
	if st.Admission.Estimate.TotalEstimatedTokens != 144 {
		t.Errorf("expected total tokens 144, got %d", st.Admission.Estimate.TotalEstimatedTokens)
	}
	if st.Usage == nil {
		t.Fatalf("expected non-nil usage")
	}
	if st.Usage.InputTokens == nil || *st.Usage.InputTokens != 300 {
		t.Errorf("expected input tokens 300, got %v", st.Usage.InputTokens)
	}
	if st.Usage.OutputTokens == nil || *st.Usage.OutputTokens != 0 {
		t.Errorf("expected explicit output tokens 0 preserved, got %v", st.Usage.OutputTokens)
	}
	if st.Usage.TotalTokens != nil {
		t.Errorf("expected missing total tokens nil preserved, got %v", st.Usage.TotalTokens)
	}
	if st.UsageSource != backend.ProvenanceExact {
		t.Errorf("expected usage source exact, got %s", st.UsageSource)
	}
}
