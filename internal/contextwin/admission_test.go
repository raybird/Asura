package contextwin_test

import (
	"strings"
	"testing"

	"asura/internal/backend"
	"asura/internal/contextwin"
)

func TestAdmissionEngine_EvaluateStage(t *testing.T) {
	cat := contextwin.NewStaticCatalog()
	est := contextwin.NewHeuristicEstimator(0.20)
	engine := contextwin.NewAdmissionEngine(cat, est)

	tests := []struct {
		name         string
		stage        string
		prompt       string
		model        string
		cfg          contextwin.Config
		wantVerdict  contextwin.AdmissionVerdict
		wantSource   contextwin.LimitSource
		checkReason  string
	}{
		{
			name:   "catalog model within limit admitted",
			stage:  "oracle",
			prompt: "Simple task prompt",
			model:  "gpt-4o", // limit = 128000
			cfg: contextwin.Config{
				Policy: contextwin.PolicyReject,
			},
			wantVerdict: contextwin.VerdictAdmitted,
			wantSource:  contextwin.LimitSourceCatalog,
		},
		{
			name:   "catalog model exceeds limit with reject policy",
			stage:  "striker",
			prompt: strings.Repeat("This is a very long prompt to exceed tokens. ", 15000), // ~150k tokens
			model:  "gpt-4o",                                                             // limit = 128000
			cfg: contextwin.Config{
				Policy: contextwin.PolicyReject,
			},
			wantVerdict: contextwin.VerdictRejected,
			wantSource:  contextwin.LimitSourceCatalog,
			checkReason: "exceeds limit",
		},
		{
			name:   "catalog model exceeds limit with observe policy warning",
			stage:  "striker",
			prompt: strings.Repeat("This is a very long prompt to exceed tokens. ", 15000),
			model:  "gpt-4o",
			cfg: contextwin.Config{
				Policy: contextwin.PolicyObserve,
			},
			wantVerdict: contextwin.VerdictWarning,
			wantSource:  contextwin.LimitSourceCatalog,
			checkReason: "exceeds limit",
		},
		{
			name:   "explicit config limit priority over catalog",
			stage:  "critic",
			prompt: "Short prompt that fits 128k but exceeds custom 5 tokens",
			model:  "gpt-4o",
			cfg: contextwin.Config{
				ContextLimit: 5,
				Policy:       contextwin.PolicyReject,
			},
			wantVerdict: contextwin.VerdictRejected,
			wantSource:  contextwin.LimitSourceConfig,
			checkReason: "exceeds limit",
		},
		{
			name:   "explicit config limit applies to unknown model",
			stage:  "striker",
			prompt: "Short prompt for unknown model exceeding limit 5",
			model:  "my-unknown-custom-model",
			cfg: contextwin.Config{
				ContextLimit: 5,
				Policy:       contextwin.PolicyReject,
			},
			wantVerdict: contextwin.VerdictRejected,
			wantSource:  contextwin.LimitSourceConfig,
			checkReason: "exceeds limit",
		},
		{
			name:   "unknown model without explicit limit is bypassed",
			stage:  "striker",
			prompt: "Some prompt",
			model:  "custom-unknown-model",
			cfg: contextwin.Config{
				ContextLimit: 0,
				Policy:       contextwin.PolicyReject,
			},
			wantVerdict: contextwin.VerdictBypassed,
			wantSource:  contextwin.LimitSourceUnknown,
		},
		{
			name:   "empty model without explicit limit is bypassed",
			stage:  "striker",
			prompt: "Some prompt",
			model:  "",
			cfg: contextwin.Config{
				ContextLimit: 0,
				Policy:       contextwin.PolicyReject,
			},
			wantVerdict: contextwin.VerdictBypassed,
			wantSource:  contextwin.LimitSourceUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adm := engine.EvaluateStage(tt.stage, tt.prompt, tt.model, tt.cfg)

			if adm.Verdict != tt.wantVerdict {
				t.Errorf("verdict mismatch: got %s, want %s (reason: %s)", adm.Verdict, tt.wantVerdict, adm.Reason)
			}
			if adm.LimitSource != tt.wantSource {
				t.Errorf("limit source mismatch: got %s, want %s", adm.LimitSource, tt.wantSource)
			}
			if tt.checkReason != "" && !strings.Contains(adm.Reason, tt.checkReason) {
				t.Errorf("expected reason containing %q, got %q", tt.checkReason, adm.Reason)
			}
			if adm.Estimate.Provenance != backend.ProvenanceEstimated {
				t.Errorf("expected ProvenanceEstimated, got %s", adm.Estimate.Provenance)
			}
		})
	}
}

func TestAdmissionEngine_ExactLimitAdmitted(t *testing.T) {
	cat := contextwin.NewStaticCatalog()
	est := contextwin.NewHeuristicEstimator(0.20)
	engine := contextwin.NewAdmissionEngine(cat, est)

	// 計算 1 個字元所產生的 total estimated tokens
	sample := est.Estimate("oracle", "A")
	exactTokens := sample.TotalEstimatedTokens

	// 當 limit 剛好等於 TotalEstimatedTokens 時，依規範必須判定為 Admitted（通過）
	cfg := contextwin.Config{
		ContextLimit: exactTokens,
		Policy:       contextwin.PolicyReject,
	}

	adm := engine.EvaluateStage("oracle", "A", "", cfg)
	if adm.Verdict != contextwin.VerdictAdmitted {
		t.Errorf("expected exactly equal tokens to be Admitted, got %s", adm.Verdict)
	}
}
