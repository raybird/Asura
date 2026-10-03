package web_test

import (
	"asura/internal/daemon"
	"asura/internal/storage"
	"asura/internal/web"
	"net/http"
	"testing"
)

// 2026-10-03：RTSEL-011，Web HTTP 至 IPC 只測 boundary，resolver 另由真 daemon 測試保障。
func TestRuntimeSelectionWebIntent(t *testing.T) {
	received := make(chan string, 2)
	sock := startMockSocket(t, func(req daemon.IPCRequest) daemon.IPCResponse {
		received <- req.Backend
		return daemon.IPCResponse{Task: &storage.Task{ID: "task", Backend: "codex"}}
	})
	server, err := web.NewServer(web.Config{Key: secret, Origin: origin, SocketPath: sock})
	if err != nil {
		t.Fatal(err)
	}
	login := request(server, "POST", "/api/login", `{"key":"`+secret+`"}`, origin, nil)
	cookie := login.Result().Cookies()[0]
	for _, body := range []string{`{"id":"auto","workspace":"/project","prompt":"probe","backend":"auto"}`, `{"id":"legacy","workspace":"/project","prompt":"probe"}`} {
		response := request(server, "POST", "/api/tasks", body, origin, cookie)
		if response.Code != http.StatusAccepted {
			t.Fatalf("response=%d %s", response.Code, response.Body.String())
		}
	}
	if got := <-received; got != "auto" {
		t.Fatalf("auto intent=%q", got)
	}
	if got := <-received; got != "" {
		t.Fatalf("legacy intent=%q", got)
	}
}
