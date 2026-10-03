package web_test

import (
	"asura/internal/web"
	"net/http"
	"testing"
)

// 2026-10-04：RTINV-014，認證後才可查詢 fresh inventory；無 daemon 回報 unavailable。
func TestInventoryHTTPBoundary(t *testing.T) {
	s, err := web.NewServer(web.Config{Key: secret, Origin: origin, SocketPath: "/missing"})
	if err != nil {
		t.Fatal(err)
	}
	if w := request(s, "GET", "/api/runtime-inventory", "", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatal("inventory leaked")
	}
	login := request(s, "POST", "/api/login", `{"key":"`+secret+`"}`, origin, nil)
	w := request(s, "GET", "/api/runtime-inventory", "", origin, login.Result().Cookies()[0])
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("inventory route absent: %d", w.Code)
	}
}
