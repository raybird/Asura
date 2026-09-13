package web

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 2026-09-13 WEB-010：使用已知過期的工作階段，確認 HTTP 入口拒絕而非依賴 cookie 到期。
func TestWEB010ExpiredSession(t *testing.T) {
	s, err := NewServer(Config{Key: "test-key", Origin: "https://asura.example", SocketPath: "/missing.sock"})
	if err != nil {
		t.Fatal(err)
	}
	s.sessions[sha256.Sum256([]byte("expired"))] = time.Unix(0, 0)
	r := httptest.NewRequest("GET", "https://asura.example/api/tasks/private", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "expired"})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expired session status: %d", w.Code)
	}
}

// 2026-09-13 審查修復：驗證活躍工作階段達 128 上限時，登入自動逐出最舊 session，不拒絕合法使用者。
func TestSession_EvictOldestWhenCapacityReached(t *testing.T) {
	s, err := NewServer(Config{Key: "test-key", Origin: "http://127.0.0.1:8080", SocketPath: "/missing.sock"})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	// 填滿 128 個活躍 session，其中 oldestToken 到期時間最早
	oldestToken := "token-oldest"
	s.sessions[sha256.Sum256([]byte(oldestToken))] = now.Add(1 * time.Hour)
	for i := 1; i < 128; i++ {
		s.sessions[sha256.Sum256([]byte(string(rune(i))))] = now.Add(2 * time.Hour)
	}

	if len(s.sessions) != 128 {
		t.Fatalf("expected 128 sessions, got %d", len(s.sessions))
	}

	// 發起新登入
	body := strings.NewReader(`{"key":"test-key"}`)
	r := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/login", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:8080")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected login status 204, got %d (body: %s)", w.Code, w.Body.String())
	}

	// 最舊 session 應已被逐出
	if s.authenticated(oldestToken) {
		t.Fatalf("expected oldest session to be evicted, but still authenticated")
	}

	// 取得新 session cookie 並驗證其有效
	cookies := w.Result().Cookies()
	var newToken string
	for _, c := range cookies {
		if c.Name == sessionCookie {
			newToken = c.Value
			break
		}
	}
	if newToken == "" {
		t.Fatalf("expected session cookie to be set")
	}
	if !s.authenticated(newToken) {
		t.Fatalf("expected new session to be authenticated")
	}

	// 活躍 session 總數仍維持在 128
	if len(s.sessions) != 128 {
		t.Fatalf("expected session count to remain 128, got %d", len(s.sessions))
	}
}
