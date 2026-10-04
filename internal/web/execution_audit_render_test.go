package web

import (
	"asura/internal/auth"
	"asura/internal/backend"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type auditRenderRuntime struct{ webStageRuntime }

func (b *auditRenderRuntime) Run(_ context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	out := "done"
	if req.Role == backend.RoleOracle {
		out = `{"goal":"g","steps":["s"]}`
	}
	if req.Role == backend.RoleCritic {
		out = `{"verdict":"pass"}`
	}
	return &backend.ExecutionResult{Output: out}, nil
}

// 2026-10-04：EXAUD-002/014/019，執行真正頁面的 JS，fixture 來自真 SQLite audit。
func TestExecutionAuditWebRender(t *testing.T) {
	root := t.TempDir()
	s, err := storage.NewSQLiteStore(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	write := &auditRenderRuntime{webStageRuntime{id: "opencode", caps: []backend.Capability{backend.CapWorkspaceWrite}}}
	read := &auditRenderRuntime{webStageRuntime{id: "codex", caps: []backend.Capability{backend.CapReadOnlySandbox}}}
	reg := backend.NewRegistry()
	reg.Register(write)
	reg.Register(read)
	o := pipeline.NewOrchestrator(pipeline.NewOracleStageWithBackend(read), pipeline.NewStrikerStageWithBackend(write), pipeline.NewCriticStageWithBackend(read)).WithRegistry(reg)
	e := engine.NewEngine(s, auth.NewPolicy([]string{root}), engine.WithPipelineRunner(o))
	task, err := e.SubmitTask(context.Background(), engine.SubmitRequest{ID: "render", Workspace: root, Prompt: "secret-sentinel", Mode: "three-face", Backend: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.GetTaskAudit(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	// 2026-10-04：真 SQLite record 經 Unix IPC 與 session HTTP，核心 fold 不 mock。
	socket := filepath.Join(root, "audit.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	served := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			served <- err
			return
		}
		defer conn.Close()
		var req daemon.IPCRequest
		if err = json.NewDecoder(conn).Decode(&req); err != nil {
			served <- err
			return
		}
		if req.Action != "task_audit" || req.TaskID != task.ID {
			served <- fmt.Errorf("wrong query %+v", req)
			return
		}
		record, err := s.GetTaskAudit(context.Background(), req.TaskID)
		if err != nil {
			served <- err
			return
		}
		served <- json.NewEncoder(conn).Encode(daemon.IPCResponse{Audit: record})
	}()
	server, err := NewServer(Config{Key: "test-key", Origin: "https://asura.example", SocketPath: socket})
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRequest("POST", "https://asura.example/api/login", strings.NewReader(`{"key":"test-key"}`))
	login.Header.Set("Origin", "https://asura.example")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, login)
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("login %d", response.Code)
	}
	query := httptest.NewRequest("GET", "https://asura.example/api/tasks/render/audit", nil)
	query.AddCookie(cookies[0])
	query.Header.Set("Origin", "https://asura.example")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, query)
	if response.Code != 200 || strings.TrimSpace(response.Body.String()) != string(raw) || strings.Contains(response.Body.String(), "secret-sentinel") {
		t.Fatalf("HTTP audit %d %s", response.Code, response.Body.String())
	}
	if err = <-served; err != nil {
		t.Fatal(err)
	}

	page := indexHTML
	if path := os.Getenv("ASURA_AUDIT_BASELINE_FILE"); path != "" {
		bytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		page = string(bytes)
	}
	start := strings.Index(page, "  function renderExecutionAudit(record) {")
	end := strings.Index(page, "  async function loadExecutionAudit(")
	if start < 0 || end < start {
		t.Fatal("EXAUD-002 persisted audit renderer missing")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	script := `const executionAudit={textContent:''};` + page[start:end] + `const assert=require('node:assert/strict');const r=` + string(raw) + `;
renderExecutionAudit(r);
for(const value of ['Stage oracle：sealed=codex v1','Stage striker：sealed=opencode v1','Stage critic：sealed=codex v1','Actual returned：codex v1','Terminal：completed / none','Calls：intents=3 returns=3','model=unknown'])assert.ok(executionAudit.textContent.includes(value),executionAudit.textContent);
assert.ok(!executionAudit.textContent.includes('secret-sentinel'));
r.header.schema='future';renderExecutionAudit(r);assert.match(executionAudit.textContent,/invalid\/unavailable/);
console.log('PASS persisted mixed stage renderer, unknown model calls, no prompt leakage');`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("renderer %v %s", err, out)
	}
}
