package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"asura/internal/engine"
)

// StrikerRunnerFunc 定義 Striker 呼叫底層模型執行的函式簽名（與既有 RunnerFunc 一致）
type StrikerRunnerFunc func(ctx context.Context, workspace string, prompt string) (string, error)

// DefaultStrikerStage 實作 Striker 階段
type DefaultStrikerStage struct {
	runner StrikerRunnerFunc
}

// NewStrikerStage 建立 Striker 階段實例
func NewStrikerStage(runner StrikerRunnerFunc) *DefaultStrikerStage {
	return &DefaultStrikerStage{runner: runner}
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

	return s.runner(ctx, workspace, finalPrompt)
}
