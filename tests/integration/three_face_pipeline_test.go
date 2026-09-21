package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
)

// pipelineTestFixture 提供整合測試所需的 Daemon、Client 與可自訂行為的 Mock Runner
type pipelineTestFixture struct {
	rootDir  string
	projDir  string
	dbPath   string
	sockPath string
	store    *storage.SQLiteStore
	policy   *auth.Policy
	daemon   *daemon.Daemon
	client   *daemon.Client

	mu               sync.Mutex
	callHistory      []string
	oracleFunc       func(ctx context.Context, execCtx *engine.ExecutionContext) (*pipeline.OraclePlan, error)
	strikerFunc      func(ctx context.Context, execCtx *engine.ExecutionContext, plan *pipeline.OraclePlan) (string, error)
	criticFunc       func(ctx context.Context, execCtx *engine.ExecutionContext, plan *pipeline.OraclePlan, strikerOutput string) (*pipeline.CriticReview, error)
	directRunnerFunc func(ctx context.Context, ws string, prompt string) (string, error)
}

func setupPipelineTestFixture(t *testing.T) *pipelineTestFixture {
	t.Helper()
	rootDir := t.TempDir()
	projDir := filepath.Join(rootDir, "project-a")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	dbPath := filepath.Join(rootDir, "pipeline_test.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}

	policy := auth.NewPolicy([]string{rootDir})
	sockPath := filepath.Join(rootDir, "asura_pipeline.sock")

	f := &pipelineTestFixture{
		rootDir:     rootDir,
		projDir:     projDir,
		dbPath:      dbPath,
		sockPath:    sockPath,
		store:       store,
		policy:      policy,
		callHistory: make([]string, 0),
	}

	// 預設各 Stage 行為
	f.oracleFunc = func(ctx context.Context, execCtx *engine.ExecutionContext) (*pipeline.OraclePlan, error) {
		f.mu.Lock()
		f.callHistory = append(f.callHistory, "oracle")
		f.mu.Unlock()
		return &pipeline.OraclePlan{
			Goal:            "標準架構規劃",
			Constraints:     []string{"no-side-effects", "strict-types"},
			Steps:           []string{"step 1: 分析需求", "step 2: 產生代碼"},
			ExpectedOutcome: "任務成功達成",
		}, nil
	}

	f.strikerFunc = func(ctx context.Context, execCtx *engine.ExecutionContext, plan *pipeline.OraclePlan) (string, error) {
		if plan == nil {
			prompt := ""
			workspace := ""
			if execCtx != nil {
				workspace = execCtx.Workspace
				prompt = engine.ComposePrompt(execCtx.OriginalPrompt, execCtx)
			}
			return f.directRunnerFunc(ctx, workspace, prompt)
		}
		f.mu.Lock()
		f.callHistory = append(f.callHistory, "striker")
		f.mu.Unlock()
		return fmt.Sprintf("Striker 已依據計畫完成工作 [Plan Goal: %s]", plan.Goal), nil
	}

	f.criticFunc = func(ctx context.Context, execCtx *engine.ExecutionContext, plan *pipeline.OraclePlan, strikerOutput string) (*pipeline.CriticReview, error) {
		f.mu.Lock()
		f.callHistory = append(f.callHistory, "critic")
		f.mu.Unlock()
		return &pipeline.CriticReview{
			Verdict:  pipeline.VerdictPass,
			Findings: []string{"符合規範", "測試皆通過"},
			Summary:  "審查完全通過",
		}, nil
	}

	f.directRunnerFunc = func(ctx context.Context, ws string, prompt string) (string, error) {
		f.mu.Lock()
		f.callHistory = append(f.callHistory, "direct-striker")
		f.mu.Unlock()
		return "Direct Runner 執行成功", nil
	}

	// 包裝 Stage
	oracleStage := &mockOracleStage{fixture: f}
	strikerStage := &mockStrikerStage{fixture: f}
	criticStage := &mockCriticStage{fixture: f}
	orc := pipeline.NewOrchestrator(oracleStage, strikerStage, criticStage)

	runnerWrapper := func(ctx context.Context, ws string, prompt string) (string, error) {
		return f.directRunnerFunc(ctx, ws, prompt)
	}

	eng := engine.NewEngine(store, policy,
		engine.WithRunner(runnerWrapper),
		engine.WithPipelineRunner(orc),
	)

	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("Start daemon failed: %v", err)
	}
	f.daemon = d

	waitForSocket(t, sockPath)

	client, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	f.client = client

	t.Cleanup(func() {
		client.Close()
		d.Close()
		store.Close()
	})

	return f
}

type mockOracleStage struct {
	fixture *pipelineTestFixture
}

func (m *mockOracleStage) Execute(ctx context.Context, execCtx *engine.ExecutionContext) (*pipeline.OraclePlan, error) {
	return m.fixture.oracleFunc(ctx, execCtx)
}

type mockStrikerStage struct {
	fixture *pipelineTestFixture
}

func (m *mockStrikerStage) Execute(ctx context.Context, execCtx *engine.ExecutionContext, plan *pipeline.OraclePlan) (string, error) {
	return m.fixture.strikerFunc(ctx, execCtx, plan)
}

type mockCriticStage struct {
	fixture *pipelineTestFixture
}

func (m *mockCriticStage) Execute(ctx context.Context, execCtx *engine.ExecutionContext, plan *pipeline.OraclePlan, strikerOutput string) (*pipeline.CriticReview, error) {
	return m.fixture.criticFunc(ctx, execCtx, plan, strikerOutput)
}

func (f *pipelineTestFixture) getCallHistory() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make([]string, len(f.callHistory))
	copy(copied, f.callHistory)
	return copied
}

func (f *pipelineTestFixture) resetCallHistory() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callHistory = make([]string, 0)
}

func (f *pipelineTestFixture) waitForTask(t *testing.T, taskID string) *storage.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	ctx := context.Background()
	for time.Now().Before(deadline) {
		task, err := f.client.GetTask(ctx, taskID)
		if err == nil && task != nil {
			if task.Status == storage.StatusCompleted || task.Status == storage.StatusFailed || task.Status == storage.StatusCancelled {
				return task
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for task %s to complete", taskID)
	return nil
}

// =========================================================================================
// @TFACE-001: Three-Face 任務依固定順序完成
// =========================================================================================
func TestTFACE001_FixedOrderExecution(t *testing.T) {
	f := setupPipelineTestFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	task, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projDir,
		Prompt:    "開發新模組",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	completed := f.waitForTask(t, task.ID)
	if completed.Status != storage.StatusCompleted {
		t.Fatalf("expected completed, got %s: %s", completed.Status, completed.ErrorMessage)
	}

	// 驗證順序嚴格為 Oracle -> Striker -> Critic 各執行一次
	history := f.getCallHistory()
	if len(history) != 3 {
		t.Fatalf("expected 3 calls, got %d: %v", len(history), history)
	}
	if history[0] != "oracle" || history[1] != "striker" || history[2] != "critic" {
		t.Fatalf("expected [oracle, striker, critic], got %v", history)
	}

	// 驗證保存 pipeline trace
	if completed.PipelineTrace == "" {
		t.Fatalf("expected PipelineTrace to be non-empty")
	}
	var trace pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(completed.PipelineTrace), &trace); err != nil {
		t.Fatalf("unmarshal trace failed: %v", err)
	}
	if trace.Mode != pipeline.ModeThreeFace {
		t.Errorf("expected trace mode %q, got %q", pipeline.ModeThreeFace, trace.Mode)
	}
	if len(trace.Stages) != 3 {
		t.Errorf("expected 3 stages in trace, got %d", len(trace.Stages))
	}
}

// =========================================================================================
// @TFACE-002: Oracle Plan 為結構化且可追溯
// =========================================================================================
func TestTFACE002_OraclePlanStructuredAndTraceable(t *testing.T) {
	f := setupPipelineTestFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	customGoal := "高可用微服務架構規劃-2026-09-17"
	f.oracleFunc = func(ctx context.Context, execCtx *engine.ExecutionContext) (*pipeline.OraclePlan, error) {
		f.mu.Lock()
		f.callHistory = append(f.callHistory, "oracle")
		f.mu.Unlock()
		return &pipeline.OraclePlan{
			Goal:            customGoal,
			Constraints:     []string{"max-p99-latency-50ms", "zero-downtime"},
			Steps:           []string{"step 1: 設計資料庫", "step 2: 實作 API", "step 3: 壓力測試"},
			ExpectedOutcome: "達到 10,000 QPS",
		}, nil
	}

	var strikerReceivedPlan *pipeline.OraclePlan
	f.strikerFunc = func(ctx context.Context, execCtx *engine.ExecutionContext, plan *pipeline.OraclePlan) (string, error) {
		f.mu.Lock()
		f.callHistory = append(f.callHistory, "striker")
		strikerReceivedPlan = plan
		f.mu.Unlock()
		return "Striker completed successfully", nil
	}

	task, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projDir,
		Prompt:    "建構微服務",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	completed := f.waitForTask(t, task.ID)
	if completed.Status != storage.StatusCompleted {
		t.Fatalf("expected completed, got %s: %s", completed.Status, completed.ErrorMessage)
	}

	// 驗證 Striker 取得該結構化 Plan
	if strikerReceivedPlan == nil || strikerReceivedPlan.Goal != customGoal {
		t.Fatalf("striker did not receive expected plan, got: %+v", strikerReceivedPlan)
	}

	// 驗證查詢 Task 可取得當時的 Plan
	var trace pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(completed.PipelineTrace), &trace); err != nil {
		t.Fatalf("unmarshal trace: %v", err)
	}
	if trace.OraclePlan == nil || trace.OraclePlan.Goal != customGoal {
		t.Errorf("expected trace.OraclePlan.Goal %q, got %+v", customGoal, trace.OraclePlan)
	}
	if len(trace.OraclePlan.Constraints) != 2 || trace.OraclePlan.Constraints[0] != "max-p99-latency-50ms" {
		t.Errorf("expected trace.OraclePlan.Constraints preserved, got %+v", trace.OraclePlan.Constraints)
	}
}

// =========================================================================================
// @TFACE-003: 所有角色使用同一 ExecutionContext
// =========================================================================================
func TestTFACE003_SharedSealedExecutionContext(t *testing.T) {
	f := setupPipelineTestFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	memContent := "初始不可變專案記憶-2026-09-17"
	mem, err := f.client.AddMemory(ctx, f.projDir, memContent)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	var oracleContext, strikerContext, criticContext *engine.ExecutionContext
	f.oracleFunc = func(ctx context.Context, execCtx *engine.ExecutionContext) (*pipeline.OraclePlan, error) {
		f.mu.Lock()
		f.callHistory = append(f.callHistory, "oracle")
		oracleContext = execCtx
		f.mu.Unlock()
		return &pipeline.OraclePlan{Goal: "Shared Context Test"}, nil
	}
	f.strikerFunc = func(ctx context.Context, execCtx *engine.ExecutionContext, plan *pipeline.OraclePlan) (string, error) {
		f.mu.Lock()
		f.callHistory = append(f.callHistory, "striker")
		strikerContext = execCtx
		f.mu.Unlock()
		return "Striker Output", nil
	}
	f.criticFunc = func(ctx context.Context, execCtx *engine.ExecutionContext, plan *pipeline.OraclePlan, strikerOutput string) (*pipeline.CriticReview, error) {
		f.mu.Lock()
		f.callHistory = append(f.callHistory, "critic")
		criticContext = execCtx
		f.mu.Unlock()
		return &pipeline.CriticReview{Verdict: pipeline.VerdictPass}, nil
	}

	task, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projDir,
		Prompt:    "密封測試任務",
		Mode:      "three-face",
		MemoryIDs: []string{mem.ID},
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// 關鍵步驟：在任務接受後，立即刪除該記憶！
	if err := f.client.DeleteMemory(ctx, f.projDir, mem.ID); err != nil {
		t.Fatalf("DeleteMemory failed: %v", err)
	}

	completed := f.waitForTask(t, task.ID)
	if completed.Status != storage.StatusCompleted {
		t.Fatalf("expected completed, got %s: %s", completed.Status, completed.ErrorMessage)
	}

	// 驗證三個角色看到的 ExecutionContext 完全一致且包含已被刪除的記憶
	if oracleContext == nil || strikerContext == nil || criticContext == nil {
		t.Fatalf("contexts missing: oracle=%v, striker=%v, critic=%v", oracleContext, strikerContext, criticContext)
	}
	if len(oracleContext.ProjectMemories) != 1 || oracleContext.ProjectMemories[0].Content != memContent {
		t.Errorf("oracle did not receive original memory, got: %+v", oracleContext.ProjectMemories)
	}
	if len(strikerContext.ProjectMemories) != 1 || strikerContext.ProjectMemories[0].Content != memContent {
		t.Errorf("striker did not receive original memory, got: %+v", strikerContext.ProjectMemories)
	}
	if len(criticContext.ProjectMemories) != 1 || criticContext.ProjectMemories[0].Content != memContent {
		t.Errorf("critic did not receive original memory, got: %+v", criticContext.ProjectMemories)
	}
}

// =========================================================================================
// @TFACE-004: Oracle 失敗時 Fail-Closed
// =========================================================================================
func TestTFACE004_OracleFailureFailClosed(t *testing.T) {
	f := setupPipelineTestFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	oracleErr := errors.New("oracle planning timeout or model failure")
	f.oracleFunc = func(ctx context.Context, execCtx *engine.ExecutionContext) (*pipeline.OraclePlan, error) {
		f.mu.Lock()
		f.callHistory = append(f.callHistory, "oracle")
		f.mu.Unlock()
		return nil, oracleErr
	}

	task, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projDir,
		Prompt:    "測試 Oracle 故障",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	failed := f.waitForTask(t, task.ID)
	if failed.Status != storage.StatusFailed {
		t.Fatalf("expected status failed, got %s", failed.Status)
	}

	// 驗證 Striker 不啟動、Critic 不啟動
	history := f.getCallHistory()
	if len(history) != 1 || history[0] != "oracle" {
		t.Fatalf("expected only oracle to be called, got %v", history)
	}

	// 驗證 Oracle 錯誤訊息可追溯
	if !strings.Contains(failed.ErrorMessage, "oracle planning timeout") {
		t.Errorf("expected error message to contain oracle error, got: %s", failed.ErrorMessage)
	}

	var trace pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(failed.PipelineTrace), &trace); err != nil {
		t.Fatalf("unmarshal trace: %v", err)
	}
	if len(trace.Stages) != 1 || trace.Stages[0].Status != pipeline.StageFailed {
		t.Errorf("expected 1 failed stage in trace, got: %+v", trace.Stages)
	}
}

// =========================================================================================
// @TFACE-005: Striker 失敗不進行 Critic 或自動重試
// =========================================================================================
func TestTFACE005_StrikerFailureNoCriticNoRetry(t *testing.T) {
	f := setupPipelineTestFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	strikerErr := errors.New("striker compilation error in workspace")
	f.strikerFunc = func(ctx context.Context, execCtx *engine.ExecutionContext, plan *pipeline.OraclePlan) (string, error) {
		f.mu.Lock()
		f.callHistory = append(f.callHistory, "striker")
		f.mu.Unlock()
		return "partial output before failure", strikerErr
	}

	task, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projDir,
		Prompt:    "測試 Striker 故障",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	failed := f.waitForTask(t, task.ID)
	if failed.Status != storage.StatusFailed {
		t.Fatalf("expected status failed, got %s", failed.Status)
	}

	// 驗證 Critic 不啟動，Striker 不自動 retry
	history := f.getCallHistory()
	if len(history) != 2 || history[0] != "oracle" || history[1] != "striker" {
		t.Fatalf("expected [oracle, striker] calls, got %v", history)
	}

	// 驗證保存 Oracle Plan 與 Striker 錯誤
	var trace pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(failed.PipelineTrace), &trace); err != nil {
		t.Fatalf("unmarshal trace: %v", err)
	}
	if trace.OraclePlan == nil {
		t.Errorf("expected trace to preserve OraclePlan")
	}
	if len(trace.Stages) != 2 || trace.Stages[1].Status != pipeline.StageFailed {
		t.Errorf("expected stage 1 to be failed, got: %+v", trace.Stages)
	}
	if !strings.Contains(failed.ErrorMessage, "striker compilation error") {
		t.Errorf("expected error message to contain striker error, got: %s", failed.ErrorMessage)
	}
}

// =========================================================================================
// @TFACE-006: Critic 成功 Review Striker Output
// =========================================================================================
func TestTFACE006_CriticReviewSuccess(t *testing.T) {
	f := setupPipelineTestFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	customFindings := []string{"效能提升 20%", "無安全性疑慮"}
	customSummary := "審查報告：非常優良"
	f.criticFunc = func(ctx context.Context, execCtx *engine.ExecutionContext, plan *pipeline.OraclePlan, strikerOutput string) (*pipeline.CriticReview, error) {
		f.mu.Lock()
		f.callHistory = append(f.callHistory, "critic")
		f.mu.Unlock()
		return &pipeline.CriticReview{
			Verdict:  pipeline.VerdictPass,
			Findings: customFindings,
			Summary:  customSummary,
		}, nil
	}

	task, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projDir,
		Prompt:    "高品質產出任務",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	completed := f.waitForTask(t, task.ID)
	if completed.Status != storage.StatusCompleted {
		t.Fatalf("expected completed, got %s: %s", completed.Status, completed.ErrorMessage)
	}

	var trace pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(completed.PipelineTrace), &trace); err != nil {
		t.Fatalf("unmarshal trace: %v", err)
	}
	if trace.CriticReview == nil {
		t.Fatalf("expected CriticReview in trace")
	}
	if trace.CriticReview.Verdict != pipeline.VerdictPass {
		t.Errorf("expected verdict pass, got %s", trace.CriticReview.Verdict)
	}
	if len(trace.CriticReview.Findings) != 2 || trace.CriticReview.Findings[0] != customFindings[0] {
		t.Errorf("expected findings %v, got %v", customFindings, trace.CriticReview.Findings)
	}
	if trace.CriticReview.Summary != customSummary {
		t.Errorf("expected summary %q, got %q", customSummary, trace.CriticReview.Summary)
	}
}

// =========================================================================================
// @TFACE-007: Critic 失敗不得假裝完整成功
// =========================================================================================
func TestTFACE007_CriticFailureMustNotClaimSuccess(t *testing.T) {
	t.Run("Critic 執行異常報錯", func(t *testing.T) {
		f := setupPipelineTestFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		criticErr := errors.New("critic network error")
		f.criticFunc = func(ctx context.Context, execCtx *engine.ExecutionContext, plan *pipeline.OraclePlan, strikerOutput string) (*pipeline.CriticReview, error) {
			f.mu.Lock()
			f.callHistory = append(f.callHistory, "critic")
			f.mu.Unlock()
			return nil, criticErr
		}

		task, err := f.client.Submit(ctx, engine.SubmitRequest{
			Workspace: f.projDir,
			Prompt:    "Critic 報錯測試",
			Mode:      "three-face",
		})
		if err != nil {
			t.Fatalf("Submit failed: %v", err)
		}

		failed := f.waitForTask(t, task.ID)
		if failed.Status != storage.StatusFailed {
			t.Fatalf("expected task status failed, got %s", failed.Status)
		}

		// 驗證保存既有 Striker Output
		var trace pipeline.PipelineTrace
		if err := json.Unmarshal([]byte(failed.PipelineTrace), &trace); err != nil {
			t.Fatalf("unmarshal trace: %v", err)
		}
		if len(trace.Stages) != 3 || trace.Stages[1].Status != pipeline.StageCompleted || trace.Stages[1].Output == "" {
			t.Errorf("expected striker stage output preserved, got: %+v", trace.Stages)
		}
		if trace.Stages[2].Status != pipeline.StageFailed {
			t.Errorf("expected critic stage failed, got: %+v", trace.Stages[2])
		}
	})

	t.Run("Critic Verdict 判定 Failed", func(t *testing.T) {
		f := setupPipelineTestFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		f.criticFunc = func(ctx context.Context, execCtx *engine.ExecutionContext, plan *pipeline.OraclePlan, strikerOutput string) (*pipeline.CriticReview, error) {
			f.mu.Lock()
			f.callHistory = append(f.callHistory, "critic")
			f.mu.Unlock()
			return &pipeline.CriticReview{
				Verdict:  pipeline.VerdictFailed,
				Findings: []string{"發現嚴重記憶體洩漏風險"},
				Summary:  "不符驗收標準",
			}, nil
		}

		task, err := f.client.Submit(ctx, engine.SubmitRequest{
			Workspace: f.projDir,
			Prompt:    "Critic 拒絕測試",
			Mode:      "three-face",
		})
		if err != nil {
			t.Fatalf("Submit failed: %v", err)
		}

		failed := f.waitForTask(t, task.ID)
		if failed.Status != storage.StatusFailed {
			t.Fatalf("expected task status failed on verdict failed, got %s", failed.Status)
		}
	})
}

// =========================================================================================
// @TFACE-008: Direct mode 維持既有任務語意
// =========================================================================================
func TestTFACE008_DirectModeSemanticsPreserved(t *testing.T) {
	f := setupPipelineTestFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	memContent := "Direct 模式專案記憶"
	mem, err := f.client.AddMemory(ctx, f.projDir, memContent)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	var directRunnerPrompt string
	f.directRunnerFunc = func(ctx context.Context, ws string, prompt string) (string, error) {
		f.mu.Lock()
		f.callHistory = append(f.callHistory, "direct-striker")
		directRunnerPrompt = prompt
		f.mu.Unlock()
		return "Direct Mode 執行完成", nil
	}

	// 預設或指定 direct mode
	task, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projDir,
		Prompt:    "直通模式任務",
		Mode:      "direct",
		MemoryIDs: []string{mem.ID},
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	completed := f.waitForTask(t, task.ID)
	if completed.Status != storage.StatusCompleted {
		t.Fatalf("expected completed, got %s: %s", completed.Status, completed.ErrorMessage)
	}

	// 斷言：不啟動 Oracle 與 Critic，只執行 direct-striker 1 次
	history := f.getCallHistory()
	if len(history) != 1 || history[0] != "direct-striker" {
		t.Fatalf("expected only 1 direct-striker call, got: %v", history)
	}

	// 斷言：Prompt 注入 Markdown 記憶區塊與 Phase 3B 一致
	if !strings.Contains(directRunnerPrompt, "### Project Context") || !strings.Contains(directRunnerPrompt, memContent) {
		t.Errorf("expected direct runner prompt to contain memory context, got: %s", directRunnerPrompt)
	}
}

// =========================================================================================
// @TFACE-009: Three-Face Redelivery 不造成重複執行
// =========================================================================================
func TestTFACE009_ThreeFaceRedeliveryIdempotency(t *testing.T) {
	f := setupPipelineTestFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	customReqID := "three-face-req-idempotent-123"
	task1, err := f.client.Submit(ctx, engine.SubmitRequest{
		ID:        customReqID,
		Workspace: f.projDir,
		Prompt:    "冪等防禦測試",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("First submit failed: %v", err)
	}

	completed1 := f.waitForTask(t, task1.ID)
	if completed1.Status != storage.StatusCompleted {
		t.Fatalf("expected completed, got %s", completed1.Status)
	}

	historyBefore := f.getCallHistory()
	if len(historyBefore) != 3 {
		t.Fatalf("expected 3 calls on initial run, got %v", historyBefore)
	}

	// 再次送出相同 Request ID 與相同 Payload (Redelivery)
	task2, err := f.client.Submit(ctx, engine.SubmitRequest{
		ID:        customReqID,
		Workspace: f.projDir,
		Prompt:    "冪等防禦測試",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("Second submit (redelivery) failed: %v", err)
	}

	if task2.ID != task1.ID {
		t.Fatalf("expected same task ID %s, got %s", task1.ID, task2.ID)
	}

	// 斷言：呼叫歷史完全沒有增加（不重複呼叫 Oracle, Striker, Critic）
	historyAfter := f.getCallHistory()
	if len(historyAfter) != 3 {
		t.Fatalf("redelivery triggered unexpected execution! calls: %v", historyAfter)
	}

	// 斷言：Pipeline trace 保持一致
	if task2.PipelineTrace != completed1.PipelineTrace {
		t.Errorf("expected identical pipeline trace on redelivery")
	}
}

// =========================================================================================
// @TFACE-010: Pipeline Metrics 可識別額外模型成本
// =========================================================================================
func TestTFACE010_PipelineMetricsAndCostDistinction(t *testing.T) {
	f := setupPipelineTestFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. 執行 Direct 任務
	directTask, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projDir,
		Prompt:    "Direct 指標測試",
		Mode:      "direct",
	})
	if err != nil {
		t.Fatalf("Direct submit failed: %v", err)
	}
	completedDirect := f.waitForTask(t, directTask.ID)

	var directTrace pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(completedDirect.PipelineTrace), &directTrace); err != nil {
		t.Fatalf("unmarshal direct trace: %v", err)
	}
	if directTrace.ModelCalls != 1 {
		t.Errorf("expected direct ModelCalls=1, got %d", directTrace.ModelCalls)
	}
	if len(directTrace.Stages) != 1 || directTrace.Stages[0].Name != "striker" {
		t.Errorf("expected direct to have 1 striker stage, got %+v", directTrace.Stages)
	}

	// 2. 執行 Three-Face 任務
	threeFaceTask, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projDir,
		Prompt:    "Three-Face 指標測試",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("Three-face submit failed: %v", err)
	}
	completedThreeFace := f.waitForTask(t, threeFaceTask.ID)

	var tfTrace pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(completedThreeFace.PipelineTrace), &tfTrace); err != nil {
		t.Fatalf("unmarshal three-face trace: %v", err)
	}
	if tfTrace.ModelCalls != 3 {
		t.Errorf("expected three-face ModelCalls=3, got %d", tfTrace.ModelCalls)
	}
	if len(tfTrace.Stages) != 3 {
		t.Fatalf("expected 3 stages in three-face trace, got %d", len(tfTrace.Stages))
	}
	expectedStageNames := []string{"oracle", "striker", "critic"}
	for i, name := range expectedStageNames {
		if tfTrace.Stages[i].Name != name {
			t.Errorf("expected stage %d to be %q, got %q", i, name, tfTrace.Stages[i].Name)
		}
		if tfTrace.Stages[i].Status != pipeline.StageCompleted {
			t.Errorf("expected stage %q to be completed, got %q", name, tfTrace.Stages[i].Status)
		}
	}
}
