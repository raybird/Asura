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
	runner  OracleRunnerFunc
	backend backend.Backend
}

// NewOracleStage 建立 Oracle 階段實例
func NewOracleStage(runner OracleRunnerFunc) *DefaultOracleStage {
	return &DefaultOracleStage{runner: runner}
}

// NewOracleStageWithBackend 建立基於共同 Backend 契約之 Oracle 階段實例
func NewOracleStageWithBackend(b backend.Backend) *DefaultOracleStage {
	return &DefaultOracleStage{backend: b}
}

// Execute 執行 Oracle 分析並產出結構化計畫
func (s *DefaultOracleStage) Execute(ctx context.Context, execCtx *engine.ExecutionContext) (*OraclePlan, error) {
	workspace := ""
	prompt := ""
	if execCtx != nil {
		workspace = execCtx.Workspace
		prompt = engine.ComposePrompt(execCtx.OriginalPrompt, execCtx)
	}

	systemInstruction := "You are Oracle, an AI architecture planner. Analyze the execution context and produce a JSON plan matching the schema: {\"goal\":\"...\",\"constraints\":[...],\"steps\":[...],\"expected_outcome\":\"...\"}.\nReturn ONLY the JSON or a json markdown code block."
	oraclePrompt := fmt.Sprintf("%s\n\nTask Context and Requirements:\n%s", systemInstruction, prompt)

	var rawOutput string
	if s.backend != nil {
		res, err := s.backend.Run(ctx, &backend.ExecutionRequest{
			Role:      backend.RoleOracle,
			Workspace: workspace,
			Prompt:    oraclePrompt,
		})
		if err != nil {
			return nil, fmt.Errorf("oracle backend error: %w", err)
		}
		rawOutput = res.Output
	} else if s.runner != nil {
		out, err := s.runner(ctx, workspace, oraclePrompt)
		if err != nil {
			return nil, fmt.Errorf("oracle runner error: %w", err)
		}
		rawOutput = out
	} else {
		return nil, errors.New("oracle runner is nil")
	}

	jsonStr := extractJSON(rawOutput)
	var plan OraclePlan
	if err := json.Unmarshal([]byte(jsonStr), &plan); err != nil {
		return nil, fmt.Errorf("oracle output invalid json: %w", err)
	}

	if strings.TrimSpace(plan.Goal) == "" {
		return nil, errors.New("oracle plan missing goal")
	}
	if len(plan.Steps) == 0 {
		return nil, errors.New("oracle plan steps cannot be empty")
	}
	for i, step := range plan.Steps {
		if strings.TrimSpace(step) == "" {
			return nil, fmt.Errorf("oracle plan step %d is empty", i+1)
		}
	}

	if plan.Metadata == nil {
		plan.Metadata = make(map[string]string)
	}
	plan.Metadata["stage"] = "oracle"

	return &plan, nil
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
