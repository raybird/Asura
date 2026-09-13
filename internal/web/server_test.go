package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
