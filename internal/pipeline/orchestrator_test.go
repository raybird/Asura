package pipeline

import (
	"context"
	"errors"
	"testing"

	"asura/internal/backend"
	"asura/internal/contextwin"
	"asura/internal/engine"
)

type mockOracleStage struct {
	plan      *OraclePlan
	err       error
	callCount int
	onCall    func()
}

func (m *mockOracleStage) Prepare(execCtx *engine.ExecutionContext) (*backend.ExecutionRequest, error) {
	p := ""
	if execCtx != nil {
		p = execCtx.OriginalPrompt
	}
	return &backend.ExecutionRequest{Role: backend.RoleOracle, Prompt: p}, nil
}

func (m *mockOracleStage) ExecutePrepared(ctx context.Context, execCtx *engine.ExecutionContext, req *backend.ExecutionRequest) (*OraclePlan, *backend.ExecutionResult, error) {
	plan, err := m.Execute(ctx, execCtx)
	return plan, nil, err
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
	output      string
	err         error
	callCount   int
	onCall      func()
	PrepareFunc func(execCtx *engine.ExecutionContext, plan *OraclePlan) (*backend.ExecutionRequest, error)
}

func (m *mockStrikerStage) Prepare(execCtx *engine.ExecutionContext, plan *OraclePlan) (*backend.ExecutionRequest, error) {
	if m.PrepareFunc != nil {
		return m.PrepareFunc(execCtx, plan)
	}
	p := ""
	if execCtx != nil {
		p = execCtx.OriginalPrompt
	}
	return &backend.ExecutionRequest{Role: backend.RoleStriker, Prompt: p}, nil
}

func (m *mockStrikerStage) ExecutePrepared(ctx context.Context, execCtx *engine.ExecutionContext, req *backend.ExecutionRequest) (string, *backend.ExecutionResult, error) {
	out, err := m.Execute(ctx, execCtx, nil)
	return out, nil, err
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

func (m *mockCriticStage) Prepare(execCtx *engine.ExecutionContext, plan *OraclePlan, strikerOutput string) (*backend.ExecutionRequest, error) {
	p := ""
	if execCtx != nil {
		p = execCtx.OriginalPrompt
	}
	return &backend.ExecutionRequest{Role: backend.RoleCritic, Prompt: p}, nil
}

func (m *mockCriticStage) ExecutePrepared(ctx context.Context, execCtx *engine.ExecutionContext, req *backend.ExecutionRequest) (*CriticReview, *backend.ExecutionResult, error) {
	rev, err := m.Execute(ctx, execCtx, nil, "")
	return rev, nil, err
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

func TestOrchestrator_ContextBudget_RejectFailClosed(t *testing.T) {
	oracle := &mockOracleStage{plan: &OraclePlan{Goal: "Refactor"}}
	striker := &mockStrikerStage{output: "direct output"}
	critic := &mockCriticStage{review: &CriticReview{Verdict: VerdictPass}}

	orc := NewOrchestrator(oracle, striker, critic).WithContextBudget(contextwin.Config{
		ContextLimit: 5, // 極低上限，必超量
		Policy:       contextwin.PolicyReject,
	})

	execCtx := &engine.ExecutionContext{OriginalPrompt: "this is a relatively long prompt to exceed 5 tokens limit"}

	output, trace, err := orc.Execute(context.Background(), ModeDirect, execCtx)
	if err == nil {
		t.Fatalf("expected context limit exceeded error, got nil")
	}
	if output != "" {
		t.Errorf("expected empty output on reject, got %q", output)
	}
	if striker.callCount != 0 {
		t.Errorf("fail-closed violation: striker was called %d times, expected 0", striker.callCount)
	}
	if trace == nil {
		t.Fatalf("expected trace to be returned")
	}
	if trace.ModelCalls != 0 {
		t.Errorf("expected ModelCalls=0 on preflight reject, got %d", trace.ModelCalls)
	}
	if len(trace.Stages) != 1 {
		t.Fatalf("expected 1 stage trace, got %d", len(trace.Stages))
	}
	st := trace.Stages[0]
	if st.Status != StageFailed {
		t.Errorf("expected stage status failed, got %s", st.Status)
	}
	if st.Admission == nil || st.Admission.Verdict != contextwin.VerdictRejected {
		t.Errorf("expected admission verdict rejected, got %+v", st.Admission)
	}
}

func TestOrchestrator_ThreeFace_StrikerRejectPreservesOracle(t *testing.T) {
	oracle := &mockOracleStage{
		plan: &OraclePlan{Goal: "Analyze architecture"},
	}
	striker := &mockStrikerStage{output: "striker output"}
	critic := &mockCriticStage{review: &CriticReview{Verdict: VerdictPass}}

	// 客製准入引擎：oracle 准入通過，striker 准入拒絕
	cat := contextwin.NewStaticCatalog()
	est := contextwin.NewHeuristicEstimator(0.20)
	admEngine := contextwin.NewAdmissionEngine(cat, est)

	orc := NewOrchestrator(oracle, striker, critic).
		WithAdmissionEngine(admEngine).
		WithContextBudget(contextwin.Config{
			ContextLimit: 10, // 讓 2 tokens 的 oracle 通過，長 prompt 在 striker 超量
			Policy:       contextwin.PolicyReject,
		})

	execCtx := &engine.ExecutionContext{
		OriginalPrompt: "A", // 很短，~2 tokens，小於 limit 10
	}

	// 覆寫 striker mock 的 Prepare 讓其產生超長 Prompt (約 100+ tokens，遠大於 limit 10)
	striker.PrepareFunc = func(execCtx *engine.ExecutionContext, plan *OraclePlan) (*backend.ExecutionRequest, error) {
		return &backend.ExecutionRequest{
			Role:   backend.RoleStriker,
			Prompt: "This is a very long striker prompt designed to definitely exceed the context limit of 10 tokens by having plenty of characters in English text.",
		}, nil
	}

	output, trace, err := orc.Execute(context.Background(), ModeThreeFace, execCtx)
	if err == nil {
		t.Fatalf("expected context limit exceeded error, got nil")
	}
	if output != "" {
		t.Errorf("expected empty striker output, got %q", output)
	}
	if oracle.callCount != 1 {
		t.Errorf("expected oracle called exactly once, got %d", oracle.callCount)
	}
	if striker.callCount != 0 {
		t.Errorf("fail-closed violation: striker called %d times on reject, expected 0", striker.callCount)
	}
	if critic.callCount != 0 {
		t.Errorf("critic should not be called when striker is rejected, got %d", critic.callCount)
	}
	if trace == nil {
		t.Fatalf("expected trace to be preserved")
	}
	if trace.OraclePlan == nil || trace.OraclePlan.Goal != "Analyze architecture" {
		t.Errorf("expected oracle plan to be preserved in trace, got %+v", trace.OraclePlan)
	}
	if trace.ModelCalls != 1 {
		t.Errorf("expected ModelCalls=1 (only oracle), got %d", trace.ModelCalls)
	}
}

func TestOrchestrator_ContextBudget_ObserveWarningContinues(t *testing.T) {
	oracle := &mockOracleStage{plan: &OraclePlan{Goal: "Refactor"}}
	striker := &mockStrikerStage{output: "observed output"}
	critic := &mockCriticStage{review: &CriticReview{Verdict: VerdictPass}}

	orc := NewOrchestrator(oracle, striker, critic).WithContextBudget(contextwin.Config{
		ContextLimit: 5, // 超量
		Policy:       contextwin.PolicyObserve, // observe 模式不中斷
	})

	execCtx := &engine.ExecutionContext{OriginalPrompt: "this is a prompt that exceeds 5 tokens"}

	output, trace, err := orc.Execute(context.Background(), ModeDirect, execCtx)
	if err != nil {
		t.Fatalf("unexpected error in observe mode: %v", err)
	}
	if output != "observed output" {
		t.Errorf("expected 'observed output', got %q", output)
	}
	if striker.callCount != 1 {
		t.Errorf("expected striker called once, got %d", striker.callCount)
	}
	if trace.Stages[0].Admission == nil || trace.Stages[0].Admission.Verdict != contextwin.VerdictWarning {
		t.Errorf("expected warning verdict, got %+v", trace.Stages[0].Admission)
	}
}

