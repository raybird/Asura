package contextwin_test

import (
	"math"
	"testing"

	"asura/internal/backend"
	"asura/internal/contextwin"
)

func TestHeuristicEstimator_Estimate(t *testing.T) {
	est := contextwin.NewHeuristicEstimator(0.20)

	tests := []struct {
		name          string
		stage         string
		prompt        string
		reserveRatio  float64
		minPrompt     int64
		maxPrompt     int64
		expectZero    bool
	}{
		{
			name:       "empty prompt",
			stage:      "oracle",
			prompt:     "",
			expectZero: true,
		},
		{
			name:         "pure english ascii",
			stage:        "striker",
			prompt:       "Hello world, this is a test prompt for token estimation.", // 56 chars -> ~16 tokens
			reserveRatio: 0.20,
			minPrompt:    10,
			maxPrompt:    30,
		},
		{
			name:         "traditional chinese cjk",
			stage:        "critic",
			prompt:       "這是一個測試繁體中文的提示詞，用來驗證預估算力。", // 24 runes -> ~48 tokens
			reserveRatio: 0.20,
			minPrompt:    30,
			maxPrompt:    60,
		},
		{
			name:         "mixed english and chinese",
			stage:        "striker",
			prompt:       "Asura 是一個多後端協同的 CLI 工具，支援 OpenCode 與 Codex。",
			reserveRatio: 0.20,
			minPrompt:    30,
			maxPrompt:    70,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := est.Estimate(tt.stage, tt.prompt)

			if tt.expectZero {
				if res.EstimatedPromptTokens != 0 || res.TotalEstimatedTokens != 0 || res.ReservedOutputTokens != 0 {
					t.Errorf("expected 0 tokens for empty prompt, got %+v", res)
				}
				return
			}

			if res.EstimatedPromptTokens < tt.minPrompt || res.EstimatedPromptTokens > tt.maxPrompt {
				t.Errorf("prompt tokens %d out of expected range [%d, %d]",
					res.EstimatedPromptTokens, tt.minPrompt, tt.maxPrompt)
			}

			// 驗證輸出保留比例 20%
			expectedReserve := int64(float64(res.EstimatedPromptTokens)*0.20 + 0.999999) // ceil
			if res.ReservedOutputTokens != expectedReserve {
				t.Errorf("reserved tokens mismatch: got %d, want %d", res.ReservedOutputTokens, expectedReserve)
			}

			if res.TotalEstimatedTokens != res.EstimatedPromptTokens+res.ReservedOutputTokens {
				t.Errorf("total tokens mismatch: got %d, want %d",
					res.TotalEstimatedTokens, res.EstimatedPromptTokens+res.ReservedOutputTokens)
			}

			if res.Provenance != backend.ProvenanceEstimated {
				t.Errorf("expected ProvenanceEstimated, got %s", res.Provenance)
			}
			if res.Stage != tt.stage {
				t.Errorf("stage mismatch: got %s, want %s", res.Stage, tt.stage)
			}
			if res.EstimationMethod != "heuristic-char-ratio-v1" {
				t.Errorf("expected method heuristic-char-ratio-v1, got %s", res.EstimationMethod)
			}
		})
	}
}

func TestHeuristicEstimator_CustomReserveRatio(t *testing.T) {
	est := contextwin.NewHeuristicEstimator(0.20)
	prompt := "Short prompt"

	res := est.EstimateWithReserve("oracle", prompt, 0.50)
	expectedReserve := int64(float64(res.EstimatedPromptTokens)*0.50 + 0.999999)
	if res.ReservedOutputTokens != expectedReserve {
		t.Errorf("custom reserve mismatch: got %d, want %d", res.ReservedOutputTokens, expectedReserve)
	}
}

func TestHeuristicEstimator_NaNAndInfDefense(t *testing.T) {
	est := contextwin.NewHeuristicEstimator(0.20)
	prompt := "Defense prompt for NaN and Inf"

	badRatios := []float64{
		math.NaN(),
		math.Inf(1),
		math.Inf(-1),
		-0.5,
		0.0,
		1.0,
		2.5,
	}

	for _, bad := range badRatios {
		res := est.EstimateWithReserve("striker", prompt, bad)
		if res.ReservedOutputTokens <= 0 {
			t.Errorf("expected positive reserved tokens for ratio %v, got %d", bad, res.ReservedOutputTokens)
		}
		if res.TotalEstimatedTokens <= res.EstimatedPromptTokens {
			t.Errorf("expected total tokens > prompt tokens for ratio %v, got total=%d, prompt=%d",
				bad, res.TotalEstimatedTokens, res.EstimatedPromptTokens)
		}
	}
}
