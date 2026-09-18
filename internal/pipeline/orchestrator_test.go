package pipeline

import (
	"context"
	"errors"
	"testing"

	"asura/internal/engine"
)

type mockOracleStage struct {
	plan      *OraclePlan
	err       error
	callCount int
	onCall    func()
}

func (m *mockOracleStage) Execute(ctx context.Context, execCtx *engine.ExecutionContext) (*OraclePlan, error) {
	m.callCount++
	if m.onCall != nil {
		m.onCall()
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.plan, nil
}

type mockStrikerStage struct {
	output    string
	err       error
	callCount int
	onCall    func()
}

func (m *mockStrikerStage) Execute(ctx context.Context, execCtx *engine.ExecutionContext, plan *OraclePlan) (string, error) {
	m.callCount++
	if m.onCall != nil {
		m.onCall()
	}
	if m.err != nil {
		return m.output, m.err
	}
	return m.output, nil
}

type mockCriticStage struct {
	review    *CriticReview
	err       error
	callCount int
	onCall    func()
}

func (m *mockCriticStage) Execute(ctx context.Context, execCtx *engine.ExecutionContext, plan *OraclePlan, strikerOutput string) (*CriticReview, error) {
	m.callCount++
	if m.onCall != nil {
		m.onCall()
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.review, nil
}

func TestOrchestrator_ThreeFace_Success_StrictOrder(t *testing.T) {
	order := []string{}

	oracle := &mockOracleStage{
		plan: &OraclePlan{Goal: "Refactor"},
		onCall: func() {
			order = append(order, "oracle")
		},
	}
	striker := &mockStrikerStage{
		output: "striker done",
		onCall: func() {
			order = append(order, "striker")
		},
	}
	critic := &mockCriticStage{
		review: &CriticReview{Verdict: VerdictPass},
		onCall: func() {
			order = append(order, "critic")
		},
	}

	orc := NewOrchestrator(oracle, striker, critic)
	execCtx := &engine.ExecutionContext{OriginalPrompt: "hello"}

	output, trace, err := orc.Execute(context.Background(), ModeThreeFace, execCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if output != "striker done" {
		t.Errorf("expected output 'striker done', got %s", output)
	}

	if len(order) != 3 || order[0] != "oracle" || order[1] != "striker" || order[2] != "critic" {
		t.Errorf("expected strict execution order [oracle, striker, critic], got %v", order)
	}

	if oracle.callCount != 1 || striker.callCount != 1 || critic.callCount != 1 {
		t.Errorf("each stage must be called exactly once: oracle=%d, striker=%d, critic=%d",
			oracle.callCount, striker.callCount, critic.callCount)
	}

	if trace == nil {
		t.Fatalf("expected trace to not be nil")
	}
	if trace.Mode != ModeThreeFace {
		t.Errorf("expected mode %s, got %s", ModeThreeFace, trace.Mode)
	}
	if trace.ModelCalls != 3 {
		t.Errorf("expected 3 ModelCalls, got %d", trace.ModelCalls)
	}
	if trace.FinalResult != "striker done" {
		t.Errorf("expected FinalResult 'striker done', got %s", trace.FinalResult)
	}
	if trace.OraclePlan == nil || trace.OraclePlan.Goal != "Refactor" {
		t.Errorf("expected OraclePlan to be preserved in trace")
	}
	if trace.CriticReview == nil || trace.CriticReview.Verdict != VerdictPass {
		t.Errorf("expected CriticReview to be preserved in trace")
	}
	if len(trace.Stages) != 3 {
		t.Fatalf("expected 3 stages in trace, got %d", len(trace.Stages))
	}
	for _, st := range trace.Stages {
		if st.Status != StageCompleted {
			t.Errorf("stage %s expected status completed, got %s", st.Name, st.Status)
		}
	}
}

func TestOrchestrator_ThreeFace_OracleFailed_FailClosed(t *testing.T) {
	oracleErr := errors.New("oracle model failed")
	oracle := &mockOracleStage{err: oracleErr}
	striker := &mockStrikerStage{output: "striker done"}
	critic := &mockCriticStage{review: &CriticReview{Verdict: VerdictPass}}

	orc := NewOrchestrator(oracle, striker, critic)
	execCtx := &engine.ExecutionContext{OriginalPrompt: "test"}

	output, trace, err := orc.Execute(context.Background(), ModeThreeFace, execCtx)
	if err == nil {
		t.Fatalf("expected error when oracle fails, got nil")
	}
	if !errors.Is(err, oracleErr) && err.Error() != oracleErr.Error() {
		t.Errorf("expected oracle error, got %v", err)
	}
	if output != "" {
		t.Errorf("expected empty output, got %s", output)
	}

	// Fail-closed 驗證：Striker 與 Critic 絕不啟動
	if striker.callCount != 0 {
		t.Errorf("striker must NOT be executed when oracle fails, got %d calls", striker.callCount)
	}
	if critic.callCount != 0 {
		t.Errorf("critic must NOT be executed when oracle fails, got %d calls", critic.callCount)
	}

	if trace == nil {
		t.Fatalf("expected trace to be preserved")
	}
	if trace.ModelCalls != 1 {
		t.Errorf("expected 1 model call when oracle fails, got %d", trace.ModelCalls)
	}
	if len(trace.Stages) < 1 || trace.Stages[0].Status != StageFailed {
		t.Errorf("expected oracle stage to be marked failed in trace")
	}
}

func TestOrchestrator_ThreeFace_StrikerFailed_FailClosed(t *testing.T) {
	strikerErr := errors.New("striker execution failed")
	oracle := &mockOracleStage{plan: &OraclePlan{Goal: "Build"}}
	striker := &mockStrikerStage{err: strikerErr, output: "partial output"}
	critic := &mockCriticStage{review: &CriticReview{Verdict: VerdictPass}}

	orc := NewOrchestrator(oracle, striker, critic)
	execCtx := &engine.ExecutionContext{OriginalPrompt: "test"}

	output, trace, err := orc.Execute(context.Background(), ModeThreeFace, execCtx)
	if err == nil {
		t.Fatalf("expected error when striker fails, got nil")
	}
	if output != "partial output" {
		t.Errorf("expected partial output preserved, got %s", output)
	}

	// Fail-closed 驗證：Critic 絕不啟動
	if critic.callCount != 0 {
		t.Errorf("critic must NOT be executed when striker fails, got %d calls", critic.callCount)
	}

	if trace == nil {
		t.Fatalf("expected trace to be preserved")
	}
	if trace.ModelCalls != 2 {
		t.Errorf("expected 2 model calls (oracle + striker), got %d", trace.ModelCalls)
	}
	if trace.OraclePlan == nil || trace.OraclePlan.Goal != "Build" {
		t.Errorf("expected OraclePlan preserved in trace")
	}
}

func TestOrchestrator_ThreeFace_CriticFailed_NotSuccess(t *testing.T) {
	criticErr := errors.New("critic review failed")
	oracle := &mockOracleStage{plan: &OraclePlan{Goal: "Build"}}
	striker := &mockStrikerStage{output: "striker final output"}
	critic := &mockCriticStage{err: criticErr}

	orc := NewOrchestrator(oracle, striker, critic)
	execCtx := &engine.ExecutionContext{OriginalPrompt: "test"}

	output, trace, err := orc.Execute(context.Background(), ModeThreeFace, execCtx)
	if err == nil {
		t.Fatalf("expected error when critic fails, got nil (critic failure must not be masked as success)")
	}
	// Striker 成果應當保留
	if output != "striker final output" {
		t.Errorf("expected striker output preserved, got %s", output)
	}

	if trace == nil {
		t.Fatalf("expected trace to be preserved")
	}
	if trace.ModelCalls != 3 {
		t.Errorf("expected 3 model calls, got %d", trace.ModelCalls)
	}
	// 找到 Critic stage trace
	var criticTrace *StageTrace
	for i := range trace.Stages {
		if trace.Stages[i].Name == "critic" {
			criticTrace = &trace.Stages[i]
			break
		}
	}
	if criticTrace == nil || criticTrace.Status != StageFailed {
		t.Errorf("expected critic stage trace to be marked failed")
	}
}

func TestOrchestrator_DirectMode(t *testing.T) {
	oracle := &mockOracleStage{plan: &OraclePlan{Goal: "Refactor"}}
	striker := &mockStrikerStage{output: "direct output"}
	critic := &mockCriticStage{review: &CriticReview{Verdict: VerdictPass}}

	orc := NewOrchestrator(oracle, striker, critic)
	execCtx := &engine.ExecutionContext{OriginalPrompt: "direct request"}

	output, trace, err := orc.Execute(context.Background(), ModeDirect, execCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "direct output" {
		t.Errorf("expected 'direct output', got %s", output)
	}

	// Direct mode 下 Oracle 與 Critic 均不啟動
	if oracle.callCount != 0 {
		t.Errorf("oracle must NOT be called in direct mode, got %d", oracle.callCount)
	}
	if striker.callCount != 1 {
		t.Errorf("striker must be called exactly once in direct mode, got %d", striker.callCount)
	}
	if critic.callCount != 0 {
		t.Errorf("critic must NOT be called in direct mode, got %d", critic.callCount)
	}

	if trace == nil {
		t.Fatalf("expected trace to be preserved")
	}
	if trace.Mode != ModeDirect {
		t.Errorf("expected mode %s, got %s", ModeDirect, trace.Mode)
	}
	if trace.ModelCalls != 1 {
		t.Errorf("expected 1 model call in direct mode, got %d", trace.ModelCalls)
	}
}
