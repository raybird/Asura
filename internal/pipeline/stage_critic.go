package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"asura/internal/engine"
)

// CriticRunnerFunc 定義 Critic 呼叫底層模型執行的函式簽名
type CriticRunnerFunc func(ctx context.Context, workspace string, prompt string) (string, error)

// DefaultCriticStage 實作 Critic 階段
type DefaultCriticStage struct {
	runner CriticRunnerFunc
}

// NewCriticStage 建立 Critic 階段實例
func NewCriticStage(runner CriticRunnerFunc) *DefaultCriticStage {
	return &DefaultCriticStage{runner: runner}
}

// Execute 執行 Critic 審查並產出結構化審查報告
func (s *DefaultCriticStage) Execute(ctx context.Context, execCtx *engine.ExecutionContext, plan *OraclePlan, strikerOutput string) (*CriticReview, error) {
	if s.runner == nil {
		return nil, errors.New("critic runner is nil")
	}

	workspace := ""
	prompt := ""
	if execCtx != nil {
		workspace = execCtx.Workspace
		prompt = execCtx.OriginalPrompt
	}

	planJSON, _ := json.Marshal(plan)
	systemInstruction := "You are Critic, an AI quality reviewer. Review the Striker execution output against the original prompt and Oracle Plan. Produce a JSON review matching schema: {\"verdict\":\"pass|needs_attention|failed\",\"findings\":[...],\"summary\":\"...\"}.\nReturn ONLY JSON or a json markdown code block."
	criticPrompt := fmt.Sprintf("%s\n\nOriginal Prompt:\n%s\n\nOracle Plan:\n%s\n\nStriker Output:\n%s",
		systemInstruction, prompt, string(planJSON), strikerOutput)

	rawOutput, err := s.runner(ctx, workspace, criticPrompt)
	if err != nil {
		return nil, fmt.Errorf("critic runner error: %w", err)
	}

	jsonStr := extractJSON(rawOutput)
	var review CriticReview
	if err := json.Unmarshal([]byte(jsonStr), &review); err != nil {
		// 若無法解析 JSON，提供基本的 fallback
		review = CriticReview{
			Verdict:  VerdictNeedsAttention,
			Findings: []string{"Non-structured critic output"},
			Summary:  strings.TrimSpace(rawOutput),
		}
	}

	if review.Verdict == "" {
		review.Verdict = VerdictNeedsAttention
	}

	if review.Metadata == nil {
		review.Metadata = make(map[string]string)
	}
	review.Metadata["stage"] = "critic"

	return &review, nil
}
