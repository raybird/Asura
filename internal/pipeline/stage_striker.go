package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"asura/internal/backend"
	"asura/internal/engine"
)

// StrikerRunnerFunc 定義 Striker 呼叫底層模型執行的函式簽名（與既有 RunnerFunc 一致）
type StrikerRunnerFunc func(ctx context.Context, workspace string, prompt string) (string, error)

// DefaultStrikerStage 實作 Striker 階段
type DefaultStrikerStage struct {
	runner  StrikerRunnerFunc
	backend backend.Backend
}

// NewStrikerStage 建立 Striker 階段實例
func NewStrikerStage(runner StrikerRunnerFunc) *DefaultStrikerStage {
	return &DefaultStrikerStage{runner: runner}
}

// NewStrikerStageWithBackend 建立基於共同 Backend 契約之 Striker 階段實例
func NewStrikerStageWithBackend(b backend.Backend) *DefaultStrikerStage {
	return &DefaultStrikerStage{backend: b}
}

// Execute 執行 Striker 工作並產生輸出結果
func (s *DefaultStrikerStage) Execute(ctx context.Context, execCtx *engine.ExecutionContext, plan *OraclePlan) (string, error) {
	if s.runner == nil {
		return "", errors.New("striker runner is nil")
	}

	workspace := ""
	basePrompt := ""
	if execCtx != nil {
		workspace = execCtx.Workspace
		basePrompt = engine.ComposePrompt(execCtx.OriginalPrompt, execCtx)
	}

	finalPrompt := basePrompt
	if plan != nil {
		var sb strings.Builder
		sb.WriteString("### Oracle Execution Plan\n")
		if plan.Goal != "" {
			sb.WriteString(fmt.Sprintf("Goal: %s\n", plan.Goal))
		}
		if len(plan.Constraints) > 0 {
			sb.WriteString("Constraints:\n")
			for _, c := range plan.Constraints {
				sb.WriteString(fmt.Sprintf("- %s\n", c))
			}
		}
		if len(plan.Steps) > 0 {
			sb.WriteString("Steps:\n")
			for i, step := range plan.Steps {
				sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
			}
		}
		if plan.ExpectedOutcome != "" {
			sb.WriteString(fmt.Sprintf("Expected Outcome: %s\n", plan.ExpectedOutcome))
		}
		sb.WriteString("\n")
		sb.WriteString(basePrompt)
		finalPrompt = sb.String()
	}

	if s.backend != nil {
		res, err := s.backend.Run(ctx, &backend.ExecutionRequest{
			Role:      backend.RoleStriker,
			Workspace: workspace,
			Prompt:    finalPrompt,
		})
		if err != nil {
			if res != nil && res.PartialOutput != "" {
				return res.PartialOutput, err
			}
			return "", err
		}
		return res.Output, nil
	}

	if s.runner == nil {
		return "", errors.New("striker runner is nil")
	}

	return s.runner(ctx, workspace, finalPrompt)
}
