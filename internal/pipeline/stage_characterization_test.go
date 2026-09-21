package pipeline_test

import (
	"context"
	"errors"
	"testing"

	"asura/internal/backend"
	"asura/internal/engine"
	"asura/internal/pipeline"
)

// captureBackend 攔截傳入 Backend.Run 的 ExecutionRequest
type captureBackend struct {
	capturedReq *backend.ExecutionRequest
	retResult   *backend.ExecutionResult
	retErr      error
}

func (c *captureBackend) ID() string { return "capture" }
func (c *captureBackend) Version(ctx context.Context) (string, error) { return "1.0.0", nil }
func (c *captureBackend) HasCapability(cap backend.Capability) bool { return true }
func (c *captureBackend) Capabilities() []backend.Capability {
	return []backend.Capability{backend.CapWorkspaceWrite, backend.CapReadOnlySandbox}
}
func (c *captureBackend) Run(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	c.capturedReq = req
	return c.retResult, c.retErr
}

func TestStagePromptCharacterization_ByteForByteEquivalence(t *testing.T) {
	execCtx := &engine.ExecutionContext{
		OriginalPrompt: "Write a high performance Go service",
		Workspace:      "/test/workspace",
	}
	plan := &pipeline.OraclePlan{
		Goal:            "Service Architecture",
		Constraints:     []string{"No CGO", "Memory under 50MB"},
		Steps:           []string{"Design", "Implement", "Test"},
		ExpectedOutcome: "Clean Go code",
	}
	strikerOutput := "package main\nfunc main() {}"

	// 1. Oracle Characterization: Prepare vs Backend intercept
	bOracle := &captureBackend{
		retResult: &backend.ExecutionResult{
			Output: `{"goal":"Architecture","constraints":["none"],"steps":["s1"],"expected_outcome":"done"}`,
		},
	}
	oracleStage := pipeline.NewOracleStageWithBackend(bOracle)
	reqOracle, err := oracleStage.Prepare(execCtx)
	if err != nil {
		t.Fatalf("oracle Prepare error: %v", err)
	}

	_, _, err = oracleStage.ExecutePrepared(context.Background(), execCtx, reqOracle)
	if err != nil {
		t.Fatalf("oracle ExecutePrepared error: %v", err)
	}
	if bOracle.capturedReq.Prompt != reqOracle.Prompt {
		t.Errorf("oracle prompt mismatch between Prepare and Backend.Run:\nPrepare:\n%s\nCaptured:\n%s",
			reqOracle.Prompt, bOracle.capturedReq.Prompt)
	}

	// 2. Striker Characterization: Prepare vs Backend intercept
	bStriker := &captureBackend{
		retResult: &backend.ExecutionResult{
			Output: "striker result",
		},
	}
	strikerStage := pipeline.NewStrikerStageWithBackend(bStriker)
	reqStriker, err := strikerStage.Prepare(execCtx, plan)
	if err != nil {
		t.Fatalf("striker Prepare error: %v", err)
	}

	_, _, err = strikerStage.ExecutePrepared(context.Background(), execCtx, reqStriker)
	if err != nil {
		t.Fatalf("striker ExecutePrepared error: %v", err)
	}
	if bStriker.capturedReq.Prompt != reqStriker.Prompt {
		t.Errorf("striker prompt mismatch between Prepare and Backend.Run:\nPrepare:\n%s\nCaptured:\n%s",
			reqStriker.Prompt, bStriker.capturedReq.Prompt)
	}

	// 3. Critic Characterization: Prepare vs Backend intercept
	bCritic := &captureBackend{
		retResult: &backend.ExecutionResult{
			Output: `{"verdict":"pass","findings":[],"summary":"looks great"}`,
		},
	}
	criticStage := pipeline.NewCriticStageWithBackend(bCritic)
	reqCritic, err := criticStage.Prepare(execCtx, plan, strikerOutput)
	if err != nil {
		t.Fatalf("critic Prepare error: %v", err)
	}

	_, _, err = criticStage.ExecutePrepared(context.Background(), execCtx, reqCritic)
	if err != nil {
		t.Fatalf("critic ExecutePrepared error: %v", err)
	}
	if bCritic.capturedReq.Prompt != reqCritic.Prompt {
		t.Errorf("critic prompt mismatch between Prepare and Backend.Run:\nPrepare:\n%s\nCaptured:\n%s",
			reqCritic.Prompt, bCritic.capturedReq.Prompt)
	}
}

func TestStage_ExecutionResultPreservedOnBackendAndJSONErrors(t *testing.T) {
	inTok := int64(150)
	dummyUsage := &backend.TokenUsage{InputTokens: &inTok}

	// 1. Oracle: Backend error preserves usage
	bOracleFail := &captureBackend{
		retResult: &backend.ExecutionResult{Usage: dummyUsage, UsageSource: backend.ProvenanceExact},
		retErr:    errors.New("connection reset by peer"),
	}
	oracleStage := pipeline.NewOracleStageWithBackend(bOracleFail)
	req, _ := oracleStage.Prepare(nil)
	plan, res, err := oracleStage.ExecutePrepared(context.Background(), nil, req)
	if err == nil || plan != nil {
		t.Fatalf("expected error and nil plan on backend failure")
	}
	if res == nil || res.Usage == nil || *res.Usage.InputTokens != 150 {
		t.Errorf("expected usage preserved on oracle backend failure, got %+v", res)
	}

	// 2. Oracle: Invalid JSON preserves usage
	bOracleJSONFail := &captureBackend{
		retResult: &backend.ExecutionResult{
			Output:      "This is not a JSON object",
			Usage:       dummyUsage,
			UsageSource: backend.ProvenanceExact,
		},
	}
	oracleStage2 := pipeline.NewOracleStageWithBackend(bOracleJSONFail)
	plan2, res2, err2 := oracleStage2.ExecutePrepared(context.Background(), nil, req)
	if err2 == nil || plan2 != nil {
		t.Fatalf("expected JSON parse error on non-json output")
	}
	if res2 == nil || res2.Usage == nil || *res2.Usage.InputTokens != 150 {
		t.Errorf("expected usage preserved on oracle JSON parse failure, got %+v", res2)
	}

	// 3. Critic: Backend error preserves usage
	bCriticFail := &captureBackend{
		retResult: &backend.ExecutionResult{Usage: dummyUsage, UsageSource: backend.ProvenanceExact},
		retErr:    errors.New("timeout"),
	}
	criticStage := pipeline.NewCriticStageWithBackend(bCriticFail)
	reqCrit, _ := criticStage.Prepare(nil, nil, "")
	rev, resCrit, errCrit := criticStage.ExecutePrepared(context.Background(), nil, reqCrit)
	if errCrit == nil || rev != nil {
		t.Fatalf("expected error and nil review on critic failure")
	}
	if resCrit == nil || resCrit.Usage == nil || *resCrit.Usage.InputTokens != 150 {
		t.Errorf("expected usage preserved on critic backend failure, got %+v", resCrit)
	}

	// 4. Critic: Invalid JSON preserves usage
	bCriticJSONFail := &captureBackend{
		retResult: &backend.ExecutionResult{
			Output:      "Invalid critic output markdown without json",
			Usage:       dummyUsage,
			UsageSource: backend.ProvenanceExact,
		},
	}
	criticStage2 := pipeline.NewCriticStageWithBackend(bCriticJSONFail)
	rev2, resCrit2, errCrit2 := criticStage2.ExecutePrepared(context.Background(), nil, reqCrit)
	if errCrit2 == nil || rev2 != nil {
		t.Fatalf("expected JSON error on critic failure")
	}
	if resCrit2 == nil || resCrit2.Usage == nil || *resCrit2.Usage.InputTokens != 150 {
		t.Errorf("expected usage preserved on critic JSON parse failure, got %+v", resCrit2)
	}
}
