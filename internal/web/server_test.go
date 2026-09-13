package web_test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"asura/internal/daemon"
	"asura/internal/storage"
	"asura/internal/web"
)

const origin = "https://asura.example"
const secret = "test-only-secret-please-do-not-use"

func request(s http.Handler, method, path, body, source string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, origin+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if source != "" {
		r.Header.Set("Origin", source)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

// 2026-09-13 WEB-001：以 HTTP 狀態與 cookie 屬性驗證登入邊界。
func TestWEB001Unit(t *testing.T) {
	s, err := web.NewServer(web.Config{Key: secret, Origin: origin, SocketPath: "/does-not-exist.sock"})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"key":"wrong"}`, `{}`, `{"key":"` + secret + `","allowed_roots":["/"]}`} {
		w := request(s, "POST", "/api/login", body, origin, nil)
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusBadRequest {
			t.Errorf("bad login: %d", w.Code)
		}
		if len(w.Result().Cookies()) != 0 {
			t.Error("bad login created a session")
		}
	}
	w := request(s, "POST", "/api/login", `{"key":"`+secret+`"}`, origin, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("valid login: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies: %v", cookies)
	}
	c := cookies[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Value == secret {
		t.Errorf("unsafe session cookie: %+v", c)
	}
	if strings.Contains(w.Body.String(), secret) || w.Header().Get("Cache-Control") != "no-store" {
		t.Error("login leaks or caches secret")
	}
	for _, cookie := range []*http.Cookie{nil, {Name: c.Name, Value: "forged"}} {
		w = request(s, "GET", "/api/tasks/private", "", origin, cookie)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated read: %d", w.Code)
		}
	}
	for _, cfg := range []web.Config{{Origin: origin, SocketPath: "/tmp/d.sock"}, {Key: secret, Origin: "https://asura.example/path", SocketPath: "/tmp/d.sock"}, {Key: secret, Origin: origin}} {
		if _, err := web.NewServer(cfg); err == nil {
			t.Errorf("invalid config accepted: %+v", cfg)
		}
	}
}

// 2026-09-13 WEB-010：即使持有 cookie，跨站、偽造 Host 與登出後均不得操作。
func TestWEB010Unit(t *testing.T) {
	s, err := web.NewServer(web.Config{Key: secret, Origin: origin, SocketPath: "/does-not-exist.sock"})
	if err != nil {
		t.Fatal(err)
	}
	w := request(s, "POST", "/api/login", `{"key":"`+secret+`"}`, origin, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("login: %d", w.Code)
	}
	c := w.Result().Cookies()[0]
	for _, source := range []string{"https://evil.example", "null", ""} {
		w = request(s, "POST", "/api/tasks", `{}`, source, c)
		if w.Code != http.StatusForbidden {
			t.Errorf("cross-site request %q: %d", source, w.Code)
		}
	}
	r := httptest.NewRequest("GET", origin+"/api/tasks/private", nil)
	r.Host = "evil.example"
	r.AddCookie(c)
	r.Header.Set("X-Forwarded-Host", "asura.example")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("forged Host: %d", w.Code)
	}
	w = request(s, "POST", "/api/logout", `{}`, origin, c)
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", w.Code)
	}
	w = request(s, "GET", "/api/tasks/private", "", origin, c)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("revoked cookie: %d", w.Code)
	}
}

func startMockSocket(t *testing.T, handler func(req daemon.IPCRequest) daemon.IPCResponse) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "mock.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				var req daemon.IPCRequest
				if err := json.NewDecoder(c).Decode(&req); err == nil {
					resp := handler(req)
					_ = json.NewEncoder(c).Encode(resp)
				}
			}(conn)
		}
	}()
	return sockPath
}

// 2026-09-13 WEB-003：提交參數校驗、中樞錯誤映射與接受回應格式化
func TestWEB003Unit(t *testing.T) {
	mockSock := startMockSocket(t, func(req daemon.IPCRequest) daemon.IPCResponse {
		if req.Action == "submit" {
			if req.Workspace == "/capped" {
				return daemon.IPCResponse{Error: daemon.ErrCapacityExceeded.Error()}
			}
			return daemon.IPCResponse{
				Task: &storage.Task{
					ID:        "task-accepted-1",
					RequestID: req.RequestID,
					Workspace: req.Workspace,
					Prompt:    req.Prompt,
					Status:    storage.StatusPending,
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
			}
		}
		return daemon.IPCResponse{Error: "unknown action"}
	})

	s, err := web.NewServer(web.Config{Key: secret, Origin: origin, SocketPath: mockSock})
	if err != nil {
		t.Fatal(err)
	}
	wLogin := request(s, "POST", "/api/login", `{"key":"`+secret+`"}`, origin, nil)
	c := wLogin.Result().Cookies()[0]

	// 1. 缺少必要欄位或畸形 JSON 拒絕 400
	for _, badBody := range []string{
		`{}`,
		`{"id":""}`,
		`{"id":"test","workspace":"/tmp"}`,
		`{"id":"test","prompt":"prompt"}`,
		`{"id":"test","workspace":"/tmp","prompt":"   "}`,
		`{"id":"test","workspace":"/tmp","prompt":"p","unknown_field":1}`,
		`not-json`,
	} {
		w := request(s, "POST", "/api/tasks", badBody, origin, c)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for body %q, got %d", badBody, w.Code)
		}
	}

	// 2. 正常提交回傳 202 與 Task JSON
	validBody := `{"id":"valid-req","workspace":"/tmp","prompt":"run task"}`
	wValid := request(s, "POST", "/api/tasks", validBody, origin, c)
	if wValid.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", wValid.Code)
	}
	if ct := wValid.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected application/json, got %q", ct)
	}
	var task storage.Task
	if err := json.Unmarshal(wValid.Body.Bytes(), &task); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if task.ID != "task-accepted-1" || task.RequestID != "valid-req" {
		t.Errorf("unexpected task: %+v", task)
	}

	// 3. 容量超出回傳 503 capacity exceeded
	cappedBody := `{"id":"capped-req","workspace":"/capped","prompt":"run task"}`
	wCapped := request(s, "POST", "/api/tasks", cappedBody, origin, c)
	if wCapped.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for capacity exceeded, got %d", wCapped.Code)
	}
	if !strings.Contains(wCapped.Body.String(), "capacity exceeded") {
		t.Errorf("expected 'capacity exceeded' in body, got %q", wCapped.Body.String())
	}
}

// 2026-09-13 WEB-004：同識別重送時回傳原任務並保持 202
func TestWEB004Unit(t *testing.T) {
	mockSock := startMockSocket(t, func(req daemon.IPCRequest) daemon.IPCResponse {
		return daemon.IPCResponse{
			Task: &storage.Task{
				ID:        "original-task-id",
				RequestID: req.RequestID,
				Workspace: req.Workspace,
				Prompt:    req.Prompt,
				Status:    storage.StatusRunning,
			},
		}
	})
	s, err := web.NewServer(web.Config{Key: secret, Origin: origin, SocketPath: mockSock})
	if err != nil {
		t.Fatal(err)
	}
	wLogin := request(s, "POST", "/api/login", `{"key":"`+secret+`"}`, origin, nil)
	c := wLogin.Result().Cookies()[0]

	body := `{"id":"idempotent-key","workspace":"/tmp","prompt":"prompt"}`
	w1 := request(s, "POST", "/api/tasks", body, origin, c)
	w2 := request(s, "POST", "/api/tasks", body, origin, c)
	if w1.Code != http.StatusAccepted || w2.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for both submissions, got %d and %d", w1.Code, w2.Code)
	}
	var t1, t2 storage.Task
	_ = json.Unmarshal(w1.Body.Bytes(), &t1)
	_ = json.Unmarshal(w2.Body.Bytes(), &t2)
	if t1.ID != "original-task-id" || t2.ID != "original-task-id" {
		t.Errorf("expected original-task-id, got %s and %s", t1.ID, t2.ID)
	}
}

// 2026-09-13 WEB-005：重送衝突（異內容、已取消）之 HTTP 錯誤映射為 400
func TestWEB005Unit(t *testing.T) {
	mockSock := startMockSocket(t, func(req daemon.IPCRequest) daemon.IPCResponse {
		if req.Prompt == "different" {
			return daemon.IPCResponse{Error: "request ID reused with different payload"}
		}
		if req.Prompt == "cancelled" {
			return daemon.IPCResponse{Error: "cannot redeliver cancelled request"}
		}
		return daemon.IPCResponse{Task: &storage.Task{ID: "new-task-id"}}
	})
	s, err := web.NewServer(web.Config{Key: secret, Origin: origin, SocketPath: mockSock})
	if err != nil {
		t.Fatal(err)
	}
	wLogin := request(s, "POST", "/api/login", `{"key":"`+secret+`"}`, origin, nil)
	c := wLogin.Result().Cookies()[0]

	// 1. 同識別異內容拒絕 400
	wDiff := request(s, "POST", "/api/tasks", `{"id":"dup-key","workspace":"/tmp","prompt":"different"}`, origin, c)
	if wDiff.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for different payload, got %d", wDiff.Code)
	}
	if !strings.Contains(wDiff.Body.String(), "request ID reused with different payload") {
		t.Errorf("expected conflict message in body, got %q", wDiff.Body.String())
	}

	// 2. 已取消任務重送拒絕 400
	wCancelled := request(s, "POST", "/api/tasks", `{"id":"dup-key","workspace":"/tmp","prompt":"cancelled"}`, origin, c)
	if wCancelled.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for cancelled redelivery, got %d", wCancelled.Code)
	}
	if !strings.Contains(wCancelled.Body.String(), "cannot redeliver cancelled request") {
		t.Errorf("expected cancelled redelivery message in body, got %q", wCancelled.Body.String())
	}
}
