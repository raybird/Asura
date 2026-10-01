package web

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 2026-09-30 WEBGIT-001：過期 session 在新路由分派前拒絕。
func TestWEBGITExpiredSession(t *testing.T) {
	s, e := NewServer(Config{Key: "test-key", Origin: "https://asura.example", SocketPath: "/missing.sock"})
	if e != nil {
		t.Fatal(e)
	}
	for _, x := range []struct{ method, path string }{{"POST", "/api/git-memory/capture"}, {"GET", "/api/git-memory/candidates?workspace=/a"}, {"GET", "/api/git-memory/candidates/c?workspace=/a"}, {"POST", "/api/git-memory/candidates/c/approve"}, {"POST", "/api/git-memory/candidates/c/discard"}} {
		s.sessions[sha256.Sum256([]byte("expired"))] = time.Unix(0, 0)
		r := httptest.NewRequest(x.method, "https://asura.example"+x.path, nil)
		r.Header.Set("Origin", "https://asura.example")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "expired"})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("expired %s %d", x.path, w.Code)
		}
	}
}
