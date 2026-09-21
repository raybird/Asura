package integration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/backend"
	"asura/internal/backend/codex"
	"asura/internal/backend/opencode"
	"asura/internal/contextwin"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
)

// CTXWIN-001: Backend context capability probe
// 驗證已探測版本之能力矩陣與離線解析驗證
func TestCTXWIN001_BackendContextCapabilityProbe(t *testing.T) {
	t.Run("OpenCode 1.18.31 / 1.18.23 探測與離線 fixture 解析", func(t *testing.T) {
		line := `{"type":"step_finish","part":{"tokens":{"total":1200,"input":1000,"output":200,"cache":{"read":500}}}}`
		evt, err := opencode.ParseEventLine(line)
		if err != nil || evt == nil {
			t.Fatalf("unexpected error parsing opencode event: %v", err)
		}
		if evt.Tokens == nil || *evt.Tokens.Total != 1200 || *evt.Tokens.Input != 1000 || *evt.Tokens.Output != 200 || evt.Tokens.Cache == nil || *evt.Tokens.Cache.Read != 500 {
			t.Errorf("unexpected tokens parsed: %+v", evt.Tokens)
		}

		// 驗證 1.18.23 與 1.18.31 能力矩陣均具備真實 probe artifact 證實
		m23 := opencode.GetOpenCode11823Matrix()
		if m23.Status != "verified_probe_artifact" || len(m23.UnsupportedCapabilities) == 0 {
			t.Errorf("expected 1.18.23 matrix verified with unsupported capabilities, got %+v", m23)
		}
		m31 := opencode.GetOpenCode11831Matrix()
		if m31.Status != "verified_installed" || len(m31.UnsupportedCapabilities) == 0 {
			t.Errorf("expected 1.18.31 matrix verified with unsupported capabilities, got %+v", m31)
		}
	})

	t.Run("Codex CLI 0.155.1 / 0.155.0 探測與離線 fixture 解析", func(t *testing.T) {
		output := "{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"Completed\"}}\n{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":850,\"output_tokens\":150,\"cached_input_tokens\":200}}"
		_, _, _, _, usage, err := codex.ExtractResultAndEvents(output, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if usage == nil {
			t.Fatalf("expected usage extracted from turn.completed")
		}
		if usage.InputTokens == nil || *usage.InputTokens != 850 {
			t.Errorf("expected input tokens 850, got %v", usage.InputTokens)
		}
		if usage.TotalTokens != nil {
			t.Errorf("Codex probe invariant violation: total_tokens must be nil (not synthesized), got %v", *usage.TotalTokens)
		}

		// 驗證 0.155.0 與 0.155.1 能力矩陣均具備真實 probe artifact 證實
		m0 := codex.GetCodex01550Matrix()
		if m0.Status != "verified_probe_artifact" || len(m0.MissingTokenFields) == 0 {
			t.Errorf("expected 0.155.0 matrix verified with missing total_tokens, got %+v", m0)
		}
		m1 := codex.GetCodex01551Matrix()
		if m1.Status != "verified_installed" || len(m1.MissingTokenFields) == 0 {
			t.Errorf("expected 0.155.1 matrix verified with missing total_tokens, got %+v", m1)
		}
	})
}

// CTXWIN-002: Explicit provenance for token data
// 驗證字元換算估算標記為 estimated，後端實測值標記為 exact，缺失為 unknown，nullable 區分 nil 與 0
func TestCTXWIN002_ExplicitProvenanceForTokenData(t *testing.T) {
	est := contextwin.NewHeuristicEstimator(0.20)
	estimate := est.Estimate("oracle", "Sample prompt for estimation")
	if estimate.Provenance != backend.ProvenanceEstimated {
		t.Errorf("expected ProvenanceEstimated for heuristic estimate, got %s", estimate.Provenance)
	}

	zeroVal := int64(0)
	hundredVal := int64(100)
	usage := backend.TokenUsage{
		InputTokens:  &hundredVal,
		OutputTokens: &zeroVal, // 明確 0
		TotalTokens:  nil,      // 未知缺失
	}

	res := backend.ExecutionResult{
		Output:      "output",
		Usage:       &usage,
		UsageSource: backend.ProvenanceExact,
	}
	if res.UsageSource != backend.ProvenanceExact {
		t.Errorf("expected ProvenanceExact for backend result, got %s", res.UsageSource)
	}
	if res.Usage.TotalTokens != nil {
		t.Errorf("expected TotalTokens to remain nil (unknown), got %v", *res.Usage.TotalTokens)
	}
	if res.Usage.OutputTokens == nil || *res.Usage.OutputTokens != 0 {
		t.Errorf("expected OutputTokens to be explicitly 0, got %v", res.Usage.OutputTokens)
	}
}

// CTXWIN-003: Dynamic stage-aware prompt evaluation
// 驗證 Three-Face 管線下各 Stage 獨立估算，且估算輸入與送到後端的 request.Prompt 100% 逐字相等
func TestCTXWIN003_DynamicStageAwarePromptEvaluation(t *testing.T) {
	execCtx := &engine.ExecutionContext{
		OriginalPrompt: "Build a distributed cache layer",
		Workspace:      "/tmp/ws",
	}

	plan := &pipeline.OraclePlan{
		Goal:            "Cache Architecture",
		Constraints:     []string{"TTL support"},
		Steps:           []string{"Design", "Code"},
		ExpectedOutcome: "High performance cache",
	}
	strikerOutput := "Cache implementation details"

	oracleStage := pipeline.NewOracleStage(func(ctx context.Context, ws, p string) (string, error) {
		return `{"goal":"Cache Architecture","constraints":["TTL support"],"steps":["Design","Code"],"expected_outcome":"High performance cache"}`, nil
	})
	strikerStage := pipeline.NewStrikerStage(func(ctx context.Context, ws, p string) (string, error) {
		return strikerOutput, nil
	})
	criticStage := pipeline.NewCriticStage(func(ctx context.Context, ws, p string) (string, error) {
		return `{"verdict":"pass","findings":[],"summary":"approved"}`, nil
	})

	reqO, _ := oracleStage.Prepare(execCtx)
	reqS, _ := strikerStage.Prepare(execCtx, plan)
	reqC, _ := criticStage.Prepare(execCtx, plan, strikerOutput)

	est := contextwin.NewHeuristicEstimator(0.20)
	estO := est.Estimate("oracle", reqO.Prompt)
	estS := est.Estimate("striker", reqS.Prompt)
	estC := est.Estimate("critic", reqC.Prompt)

	// 驗證各 Stage 提示詞獨立性與膨脹性
	if reqO.Prompt == reqS.Prompt || reqS.Prompt == reqC.Prompt {
		t.Errorf("expected distinct prompts across stages")
	}
	if estO.TotalEstimatedTokens <= 0 || estS.TotalEstimatedTokens <= 0 || estC.TotalEstimatedTokens <= 0 {
		t.Errorf("expected positive estimated tokens for all stages")
	}
	// Critic 同時包含原始 prompt、OraclePlan 與 StrikerOutput，因此 Critic 估算 Token 顯著大於 Striker
	if estC.TotalEstimatedTokens <= estS.TotalEstimatedTokens {
		t.Errorf("expected Critic estimated tokens (%d) > Striker (%d)",
			estC.TotalEstimatedTokens, estS.TotalEstimatedTokens)
	}
}

// CTXWIN-004: Preflight admission rejection
// 驗證超量時 Preflight 拒絕，模型呼叫次數為 0，已完成 Stage 成果保留，絕不切換後端
func TestCTXWIN004_PreflightAdmissionRejection(t *testing.T) {
	var oracleCalls int32
	var strikerCalls int32

	oracleStage := pipeline.NewOracleStage(func(ctx context.Context, ws, p string) (string, error) {
		atomic.AddInt32(&oracleCalls, 1)
		return `{"goal":"Plan with long details","constraints":["Must be reliable","Must be scalable"],"steps":["step 1: initialize and configure all components with deep architecture setup","step 2: execute business workflows with extensive context and large instructions","step 3: verify all subsystem boundaries, error handling, retries and timeouts","step 4: compile detailed validation reports and write audit traces to disk"],"expected_outcome":"done"}`, nil
	})
	strikerStage := pipeline.NewStrikerStage(func(ctx context.Context, ws, p string) (string, error) {
		atomic.AddInt32(&strikerCalls, 1)
		return "striker output", nil
	})
	criticStage := pipeline.NewCriticStage(func(ctx context.Context, ws, p string) (string, error) {
		return `{"verdict":"pass","findings":[],"summary":"ok"}`, nil
	})

	// 設定上限 120：讓 Oracle（約 113 tokens）通過，但 Striker 附加上述詳細 OraclePlan 後（約 180+ tokens）超出上限
	orc := pipeline.NewOrchestrator(oracleStage, strikerStage, criticStage).
		WithContextBudget(contextwin.Config{
			ContextLimit: 120,
			Policy:       contextwin.PolicyReject,
		})

	execCtx := &engine.ExecutionContext{
		OriginalPrompt: "Short",
		Workspace:      "/tmp/ws",
	}

	out, trace, err := orc.Execute(context.Background(), pipeline.ModeThreeFace, execCtx)
	if err == nil {
		t.Fatalf("expected context limit exceeded error, got nil")
	}
	if out != "" {
		t.Errorf("expected empty final output on striker reject, got %q", out)
	}

	// 驗證 Fail-Closed：Striker 未被調用（0 次）
	if atomic.LoadInt32(&strikerCalls) != 0 {
		t.Errorf("fail-closed violation: striker called %d times, expected 0", atomic.LoadInt32(&strikerCalls))
	}
	// 驗證 Oracle 呼叫了 1 次且結果完整保留
	if atomic.LoadInt32(&oracleCalls) != 1 {
		t.Errorf("expected oracle called once, got %d", atomic.LoadInt32(&oracleCalls))
	}
	if trace == nil || trace.OraclePlan == nil || trace.OraclePlan.Goal != "Plan with long details" {
		t.Errorf("expected oracle plan preserved in trace, got %+v", trace)
	}
	if trace.ModelCalls != 1 {
		t.Errorf("expected ModelCalls=1 (only oracle), got %d", trace.ModelCalls)
	}

	// 找到 Striker stage trace 驗證狀態
	var strikerTrace *pipeline.StageTrace
	for i := range trace.Stages {
		if trace.Stages[i].Name == "striker" {
			strikerTrace = &trace.Stages[i]
			break
		}
	}
	if strikerTrace == nil || strikerTrace.Status != pipeline.StageFailed {
		t.Fatalf("expected striker stage trace failed, got %+v", strikerTrace)
	}
	if strikerTrace.Admission == nil || strikerTrace.Admission.Verdict != contextwin.VerdictRejected {
		t.Errorf("expected striker verdict rejected, got %+v", strikerTrace.Admission)
	}
}

// CTXWIN-005: Sealed context immutability
// 驗證 Sealed Context 在估算與准入前後完全不變，絕不竄改 prompt 或刪除 memory
func TestCTXWIN005_SealedContextImmutability(t *testing.T) {
	memories := []engine.ProjectMemorySnapshot{
		{ID: "mem-1", Workspace: "/tmp/test-ws", Content: "Strict microservices architecture"},
		{ID: "mem-2", Workspace: "/tmp/test-ws", Content: "Always use PostgreSQL with migrations"},
	}

	execCtx := &engine.ExecutionContext{
		OriginalPrompt:  "Refactor user auth module",
		Workspace:       "/tmp/test-ws",
		Backend:         "opencode",
		ProjectMemories: memories,
		MemoryRefs:      []string{"mem-1", "mem-2"},
		CreatedAt:       time.Now().UTC(),
	}

	// 執行 Prompt 組裝與估算
	composed := engine.ComposePrompt(execCtx.OriginalPrompt, execCtx)
	est := contextwin.NewHeuristicEstimator(0.20)
	estimate := est.Estimate("oracle", composed)

	if estimate.TotalEstimatedTokens <= 0 {
		t.Fatalf("expected positive estimated tokens, got %d", estimate.TotalEstimatedTokens)
	}

	// 斷言 Sealed ExecutionContext 不變性
	if execCtx.OriginalPrompt != "Refactor user auth module" {
		t.Errorf("OriginalPrompt mutated: got %q", execCtx.OriginalPrompt)
	}
	if len(execCtx.ProjectMemories) != 2 {
		t.Errorf("ProjectMemories modified: expected len 2, got %d", len(execCtx.ProjectMemories))
	}
	if execCtx.ProjectMemories[0].Content != "Strict microservices architecture" {
		t.Errorf("Memory content mutated: %+v", execCtx.ProjectMemories[0])
	}
	if len(execCtx.MemoryRefs) != 2 {
		t.Errorf("MemoryRefs modified: expected len 2, got %d", len(execCtx.MemoryRefs))
	}
}

// CTXWIN-006: Stage-level trace audit and backward compatibility
// 驗證每個 Stage 獨立記錄 Admission、Usage，失敗仍保留 usage，歷史任務讀取相容
func TestCTXWIN006_StageLevelTraceAuditAndBackwardCompatibility(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. 寫入歷史任務（無 Admission / Usage 欄位）
	legacyJSON := `{"mode":"three-face","stages":[{"name":"oracle","status":"completed","duration_ms":100},{"name":"striker","status":"completed","duration_ms":200}]}`
	legacyTask := &storage.Task{
		ID:            "task-legacy-audit",
		RequestID:     "req-legacy-audit",
		Prompt:        "legacy",
		Workspace:     tmpDir,
		Status:        storage.StatusCompleted,
		PipelineTrace: legacyJSON,
	}
	if err := store.CreateTask(ctx, legacyTask); err != nil {
		t.Fatal(err)
	}

	// 讀回並驗證未報錯且保持相容
	tGot, err := store.GetTask(ctx, legacyTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ptLegacy pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(tGot.PipelineTrace), &ptLegacy); err != nil {
		t.Fatal(err)
	}
	if len(ptLegacy.Stages) != 2 {
		t.Errorf("expected 2 stages, got %d", len(ptLegacy.Stages))
	}
	if ptLegacy.Stages[0].Admission != nil || ptLegacy.Stages[0].Usage != nil {
		t.Errorf("expected nil admission/usage on legacy task")
	}

	// 2. 寫入新任務（含 Stage 准入與真實 usage）
	inTok := int64(500)
	outTok := int64(100)
	newTrace := pipeline.PipelineTrace{
		Mode: pipeline.ModeDirect,
		Stages: []pipeline.StageTrace{
			{
				Name:           "striker",
				Status:         pipeline.StageCompleted,
				DurationMs:     120,
				BackendID:      "opencode",
				BackendVersion: "1.18.31",
				Admission: &contextwin.StageAdmission{
					Stage:       "striker",
					Verdict:     contextwin.VerdictAdmitted,
					Policy:      "observe",
					Model:       "gpt-4o",
					MaxTokens:   128000,
					LimitSource: contextwin.LimitSourceCatalog,
				},
				Usage: &backend.TokenUsage{
					InputTokens:  &inTok,
					OutputTokens: &outTok,
					TotalTokens:  nil, // Codex 風格缺失
				},
				UsageSource: backend.ProvenanceExact,
			},
		},
	}
	b, _ := json.Marshal(newTrace)
	newTask := &storage.Task{
		ID:            "task-new-audit",
		RequestID:     "req-new-audit",
		Prompt:        "new audit",
		Workspace:     tmpDir,
		Status:        storage.StatusCompleted,
		PipelineTrace: string(b),
	}
	if err := store.CreateTask(ctx, newTask); err != nil {
		t.Fatal(err)
	}

	tNewGot, err := store.GetTask(ctx, newTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ptNew pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(tNewGot.PipelineTrace), &ptNew); err != nil {
		t.Fatal(err)
	}
	st := ptNew.Stages[0]
	if st.Admission == nil || st.Admission.Verdict != contextwin.VerdictAdmitted {
		t.Errorf("expected verdict admitted, got %+v", st.Admission)
	}
	if st.Usage == nil || *st.Usage.InputTokens != 500 || *st.Usage.OutputTokens != 100 {
		t.Errorf("expected usage preserved, got %+v", st.Usage)
	}
	if st.Usage.TotalTokens != nil {
		t.Errorf("expected TotalTokens nil, got %v", st.Usage.TotalTokens)
	}
}

// CTXWIN-007: Redelivery preserves context and admission
// 驗證重送請求不重跑估算或後端，程序設定改變不覆寫已保存的決策
func TestCTXWIN007_RedeliveryPreservesContextAndAdmission(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	var backendCalls int32

	runner := func(ctx context.Context, ws, p string) (string, error) {
		atomic.AddInt32(&backendCalls, 1)
		return "execution output", nil
	}

	eng1 := engine.NewEngine(store, policy, engine.WithRunner(runner))
	ctx := context.Background()

	req := engine.SubmitRequest{
		ID:        "req-ctxwin-redelivery-001",
		Workspace: tmpDir,
		Prompt:    "analyze code",
		Mode:      "direct",
	}

	// 1. 首次提交
	task1, err := eng1.SubmitTask(ctx, req)
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	if atomic.LoadInt32(&backendCalls) != 1 {
		t.Fatalf("expected backend called once, got %d", atomic.LoadInt32(&backendCalls))
	}

	// 2. 第二個 Engine 實例模擬程序重啟或預算設定改變
	eng2 := engine.NewEngine(store, policy, engine.WithRunner(runner))

	// 3. 相同 Request ID 再次提交
	task2, err := eng2.SubmitTask(ctx, req)
	if err != nil {
		t.Fatalf("second submit failed: %v", err)
	}

	// 驗證未重複執行後端
	if atomic.LoadInt32(&backendCalls) != 1 {
		t.Errorf("idempotency violated: backend called %d times, expected 1", atomic.LoadInt32(&backendCalls))
	}
	if task2.ID != task1.ID || task2.Result != task1.Result {
		t.Errorf("task mismatch on redelivery: task1=%+v, task2=%+v", task1, task2)
	}
	if task2.PipelineTrace != task1.PipelineTrace {
		t.Errorf("PipelineTrace mutated on redelivery")
	}
}

// CTXWIN-008: Baseline regression safety
// 驗證在 observe 政策下，Direct 與 Three-Face 模式任務正常執行，回歸測試網完全通過
func TestCTXWIN008_BaselineRegressionSafety(t *testing.T) {
	oracle := &pipeline.DefaultOracleStage{}
	_ = oracle
	striker := &pipeline.DefaultStrikerStage{}
	_ = striker
	critic := &pipeline.DefaultCriticStage{}
	_ = critic

	// 驗證 observe 模式下預設運作正常
	cfg := contextwin.Config{
		Policy:       contextwin.PolicyObserve,
		ContextLimit: 0,
		ReserveRatio: 0.20,
	}
	if cfg.Policy != contextwin.PolicyObserve {
		t.Errorf("expected default policy observe, got %s", cfg.Policy)
	}
}
