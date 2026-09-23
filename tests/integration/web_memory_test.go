package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"asura/internal/storage"
)

// 2026-09-23：WEBMEM-001～004 的真實 HTTP → IPC → SQLite 邊界。
func TestWEBMEMHTTPBoundary(t *testing.T) {
	f := startWeb(t)
	ctx := context.Background()
	if err := f.store.CreateMemory(ctx, storage.Memory{ID: "known", Workspace: f.root, Content: "known content"}); err != nil {
		t.Fatal(err)
	}
	cookie := f.login(t)
	path := "/api/memories?workspace=" + url.QueryEscape(f.root)
	for _, method := range []string{"GET", "POST"} {
		code, _, _ := f.call(t, method, path, `{"workspace":"`+f.root+`","content":"intruder"}`, f.server.URL, nil)
		if code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s=%d", method, code)
		}
	}
	for _, method := range []string{"POST", "DELETE"} {
		endpoint := path
		if method == "DELETE" {
			endpoint = "/api/memories/known?workspace=" + url.QueryEscape(f.root)
		}
		code, _, _ := f.call(t, method, endpoint, `{"workspace":"`+f.root+`","content":"intruder"}`, "https://evil.example", cookie)
		if code != http.StatusForbidden {
			t.Fatalf("cross-origin %s=%d", method, code)
		}
	}
	code, body, _ := f.call(t, "GET", path, "", "", cookie)
	if code != http.StatusOK {
		t.Fatalf("valid GET without Origin=%d %s", code, body)
	}
	var page storage.MemoryPage
	if err := json.Unmarshal([]byte(body), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != "known" {
		t.Fatalf("page=%+v, err=%v", page, err)
	}
	if _, err := f.store.GetMemory(ctx, f.root, "known"); err != nil || f.starts.Load() != 0 {
		t.Fatalf("rejection changed state: err=%v starts=%d", err, f.starts.Load())
	}
	code, body, _ = f.call(t, "GET", "/api/memories/known?workspace="+url.QueryEscape(f.root), "", "https://evil.example", cookie)
	if code != http.StatusOK || !strings.Contains(body, "known content") {
		t.Fatalf("GET with Origin=%d %s", code, body)
	}
}

func TestWEBMEMCRUDAndLimits(t *testing.T) {
	f := startWeb(t)
	cookie := f.login(t)
	ctx := context.Background()
	path := "/api/memories?workspace=" + url.QueryEscape(f.root)
	code, body, _ := f.call(t, "POST", "/api/memories", `{"workspace":"`+f.root+`","content":"手動記憶"}`, f.server.URL, cookie)
	if code != http.StatusCreated {
		t.Fatalf("add=%d %s", code, body)
	}
	var added storage.Memory
	if err := json.Unmarshal([]byte(body), &added); err != nil || added.ID == "" || added.Source != "manual" {
		t.Fatalf("added=%+v err=%v", added, err)
	}
	code, body, _ = f.call(t, "GET", path, "", "", cookie)
	if code != http.StatusOK || !strings.Contains(body, added.ID) || strings.Contains(body, "手動記憶\"\n") {
		t.Fatalf("list=%d %s", code, body)
	}
	for _, bad := range []string{
		`{"workspace":"` + f.root + `","content":"` + strings.Repeat("x", 32769) + `"}`,
		`{"workspace":"` + f.root + `","content":"ok","source":"ai"}`,
	} {
		code, _, _ = f.call(t, "POST", "/api/memories", bad, f.server.URL, cookie)
		if code != http.StatusRequestEntityTooLarge && code != http.StatusBadRequest {
			t.Fatalf("bad add=%d", code)
		}
	}
	if err := f.store.CreateMemory(ctx, storage.Memory{ID: "oversize", Workspace: f.root, Content: strings.Repeat("z", 65537)}); err != nil {
		t.Fatal(err)
	}
	code, _, _ = f.call(t, "GET", "/api/memories/oversize?workspace="+url.QueryEscape(f.root), "", "", cookie)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize detail=%d", code)
	}
	code, _, _ = f.call(t, "GET", "/api/memories/oversize?workspace="+url.QueryEscape(f.outside), "", "", cookie)
	if code != http.StatusForbidden {
		t.Fatalf("outside scope=%d", code)
	}
	code, _, _ = f.call(t, "DELETE", "/api/memories/"+url.PathEscape(added.ID)+"?workspace="+url.QueryEscape(f.root), "", f.server.URL, cookie)
	if code != http.StatusNoContent {
		t.Fatalf("delete=%d", code)
	}
	if _, err := f.store.GetMemory(ctx, f.root, added.ID); err != storage.ErrMemoryNotFound {
		t.Fatalf("delete storage=%v", err)
	}
	code, _, _ = f.call(t, "DELETE", "/api/memories/"+url.PathEscape(added.ID)+"?workspace="+url.QueryEscape(f.root), "", f.server.URL, cookie)
	if code != http.StatusNotFound {
		t.Fatalf("repeat delete=%d", code)
	}
}

func TestWEBMEMHTTPPageLargeDataset(t *testing.T) {
	f := startWeb(t)
	ctx := context.Background()
	for i := 0; i < 10000; i++ {
		if err := f.store.CreateMemory(ctx, storage.Memory{ID: fmt.Sprintf("m%05d", i), Workspace: f.root, Content: strings.Repeat("a", 1024)}); err != nil {
			t.Fatal(err)
		}
	}
	cookie := f.login(t)
	path := "/api/memories?workspace=" + url.QueryEscape(f.root) + "&limit=50"
	code, body, _ := f.call(t, "GET", path, "", "", cookie)
	if code != http.StatusOK || len(body) > 512<<10 {
		t.Fatalf("page code=%d bytes=%d", code, len(body))
	}
	var page storage.MemoryPage
	if err := json.Unmarshal([]byte(body), &page); err != nil || len(page.Items) != 50 || page.Items[0].ID != "m00000" || page.NextCursor == "" {
		t.Fatalf("first page=%+v err=%v", page, err)
	}
	code, body, _ = f.call(t, "GET", path+"&cursor="+url.QueryEscape(page.NextCursor), "", "", cookie)
	if code != http.StatusOK {
		t.Fatalf("second page=%d", code)
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil || page.Items[0].ID != "m00050" {
		t.Fatalf("second page=%+v err=%v", page, err)
	}
}
