package web_test

import (
	"asura/internal/daemon"
	"asura/internal/storage"
	"asura/internal/web"
	"net/http"
	"strings"
	"testing"
)

// 2026-09-30 WEBGIT-004：即使 Daemon 回傳不合 bounded contract 的資料，reader 仍拒絕。
func TestWEBGITIPCReaderBound(t *testing.T) {
	sock := startMockSocket(t, func(req daemon.IPCRequest) daemon.IPCResponse {
		return daemon.IPCResponse{Candidate: &storage.MemoryCandidate{ID: "c", DraftContent: strings.Repeat("x", 2<<20)}}
	})
	s, err := web.NewServer(web.Config{Key: secret, Origin: origin, SocketPath: sock})
	if err != nil {
		t.Fatal(err)
	}
	w := request(s, "POST", "/api/login", `{"key":"`+secret+`"}`, origin, nil)
	cookie := w.Result().Cookies()[0]
	w = request(s, "GET", "/api/git-memory/candidates/c?workspace=/a", "", "", cookie)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize IPC reply: %d", w.Code)
	}
	if w.Body.Len() > 1024 {
		t.Fatal("oversized IPC reply leaked to browser")
	}
}
