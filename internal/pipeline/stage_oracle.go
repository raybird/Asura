package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"asura/internal/backend"
	"asura/internal/engine"
)

// OracleRunnerFunc 定義 Oracle 呼叫底層模型執行的函式簽名
type OracleRunnerFunc func(ctx context.Context, workspace string, prompt string) (string, error)

// DefaultOracleStage 實作 Oracle 階段
type DefaultOracleStage struct {
	runner   OracleRunnerFunc
	backend  backend.Backend
	registry backend.Registry
}

// NewOracleStage 建立 Oracle 階段實例
func NewOracleStage(runner OracleRunnerFunc) *DefaultOracleStage {
	return &DefaultOracleStage{runner: runner}
}

// NewOracleStageWithBackend 建立基於共同 Backend 契約之 Oracle 階段實例
func NewOracleStageWithBackend(b backend.Backend) *DefaultOracleStage {
	return &DefaultOracleStage{backend: b}
}

// WithRegistry 注入後端註冊表以支援依任務動態解析後端
func (s *DefaultOracleStage) WithRegistry(reg backend.Registry) *DefaultOracleStage {
	s.registry = reg
	return s
}

// Prepare 組裝 Oracle 的唯一 ExecutionRequest
func (s *DefaultOracleStage) Prepare(execCtx *engine.ExecutionContext) (*backend.ExecutionRequest, error) {
	workspace := ""
	prompt := ""
	if execCtx != nil {
		workspace = execCtx.Workspace
		prompt = engine.ComposePrompt(execCtx.OriginalPrompt, execCtx)
	}

	systemInstruction := "You are Oracle, an AI architecture planner. Analyze the execution context and produce a JSON plan matching the schema: {\"goal\":\"...\",\"constraints\":[...],\"steps\":[...],\"expected_outcome\":\"...\"}.\nReturn ONLY the JSON or a json markdown code block."
	oraclePrompt := fmt.Sprintf("%s\n\nTask Context and Requirements:\n%s", systemInstruction, prompt)

	return &backend.ExecutionRequest{
		Role:      backend.RoleOracle,
		Workspace: workspace,
		Prompt:    oraclePrompt,
	}, nil
}

// ExecutePrepared 執行已準備之 ExecutionRequest 並解析 OraclePlan，即使失敗亦回傳 ExecutionResult
func (s *DefaultOracleStage) ExecutePrepared(ctx context.Context, execCtx *engine.ExecutionContext, req *backend.ExecutionRequest) (*OraclePlan, *backend.ExecutionResult, error) {
	if req == nil {
		return nil, nil, errors.New("execution request is nil")
	}

	if execCtx != nil && execCtx.ExecutionContract != nil && (execCtx.ExecutionContract.ContractVersion == backend.StageBindingContractVersion || execCtx.ExecutionContract.ContractVersion == backend.InventoryContractVersion || execCtx.ExecutionContract.StageBindingPlan != nil) {
		if err := execCtx.ValidateStageBackend(req.Role); err != nil {
			return nil, nil, err
		}
		if req.Role != backend.RoleOracle {
			return nil, nil, fmt.Errorf("%w: stage request role mismatch", backend.ErrInvalidContract)
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

	var rawOutput string
	var execRes *backend.ExecutionResult
	if activeBackend != nil {
		res, err := observedRun(ctx, execCtx, activeBackend, req)
		execRes = res
		if err != nil {
			return nil, execRes, fmt.Errorf("oracle backend error: %w", err)
		}
		if res != nil {
			rawOutput = res.Output
		}
	} else if s.runner != nil {
		out, err := s.runner(ctx, req.Workspace, req.Prompt)
		if err != nil {
			return nil, nil, fmt.Errorf("oracle runner error: %w", err)
		}
		rawOutput = out
	} else {
		return nil, nil, errors.New("oracle runner is nil")
	}

	jsonStr := extractJSON(rawOutput)
	var plan OraclePlan
	if err := json.Unmarshal([]byte(jsonStr), &plan); err != nil {
		return nil, execRes, fmt.Errorf("oracle output invalid json: %w", err)
	}

	if strings.TrimSpace(plan.Goal) == "" {
		return nil, execRes, errors.New("oracle plan missing goal")
	}
	if len(plan.Steps) == 0 {
		return nil, execRes, errors.New("oracle plan steps cannot be empty")
	}
	for i, step := range plan.Steps {
		if strings.TrimSpace(step) == "" {
			return nil, execRes, fmt.Errorf("oracle plan step %d is empty", i+1)
		}
	}

	if plan.Metadata == nil {
		plan.Metadata = make(map[string]string)
	}
	plan.Metadata["stage"] = "oracle"

	return &plan, execRes, nil
}

// Execute 執行 Oracle 分析並產出結構化計畫（向後相容入口）
func (s *DefaultOracleStage) Execute(ctx context.Context, execCtx *engine.ExecutionContext) (*OraclePlan, error) {
	req, err := s.Prepare(execCtx)
	if err != nil {
		return nil, err
	}
	plan, _, err := s.ExecutePrepared(ctx, execCtx, req)
	return plan, err
}

// extractJSON 嘗試從輸出中抽取 JSON 內容
func extractJSON(s string) string {
	trimmed := strings.TrimSpace(s)
	// 檢查是否有 ```json ... ``` 區塊
	if start := strings.Index(trimmed, "```json"); start != -1 {
		rest := trimmed[start+7:]
		if end := strings.Index(rest, "```"); end != -1 {
			return strings.TrimSpace(rest[:end])
		}
	}
	// 檢查是否有 ``` ... ``` 區塊
	if start := strings.Index(trimmed, "```"); start != -1 {
		rest := trimmed[start+3:]
		if end := strings.Index(rest, "```"); end != -1 {
			return strings.TrimSpace(rest[:end])
		}
	}
	return trimmed
}
