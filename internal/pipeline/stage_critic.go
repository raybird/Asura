package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"asura/internal/backend"
	"asura/internal/engine"
)

// CriticRunnerFunc 定義 Critic 呼叫底層模型執行的函式簽名
type CriticRunnerFunc func(ctx context.Context, workspace string, prompt string) (string, error)

// DefaultCriticStage 實作 Critic 階段
type DefaultCriticStage struct {
	runner   CriticRunnerFunc
	backend  backend.Backend
	registry backend.Registry
}

// NewCriticStage 建立 Critic 階段實例
func NewCriticStage(runner CriticRunnerFunc) *DefaultCriticStage {
	return &DefaultCriticStage{runner: runner}
}

// NewCriticStageWithBackend 建立基於共同 Backend 契約之 Critic 階段實例
func NewCriticStageWithBackend(b backend.Backend) *DefaultCriticStage {
	return &DefaultCriticStage{backend: b}
}

// WithRegistry 注入後端註冊表以支援依任務動態解析後端
func (s *DefaultCriticStage) WithRegistry(reg backend.Registry) *DefaultCriticStage {
	s.registry = reg
	return s
}

// Execute 執行 Critic 審查並產出結構化審查報告
func (s *DefaultCriticStage) Execute(ctx context.Context, execCtx *engine.ExecutionContext, plan *OraclePlan, strikerOutput string) (*CriticReview, error) {
	workspace := ""
	prompt := ""
	if execCtx != nil {
		workspace = execCtx.Workspace
		prompt = engine.ComposePrompt(execCtx.OriginalPrompt, execCtx)
	}

	planJSON, _ := json.Marshal(plan)
	systemInstruction := "You are Critic, an AI quality reviewer. Review the Striker execution output against the original prompt and Oracle Plan. Produce a JSON review matching schema: {\"verdict\":\"pass|needs_attention|failed\",\"findings\":[...],\"summary\":\"...\"}.\nReturn ONLY JSON or a json markdown code block."
	criticPrompt := fmt.Sprintf("%s\n\nOriginal Prompt:\n%s\n\nOracle Plan:\n%s\n\nStriker Output:\n%s",
		systemInstruction, prompt, string(planJSON), strikerOutput)

	var activeBackend = s.backend
	if s.registry != nil && execCtx != nil && execCtx.Backend != "" {
		if b, err := s.registry.Get(execCtx.Backend); err == nil && b != nil {
			activeBackend = b
		}
	}

	var rawOutput string
	if activeBackend != nil {
		res, err := activeBackend.Run(ctx, &backend.ExecutionRequest{
			Role:      backend.RoleCritic,
			Workspace: workspace,
			Prompt:    criticPrompt,
		})
		if err != nil {
			return nil, fmt.Errorf("critic backend error: %w", err)
		}
		rawOutput = res.Output
	} else if s.runner != nil {
		out, err := s.runner(ctx, workspace, criticPrompt)
		if err != nil {
			return nil, fmt.Errorf("critic runner error: %w", err)
		}
		rawOutput = out
	} else {
		return nil, errors.New("critic runner is nil")
	}

	jsonStr := extractJSON(rawOutput)
	var review CriticReview
	if err := json.Unmarshal([]byte(jsonStr), &review); err != nil {
		return nil, fmt.Errorf("critic output invalid json: %w", err)
	}

	switch review.Verdict {
	case VerdictPass, VerdictNeedsAttention, VerdictFailed:
		// 合法審查結論
	case "":
		return nil, errors.New("critic review missing verdict")
	default:
		return nil, fmt.Errorf("critic review invalid verdict: %q", review.Verdict)
	}

	if review.Metadata == nil {
		review.Metadata = make(map[string]string)
	}
	review.Metadata["stage"] = "critic"

	return &review, nil
}
