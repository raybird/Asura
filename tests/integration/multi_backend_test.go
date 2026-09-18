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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/backend"
	"asura/internal/backend/codex"
	"asura/internal/backend/opencode"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
)

// mockTrackableBackend 用於追蹤調用次數、角色與模式的通用測試後端
type mockTrackableBackend struct {
	id         string
	version    string
	caps       []backend.Capability
	calls      int32
	callRoles  []backend.Role
	mu         sync.Mutex
	customRun  func(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error)
}

func (m *mockTrackableBackend) ID() string { return m.id }
func (m *mockTrackableBackend) Version(ctx context.Context) (string, error) {
	return m.version, nil
}
func (m *mockTrackableBackend) Capabilities() []backend.Capability { return m.caps }
func (m *mockTrackableBackend) HasCapability(cap backend.Capability) bool {
	for _, c := range m.caps {
		if c == cap {
			return true
		}
	}
	return false
}
func (m *mockTrackableBackend) Run(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	atomic.AddInt32(&m.calls, 1)
	m.mu.Lock()
	m.callRoles = append(m.callRoles, req.Role)
	m.mu.Unlock()

	if m.customRun != nil {
		return m.customRun(ctx, req)
	}

	out := fmt.Sprintf("output from %s (%s)", m.id, req.Role)
	if req.Role == backend.RoleOracle {
		out = `{"goal":"G","constraints":["C"],"steps":["S1"],"expected_outcome":"O"}`
	} else if req.Role == backend.RoleCritic {
		out = `{"verdict":"pass","summary":"LGTM"}`
	}
	return &backend.ExecutionResult{
		BackendID:      m.id,
		BackendVersion: m.version,
		Output:         out,
	}, nil
}

type mbackTestFixture struct {
	rootDir   string
	sockPath  string
	store     *storage.SQLiteStore
	policy    *auth.Policy
	reg       *backend.InMemoryRegistry
	opencodeB *mockTrackableBackend
	codexB    *mockTrackableBackend
	daemon    *daemon.Daemon
	client    *daemon.Client
}

func setupMBACKFixture(t *testing.T, opts ...func(*mbackTestFixture)) *mbackTestFixture {
	t.Helper()
	rootDir := t.TempDir()
	dbPath := filepath.Join(rootDir, "asura.db")
	sockPath := filepath.Join(rootDir, "m.sock")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}

	policy := auth.NewPolicy([]string{rootDir})

	opencodeB := &mockTrackableBackend{
		id:      "opencode",
		version: "1.18.31",
		caps: []backend.Capability{
			backend.CapReadOnlySandbox,
			backend.CapWorkspaceWrite,
			backend.CapProcessCancellation,
			backend.CapStructuredEvents,
		},
	}

	codexB := &mockTrackableBackend{
		id:      "codex",
		version: "0.155.0",
		caps: []backend.Capability{
			backend.CapReadOnlySandbox,
			backend.CapWorkspaceWrite,
			backend.CapProcessCancellation,
			backend.CapStructuredEvents,
		},
	}

	reg := backend.NewRegistry()
	_ = reg.Register(opencodeB)
	_ = reg.Register(codexB)

	fix := &mbackTestFixture{
		rootDir:   rootDir,
		sockPath:  sockPath,
		store:     store,
		policy:    policy,
		reg:       reg,
		opencodeB: opencodeB,
		codexB:    codexB,
	}

	for _, opt := range opts {
		opt(fix)
	}

	oracleStage := pipeline.NewOracleStageWithBackend(opencodeB)
	strikerStage := pipeline.NewStrikerStageWithBackend(opencodeB)
	criticStage := pipeline.NewCriticStageWithBackend(opencodeB)
	orc := pipeline.NewOrchestrator(oracleStage, strikerStage, criticStage).WithRegistry(reg)

	engRunner := func(ctx context.Context, ws, p string) (string, error) {
		var b backend.Backend = opencodeB
		if taskID := engine.TaskIDFromContext(ctx); taskID != "" {
			if t, err := store.GetTask(context.Background(), taskID); err == nil && t != nil && t.Backend != "" {
				if regB, err := reg.Get(t.Backend); err == nil && regB != nil {
					b = regB
				}
			}
		}
		res, err := b.Run(ctx, &backend.ExecutionRequest{
			Role:      backend.RoleStriker,
			Workspace: ws,
			Prompt:    p,
		})
		if err != nil {
			return "", err
		}
		return res.Output, nil
	}

	eng := engine.NewEngine(store, policy,
		engine.WithRunner(engRunner),
		engine.WithPipelineRunner(orc),
	)

	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("daemon.Start failed: %v", err)
	}
	fix.daemon = d

	client, err := daemon.NewClient(sockPath)
	if err != nil {
		_ = d.Close()
		t.Fatalf("NewClient failed: %v", err)
	}
	fix.client = client

	t.Cleanup(func() {
		_ = client.Close()
		_ = d.Close()
		_ = store.Close()
	})

	return fix
}

func waitForTaskTerminalState(t *testing.T, client *daemon.Client, taskID string, timeout time.Duration) *storage.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := client.GetTask(context.Background(), taskID)
		if err == nil && (task.Status == storage.StatusCompleted || task.Status == storage.StatusFailed || task.Status == storage.StatusCancelled) {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	latest, _ := client.GetTask(context.Background(), taskID)
	t.Fatalf("timed out waiting for task %s to reach terminal state, last task: %+v", taskID, latest)
	return nil
}

// @MBACK-001: 未指定 --backend 時預設使用 OpenCode，相容 Direct 與 Three-Face
func TestMBACK_001_DefaultBackendOpenCode(t *testing.T) {
	fix := setupMBACKFixture(t)

	// 1. Direct 模式未帶 backend
	taskDirect, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: fix.rootDir,
		Prompt:    "hello direct default",
		Mode:      "direct",
	})
	if err != nil {
		t.Fatalf("Submit direct failed: %v", err)
	}
	resDirect := waitForTaskTerminalState(t, fix.client, taskDirect.ID, 3*time.Second)
	if resDirect.Status != storage.StatusCompleted {
		t.Errorf("expected direct task completed, got %s (err: %s)", resDirect.Status, resDirect.ErrorMessage)
	}
	if resDirect.Backend != "opencode" {
		t.Errorf("expected task backend 'opencode', got %q", resDirect.Backend)
	}

	// 2. Three-Face 模式未帶 backend
	taskTF, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: fix.rootDir,
		Prompt:    "hello three-face default",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("Submit three-face failed: %v", err)
	}
	resTF := waitForTaskTerminalState(t, fix.client, taskTF.ID, 3*time.Second)
	if resTF.Status != storage.StatusCompleted {
		t.Errorf("expected three-face task completed, got %s (err: %s)", resTF.Status, resTF.ErrorMessage)
	}
	if resTF.Backend != "opencode" {
		t.Errorf("expected task backend 'opencode', got %q", resTF.Backend)
	}

	// 斷言：Codex 後端調用次數為 0，OpenCode 調用次數 > 0
	if codexCalls := atomic.LoadInt32(&fix.codexB.calls); codexCalls != 0 {
		t.Errorf("expected 0 calls to codex, got %d", codexCalls)
	}
	if opencodeCalls := atomic.LoadInt32(&fix.opencodeB.calls); opencodeCalls == 0 {
		t.Errorf("expected opencode to be called, got 0")
	}
}

// @MBACK-002: 指定 --backend codex 時調用 Codex CLI，OpenCode 調用次數為 0
func TestMBACK_002_ExplicitBackendCodex(t *testing.T) {
	fix := setupMBACKFixture(t)

	task, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: fix.rootDir,
		Prompt:    "run with codex",
		Backend:   "codex",
		Mode:      "direct",
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	res := waitForTaskTerminalState(t, fix.client, task.ID, 3*time.Second)
	if res.Status != storage.StatusCompleted {
		t.Errorf("expected task completed, got %s (err: %s)", res.Status, res.ErrorMessage)
	}
	if res.Backend != "codex" {
		t.Errorf("expected task backend 'codex', got %q", res.Backend)
	}

	// 斷言：OpenCode 呼叫次數恆為 0，Codex 呼叫次數 > 0
	if opencodeCalls := atomic.LoadInt32(&fix.opencodeB.calls); opencodeCalls != 0 {
		t.Errorf("expected 0 calls to opencode, got %d", opencodeCalls)
	}
	if codexCalls := atomic.LoadInt32(&fix.codexB.calls); codexCalls == 0 {
		t.Errorf("expected codex to be called, got 0")
	}
}

// @MBACK-003: 指定未知後端 --backend unknown_backend，在執行前 fail-closed 拒絕，0 進程與模型呼叫，不 fallback (Three-Face 與 Direct 模式)
func TestMBACK_003_UnknownBackendFailClosed(t *testing.T) {
	fix := setupMBACKFixture(t)

	// 1. Three-Face 模式未知後端
	taskTF, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: fix.rootDir,
		Prompt:    "run with unknown backend three-face",
		Backend:   "unknown_backend",
		Mode:      "three-face",
	})
	if err == nil {
		res := waitForTaskTerminalState(t, fix.client, taskTF.ID, 3*time.Second)
		if res.Status != storage.StatusFailed {
			t.Errorf("expected three-face task to fail for unknown backend, got %s", res.Status)
		}
		if !strings.Contains(res.ErrorMessage, "backend preflight failed") && !strings.Contains(res.ErrorMessage, "not found") {
			t.Errorf("expected error message to mention backend failure, got: %s", res.ErrorMessage)
		}
	}

	// 2. Direct 模式未知後端 (MUST FIX 2: 無 silent fallback)
	taskDirect, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: fix.rootDir,
		Prompt:    "run with unknown backend direct",
		Backend:   "unknown_backend",
		Mode:      "direct",
	})
	if err == nil {
		res := waitForTaskTerminalState(t, fix.client, taskDirect.ID, 3*time.Second)
		if res.Status != storage.StatusFailed {
			t.Errorf("expected direct task to fail for unknown backend, got %s", res.Status)
		}
		if !strings.Contains(res.ErrorMessage, "backend preflight failed") && !strings.Contains(res.ErrorMessage, "not found") {
			t.Errorf("expected direct error message to mention backend failure, got: %s", res.ErrorMessage)
		}
	}

	// 核心安全斷言：所有後端呼叫次數恆為 0，絕不自動 fallback 到 opencode
	if opencodeCalls := atomic.LoadInt32(&fix.opencodeB.calls); opencodeCalls != 0 {
		t.Errorf("expected 0 calls to opencode on unknown backend fail-closed, got %d", opencodeCalls)
	}
	if codexCalls := atomic.LoadInt32(&fix.codexB.calls); codexCalls != 0 {
		t.Errorf("expected 0 calls to codex, got %d", codexCalls)
	}
}

// @MBACK-004: 同 Request ID 帶不同 Backend 被拒絕
func TestMBACK_004_DifferentBackendConflictRejected(t *testing.T) {
	fix := setupMBACKFixture(t)

	// 1. 首次以 opencode 提交
	task1, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		ID:        "req-mback-004-conflict",
		Workspace: fix.rootDir,
		Prompt:    "fingerprint test",
		Backend:   "opencode",
	})
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	_ = waitForTaskTerminalState(t, fix.client, task1.ID, 3*time.Second)

	initialOpencodeRuns := atomic.LoadInt32(&fix.opencodeB.calls)
	initialCodexRuns := atomic.LoadInt32(&fix.codexB.calls)

	// 2. 相同 Request ID 再次以 codex 提交，必須被拒絕
	_, err = fix.client.Submit(context.Background(), engine.SubmitRequest{
		ID:        "req-mback-004-conflict",
		Workspace: fix.rootDir,
		Prompt:    "fingerprint test",
		Backend:   "codex",
	})
	if err == nil {
		t.Fatalf("expected conflict error when reusing Request ID with different backend, got nil")
	}
	if !strings.Contains(err.Error(), "request ID reused with different payload") {
		t.Errorf("expected 'request ID reused with different payload', got: %v", err)
	}

	// 斷言：模型調用次數未增加
	if currentRuns := atomic.LoadInt32(&fix.opencodeB.calls); currentRuns != initialOpencodeRuns {
		t.Errorf("expected 0 additional opencode calls, got %d", currentRuns-initialOpencodeRuns)
	}
	if currentCodex := atomic.LoadInt32(&fix.codexB.calls); currentCodex != initialCodexRuns {
		t.Errorf("expected 0 additional codex calls, got %d", currentCodex-initialCodexRuns)
	}
}

// @MBACK-005: 相同 Request ID 且相同 Backend 重送 (Redelivery)，返回既有 Task，0 重複調用
func TestMBACK_005_SameBackendRedelivery(t *testing.T) {
	fix := setupMBACKFixture(t)

	// 1. 首次提交
	task1, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		ID:        "req-mback-005-redelivery",
		Workspace: fix.rootDir,
		Prompt:    "redelivery test",
		Backend:   "codex",
	})
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	res1 := waitForTaskTerminalState(t, fix.client, task1.ID, 3*time.Second)

	initialCodexRuns := atomic.LoadInt32(&fix.codexB.calls)

	// 2. 相同 Request ID 且相同 Backend 重送
	task2, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		ID:        "req-mback-005-redelivery",
		Workspace: fix.rootDir,
		Prompt:    "redelivery test",
		Backend:   "codex",
	})
	if err != nil {
		t.Fatalf("redelivery submit failed: %v", err)
	}

	if task2.ID != task1.ID {
		t.Errorf("expected same task ID %q, got %q", task1.ID, task2.ID)
	}
	if task2.Backend != "codex" {
		t.Errorf("expected task backend 'codex', got %q", task2.Backend)
	}
	if task2.Result != res1.Result {
		t.Errorf("expected same result %q, got %q", res1.Result, task2.Result)
	}

	// 斷言：完全無二次調用
	if currentCodex := atomic.LoadInt32(&fix.codexB.calls); currentCodex != initialCodexRuns {
		t.Errorf("expected 0 additional codex calls on redelivery, got %d", currentCodex-initialCodexRuns)
	}
}

// @MBACK-006: 後端錯誤結構化正規化為 FailureClassification
func TestMBACK_006_FailureClassification(t *testing.T) {
	// 驗證 Codex Parser 精確將事件錯誤分類
	rawAuth := `{"type":"turn.completed","usage":{},"error":{"type":"auth_error","message":"Invalid API token provided"}}`
	_, _, _, failClassAuth, errAuth := codex.ExtractResultAndEvents(rawAuth, "")
	if errAuth == nil {
		t.Fatal("expected error for auth_error")
	}
	if failClassAuth != backend.FailureAuth {
		t.Errorf("expected FailureAuth, got %s", failClassAuth)
	}

	rawRL := `{"type":"turn.completed","usage":{},"error":{"type":"rate_limit_error","message":"Quota exceeded, retry in 20s"}}`
	_, _, _, failClassRL, errRL := codex.ExtractResultAndEvents(rawRL, "")
	if errRL == nil {
		t.Fatal("expected error for rate_limit_error")
	}
	if failClassRL != backend.FailureRateLimit {
		t.Errorf("expected FailureRateLimit, got %s", failClassRL)
	}

	rawEmpty := `{"type":"turn.completed","usage":{}}`
	_, _, _, failClassEmpty, errEmpty := codex.ExtractResultAndEvents(rawEmpty, "")
	if errEmpty == nil {
		t.Fatal("expected error for empty result")
	}
	if failClassEmpty != backend.FailureEmptyResult {
		t.Errorf("expected FailureEmptyResult, got %s", failClassEmpty)
	}
}

// @MBACK-007: 任務取消時 selected backend context 被取消，process tree 回收，Task 進入 StatusCancelled
func TestMBACK_007_CancellationProcessGroup(t *testing.T) {
	// 建立一個會長時間 sleep 的 fake codex binary
	tmpDir := t.TempDir()
	fakeBin := filepath.Join(tmpDir, "fake_codex.sh")
	fakeScript := `#!/bin/sh
if [ "$1" = "--version" ]; then
    echo "codex-cli 0.155.0"
    exit 0
fi
sleep 10
`
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0755); err != nil {
		t.Fatalf("failed to create fake codex script: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var pgid int
	adapter := codex.NewAdapter(codex.AdapterConfig{
		BinaryPath: fakeBin,
		OnProcessStarted: func(c context.Context, p int) {
			pgid = p
			cancel() // 啟動後立即取消
		},
	})

	_, err := adapter.Run(ctx, &backend.ExecutionRequest{
		Role:      backend.RoleStriker,
		Workspace: tmpDir,
		Prompt:    "10",
	})

	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled error, got: %v", err)
	}

	// 驗證進程組已被乾淨回收
	time.Sleep(50 * time.Millisecond)
	if pgid > 0 {
		errKill := syscall.Kill(-pgid, 0)
		if errKill == nil {
			t.Errorf("expected process group %d to be killed, but it is still alive", pgid)
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}
}

// @MBACK-008: Three-Face capability preflight 檢查唯讀沙箱能力，不支援者在 Oracle 前立即 fail-closed
func TestMBACK_008_ThreeFaceCapabilityPreflight(t *testing.T) {
	fix := setupMBACKFixture(t, func(f *mbackTestFixture) {
		// 註冊一個不具備 CapReadOnlySandbox 的弱後端
		weakBackend := &mockTrackableBackend{
			id:      "weak-backend",
			version: "1.0.0",
			caps:    []backend.Capability{backend.CapWorkspaceWrite}, // 缺少 CapReadOnlySandbox
		}
		_ = f.reg.Register(weakBackend)
	})

	task, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: fix.rootDir,
		Prompt:    "three-face on weak backend",
		Backend:   "weak-backend",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	res := waitForTaskTerminalState(t, fix.client, task.ID, 3*time.Second)
	if res.Status != storage.StatusFailed {
		t.Errorf("expected task to fail due to missing capability, got %s", res.Status)
	}
	if !strings.Contains(res.ErrorMessage, "does not support read-only sandbox") {
		t.Errorf("expected error message to mention read-only sandbox requirement, got: %s", res.ErrorMessage)
	}

	// 斷言：全後端 0 模型調用
	if opencodeCalls := atomic.LoadInt32(&fix.opencodeB.calls); opencodeCalls != 0 {
		t.Errorf("expected 0 opencode calls, got %d", opencodeCalls)
	}
	if codexCalls := atomic.LoadInt32(&fix.codexB.calls); codexCalls != 0 {
		t.Errorf("expected 0 codex calls, got %d", codexCalls)
	}
}

// @MBACK-009: PipelineTrace 完整記錄各 Stage 之 BackendID 與 Version (支援 Three-Face 與 Direct 模式)
func TestMBACK_009_PipelineTraceRecordsBackend(t *testing.T) {
	fix := setupMBACKFixture(t)

	// 1. Three-Face 模式 Trace
	taskTF, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: fix.rootDir,
		Prompt:    "trace audit test three-face",
		Backend:   "codex",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("Submit three-face failed: %v", err)
	}

	resTF := waitForTaskTerminalState(t, fix.client, taskTF.ID, 3*time.Second)
	if resTF.Status != storage.StatusCompleted {
		t.Fatalf("expected completed three-face task, got %s", resTF.Status)
	}

	if resTF.PipelineTrace == "" {
		t.Fatalf("expected non-empty PipelineTrace in three-face")
	}

	var traceTF pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(resTF.PipelineTrace), &traceTF); err != nil {
		t.Fatalf("unmarshal PipelineTrace failed: %v", err)
	}

	if traceTF.BackendID != "codex" {
		t.Errorf("expected trace BackendID 'codex', got %q", traceTF.BackendID)
	}

	if len(traceTF.Stages) != 3 {
		t.Fatalf("expected 3 stages in three-face trace, got %d", len(traceTF.Stages))
	}

	for _, stage := range traceTF.Stages {
		if stage.BackendID != "codex" {
			t.Errorf("stage %s: expected BackendID 'codex', got %q", stage.Name, stage.BackendID)
		}
		if stage.BackendVersion != "0.155.0" {
			t.Errorf("stage %s: expected BackendVersion '0.155.0', got %q", stage.Name, stage.BackendVersion)
		}
	}

	// 2. Direct 模式 Trace (MUST FIX 3: Direct 模式 trace 包含 BackendID 與 Version)
	taskDirect, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: fix.rootDir,
		Prompt:    "trace audit test direct",
		Backend:   "codex",
		Mode:      "direct",
	})
	if err != nil {
		t.Fatalf("Submit direct failed: %v", err)
	}

	resDirect := waitForTaskTerminalState(t, fix.client, taskDirect.ID, 3*time.Second)
	if resDirect.Status != storage.StatusCompleted {
		t.Fatalf("expected completed direct task, got %s (err: %s)", resDirect.Status, resDirect.ErrorMessage)
	}

	if resDirect.PipelineTrace == "" {
		t.Fatalf("expected non-empty PipelineTrace in direct mode")
	}

	var traceDirect pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(resDirect.PipelineTrace), &traceDirect); err != nil {
		t.Fatalf("unmarshal direct PipelineTrace failed: %v", err)
	}

	if traceDirect.BackendID != "codex" {
		t.Errorf("expected direct trace BackendID 'codex', got %q", traceDirect.BackendID)
	}
	if len(traceDirect.Stages) != 1 {
		t.Fatalf("expected 1 stage in direct trace, got %d", len(traceDirect.Stages))
	}
	if traceDirect.Stages[0].Name != "striker" {
		t.Errorf("expected stage name 'striker', got %q", traceDirect.Stages[0].Name)
	}
	if traceDirect.Stages[0].BackendID != "codex" {
		t.Errorf("direct stage: expected BackendID 'codex', got %q", traceDirect.Stages[0].BackendID)
	}
	if traceDirect.Stages[0].BackendVersion != "0.155.0" {
		t.Errorf("direct stage: expected BackendVersion '0.155.0', got %q", traceDirect.Stages[0].BackendVersion)
	}
}

// @MBACK-010: No failover after partial execution：Striker 失敗時保存 partial output 並 Failed，絕無切換至 OpenCode 重試
func TestMBACK_010_NoFailoverAfterPartialExecution(t *testing.T) {
	fix := setupMBACKFixture(t, func(f *mbackTestFixture) {
		f.codexB.customRun = func(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
			if req.Role == backend.RoleStriker {
				return &backend.ExecutionResult{
					BackendID:      "codex",
					BackendVersion: "0.155.0",
					PartialOutput:  "partial striker progress before crash",
				}, errors.New("simulated codex crash during striker")
			}
			return &backend.ExecutionResult{
				BackendID:      "codex",
				BackendVersion: "0.155.0",
				Output:         `{"goal":"G","constraints":[],"steps":["S"],"expected_outcome":"O"}`,
			}, nil
		}
	})

	task, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: fix.rootDir,
		Prompt:    "no failover test",
		Backend:   "codex",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	res := waitForTaskTerminalState(t, fix.client, task.ID, 3*time.Second)
	if res.Status != storage.StatusFailed {
		t.Errorf("expected task StatusFailed, got %s", res.Status)
	}
	if !strings.Contains(res.Result, "partial striker progress before crash") {
		t.Errorf("expected task Result to preserve partial output, got: %q", res.Result)
	}

	// 核心安全斷言：OpenCode 調用次數嚴格保持為 0，嚴禁任何 automatic failover 嘗試！
	if opencodeCalls := atomic.LoadInt32(&fix.opencodeB.calls); opencodeCalls != 0 {
		t.Errorf("VIOLATION OF NO-FAILOVER: opencode was called %d times after codex failure", opencodeCalls)
	}
}

// @MBACK-011: 後端原生事件（ndjson / jsonl）標準化映射為共同 BackendEvent
func TestMBACK_011_EventNormalization(t *testing.T) {
	// 1. 驗證 OpenCode ndjson 事件行正規化
	openLine := `{"type":"tool_use","timestamp":1789732800000,"part":{"tool":"view_file","state":{"status":"completed"}}}`
	openEvt, err := opencode.ParseEventLine(openLine)
	if err != nil || openEvt == nil {
		t.Fatalf("opencode ParseEventLine failed: %v", err)
	}
	if openEvt.Tool != "view_file" || openEvt.ToolStatus != "completed" {
		t.Errorf("unexpected normalized opencode event: %+v", openEvt)
	}

	// 2. 驗證 Codex jsonl 事件行正規化
	codexJSONL := strings.Join([]string{
		`{"type":"thread.started","thread_id":"th_1"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"it_1","type":"command_execution","command":"ls -l","status":"completed"}}`,
		`{"type":"item.completed","item":{"id":"it_2","type":"agent_message","text":"Analysis complete"}}`,
		`{"type":"turn.completed","usage":{"total_tokens":42}}`,
	}, "\n")

	output, _, events, _, err := codex.ExtractResultAndEvents(codexJSONL, "")
	if err != nil {
		t.Fatalf("codex ExtractResultAndEvents failed: %v", err)
	}
	if output != "Analysis complete" {
		t.Errorf("expected output 'Analysis complete', got %q", output)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 normalized backend events, got %d", len(events))
	}
	if events[0].Type != backend.EventTypeTool || events[0].Tool != "ls -l" {
		t.Errorf("unexpected first event: %+v", events[0])
	}
	if events[1].Type != backend.EventTypeText || events[1].Text != "Analysis complete" {
		t.Errorf("unexpected second event: %+v", events[1])
	}
}

// @MBACK-012: 排隊喚醒保護：Queued 任務喚醒時依然依照其記錄之 BackendID 執行，不重新套用預設值
func TestMBACK_012_QueueWakePreservesBackend(t *testing.T) {
	taskABlock := make(chan struct{})
	taskAStarted := make(chan struct{})

	fix := setupMBACKFixture(t, func(f *mbackTestFixture) {
		f.opencodeB.customRun = func(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
			if strings.Contains(req.Prompt, "occupy-lock") {
				close(taskAStarted)
				<-taskABlock
			}
			return &backend.ExecutionResult{
				BackendID: "opencode",
				Output:    "occupy lock done",
			}, nil
		}
	})

	// 1. 提交 Task 1 (持有專案鎖)
	task1, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: fix.rootDir,
		Prompt:    "occupy-lock",
		Backend:   "opencode",
	})
	if err != nil {
		t.Fatalf("submit task 1 failed: %v", err)
	}
	_ = task1

	select {
	case <-taskAStarted:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for task 1 to start")
	}

	// 2. 提交 Task 2 (指定 backend: codex)，同工作區必然排入隊列
	task2, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: fix.rootDir,
		Prompt:    "queued-codex-task",
		Backend:   "codex",
	})
	if err != nil {
		t.Fatalf("submit task 2 failed: %v", err)
	}

	if task2.Status != storage.StatusQueued {
		t.Fatalf("expected task 2 to be Queued, got %s", task2.Status)
	}
	if task2.Backend != "codex" {
		t.Errorf("expected task 2 Backend 'codex', got %q", task2.Backend)
	}

	// 3. 釋放 Task 1，排程器喚醒 Task 2
	close(taskABlock)

	res2 := waitForTaskTerminalState(t, fix.client, task2.ID, 5*time.Second)
	if res2.Status != storage.StatusCompleted {
		t.Fatalf("expected task 2 completed after queue wake, got %s (err: %s)", res2.Status, res2.ErrorMessage)
	}

	// 核心斷言：Task 2 喚醒執行後維持 codex，未被重新設定為 opencode
	if res2.Backend != "codex" {
		t.Errorf("expected final task 2 Backend 'codex', got %q", res2.Backend)
	}

	// 斷言：Codex 調用次數 > 0
	if codexCalls := atomic.LoadInt32(&fix.codexB.calls); codexCalls == 0 {
		t.Errorf("expected codex to be called for task 2, got 0")
	}
}

// TestMBACK_OpenCode_RoleRouting_Separation 驗證 OpenCode 後端在 Three-Face 模式下，
// Oracle 與 Critic 嚴格路由至 readOnlyAdapter，Striker 嚴格路由至 strikerAdapter (MUST FIX 1)
func TestMBACK_OpenCode_RoleRouting_Separation(t *testing.T) {
	roMock := &mockTrackableBackend{
		id:      "opencode-ro",
		version: "1.18.31",
		caps: []backend.Capability{
			backend.CapReadOnlySandbox,
			backend.CapWorkspaceWrite,
			backend.CapProcessCancellation,
			backend.CapStructuredEvents,
		},
	}
	strikerMock := &mockTrackableBackend{
		id:      "opencode-striker",
		version: "1.18.31",
		caps: []backend.Capability{
			backend.CapReadOnlySandbox,
			backend.CapWorkspaceWrite,
			backend.CapProcessCancellation,
			backend.CapStructuredEvents,
		},
	}

	roleRouting := opencode.NewRoleRoutingBackend(roMock, strikerMock)

	fix := setupMBACKFixture(t, func(f *mbackTestFixture) {
		// 將 registry 內的 "opencode" 替換為 roleRouting
		f.reg.Unregister("opencode")
		_ = f.reg.Register(roleRouting)
	})

	task, err := fix.client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: fix.rootDir,
		Prompt:    "test role routing separation",
		Backend:   "opencode",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	res := waitForTaskTerminalState(t, fix.client, task.ID, 3*time.Second)
	if res.Status != storage.StatusCompleted {
		t.Fatalf("expected task completed, got %s (err: %s)", res.Status, res.ErrorMessage)
	}

	// 核心安全斷言：
	// 1. readOnlyAdapter 必須被調用 2 次 (Oracle 與 Critic)
	// 2. strikerAdapter 必須被調用 1 次 (Striker)
	// 3. readOnlyAdapter 的調用角色只能是 Oracle 或 Critic
	// 4. strikerAdapter 的調用角色只能是 Striker
	roCalls := atomic.LoadInt32(&roMock.calls)
	strikerCalls := atomic.LoadInt32(&strikerMock.calls)

	if roCalls != 2 {
		t.Errorf("expected readOnlyAdapter to be called exactly 2 times (Oracle+Critic), got %d", roCalls)
	}
	if strikerCalls != 1 {
		t.Errorf("expected strikerAdapter to be called exactly 1 time (Striker), got %d", strikerCalls)
	}

	roMock.mu.Lock()
	for _, role := range roMock.callRoles {
		if role != backend.RoleOracle && role != backend.RoleCritic {
			t.Errorf("readOnlyAdapter called with invalid role: %s", role)
		}
	}
	roMock.mu.Unlock()

	strikerMock.mu.Lock()
	for _, role := range strikerMock.callRoles {
		if role != backend.RoleStriker {
			t.Errorf("strikerAdapter called with non-striker role: %s", role)
		}
	}
	strikerMock.mu.Unlock()
}

