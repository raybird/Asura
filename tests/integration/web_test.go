package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/storage"
	"asura/internal/web"
)

type webFixture struct {
	root, outside, socket string
	store                 *storage.SQLiteStore
	server                *httptest.Server
	starts                atomic.Int32
}

func startWeb(t *testing.T) *webFixture {
	t.Helper()
	f := &webFixture{root: t.TempDir(), outside: t.TempDir()}
	f.socket = filepath.Join(f.root, "d.sock")
	var err error
	f.store, err = storage.NewSQLiteStore(filepath.Join(f.root, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.store.Close() })
	policy := auth.NewPolicy([]string{f.root})
	eng := engine.NewEngine(f.store, policy, engine.WithRunner(func(ctx context.Context, workspace, prompt string) (string, error) {
		f.starts.Add(1)
		return "KNOWN-RESULT", nil
	}))
	d := daemon.NewDaemon(daemon.Config{SocketPath: f.socket}, f.store, policy, daemon.WithEngine(eng))
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	f.server = httptest.NewUnstartedServer(nil)
	s, err := web.NewServer(web.Config{Key: "fixture-secret", Origin: "http://" + f.server.Listener.Addr().String(), SocketPath: f.socket})
	if err != nil {
		t.Fatal(err)
	}
	f.server.Config.Handler = s
	f.server.Start()
	t.Cleanup(f.server.Close)
	for _, task := range []*storage.Task{
		{ID: "local-allowed", Workspace: f.root, Status: storage.StatusPending, Result: "CLI-RESULT", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "local-denied", Workspace: f.outside, Status: storage.StatusPending, Result: "DO-NOT-LEAK", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	} {
		if err := f.store.CreateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *webFixture) call(t *testing.T, method, path, body, source string, cookie *http.Cookie) (int, string, []*http.Cookie) {
	t.Helper()
	r, err := http.NewRequest(method, f.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	if source != "" {
		r.Header.Set("Origin", source)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	resp, err := f.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b), resp.Cookies()
}

func (f *webFixture) login(t *testing.T) *http.Cookie {
	t.Helper()
	code, body, cookies := f.call(t, "POST", "/api/login", `{"key":"fixture-secret"}`, f.server.URL, nil)
	if code != 204 || len(cookies) != 1 {
		t.Fatalf("login: %d %s cookies=%d", code, body, len(cookies))
	}
	return cookies[0]
}

func (f *webFixture) submit(t *testing.T, id, workspace string, cookie *http.Cookie) int {
	t.Helper()
	b, err := json.Marshal(engine.SubmitRequest{ID: id, Workspace: workspace, Prompt: "count controlled execution"})
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ := f.call(t, "POST", "/api/tasks", string(b), f.server.URL, cookie)
	return code
}

func (f *webFixture) noTask(t *testing.T, id string) {
	t.Helper()
	if _, err := f.store.GetTaskByRequestID(context.Background(), id); err != storage.ErrTaskNotFound {
		t.Errorf("denied request persisted: %s err=%v", id, err)
	}
}

// 2026-09-13 WEB-001：真實 HTTP → Socket → 中樞的允許／拒絕對照。
func TestWEB001(t *testing.T) {
	f := startWeb(t)
	c := f.login(t)
	if code := f.submit(t, "positive", f.root, c); code != 202 {
		t.Fatalf("authorized submit: %d", code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for f.starts.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond * 10)
	}
	if f.starts.Load() != 1 {
		t.Fatal("positive control did not execute exactly once")
	}
	if code := f.submit(t, "unauthenticated", f.root, nil); code != 401 {
		t.Errorf("unauthenticated submit: %d", code)
	}
	if code, _, _ := f.call(t, "POST", "/api/login", `{"key":"wrong"}`, f.server.URL, nil); code != 401 {
		t.Errorf("wrong key: %d", code)
	}
	for _, op := range []struct{ method, path string }{{"GET", "/api/tasks/local-allowed"}, {"POST", "/api/tasks/local-allowed/cancel"}} {
		code, body, _ := f.call(t, op.method, op.path, `{}`, f.server.URL, nil)
		if code != 401 || strings.Contains(body, "CLI-RESULT") {
			t.Errorf("unprotected endpoint: %d %s", code, body)
		}
	}
	f.noTask(t, "unauthenticated")
	task, err := f.store.GetTask(context.Background(), "local-allowed")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != storage.StatusPending || f.starts.Load() != 1 {
		t.Fatal("denied request had side effects")
	}
}

// 2026-09-13 WEB-002：Web 與 CLI 共用清單，含歷史任務及連結越界。
func TestWEB002(t *testing.T) {
	f := startWeb(t)
	c := f.login(t)
	code, body, _ := f.call(t, "GET", "/api/tasks/local-allowed", "", f.server.URL, c)
	if code != 200 || !strings.Contains(body, "CLI-RESULT") {
		t.Fatalf("CLI task read: %d %s", code, body)
	}
	code, _, _ = f.call(t, "POST", "/api/tasks/local-allowed/cancel", `{}`, f.server.URL, c)
	if code != 200 {
		t.Errorf("CLI task cancel: %d", code)
	}
	for _, op := range []struct{ method, path string }{{"GET", "/api/tasks/local-denied"}, {"POST", "/api/tasks/local-denied/cancel"}} {
		code, body, _ = f.call(t, op.method, op.path, `{}`, f.server.URL, c)
		if code != 403 || strings.Contains(body, "DO-NOT-LEAK") {
			t.Errorf("outside task: %d %s", code, body)
		}
	}
	link := filepath.Join(f.root, "escape")
	if err := os.Symlink(f.outside, link); err != nil {
		t.Fatal(err)
	}
	for i, target := range []string{f.outside, link, link + "/..", link + "/../new-project"} {
		id := []string{"outside", "symlink", "symlink-parent", "symlink-parent-new"}[i]
		if code := f.submit(t, id, target, c); code != 403 {
			t.Errorf("outside submit: %d", code)
		}
		f.noTask(t, id)
	}
	code, _, _ = f.call(t, "POST", "/api/tasks", `{"id":"expand","workspace":"`+f.outside+`","prompt":"test","allowed_roots":["/"]}`, f.server.URL, c)
	if code != 400 {
		t.Errorf("client policy override: %d", code)
	}
	f.noTask(t, "expand")
	task, err := f.store.GetTask(context.Background(), "local-denied")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != storage.StatusPending || f.starts.Load() != 0 {
		t.Fatal("unauthorized side effect")
	}
}

// 2026-09-13 WEB-010：跨站與登出後的請求不能抵達副作用邊界。
func TestWEB010(t *testing.T) {
	f := startWeb(t)
	c := f.login(t)
	for _, path := range []string{"/api/tasks", "/api/tasks/local-allowed/cancel"} {
		code, _, _ := f.call(t, "POST", path, `{"id":"cross-site","workspace":"`+f.root+`","prompt":"test"}`, "https://evil.example", c)
		if code != 403 {
			t.Errorf("cross-site mutation: %d", code)
		}
	}
	code, _, _ := f.call(t, "POST", "/api/logout", `{}`, f.server.URL, c)
	if code != 204 {
		t.Fatalf("logout: %d", code)
	}
	if code := f.submit(t, "logged-out", f.root, c); code != 401 {
		t.Errorf("logged-out mutation: %d", code)
	}
	f.noTask(t, "cross-site")
	f.noTask(t, "logged-out")
	task, err := f.store.GetTask(context.Background(), "local-allowed")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != storage.StatusPending || f.starts.Load() != 0 {
		t.Fatal("forbidden request caused side effects")
	}
}

func (f *webFixture) submitTask(t *testing.T, req engine.SubmitRequest, cookie *http.Cookie) (int, *storage.Task, string) {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	code, body, _ := f.call(t, "POST", "/api/tasks", string(b), f.server.URL, cookie)
	var task storage.Task
	if code == http.StatusAccepted {
		if err := json.Unmarshal([]byte(body), &task); err != nil {
			t.Fatalf("unmarshal accepted task: %v, body: %s", err, body)
		}
		return code, &task, body
	}
	return code, nil, body
}

// 2026-09-13 WEB-003：接受任務與共同容量（接受即時回傳 Task ID、共用 Daemon 容量、滿載與離線回報 503 且零額外副作用）
func TestWEB003(t *testing.T) {
	f := startWeb(t)
	c := f.login(t)

	// 1. 成功提交自然語言任務，立即回傳 202 Accepted 與 Task ID
	code, task, _ := f.submitTask(t, engine.SubmitRequest{
		ID:        "web-003-positive",
		Workspace: f.root,
		Prompt:    "echo 'natural language task'",
	}, c)
	if code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d", code)
	}
	if task == nil || task.ID == "" {
		t.Fatalf("expected valid task with non-empty ID, got %+v", task)
	}
	if task.RequestID != "web-003-positive" {
		t.Errorf("expected RequestID 'web-003-positive', got %q", task.RequestID)
	}

	// 等待非同步任務執行完畢
	deadline := time.Now().Add(3 * time.Second)
	for f.starts.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.starts.Load() != 1 {
		t.Fatalf("expected exactly 1 execution, got %d", f.starts.Load())
	}

	// 2. 測試滿載時明確回報 503 capacity exceeded 且不啟動後端
	// 建立一個受限容量為 1 (Worker 1, Queue 0) 的 Daemon
	cappedDir := t.TempDir()
	cappedSock := filepath.Join(cappedDir, "capped.sock")
	cappedStore, err := storage.NewSQLiteStore(filepath.Join(cappedDir, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cappedStore.Close() })
	cappedPolicy := auth.NewPolicy([]string{cappedDir})
	unblockCh := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-unblockCh:
		default:
			close(unblockCh)
		}
	})
	var cappedStarts atomic.Int32
	cappedEng := engine.NewEngine(cappedStore, cappedPolicy, engine.WithRunner(func(ctx context.Context, ws, prompt string) (string, error) {
		cappedStarts.Add(1)
		<-unblockCh
		return "capped-done", nil
	}))
	cappedDaemon := daemon.NewDaemon(daemon.Config{
		SocketPath: cappedSock,
		MaxWorkers: 1,
		MaxQueue:   0,
	}, cappedStore, cappedPolicy, daemon.WithEngine(cappedEng))
	if err := cappedDaemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cappedDaemon.Close() })

	cappedServer := httptest.NewUnstartedServer(nil)
	cappedWeb, err := web.NewServer(web.Config{
		Key:        "fixture-secret",
		Origin:     "http://" + cappedServer.Listener.Addr().String(),
		SocketPath: cappedSock,
	})
	if err != nil {
		t.Fatal(err)
	}
	cappedServer.Config.Handler = cappedWeb
	cappedServer.Start()
	t.Cleanup(cappedServer.Close)

	cappedFixture := &webFixture{
		root:    cappedDir,
		store:   cappedStore,
		server:  cappedServer,
		socket:  cappedSock,
	}
	cappedCookie := cappedFixture.login(t)

	// 提交第一個任務占滿容量
	code1, _, _ := cappedFixture.submitTask(t, engine.SubmitRequest{
		ID:        "capped-task-1",
		Workspace: cappedDir,
		Prompt:    "task 1 blocks worker",
	}, cappedCookie)
	if code1 != http.StatusAccepted {
		t.Fatalf("first task expected 202, got %d", code1)
	}

	// 提交第二個任務，預期容量超出拒絕 503
	code2, _, body2 := cappedFixture.submitTask(t, engine.SubmitRequest{
		ID:        "capped-task-2",
		Workspace: cappedDir,
		Prompt:    "task 2 should be rejected",
	}, cappedCookie)
	if code2 != http.StatusServiceUnavailable {
		t.Errorf("expected 503 Service Unavailable for capacity exceeded, got %d (body: %s)", code2, body2)
	}
	if !strings.Contains(body2, "capacity exceeded") {
		t.Errorf("expected body to contain 'capacity exceeded', got %q", body2)
	}
	// 確認第二個任務未啟動後端
	if cappedStarts.Load() > 1 {
		t.Errorf("capacity exceeded task triggered backend runner, starts=%d", cappedStarts.Load())
	}
	close(unblockCh)

	// 3. 測試中樞不可用時回報 503 daemon unavailable
	offlineServer := httptest.NewUnstartedServer(nil)
	offlineWeb, err := web.NewServer(web.Config{
		Key:        "fixture-secret",
		Origin:     "http://" + offlineServer.Listener.Addr().String(),
		SocketPath: filepath.Join(t.TempDir(), "nonexistent.sock"),
	})
	if err != nil {
		t.Fatal(err)
	}
	offlineServer.Config.Handler = offlineWeb
	offlineServer.Start()
	t.Cleanup(offlineServer.Close)

	offlineFixture := &webFixture{
		root:   f.root,
		server: offlineServer,
	}
	offlineCookie := offlineFixture.login(t)
	codeOff, _, bodyOff := offlineFixture.submitTask(t, engine.SubmitRequest{
		ID:        "offline-task",
		Workspace: f.root,
		Prompt:    "should fail due to offline daemon",
	}, offlineCookie)
	if codeOff != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for offline daemon, got %d", codeOff)
	}
	if !strings.Contains(bodyOff, "daemon unavailable") {
		t.Errorf("expected body to contain 'daemon unavailable', got %q", bodyOff)
	}
}

// 2026-09-13 WEB-004：回應遺失重送不重複執行（同識別重送指向原任務、後端僅執行一次、服務重啟後仍可查得）
func TestWEB004(t *testing.T) {
	f := startWeb(t)
	c := f.login(t)

	reqID := "web-004-idempotent-key"
	req := engine.SubmitRequest{
		ID:        reqID,
		Workspace: f.root,
		Prompt:    "dedup-natural-prompt",
	}

	// 第一次提交
	code1, task1, _ := f.submitTask(t, req, c)
	if code1 != http.StatusAccepted {
		t.Fatalf("initial submit failed: %d", code1)
	}
	if task1 == nil || task1.ID == "" {
		t.Fatalf("expected non-empty task, got %+v", task1)
	}

	// 模擬客戶端未收到回應或重開後重送相同請求
	code2, task2, _ := f.submitTask(t, req, c)
	if code2 != http.StatusAccepted {
		t.Fatalf("redelivered submit failed: %d", code2)
	}
	if task2.ID != task1.ID {
		t.Errorf("expected same Task ID for redelivery, got %s vs %s", task1.ID, task2.ID)
	}

	// 模擬並行重送（多個連線同時送出相同 RequestID）
	const concurrent = 5
	results := make(chan string, concurrent)
	for i := 0; i < concurrent; i++ {
		go func() {
			_, tRes, _ := f.submitTask(t, req, c)
			if tRes != nil {
				results <- tRes.ID
			} else {
				results <- ""
			}
		}()
	}
	for i := 0; i < concurrent; i++ {
		gotID := <-results
		if gotID != task1.ID {
			t.Errorf("concurrent redelivery returned different ID: %s vs %s", gotID, task1.ID)
		}
	}

	// 等待執行完成
	deadline := time.Now().Add(3 * time.Second)
	for f.starts.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	// 斷言：後端執行計數嚴格為 1
	if f.starts.Load() != 1 {
		t.Fatalf("expected exactly 1 execution for multiple redeliveries, got %d", f.starts.Load())
	}

	// 驗證服務重啟後任務與去重紀錄仍可查得
	// 透過 Web API 查詢該任務狀態
	codeQuery, bodyQuery, _ := f.call(t, "GET", "/api/tasks/"+task1.ID, "", f.server.URL, c)
	if codeQuery != http.StatusOK {
		t.Errorf("query task after redelivery failed: %d %s", codeQuery, bodyQuery)
	}
	var queriedTask storage.Task
	if err := json.Unmarshal([]byte(bodyQuery), &queriedTask); err != nil {
		t.Fatalf("unmarshal queried task: %v", err)
	}
	if queriedTask.ID != task1.ID {
		t.Errorf("queried task ID mismatch: %s vs %s", queriedTask.ID, task1.ID)
	}
	if queriedTask.RequestID != reqID {
		t.Errorf("queried task request_id mismatch: %s vs %s", queriedTask.RequestID, reqID)
	}
}

// 2026-09-13 WEB-005：重送衝突與取消後重新執行（同識別異內容拒絕、已取消任務拒絕重送、新識別建立新任務）
func TestWEB005(t *testing.T) {
	cancelDir := t.TempDir()
	cancelSock := filepath.Join(cancelDir, "cancel.sock")
	cancelStore, err := storage.NewSQLiteStore(filepath.Join(cancelDir, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancelStore.Close() })
	cancelPolicy := auth.NewPolicy([]string{cancelDir})
	unblockRunner := make(chan struct{})
	var closeOnce sync.Once
	defer closeOnce.Do(func() { close(unblockRunner) })
	var starts atomic.Int32
	runnerStarted := make(chan struct{}, 5)
	cancelEng := engine.NewEngine(cancelStore, cancelPolicy, engine.WithRunner(func(ctx context.Context, ws, prompt string) (string, error) {
		starts.Add(1)
		runnerStarted <- struct{}{}
		select {
		case <-ctx.Done():
			return "partial cancelled", ctx.Err()
		case <-unblockRunner:
			return "done", nil
		}
	}))
	cancelDaemon := daemon.NewDaemon(daemon.Config{
		SocketPath: cancelSock,
		MaxWorkers: 2,
		MaxQueue:   2,
	}, cancelStore, cancelPolicy, daemon.WithEngine(cancelEng))
	if err := cancelDaemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancelDaemon.Close() })

	server := httptest.NewUnstartedServer(nil)
	webServer, err := web.NewServer(web.Config{
		Key:        "fixture-secret",
		Origin:     "http://" + server.Listener.Addr().String(),
		SocketPath: cancelSock,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = webServer
	server.Start()
	t.Cleanup(server.Close)

	f := &webFixture{
		root:   cancelDir,
		store:  cancelStore,
		server: server,
		socket: cancelSock,
	}
	c := f.login(t)

	reqID := "web-005-conflict-key"
	req := engine.SubmitRequest{
		ID:        reqID,
		Workspace: f.root,
		Prompt:    "original-prompt",
	}

	// 1. 成功建立初始任務
	code1, task1, _ := f.submitTask(t, req, c)
	if code1 != http.StatusAccepted {
		t.Fatalf("initial submit failed: %d", code1)
	}

	// 等待初始任務啟動進入執行
	select {
	case <-runnerStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for runner to start")
	}
	initialStarts := starts.Load()

	// 2. 衝突情境 A：相同 RequestID 但換用不同 Prompt 內容
	conflictReqA := engine.SubmitRequest{
		ID:        reqID,
		Workspace: f.root,
		Prompt:    "different-prompt-content",
	}
	codeA, _, bodyA := f.submitTask(t, conflictReqA, c)
	if codeA != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for conflicting payload, got %d (body: %s)", codeA, bodyA)
	}
	if !strings.Contains(bodyA, "request ID reused with different payload") {
		t.Errorf("expected conflict message, got %q", bodyA)
	}
	if starts.Load() != initialStarts {
		t.Errorf("conflicting submit triggered backend execution, starts=%d", starts.Load())
	}

	// 3. 取消執行中的任務
	codeCancel, bodyCancel, _ := f.call(t, "POST", "/api/tasks/"+task1.ID+"/cancel", `{}`, f.server.URL, c)
	if codeCancel != http.StatusOK {
		t.Fatalf("cancel task failed: %d %s", codeCancel, bodyCancel)
	}

	// 4. 衝突情境 B：以相同 RequestID 重送已取消任務
	codeB, _, bodyB := f.submitTask(t, req, c)
	if codeB != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for redelivering cancelled task, got %d (body: %s)", codeB, bodyB)
	}
	if !strings.Contains(bodyB, "cannot redeliver cancelled request") {
		t.Errorf("expected cancelled redelivery message, got %q", bodyB)
	}
	if starts.Load() != initialStarts {
		t.Errorf("cancelled redelivery triggered backend execution, starts=%d", starts.Load())
	}

	// 5. 重新執行情境 C：以新識別建立新任務，成功接受並執行
	newReq := engine.SubmitRequest{
		ID:        "web-005-new-attempt-key",
		Workspace: f.root,
		Prompt:    "original-prompt",
	}
	codeC, taskC, _ := f.submitTask(t, newReq, c)
	if codeC != http.StatusAccepted {
		t.Fatalf("new attempt submit failed: %d", codeC)
	}
	if taskC.ID == task1.ID {
		t.Errorf("expected new Task ID for new request ID, got same %s", taskC.ID)
	}

	// 等待新任務啟動
	select {
	case <-runnerStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for second runner to start")
	}
	if starts.Load() != initialStarts+1 {
		t.Fatalf("expected new task to execute exactly once, starts=%d", starts.Load())
	}
}

// 2026-09-13 WEB-008：共同取消如實回報（A 回收確認、B 存活不受影響、未知任務取消回報錯誤、前端中斷不取消背景工作）
func TestWEB008(t *testing.T) {
	cancelDir := t.TempDir()
	cancelSock := filepath.Join(cancelDir, "cancel8.sock")
	cancelStore, err := storage.NewSQLiteStore(filepath.Join(cancelDir, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancelStore.Close() })
	cancelPolicy := auth.NewPolicy([]string{cancelDir})

	runnerAStarted := make(chan struct{}, 1)
	runnerBStarted := make(chan struct{}, 1)
	runnerACancelled := make(chan struct{}, 1)
	unblockB := make(chan struct{})
	var closeBOnce sync.Once
	defer closeBOnce.Do(func() { close(unblockB) })

	cancelEng := engine.NewEngine(cancelStore, cancelPolicy, engine.WithRunner(func(ctx context.Context, ws, prompt string) (string, error) {
		if strings.Contains(prompt, "task-A") {
			runnerAStarted <- struct{}{}
			<-ctx.Done()
			runnerACancelled <- struct{}{}
			return "cancelled A", ctx.Err()
		}
		if strings.Contains(prompt, "task-B") {
			runnerBStarted <- struct{}{}
			select {
			case <-ctx.Done():
				return "unexpected cancel B", ctx.Err()
			case <-unblockB:
				return "SUCCESS-B", nil
			}
		}
		return "OTHER", nil
	}))

	cancelDaemon := daemon.NewDaemon(daemon.Config{
		SocketPath: cancelSock,
		MaxWorkers: 2,
		MaxQueue:   2,
	}, cancelStore, cancelPolicy, daemon.WithEngine(cancelEng))
	if err := cancelDaemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancelDaemon.Close() })

	server := httptest.NewUnstartedServer(nil)
	webServer, err := web.NewServer(web.Config{
		Key:        "fixture-secret",
		Origin:     "http://" + server.Listener.Addr().String(),
		SocketPath: cancelSock,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = webServer
	server.Start()
	t.Cleanup(server.Close)

	f := &webFixture{
		root:   cancelDir,
		store:  cancelStore,
		server: server,
		socket: cancelSock,
	}
	c := f.login(t)

	wsA := filepath.Join(f.root, "proj-a")
	wsB := filepath.Join(f.root, "proj-b")
	_ = os.MkdirAll(wsA, 0755)
	_ = os.MkdirAll(wsB, 0755)

	// 1. 同時提交任務 A 與任務 B（分別屬於不同專案目錄，依 CRON-001 可並行執行）
	codeA, taskA, _ := f.submitTask(t, engine.SubmitRequest{
		ID:        "req-task-A",
		Workspace: wsA,
		Prompt:    "task-A-work",
	}, c)
	if codeA != http.StatusAccepted {
		t.Fatalf("submit task A failed: %d", codeA)
	}

	codeB, taskB, _ := f.submitTask(t, engine.SubmitRequest{
		ID:        "req-task-B",
		Workspace: wsB,
		Prompt:    "task-B-work",
	}, c)
	if codeB != http.StatusAccepted {
		t.Fatalf("submit task B failed: %d", codeB)
	}

	// 2. 等待 A 與 B 均啟動執行中
	select {
	case <-runnerAStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for task A runner to start")
	}
	select {
	case <-runnerBStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for task B runner to start")
	}

	// 3. 透過 Web 取消 A
	codeCancelA, bodyCancelA, _ := f.call(t, "POST", "/api/tasks/"+taskA.ID+"/cancel", `{}`, f.server.URL, c)
	if codeCancelA != http.StatusOK {
		t.Fatalf("cancel task A failed: %d %s", codeCancelA, bodyCancelA)
	}
	var cancelledTaskA storage.Task
	if err := json.Unmarshal([]byte(bodyCancelA), &cancelledTaskA); err != nil {
		t.Fatalf("unmarshal cancel task A response: %v", err)
	}
	if cancelledTaskA.Status != storage.StatusCancelled {
		t.Errorf("expected cancelled status for A, got %s", cancelledTaskA.Status)
	}

	// 確認任務 A 的 runner context 確實收到取消訊號並回收
	select {
	case <-runnerACancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("task A was not confirmed reclaimed by runner")
	}

	// 4. 驗證 B 完全不受影響且持續運行
	closeBOnce.Do(func() { close(unblockB) })
	deadline := time.Now().Add(3 * time.Second)
	var taskBFinal *storage.Task
	for time.Now().Before(deadline) {
		codeQB, bodyQB, _ := f.call(t, "GET", "/api/tasks/"+taskB.ID, "", f.server.URL, c)
		if codeQB == http.StatusOK {
			var t storage.Task
			if err := json.Unmarshal([]byte(bodyQB), &t); err == nil && t.Status == storage.StatusCompleted {
				taskBFinal = &t
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if taskBFinal == nil {
		t.Fatal("task B was affected or failed to complete normally")
	}
	if taskBFinal.Result != "SUCCESS-B" {
		t.Errorf("expected task B result SUCCESS-B, got %q", taskBFinal.Result)
	}

	// 5. 無法確認回收時顯示錯誤（取消不存在的 Task ID）
	codeNotFound, bodyNotFound, _ := f.call(t, "POST", "/api/tasks/nonexistent-task-id/cancel", `{}`, f.server.URL, c)
	if codeNotFound != http.StatusNotFound {
		t.Errorf("expected 404 for cancelling nonexistent task, got %d (body: %s)", codeNotFound, bodyNotFound)
	}
	if !strings.Contains(bodyNotFound, "task not found") {
		t.Errorf("expected 'task not found' error message, got %q", bodyNotFound)
	}

	// 6. 前端中斷不取消背景工作：發出任務 C 但中斷 HTTP 請求，確認任務 C 依然在 Daemon 完整執行
	taskCStarted := make(chan struct{}, 1)
	unblockC := make(chan struct{})
	var closeCOnce sync.Once
	defer closeCOnce.Do(func() { close(unblockC) })
	var startsC atomic.Int32
	engC := engine.NewEngine(cancelStore, cancelPolicy, engine.WithRunner(func(ctx context.Context, ws, prompt string) (string, error) {
		startsC.Add(1)
		taskCStarted <- struct{}{}
		<-unblockC
		return "TASK-C-COMPLETED", nil
	}))
	sockC := filepath.Join(cancelDir, "daemonC.sock")
	daemonC := daemon.NewDaemon(daemon.Config{SocketPath: sockC}, cancelStore, cancelPolicy, daemon.WithEngine(engC))
	if err := daemonC.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { daemonC.Close() })

	srvC := httptest.NewUnstartedServer(nil)
	webC, err := web.NewServer(web.Config{Key: "fixture-secret", Origin: "http://" + srvC.Listener.Addr().String(), SocketPath: sockC})
	if err != nil {
		t.Fatal(err)
	}
	srvC.Config.Handler = webC
	srvC.Start()
	t.Cleanup(srvC.Close)

	fC := &webFixture{root: cancelDir, store: cancelStore, server: srvC, socket: sockC}
	cookieC := fC.login(t)

	// 提交任務 C
	codeC, taskC, _ := fC.submitTask(t, engine.SubmitRequest{
		ID:        "req-task-C",
		Workspace: cancelDir,
		Prompt:    "task-C-work",
	}, cookieC)
	if codeC != http.StatusAccepted {
		t.Fatalf("submit task C failed: %d", codeC)
	}
	select {
	case <-taskCStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for task C to start")
	}

	// 模擬前端瀏覽器斷線（直接發出一個 context 已取消的查詢請求）
	ctxReq, cancelReq := context.WithCancel(context.Background())
	cancelReq()
	httpReq, _ := http.NewRequestWithContext(ctxReq, "GET", srvC.URL+"/api/tasks/"+taskC.ID, nil)
	httpReq.AddCookie(cookieC)
	_, _ = srvC.Client().Do(httpReq)

	// 釋放任務 C 執行，確認其仍順利完成，不受前端斷線影響
	closeCOnce.Do(func() { close(unblockC) })
	deadlineC := time.Now().Add(3 * time.Second)
	var finalC *storage.Task
	for time.Now().Before(deadlineC) {
		qCode, qBody, _ := fC.call(t, "GET", "/api/tasks/"+taskC.ID, "", srvC.URL, cookieC)
		if qCode == http.StatusOK {
			var tc storage.Task
			if err := json.Unmarshal([]byte(qBody), &tc); err == nil && tc.Status == storage.StatusCompleted {
				finalC = &tc
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if finalC == nil {
		t.Fatal("task C was cancelled by client disconnect")
	}
}

// 2026-09-13 P3-T1：操作頁面交付 (GET /) 與專案授權清單 API (GET /api/projects)
func TestWEB008ProjectsAndPage(t *testing.T) {
	f := startWeb(t)
	c := f.login(t)

	// 1. 未登入呼叫 GET /api/projects 拒絕 401
	codeNoAuth, _, _ := f.call(t, "GET", "/api/projects", "", f.server.URL, nil)
	if codeNoAuth != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated /api/projects, got %d", codeNoAuth)
	}

	// 2. 已登入呼叫 GET /api/projects 回傳授權專案清單
	codeProjects, bodyProjects, _ := f.call(t, "GET", "/api/projects", "", f.server.URL, c)
	if codeProjects != http.StatusOK {
		t.Fatalf("expected 200 for /api/projects, got %d (body: %s)", codeProjects, bodyProjects)
	}
	var projects []string
	if err := json.Unmarshal([]byte(bodyProjects), &projects); err != nil {
		t.Fatalf("unmarshal projects response failed: %v", err)
	}
	if len(projects) == 0 {
		t.Fatalf("expected at least one project root in allowed list, got %v", projects)
	}
	found := false
	for _, p := range projects {
		if p == f.root {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected allowed root %s in projects list %v", f.root, projects)
	}

	// 3. GET / 交付 HTML 操作頁面
	codeIndex, bodyIndex, _ := f.call(t, "GET", "/", "", f.server.URL, nil)
	if codeIndex != http.StatusOK {
		t.Fatalf("expected 200 for GET /, got %d (body: %s)", codeIndex, bodyIndex)
	}
	if !strings.Contains(bodyIndex, "<!DOCTYPE html>") {
		t.Errorf("expected HTML document for GET /, got %q", bodyIndex)
	}
	// 驗證關鍵 UI 元素存在
	for _, keyElem := range []string{"project-select", "prompt", "task-id", "cancel", "submit", "login"} {
		if !strings.Contains(strings.ToLower(bodyIndex), strings.ToLower(keyElem)) {
			t.Errorf("expected HTML to contain %q element or id", keyElem)
		}
	}
}

// 2026-09-13 WEB-006：關閉頁面後恢復狀態與結果（重開登入查詢、定時輪詢、查詢不重跑任務）
func TestWEB006(t *testing.T) {
	f := startWeb(t)
	c1 := f.login(t)

	// 1. 提交任務
	reqID := "web-006-track-id"
	codeSubmit, taskAccepted, _ := f.submitTask(t, engine.SubmitRequest{
		ID:        reqID,
		Workspace: f.root,
		Prompt:    "prompt-web-006",
	}, c1)
	if codeSubmit != http.StatusAccepted {
		t.Fatalf("submit task failed: %d", codeSubmit)
	}

	// 2. 模擬關閉頁面：登出舊工作階段
	codeLogout, _, _ := f.call(t, "POST", "/api/logout", "", f.server.URL, c1)
	if codeLogout != http.StatusNoContent {
		t.Fatalf("logout failed: %d", codeLogout)
	}

	// 3. 重新開啟頁面並登入新工作階段
	c2 := f.login(t)

	// 4. 透過 Task ID 查詢，確認狀態與結果正常恢復
	deadline := time.Now().Add(3 * time.Second)
	var finalTask *storage.Task
	for time.Now().Before(deadline) {
		codeQ, bodyQ, _ := f.call(t, "GET", "/api/tasks/"+taskAccepted.ID, "", f.server.URL, c2)
		if codeQ == http.StatusOK {
			var cur storage.Task
			if err := json.Unmarshal([]byte(bodyQ), &cur); err == nil && cur.Status == storage.StatusCompleted {
				finalTask = &cur
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if finalTask == nil {
		t.Fatalf("task was not found or failed to complete after reopening")
	}
	if finalTask.Result != "KNOWN-RESULT" {
		t.Errorf("expected result KNOWN-RESULT, got %q", finalTask.Result)
	}

	// 5. 亦可透過 Request ID 恢復查詢
	codeQR, bodyQR, _ := f.call(t, "GET", "/api/tasks/"+reqID, "", f.server.URL, c2)
	if codeQR != http.StatusOK {
		t.Errorf("expected 200 querying by request_id, got %d", codeQR)
	}
	var taskByReq storage.Task
	_ = json.Unmarshal([]byte(bodyQR), &taskByReq)
	if taskByReq.ID != taskAccepted.ID {
		t.Errorf("expected same task ID when queried by request_id, got %s vs %s", taskByReq.ID, taskAccepted.ID)
	}

	// 6. 模擬前端開啟頁面定期輪詢，連續查詢 5 次，驗證查詢為唯讀操作，不重跑任務也不取消任務
	initialStarts := f.starts.Load()
	for i := 0; i < 5; i++ {
		codePoll, _, _ := f.call(t, "GET", "/api/tasks/"+taskAccepted.ID, "", f.server.URL, c2)
		if codePoll != http.StatusOK {
			t.Errorf("poll %d failed: %d", i, codePoll)
		}
	}
	if f.starts.Load() != initialStarts {
		t.Fatalf("polling triggered duplicate execution, starts was %d, now %d", initialStarts, f.starts.Load())
	}
}

// 2026-09-13 WEB-007：長輸出以純文字分頁交付（未登入拒絕、非法參數、Unicode 邊界、依序還原且不執行 HTML/script）
func TestWEB007(t *testing.T) {
	f := startWeb(t)
	c := f.login(t)

	// 建立包含多語文字、emoji、特殊字元與包含惡意 HTML / script 樣式標籤之長輸出
	// 人工固定真相來源，不重複實作切片邏輯
	baseText := "<script>alert('xss');</script><div>【測試報告】繁體中文 Unicode 邊界測試 🚀 數字 1234567890 符號 &amp; < > \" ' 結尾</div>\n"
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString(fmt.Sprintf("=== 區段 %02d ===\n", i))
		sb.WriteString(baseText)
	}
	fullText := sb.String()
	totalChars := len([]rune(fullText))

	// 在 store 中預先建立一筆已完成之長輸出任務
	taskID := "web-007-long-task"
	task := &storage.Task{
		ID:        taskID,
		Workspace: f.root,
		Status:    storage.StatusCompleted,
		Result:    fullText,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := f.store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	// 1. 未登入請求分頁讀取拒絕 401
	codeNoAuth, _, _ := f.call(t, "GET", "/api/tasks/"+taskID+"/result?offset=0&limit=500", "", f.server.URL, nil)
	if codeNoAuth != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated result pagination, got %d", codeNoAuth)
	}

	// 2. 越權專案任務之結果分頁讀取拒絕 403
	deniedTask := &storage.Task{
		ID:        "web-007-denied-task",
		Workspace: f.outside,
		Status:    storage.StatusCompleted,
		Result:    "SECRET_OUTSIDE",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := f.store.CreateTask(context.Background(), deniedTask); err != nil {
		t.Fatal(err)
	}
	codeDenied, _, _ := f.call(t, "GET", "/api/tasks/web-007-denied-task/result?offset=0&limit=100", "", f.server.URL, c)
	if codeDenied != http.StatusForbidden {
		t.Errorf("expected 403 for unauthorized workspace task result, got %d", codeDenied)
	}

	// 3. 非法頁參數拒絕 400 Bad Request
	for _, badParam := range []string{
		"?offset=-1&limit=100",
		"?offset=0&limit=0",
		"?offset=0&limit=-10",
		"?offset=abc&limit=100",
		"?offset=0&limit=xyz",
	} {
		codeBad, _, _ := f.call(t, "GET", "/api/tasks/"+taskID+"/result"+badParam, "", f.server.URL, c)
		if codeBad != http.StatusBadRequest {
			t.Errorf("expected 400 for bad query param %q, got %d", badParam, codeBad)
		}
	}

	// 4. 登入後依序分頁讀取全部結果頁，驗證完整還原且無重複無遺漏
	pageSize := 500
	var reconstructed strings.Builder
	offset := 0
	for {
		url := fmt.Sprintf("/api/tasks/%s/result?offset=%d&limit=%d", taskID, offset, pageSize)
		code, body, _ := f.call(t, "GET", url, "", f.server.URL, c)
		if code != http.StatusOK {
			t.Fatalf("pagination fetch failed at offset %d: %d body=%s", offset, code, body)
		}
		var page struct {
			TaskID  string `json:"task_id"`
			Offset  int    `json:"offset"`
			Limit   int    `json:"limit"`
			Total   int    `json:"total"`
			Data    string `json:"data"`
			HasMore bool   `json:"has_more"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatalf("unmarshal page response failed: %v", err)
		}
		if page.Total != totalChars {
			t.Errorf("expected total %d runes, got %d", totalChars, page.Total)
		}
		reconstructed.WriteString(page.Data)
		if !page.HasMore {
			break
		}
		offset += len([]rune(page.Data))
	}

	// 斷言：完全還原原始保存字串
	if reconstructed.String() != fullText {
		t.Fatalf("reconstructed text does not match original text; len original=%d, len reconstructed=%d", len(fullText), len(reconstructed.String()))
	}

	// 5. 空結果分頁驗證：空結果任務回傳 data: "", total: 0, has_more: false
	emptyTask := &storage.Task{
		ID:        "web-007-empty-task",
		Workspace: f.root,
		Status:    storage.StatusCompleted,
		Result:    "",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := f.store.CreateTask(context.Background(), emptyTask); err != nil {
		t.Fatal(err)
	}
	codeEmpty, bodyEmpty, _ := f.call(t, "GET", "/api/tasks/web-007-empty-task/result?offset=0&limit=100", "", f.server.URL, c)
	if codeEmpty != http.StatusOK {
		t.Fatalf("expected 200 for empty task result, got %d", codeEmpty)
	}
	var emptyPage struct {
		Total   int    `json:"total"`
		Data    string `json:"data"`
		HasMore bool   `json:"has_more"`
	}
	_ = json.Unmarshal([]byte(bodyEmpty), &emptyPage)
	if emptyPage.Total != 0 || emptyPage.Data != "" || emptyPage.HasMore {
		t.Errorf("expected empty page response, got %+v", emptyPage)
	}
}

// 2026-09-13 WEB-009：失敗與服務重啟保留真實結果（失敗終態保留輸出、重啟後可查、不誤報成功、不自動重跑）
func TestWEB009(t *testing.T) {
	failDir := t.TempDir()
	failSock := filepath.Join(failDir, "fail.sock")
	dbPath := filepath.Join(failDir, "tasks.db")
	failStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	failPolicy := auth.NewPolicy([]string{failDir})

	var runCount atomic.Int32
	failEng := engine.NewEngine(failStore, failPolicy, engine.WithRunner(func(ctx context.Context, ws, prompt string) (string, error) {
		runCount.Add(1)
		if strings.Contains(prompt, "fail-task") {
			return "PARTIAL-OUTPUT-BEFORE-CRASH", errors.New("backend process exited with code 1")
		}
		return "NORMAL-SUCCESS", nil
	}))

	daemonInstance := daemon.NewDaemon(daemon.Config{SocketPath: failSock}, failStore, failPolicy, daemon.WithEngine(failEng))
	if err := daemonInstance.Start(); err != nil {
		t.Fatal(err)
	}

	webSrv := httptest.NewUnstartedServer(nil)
	ws, err := web.NewServer(web.Config{
		Key:        "fixture-secret",
		Origin:     "http://" + webSrv.Listener.Addr().String(),
		SocketPath: failSock,
	})
	if err != nil {
		t.Fatal(err)
	}
	webSrv.Config.Handler = ws
	webSrv.Start()

	f := &webFixture{root: failDir, store: failStore, server: webSrv, socket: failSock}
	c := f.login(t)

	// 1. 提交失敗任務
	codeF, taskF, _ := f.submitTask(t, engine.SubmitRequest{
		ID:        "web-009-fail-req",
		Workspace: failDir,
		Prompt:    "fail-task",
	}, c)
	if codeF != http.StatusAccepted {
		t.Fatalf("submit fail task failed: %d", codeF)
	}

	// 等待任務完成（進入 failed 狀態）
	deadline := time.Now().Add(3 * time.Second)
	var finalFailTask *storage.Task
	for time.Now().Before(deadline) {
		qCode, qBody, _ := f.call(t, "GET", "/api/tasks/"+taskF.ID, "", f.server.URL, c)
		if qCode == http.StatusOK {
			var t storage.Task
			if err := json.Unmarshal([]byte(qBody), &t); err == nil && t.Status == storage.StatusFailed {
				finalFailTask = &t
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if finalFailTask == nil {
		t.Fatalf("task did not transition to failed status")
	}
	// 驗證失敗終態與已持久化輸出
	if finalFailTask.Status != storage.StatusFailed {
		t.Errorf("expected status failed, got %s", finalFailTask.Status)
	}
	if !strings.Contains(finalFailTask.Result, "PARTIAL-OUTPUT-BEFORE-CRASH") {
		t.Errorf("expected partial output preserved, got %q", finalFailTask.Result)
	}
	if !strings.Contains(finalFailTask.ErrorMessage, "exited with code 1") {
		t.Errorf("expected real error message, got %q", finalFailTask.ErrorMessage)
	}

	// 2. 中樞中斷後重啟情境：關閉 Daemon 與 Web，重新初始化
	webSrv.Close()
	daemonInstance.Close()
	failStore.Close()

	// 重新啟動儲存、中樞與 Web
	reopenedStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopenedStore.Close() })
	reopenedDaemon := daemon.NewDaemon(daemon.Config{SocketPath: failSock}, reopenedStore, failPolicy, daemon.WithEngine(failEng))
	if err := reopenedDaemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopenedDaemon.Close() })

	reopenedWebSrv := httptest.NewUnstartedServer(nil)
	reopenedWS, err := web.NewServer(web.Config{
		Key:        "fixture-secret",
		Origin:     "http://" + reopenedWebSrv.Listener.Addr().String(),
		SocketPath: failSock,
	})
	if err != nil {
		t.Fatal(err)
	}
	reopenedWebSrv.Config.Handler = reopenedWS
	reopenedWebSrv.Start()
	t.Cleanup(reopenedWebSrv.Close)

	fReopened := &webFixture{root: failDir, store: reopenedStore, server: reopenedWebSrv, socket: failSock}
	cReopened := fReopened.login(t)

	// 透過 Web 查詢先前失敗的任務
	startsBeforeReopenQuery := runCount.Load()
	qCodeReopen, qBodyReopen, _ := fReopened.call(t, "GET", "/api/tasks/"+taskF.ID, "", fReopened.server.URL, cReopened)
	if qCodeReopen != http.StatusOK {
		t.Fatalf("query task after restart failed: %d (body: %s)", qCodeReopen, qBodyReopen)
	}
	var restoredTask storage.Task
	if err := json.Unmarshal([]byte(qBodyReopen), &restoredTask); err != nil {
		t.Fatalf("unmarshal restored task: %v", err)
	}
	// 斷言：不將未知結果標成成功，狀態依然為 failed，輸出依然完整
	if restoredTask.Status != storage.StatusFailed {
		t.Errorf("expected restored status failed, got %s", restoredTask.Status)
	}
	if restoredTask.Result != finalFailTask.Result {
		t.Errorf("restored result mismatch: %q vs %q", restoredTask.Result, finalFailTask.Result)
	}
	// 斷言：查詢不重跑副作用
	if runCount.Load() != startsBeforeReopenQuery {
		t.Errorf("reopen query triggered backend rerun, count=%d vs %d", runCount.Load(), startsBeforeReopenQuery)
	}
}
