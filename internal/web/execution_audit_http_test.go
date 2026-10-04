package web_test

import (
	"asura/internal/daemon"
	"asura/internal/web"
	"net/http"
	"strings"
	"testing"
)

// 2026-10-04：EXAUD-012，新 audit route 必須使用既有 session/origin 邊界。
func TestExecutionAuditHTTPBoundary(t *testing.T) {
	s, err := web.NewServer(web.Config{Key: secret, Origin: origin, SocketPath: "/missing"})
	if err != nil {
		t.Fatal(err)
	}
	if w := request(s, "GET", "/api/tasks/private/audit", "", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatal("audit leaked")
	}
	login := request(s, "POST", "/api/login", `{"key":"`+secret+`"}`, origin, nil)
	cookie := login.Result().Cookies()[0]
	if w := request(s, "GET", "/api/tasks/private/audit", "", origin, cookie); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("audit route absent: %d", w.Code)
	}
	if w := request(s, "GET", "/api/tasks/private/audit", "", "https://evil.example", cookie); w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin read allowed: %d", w.Code)
	}
}

// 2026-10-04：EXAUD-014，過大 IPC 回應轉有界 HTTP 錯誤，原字串不外洩。
func TestExecutionAuditHTTPResponseBound(t *testing.T) {
	sock := startMockSocket(t, func(req daemon.IPCRequest) daemon.IPCResponse {
		return daemon.IPCResponse{Error: strings.Repeat("secret-sentinel", 20000)}
	})
	server, err := web.NewServer(web.Config{Key: secret, Origin: origin, SocketPath: sock})
	if err != nil {
		t.Fatal(err)
	}
	login := request(server, "POST", "/api/login", `{"key":"`+secret+`"}`, origin, nil)
	response := request(server, "GET", "/api/tasks/id/audit", "", origin, login.Result().Cookies()[0])
	if response.Code != http.StatusServiceUnavailable || response.Body.Len() > 100 || strings.Contains(response.Body.String(), "secret-sentinel") {
		t.Fatalf("unbounded reply %d %d", response.Code, response.Body.Len())
	}
}
