package integration

import (
	"asura/internal/auth"
	"asura/internal/contextwin"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"asura/internal/daemon"
	"asura/internal/storage"
)

func recallSubmit(t *testing.T, socket, workspace, id, mode, prompt string, ids []string) (*storage.Task, string) {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = json.NewEncoder(conn).Encode(map[string]any{"action": "submit", "workspace": workspace, "request_id": id, "memory_recall": mode, "prompt": prompt, "memory_ids": ids})
	if err != nil {
		t.Fatal(err)
	}
	var response daemon.IPCResponse
	if err = json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response.Task, response.Error
}

func TestRECALL_Acceptance(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx := context.Background()
	own, err := f.client.AddMemory(ctx, f.projA, "SQLite 隔離規則")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.client.AddMemory(ctx, f.projB, "SQLite SECRET foreign")
	if err != nil {
		t.Fatal(err)
	}
	task, msg := recallSubmit(t, f.sockPath, f.projA, "recall-original", "bounded", "SQLite 隔離", nil)
	if msg != "" {
		t.Fatal(msg)
	}
	done := waitForTaskCompletion(t, f, task.ID)
	if !strings.Contains(done.Result, own.Content) {
		t.Fatalf("RECALL-002/003: automatic memory missing: %s", done.Result)
	}
	if strings.Contains(done.ResolvedContext, "SECRET") {
		t.Fatal("foreign content leaked")
	}
	if !strings.Contains(done.ResolvedContext, `"recall"`) {
		t.Fatal("RECALL-009: decision missing")
	}
	snapshot := done.ResolvedContext
	if err := f.store.DeleteMemory(ctx, f.projA, own.ID); err != nil {
		t.Fatal(err)
	}
	redelivered, msg := recallSubmit(t, f.sockPath, f.projA, "recall-original", "bounded", "SQLite 隔離", nil)
	if msg != "" || redelivered.ID != task.ID || redelivered.ResolvedContext != snapshot {
		t.Fatalf("RECALL-006: redelivery %s", msg)
	}
	if _, msg = recallSubmit(t, f.sockPath, f.projA, "recall-original", "off", "SQLite 隔離", nil); msg == "" {
		t.Fatal("RECALL-006: changed mode accepted")
	}
	zero, msg := recallSubmit(t, f.sockPath, f.projA, "", "bounded", "香蕉果汁", nil)
	if msg != "" || !strings.Contains(zero.ResolvedContext, `"recall"`) {
		t.Fatalf("RECALL-007: zero decision missing: %s", msg)
	}
	if _, msg = recallSubmit(t, f.sockPath, f.projA, "", "unknown", "query", nil); msg == "" {
		t.Fatal("RECALL-001: unknown accepted")
	}
}

func TestRECALL_OffScopeExplicitAndNoRequery(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx := context.Background()
	a, err := f.client.AddMemory(ctx, f.projA, "SQLite private A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.client.AddMemory(ctx, f.projB, "SQLite secret B")
	if err != nil {
		t.Fatal(err)
	}
	before := atomic.LoadInt32(f.runnerCalls)
	for _, ws := range []string{f.outsideDir, f.escapedLink} {
		if _, msg := recallSubmit(t, f.sockPath, ws, "", "bounded", "SQLite", nil); msg == "" {
			t.Fatal("unauthorized accepted")
		}
	}
	if _, msg := recallSubmit(t, f.sockPath, f.projA, "", "bounded", "SQLite", []string{b.ID}); msg == "" {
		t.Fatal("foreign explicit accepted")
	}
	if atomic.LoadInt32(f.runnerCalls) != before {
		t.Fatal("rejected task executed")
	}
	task, msg := recallSubmit(t, f.sockPath, f.aliasA, "same", "bounded", "SQLite", []string{a.ID, a.ID})
	if msg != "" {
		t.Fatal(msg)
	}
	done := waitForTaskCompletion(t, f, task.ID)
	var sealed engine.ExecutionContext
	if err = json.Unmarshal([]byte(done.ResolvedContext), &sealed); err != nil {
		t.Fatal(err)
	}
	if sealed.Recall == nil || len(sealed.Recall.AutoIDs) != 0 || len(sealed.Recall.ExplicitIDs) != 1 || len(sealed.ProjectMemories) != 1 || sealed.ProjectMemories[0].Content != a.Content {
		t.Fatalf("explicit dedup: %+v", sealed)
	}
	child, msg := recallSubmit(t, f.sockPath, f.subDirC, "", "bounded", "SQLite", nil)
	if msg != "" {
		t.Fatal(msg)
	}
	child = waitForTaskCompletion(t, f, child.ID)
	if strings.Contains(child.ResolvedContext, a.Content) {
		t.Fatal("parent inherited")
	}
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("DROP TABLE memories"); err != nil {
		t.Fatal(err)
	}
	// 2026-09-22：表不存在作為真實查詢偵測器，若重送重新查詢必失敗。
	again, msg := recallSubmit(t, f.sockPath, f.projA, "same", "bounded", "SQLite", []string{a.ID})
	if msg != "" || again.ID != done.ID || again.ResolvedContext != done.ResolvedContext {
		t.Fatalf("redelivery queried dropped table: %s", msg)
	}
	off, msg := recallSubmit(t, f.sockPath, f.projA, "off", "off", "unchanged", nil)
	if msg != "" {
		t.Fatalf("off queried candidates: %s", msg)
	}
	off = waitForTaskCompletion(t, f, off.ID)
	old, msg := recallSubmit(t, f.sockPath, f.projA, "off", "", "unchanged", nil)
	if msg != "" || old.ID != off.ID || off.Result != "done: unchanged" || strings.Contains(off.ResolvedContext, `"recall"`) {
		t.Fatal("off changed payload/prompt")
	}
	before = atomic.LoadInt32(f.runnerCalls)
	if _, msg = recallSubmit(t, f.sockPath, f.projA, "failed-query", "bounded", "SQLite", nil); msg != "memory recall unavailable" {
		t.Fatalf("storage error %s", msg)
	}
	if _, err = f.store.GetTaskByRequestID(ctx, "failed-query"); err == nil {
		t.Fatal("failed query saved task")
	}
	if atomic.LoadInt32(f.runnerCalls) != before {
		t.Fatal("failed query executed backend")
	}
	f.client.Close()
	if err = f.daemon.Close(); err != nil {
		t.Fatal(err)
	}
	revokedPolicy := auth.NewPolicy([]string{f.projB})
	restarted := daemon.NewDaemon(daemon.Config{SocketPath: f.sockPath, MaxWorkers: 2}, f.store, revokedPolicy, daemon.WithEngine(engine.NewEngine(f.store, revokedPolicy, engine.WithRunner(func(context.Context, string, string) (string, error) { return "unexpected", nil }))))
	if err = restarted.Start(); err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, msg = recallSubmit(t, f.sockPath, f.projA, "same", "bounded", "SQLite", []string{a.ID}); msg == "" {
		t.Fatal("revoked redelivery accepted")
	}
}

func TestRECALL_ConcurrentRequest(t *testing.T) {
	f := setupMemoryFixture(t)
	if _, err := f.client.AddMemory(context.Background(), f.projA, "SQLite snapshot"); err != nil {
		t.Fatal(err)
	}
	type response struct {
		task    *storage.Task
		message string
	}
	results := make(chan response, 2)
	for i := 0; i < 2; i++ {
		go func() {
			task, msg := recallSubmit(t, f.sockPath, f.projA, "concurrent", "bounded", "SQLite", nil)
			results <- response{task, msg}
		}()
	}
	one, two := <-results, <-results
	if one.message != "" || two.message != "" || one.task.ID != two.task.ID {
		t.Fatalf("concurrent identity: %+v %+v", one, two)
	}
	waitForTaskCompletion(t, f, one.task.ID)
	if atomic.LoadInt32(f.runnerCalls) != 1 {
		t.Fatalf("execution count %d", atomic.LoadInt32(f.runnerCalls))
	}
}

func TestRECALL_BoundedAndExplicit(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx := context.Background()
	for i := 0; i < 300; i++ {
		content := "SQLite candidate"
		if i == 0 {
			content = "SQLite " + strings.Repeat("x", 10000)
		}
		if err := f.store.CreateMemory(ctx, storage.Memory{ID: fmt.Sprintf("%04d", i), Workspace: f.projA, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	task, msg := recallSubmit(t, f.sockPath, f.projA, "", "bounded", "SQLite", []string{"0000", "0299"})
	if msg != "" {
		t.Fatal(msg)
	}
	done := waitForTaskCompletion(t, f, task.ID)
	var sealed engine.ExecutionContext
	if err := json.Unmarshal([]byte(done.ResolvedContext), &sealed); err != nil {
		t.Fatal(err)
	}
	if sealed.Recall == nil || !sealed.Recall.CandidateTruncated || sealed.Recall.CandidatesRead != 256 || len(sealed.Recall.AutoIDs) != 4 || len(sealed.ProjectMemories) != 6 {
		t.Fatalf("bound decision %+v", sealed.Recall)
	}
	if len(sealed.ProjectMemories[0].Content) != 10007 || sealed.ProjectMemories[1].ID != "0299" {
		t.Fatal("explicit cropped or lost")
	}
	if strings.Join(sealed.Recall.AutoIDs, ",") != "0001,0002,0003,0004" {
		t.Fatal("unstable ordering")
	}
	if sealed.Recall.Budget.EstimatedTokens > 2048 || sealed.Recall.Budget.FormattedBytes > 8192 {
		t.Fatal("auto budget exceeded")
	}
}

func TestRECALL_BoundedCanonicalExplicitIDs(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx := context.Background()
	memory, err := f.client.AddMemory(ctx, f.projA, "SQLite workspace isolation")
	if err != nil {
		t.Fatal(err)
	}

	task, msg := recallSubmit(t, f.sockPath, f.projA, "canonical-explicit", "bounded", "SQLite workspace", []string{" " + memory.ID + " ", memory.ID})
	if msg != "" {
		t.Fatal(msg)
	}
	done := waitForTaskCompletion(t, f, task.ID)

	var sealed engine.ExecutionContext
	if err = json.Unmarshal([]byte(done.ResolvedContext), &sealed); err != nil {
		t.Fatal(err)
	}
	if len(sealed.ProjectMemories) != 1 || sealed.ProjectMemories[0].ID != memory.ID {
		t.Fatalf("expected one canonical explicit memory, got %+v", sealed.ProjectMemories)
	}
	if sealed.Recall == nil || len(sealed.Recall.ExplicitIDs) != 1 || sealed.Recall.ExplicitIDs[0] != memory.ID {
		t.Fatalf("expected canonical explicit provenance, got %+v", sealed.Recall)
	}
	if len(sealed.Recall.AutoIDs) != 0 || len(done.MemoryRefs) != 1 || done.MemoryRefs[0] != memory.ID {
		t.Fatalf("explicit memory was also auto-selected: recall=%+v refs=%v", sealed.Recall, done.MemoryRefs)
	}

	redelivered, msg := recallSubmit(t, f.sockPath, f.projA, "canonical-explicit", "bounded", "SQLite workspace", []string{memory.ID})
	if msg != "" || redelivered.ID != done.ID || redelivered.Fingerprint != done.Fingerprint {
		t.Fatalf("canonical redelivery identity changed: message=%q task=%+v", msg, redelivered)
	}

	off, msg := recallSubmit(t, f.sockPath, f.projA, "off-whitespace-identity", "off", "legacy", []string{" " + memory.ID + " "})
	if msg != "" {
		t.Fatal(msg)
	}
	waitForTaskCompletion(t, f, off.ID)
	if _, msg = recallSubmit(t, f.sockPath, f.projA, "off-whitespace-identity", "off", "legacy", []string{memory.ID}); msg != "request ID reused with different payload" {
		t.Fatalf("off fingerprint whitespace semantics changed: %q", msg)
	}
}

func recallFixture(t *testing.T, cfg contextwin.Config, runner engine.RunnerFunc, pr engine.PipelineRunner) *memoryFixture {
	t.Helper()
	root := t.TempDir()
	s, err := storage.NewSQLiteStore(filepath.Join(root, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	policy := auth.NewPolicy([]string{root})
	eng := engine.NewEngine(s, policy, engine.WithRunner(runner), engine.WithPipelineRunner(pr))
	socket := filepath.Join(root, "recall.sock")
	d := daemon.NewDaemon(daemon.Config{SocketPath: socket, MaxWorkers: 2, MaxQueue: 8, ContextConfig: cfg}, s, policy, daemon.WithEngine(eng))
	if err = d.Start(); err != nil {
		t.Fatal(err)
	}
	c, err := daemon.NewClient(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); d.Close(); s.Close() })
	return &memoryFixture{rootDir: root, projA: root, store: s, daemon: d, client: c, sockPath: socket, dbPath: filepath.Join(root, "data.db"), policy: policy}
}

func TestRECALL_StageAdmission(t *testing.T) {
	for _, policy := range []contextwin.AdmissionPolicy{contextwin.PolicyReject, contextwin.PolicyObserve} {
		t.Run(string(policy), func(t *testing.T) {
			cfg := contextwin.Config{ContextLimit: 500, Policy: policy, ReserveRatio: .2}
			var calls atomic.Int32
			record := func(prompt string) {
				calls.Add(1)
				if !strings.Contains(prompt, "SQLite sealed memory") {
					t.Error("stage lost sealed memory")
				}
			}
			oracle := pipeline.NewOracleStage(func(_ context.Context, _ string, prompt string) (string, error) {
				record(prompt)
				return `{"goal":"build","steps":["` + strings.Repeat("huge ", 1000) + `"]}`, nil
			})
			striker := pipeline.NewStrikerStage(func(_ context.Context, _ string, prompt string) (string, error) { record(prompt); return "done", nil })
			critic := pipeline.NewCriticStage(func(_ context.Context, _ string, prompt string) (string, error) {
				record(prompt)
				return `{"verdict":"pass","summary":"ok","findings":[]}`, nil
			})
			orc := pipeline.NewOrchestrator(oracle, striker, critic).WithContextBudget(cfg)
			f := recallFixture(t, cfg, nil, orc)
			if _, err := f.client.AddMemory(context.Background(), f.projA, "SQLite sealed memory"); err != nil {
				t.Fatal(err)
			}
			task, err := f.client.Submit(context.Background(), engine.SubmitRequest{Workspace: f.projA, Prompt: "SQLite", Mode: "three-face", MemoryRecall: "bounded"})
			if err != nil {
				t.Fatal(err)
			}
			done := waitForTaskCompletion(t, f, task.ID)
			var trace pipeline.PipelineTrace
			if err = json.Unmarshal([]byte(done.PipelineTrace), &trace); err != nil {
				t.Fatal(err)
			}
			if policy == contextwin.PolicyReject {
				if done.Status != storage.StatusFailed || calls.Load() != 1 || len(trace.Stages) != 2 || trace.Stages[0].Output == "" || trace.Stages[1].Admission.Verdict != contextwin.VerdictRejected {
					t.Fatalf("overrun lost trace: %+v calls=%d", trace, calls.Load())
				}
			} else if done.Status != storage.StatusCompleted || calls.Load() != 3 || trace.Stages[1].Admission.Verdict != contextwin.VerdictWarning {
				t.Fatalf("observe: %s %+v", done.ErrorMessage, trace)
			}
		})
	}
}

func TestRECALL_QueuedRestartSnapshot(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	defer release()
	f := recallFixture(t, contextwin.Config{}, func(_ context.Context, _ string, prompt string) (string, error) {
		if prompt == "hold" {
			close(started)
			<-gate
		}
		return prompt, nil
	}, nil)
	if _, err := f.client.Submit(context.Background(), engine.SubmitRequest{Workspace: f.projA, Prompt: "hold"}); err != nil {
		t.Fatal(err)
	}
	<-started
	mem, err := f.client.AddMemory(context.Background(), f.projA, "SQLite original sealed")
	if err != nil {
		t.Fatal(err)
	}
	task, msg := recallSubmit(t, f.sockPath, f.projA, "restart", "bounded", "SQLite", nil)
	if msg != "" {
		t.Fatal(msg)
	}
	if task.Status != storage.StatusQueued {
		t.Fatalf("fixture did not queue: %s", task.Status)
	}
	snapshot := task.ResolvedContext
	if err = f.store.DeleteMemory(context.Background(), f.projA, mem.ID); err != nil {
		t.Fatal(err)
	}
	release()
	done := waitForTaskCompletion(t, f, task.ID)
	if !strings.Contains(done.Result, mem.Content) || done.ResolvedContext != snapshot {
		t.Fatal("wake reselected context")
	}
	f.client.Close()
	f.daemon.Close()
	f.store.Close()
	// 2026-09-22：建立 crash 遺留 queued 狀態，測試真實 Start 的恢復政策。
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE tasks SET status='queued' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DROP TABLE memories"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	reopened, err := storage.NewSQLiteStore(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var calls atomic.Int32
	eng := engine.NewEngine(reopened, f.policy, engine.WithRunner(func(context.Context, string, string) (string, error) { calls.Add(1); return "unexpected", nil }))
	restarted := daemon.NewDaemon(daemon.Config{SocketPath: f.sockPath, MaxWorkers: 2, ContextConfig: contextwin.Config{ContextLimit: 1, Policy: contextwin.PolicyReject}}, reopened, f.policy, daemon.WithEngine(eng))
	if err = restarted.Start(); err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	saved, msg := recallSubmit(t, f.sockPath, f.projA, "restart", "bounded", "SQLite", nil)
	if msg != "" || saved.Status != storage.StatusFailed || saved.ResolvedContext != snapshot || calls.Load() != 0 {
		t.Fatalf("restart changed snapshot or reran: %+v %s", saved, msg)
	}
}

func TestRECALL_ExplicitOverrunAndCriticOverrun(t *testing.T) {
	for _, policy := range []contextwin.AdmissionPolicy{contextwin.PolicyObserve, contextwin.PolicyReject} {
		t.Run("explicit_"+string(policy), func(t *testing.T) {
			cfg := contextwin.Config{ContextLimit: 5, Policy: policy}
			var calls atomic.Int32
			striker := pipeline.NewStrikerStage(func(_ context.Context, _ string, prompt string) (string, error) { calls.Add(1); return prompt, nil })
			f := recallFixture(t, cfg, nil, pipeline.NewOrchestrator(nil, striker, nil).WithContextBudget(cfg))
			mem, err := f.client.AddMemory(context.Background(), f.projA, "SQLite explicit entire content")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.client.AddMemory(context.Background(), f.projA, "SQLite auto"); err != nil {
				t.Fatal(err)
			}
			task, err := f.client.Submit(context.Background(), engine.SubmitRequest{Workspace: f.projA, Prompt: "SQLite original", MemoryIDs: []string{mem.ID}, MemoryRecall: "bounded"})
			if err != nil {
				t.Fatal(err)
			}
			done := waitForTaskCompletion(t, f, task.ID)
			var sealed engine.ExecutionContext
			if err = json.Unmarshal([]byte(done.ResolvedContext), &sealed); err != nil {
				t.Fatal(err)
			}
			if len(sealed.Recall.AutoIDs) != 0 || sealed.Recall.Skipped["stage_limit"] != 1 || sealed.ProjectMemories[0].Content != mem.Content {
				t.Fatal("explicit/base changed")
			}
			if policy == contextwin.PolicyReject {
				if done.Status != storage.StatusFailed || calls.Load() != 0 {
					t.Fatal("base reject failed")
				}
			} else if done.Status != storage.StatusCompleted || calls.Load() != 1 || !strings.Contains(done.Result, mem.Content) {
				t.Fatal("base observe failed")
			}
		})
	}
	t.Run("critic", func(t *testing.T) {
		cfg := contextwin.Config{ContextLimit: 500, Policy: contextwin.PolicyReject}
		var calls atomic.Int32
		oracle := pipeline.NewOracleStage(func(context.Context, string, string) (string, error) {
			calls.Add(1)
			return `{"goal":"build","steps":["one"]}`, nil
		})
		striker := pipeline.NewStrikerStage(func(context.Context, string, string) (string, error) {
			calls.Add(1)
			return strings.Repeat("large ", 1000), nil
		})
		critic := pipeline.NewCriticStage(func(context.Context, string, string) (string, error) { calls.Add(1); return "unexpected", nil })
		f := recallFixture(t, cfg, nil, pipeline.NewOrchestrator(oracle, striker, critic).WithContextBudget(cfg))
		if _, err := f.client.AddMemory(context.Background(), f.projA, "SQLite snapshot"); err != nil {
			t.Fatal(err)
		}
		task, err := f.client.Submit(context.Background(), engine.SubmitRequest{Workspace: f.projA, Prompt: "SQLite", Mode: "three-face", MemoryRecall: "bounded"})
		if err != nil {
			t.Fatal(err)
		}
		done := waitForTaskCompletion(t, f, task.ID)
		var trace pipeline.PipelineTrace
		if err = json.Unmarshal([]byte(done.PipelineTrace), &trace); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 2 || done.Status != storage.StatusFailed || len(trace.Stages) != 3 || trace.Stages[1].Output == "" || trace.Stages[2].Admission.Verdict != contextwin.VerdictRejected {
			t.Fatalf("critic overrun %+v", trace)
		}
	})
}

func TestRECALL_QueryDeadline(t *testing.T) {
	f := setupMemoryFixture(t)
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// 2026-09-22：以慢速真實 SQLite view 取代 fixture 表，強制查詢期間超時。
	if _, err = db.Exec(`DROP TABLE memories; CREATE VIEW memories AS
 WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10000000)
 SELECT printf('%08d',x) AS id, '` + strings.ReplaceAll(f.projA, "'", "''") + `' AS workspace,
 'SQLite' AS content, 'manual' AS source, '2026-09-22' AS created_at,
 'active' AS lifecycle, 0 AS is_durable, '2026-09-22' AS lifecycle_changed_at FROM n`); err != nil {
		t.Fatal(err)
	}
	// 2026-10-01：確認慢速 fixture 真正耗盡 deadline，避免 schema 錯誤造成假綠燈。
	queryCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	_, _, queryErr := f.store.RecallCandidates(queryCtx, f.projA)
	contextErr := queryCtx.Err()
	cancel()
	if queryErr == nil || contextErr != context.DeadlineExceeded {
		t.Fatalf("slow fixture did not exercise cancellation: query=%v context=%v", queryErr, contextErr)
	}
	started := time.Now()
	if _, msg := recallSubmit(t, f.sockPath, f.projA, "timeout", "bounded", "SQLite", nil); msg != "memory recall unavailable" {
		t.Fatalf("timeout not rejected: %s", msg)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("query cancellation too slow: %s", elapsed)
	}
	if atomic.LoadInt32(f.runnerCalls) != 0 {
		t.Fatal("timeout called backend")
	}
	if _, err = f.store.GetTaskByRequestID(context.Background(), "timeout"); err == nil {
		t.Fatal("timeout persisted task")
	}
	// 同一 store 的單連線必須可以繼續處理 off 任務。
	task, msg := recallSubmit(t, f.sockPath, f.projA, "", "off", "after timeout", nil)
	if msg != "" {
		t.Fatal(msg)
	}
	done := waitForTaskCompletion(t, f, task.ID)
	if done.Status != storage.StatusCompleted {
		t.Fatal(done.ErrorMessage)
	}
}
