package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

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
	code, _, _ = f.call(t, "POST", "/api/memories", `{"workspace":"`+f.root+`","content":"`+strings.Repeat("x", 32768)+`"}`, f.server.URL, cookie)
	if code != http.StatusCreated {
		t.Fatalf("exact add bound=%d", code)
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
	code, _, _ = f.call(t, "POST", "/api/memories", "{\"workspace\":\""+f.root+"\",\"content\":\""+string([]byte{0xff})+"\"}", f.server.URL, cookie)
	if code != http.StatusBadRequest {
		t.Fatalf("invalid UTF-8 add=%d", code)
	}
	code, _, _ = f.call(t, "POST", "/api/memories", `{"workspace":"`+f.root+`","content":"`+strings.Repeat("x", 70000)+`"}`, f.server.URL, cookie)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body over 64 KiB=%d", code)
	}
	if err := f.store.CreateMemory(ctx, storage.Memory{ID: "exact-detail", Workspace: f.root, Content: strings.Repeat("y", 65536)}); err != nil {
		t.Fatal(err)
	}
	code, body, _ = f.call(t, "GET", "/api/memories/exact-detail?workspace="+url.QueryEscape(f.root), "", "", cookie)
	if code != http.StatusOK || !strings.Contains(body, strings.Repeat("y", 65536)) {
		t.Fatalf("exact detail bound=%d bytes=%d", code, len(body))
	}
	if err := f.store.CreateMemory(ctx, storage.Memory{ID: "oversize", Workspace: f.root, Content: strings.Repeat("z", 65537)}); err != nil {
		t.Fatal(err)
	}
	code, _, _ = f.call(t, "GET", "/api/memories/oversize?workspace="+url.QueryEscape(f.root), "", "", cookie)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize detail=%d", code)
	}
	if err := f.store.CreateMemory(ctx, storage.Memory{ID: "legacy-metadata", Workspace: f.root, Content: "old", Source: strings.Repeat("s", 129)}); err != nil {
		t.Fatal(err)
	}
	code, _, _ = f.call(t, "GET", path, "", "", cookie)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("legacy metadata=%d", code)
	}
	if err := f.store.DeleteMemory(ctx, f.root, "legacy-metadata"); err != nil {
		t.Fatal(err)
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
	code, defaultBody, _ := f.call(t, "GET", "/api/memories?workspace="+url.QueryEscape(f.root), "", "", cookie)
	var defaultPage storage.MemoryPage
	if code != http.StatusOK || json.Unmarshal([]byte(defaultBody), &defaultPage) != nil || len(defaultPage.Items) != 20 {
		t.Fatalf("default page=%d items=%d", code, len(defaultPage.Items))
	}
	code, _, _ = f.call(t, "GET", path+"&cursor=invalid!", "", "", cookie)
	if code != http.StatusBadRequest {
		t.Fatalf("invalid cursor=%d", code)
	}
	code, body, _ = f.call(t, "GET", path+"&cursor="+url.QueryEscape(page.NextCursor), "", "", cookie)
	if code != http.StatusOK {
		t.Fatalf("second page=%d", code)
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil || page.Items[0].ID != "m00050" {
		t.Fatalf("second page=%+v err=%v", page, err)
	}
}

func TestWEBMEMSubmitExplicitAndBounded(t *testing.T) {
	f := startWeb(t)
	ctx := context.Background()
	for _, m := range []storage.Memory{
		{ID: "selected", Workspace: f.root, Content: "selected snapshot"},
		{ID: "foreign", Workspace: f.outside, Content: "foreign secret"},
		{ID: "recall", Workspace: f.root, Content: "稽核查詢專案規則"},
	} {
		if err := f.store.CreateMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	cookie := f.login(t)
	body := `{"id":"webmem-explicit","workspace":"` + f.root + `","prompt":"稽核查詢","memory_ids":["selected"]}`
	code, response, _ := f.call(t, "POST", "/api/tasks", body, f.server.URL, cookie)
	if code != http.StatusAccepted {
		t.Fatalf("explicit submit=%d %s", code, response)
	}
	var task storage.Task
	if err := json.Unmarshal([]byte(response), &task); err != nil {
		t.Fatal(err)
	}
	if len(task.MemoryRefs) != 1 || task.MemoryRefs[0] != "selected" || !strings.Contains(task.ResolvedContext, "selected snapshot") {
		t.Fatalf("explicit context=%+v", task)
	}
	deadline := time.Now().Add(2 * time.Second)
	for f.starts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	starts := f.starts.Load()
	for _, id := range []string{"foreign", "missing"} {
		body = `{"id":"webmem-invalid-` + id + `","workspace":"` + f.root + `","prompt":"p","memory_ids":["` + id + `"]}`
		code, _, _ = f.call(t, "POST", "/api/tasks", body, f.server.URL, cookie)
		if code != http.StatusNotFound {
			t.Fatalf("invalid ID %s=%d", id, code)
		}
		f.noTask(t, "webmem-invalid-"+id)
	}
	if err := f.store.DeleteMemory(ctx, f.root, "selected"); err != nil {
		t.Fatal(err)
	}
	body = `{"id":"webmem-deleted","workspace":"` + f.root + `","prompt":"p","memory_ids":["selected"]}`
	code, _, _ = f.call(t, "POST", "/api/tasks", body, f.server.URL, cookie)
	if code != http.StatusNotFound {
		t.Fatalf("deleted ID=%d", code)
	}
	ids := make([]string, 33)
	for i := range ids {
		ids[i] = "recall"
	}
	encodedIDs, _ := json.Marshal(ids)
	body = `{"id":"webmem-too-many","workspace":"` + f.root + `","prompt":"p","memory_ids":` + string(encodedIDs) + `}`
	code, _, _ = f.call(t, "POST", "/api/tasks", body, f.server.URL, cookie)
	if code != http.StatusBadRequest {
		t.Fatalf("too many IDs=%d", code)
	}
	if f.starts.Load() != starts {
		t.Fatalf("invalid ID reached backend: %d -> %d", starts, f.starts.Load())
	}
	body = `{"id":"webmem-off","workspace":"` + f.root + `","prompt":"稽核查詢"}`
	code, response, _ = f.call(t, "POST", "/api/tasks", body, f.server.URL, cookie)
	if code != http.StatusAccepted {
		t.Fatalf("off=%d %s", code, response)
	}
	task = storage.Task{}
	if err := json.Unmarshal([]byte(response), &task); err != nil || len(task.MemoryRefs) != 0 {
		t.Fatalf("off refs=%+v err=%v", task, err)
	}
	body = `{"id":"webmem-bounded","workspace":"` + f.root + `","prompt":"稽核查詢","memory_recall":"bounded"}`
	code, response, _ = f.call(t, "POST", "/api/tasks", body, f.server.URL, cookie)
	if code != http.StatusAccepted {
		t.Fatalf("bounded=%d %s", code, response)
	}
	task = storage.Task{}
	if err := json.Unmarshal([]byte(response), &task); err != nil || !strings.Contains(task.ResolvedContext, "bounded") {
		t.Fatalf("bounded context=%+v err=%v", task, err)
	}
	// 2026-10-04：RTSEL-011 核准 Web additive backend；已知 ID 不再屬注入欄位。
	body = `{"id":"webmem-explicit-backend","workspace":"` + f.root + `","prompt":"p","backend":"codex"}`
	code, response, _ = f.call(t, "POST", "/api/tasks", body, f.server.URL, cookie)
	if code != http.StatusAccepted {
		t.Fatalf("explicit backend=%d %s", code, response)
	}
	task = storage.Task{}
	if err := json.Unmarshal([]byte(response), &task); err != nil || task.Backend != "codex" {
		t.Fatalf("explicit binding=%+v %v", task, err)
	}
	for _, extra := range []string{`"resolved_context":{}`, `"backend":"unknown"`, `"mode":"three-face"`} {
		body = `{"id":"injected","workspace":"` + f.root + `","prompt":"p",` + extra + `}`
		code, _, _ = f.call(t, "POST", "/api/tasks", body, f.server.URL, cookie)
		if code != http.StatusBadRequest {
			t.Fatalf("injected %s=%d", extra, code)
		}
	}
}

func TestWEBMEMBoundedOnly(t *testing.T) {
	f := startWeb(t)
	cookie := f.login(t)
	if err := f.store.CreateMemory(context.Background(), storage.Memory{ID: "candidate", Workspace: f.root, Content: "專案稽核規則"}); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"bounded-only","workspace":"` + f.root + `","prompt":"稽核規則","memory_recall":"bounded"}`
	code, response, _ := f.call(t, "POST", "/api/tasks", body, f.server.URL, cookie)
	if code != http.StatusAccepted {
		t.Fatalf("bounded=%d %s", code, response)
	}
	var task storage.Task
	if err := json.Unmarshal([]byte(response), &task); err != nil {
		t.Fatal(err)
	}
	var context struct {
		Recall *struct {
			Mode          string   `json:"mode"`
			PolicyVersion string   `json:"policy_version"`
			AutoIDs       []string `json:"auto_ids"`
		} `json:"recall"`
	}
	if err := json.Unmarshal([]byte(task.ResolvedContext), &context); err != nil || context.Recall == nil || context.Recall.Mode != "bounded" || context.Recall.PolicyVersion != "lexical-v1" {
		t.Fatalf("bounded recall=%+v err=%v", context, err)
	}
}

func TestWEBMEMRedelivery(t *testing.T) {
	f := startWeb(t)
	cookie := f.login(t)
	ctx := context.Background()
	for _, id := range []string{"one", "two"} {
		if err := f.store.CreateMemory(ctx, storage.Memory{ID: id, Workspace: f.root, Content: "snapshot " + id}); err != nil {
			t.Fatal(err)
		}
	}
	first := `{"id":"lost-response","workspace":"` + f.root + `","prompt":"do task","memory_ids":["one","two"]}`
	code, body, _ := f.call(t, "POST", "/api/tasks", first, f.server.URL, cookie)
	if code != http.StatusAccepted {
		t.Fatalf("first=%d %s", code, body)
	}
	var original storage.Task
	if err := json.Unmarshal([]byte(body), &original); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for f.starts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.starts.Load() != 1 {
		t.Fatalf("first backend starts=%d", f.starts.Load())
	}
	if err := f.store.DeleteMemory(ctx, f.root, "one"); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{first, `{"id":"lost-response","workspace":"` + f.root + `","prompt":"do task","memory_ids":["two","one"]}`} {
		code, body, _ = f.call(t, "POST", "/api/tasks", payload, f.server.URL, cookie)
		if code != http.StatusAccepted {
			t.Fatalf("redelivery=%d %s", code, body)
		}
		var got storage.Task
		if err := json.Unmarshal([]byte(body), &got); err != nil || got.ID != original.ID || !strings.Contains(got.ResolvedContext, "snapshot one") {
			t.Fatalf("redelivery task=%+v err=%v", got, err)
		}
	}
	changed := `{"id":"lost-response","workspace":"` + f.root + `","prompt":"do task","memory_ids":["one","two"],"memory_recall":"bounded"}`
	code, _, _ = f.call(t, "POST", "/api/tasks", changed, f.server.URL, cookie)
	if code != http.StatusConflict {
		t.Fatalf("same ID different mode=%d", code)
	}
	if f.starts.Load() != 1 {
		t.Fatalf("redelivery backend starts=%d", f.starts.Load())
	}
}
