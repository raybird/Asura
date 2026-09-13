package web

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
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
