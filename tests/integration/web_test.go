package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
