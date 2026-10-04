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
	runner   StrikerRunnerFunc
	backend  backend.Backend
	registry backend.Registry
}

// NewStrikerStage 建立 Striker 階段實例
func NewStrikerStage(runner StrikerRunnerFunc) *DefaultStrikerStage {
	return &DefaultStrikerStage{runner: runner}
}

// NewStrikerStageWithBackend 建立基於共同 Backend 契約之 Striker 階段實例
func NewStrikerStageWithBackend(b backend.Backend) *DefaultStrikerStage {
	return &DefaultStrikerStage{backend: b}
}

// WithRegistry 注入後端註冊表以支援依任務動態解析後端
func (s *DefaultStrikerStage) WithRegistry(reg backend.Registry) *DefaultStrikerStage {
	s.registry = reg
	return s
}

// Prepare 組裝 Striker 的唯一 ExecutionRequest
func (s *DefaultStrikerStage) Prepare(execCtx *engine.ExecutionContext, plan *OraclePlan) (*backend.ExecutionRequest, error) {
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

	return &backend.ExecutionRequest{
		Role:      backend.RoleStriker,
		Workspace: workspace,
		Prompt:    finalPrompt,
	}, nil
}

// ExecutePrepared 執行已準備之 ExecutionRequest 並回傳輸出與 ExecutionResult
func (s *DefaultStrikerStage) ExecutePrepared(ctx context.Context, execCtx *engine.ExecutionContext, req *backend.ExecutionRequest) (string, *backend.ExecutionResult, error) {
	if req == nil {
		return "", nil, errors.New("execution request is nil")
	}

	if execCtx != nil && execCtx.ExecutionContract != nil && (execCtx.ExecutionContract.ContractVersion == backend.StageBindingContractVersion || execCtx.ExecutionContract.ContractVersion == backend.InventoryContractVersion || execCtx.ExecutionContract.StageBindingPlan != nil) {
		if err := execCtx.ValidateStageBackend(req.Role); err != nil {
			return "", nil, err
		}
		if req.Role != backend.RoleStriker {
			return "", nil, fmt.Errorf("%w: stage request role mismatch", backend.ErrInvalidContract)
		}
	}
	var activeBackend = s.backend
	if execCtx != nil && execCtx.BoundBackend != nil {
		activeBackend = execCtx.BoundBackend
	} else if s.registry != nil && execCtx != nil && execCtx.Backend != "" {
		if b, err := s.registry.Get(execCtx.Backend); err == nil && b != nil {
			activeBackend = b
		}
	}

	if activeBackend != nil {
		res, err := observedRun(ctx, execCtx, activeBackend, req)
		if err != nil {
			if res != nil && res.PartialOutput != "" {
				return res.PartialOutput, res, err
			}
			return "", res, err
		}
		return res.Output, res, nil
	}

	if s.runner == nil {
		return "", nil, errors.New("striker runner is nil")
	}

	out, err := s.runner(ctx, req.Workspace, req.Prompt)
	return out, nil, err
}

// Execute 執行 Striker 工作並產生輸出結果（向後相容入口）
func (s *DefaultStrikerStage) Execute(ctx context.Context, execCtx *engine.ExecutionContext, plan *OraclePlan) (string, error) {
	req, err := s.Prepare(execCtx, plan)
	if err != nil {
		return "", err
	}
	out, _, err := s.ExecutePrepared(ctx, execCtx, req)
	return out, err
}
