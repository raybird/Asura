package integration

import (
	"asura/internal/gitmem"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// 2026-09-30 WEBGIT-011：真實 HTTP 取消/10s timeout → Unix → Git process group。
func TestWEBGITHTTPCancellation(t *testing.T) {
	f := startWeb(t)
	repo := webGitRepo(t, f)
	cookie := f.login(t)
	info, err := gitmem.NewGitExecutor([]string{repo}).ValidateAndResolveRepo(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(f.root, "bin")
	os.Mkdir(bin, 0700)
	marker := filepath.Join(f.root, "pid")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(fmt.Sprintf("#!/bin/sh\necho $$ > '%s'\nexec /bin/sleep 60\n", marker)), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	// 2026-09-30 WEBGIT-001：拒絕先於 Git 及所有資料寫入，授權成功組在下方。
	routes := []struct{ method, path, body string }{{"POST", "/api/git-memory/capture", `{"workspace":"` + repo + `","range":"HEAD~1..HEAD"}`}, {"GET", "/api/git-memory/candidates?workspace=" + url.QueryEscape(repo), ""}, {"GET", "/api/git-memory/candidates/known?workspace=" + url.QueryEscape(repo), ""}, {"POST", "/api/git-memory/candidates/known/approve", `{"workspace":"` + repo + `"}`}, {"POST", "/api/git-memory/candidates/known/discard", `{"workspace":"` + repo + `"}`}}
	for _, x := range routes {
		code, _, _ := f.call(t, x.method, x.path, x.body, f.server.URL, nil)
		if code != 401 {
			t.Fatalf("unauth %d", code)
		}
		if x.method == "POST" {
			code, _, _ = f.call(t, x.method, x.path, x.body, "https://evil.example", cookie)
			if code != 403 {
				t.Fatalf("origin %d", code)
			}
		}
		req, _ := http.NewRequest(x.method, f.server.URL+x.path, strings.NewReader(x.body))
		req.Host = "evil.example"
		req.Header.Set("Origin", f.server.URL)
		req.AddCookie(cookie)
		resp, e := f.server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Fatalf("Host %d", resp.StatusCode)
		}
	}
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("rejected HTTP request spawned Git")
	}
	cands, e := f.store.ListCandidates(context.Background(), repo, "", 50)
	if e != nil || len(cands) != 0 {
		t.Fatal("boundary wrote candidate")
	}
	mems, e := f.store.ListMemories(context.Background(), repo)
	if e != nil || len(mems) != 0 {
		t.Fatal("boundary wrote memory")
	}
	for _, mode := range []string{"disconnect", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			os.Remove(marker)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, "POST", f.server.URL+"/api/git-memory/capture", strings.NewReader(`{"workspace":"`+repo+`","range":"HEAD~1..HEAD"}`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Origin", f.server.URL)
			req.AddCookie(cookie)
			done := make(chan int, 1)
			go func() {
				resp, err := f.server.Client().Do(req)
				if err != nil {
					done <- 0
					return
				}
				defer resp.Body.Close()
				done <- resp.StatusCode
			}()
			var pid int
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				data, e := os.ReadFile(marker)
				if e == nil {
					pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
					if pid > 0 {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if pid == 0 {
				t.Fatal("capture process did not start")
			}
			if syscall.Kill(pid, 0) != nil {
				t.Fatal("positive process control absent")
			}
			if mode == "disconnect" {
				cancel()
			}
			select {
			case code := <-done:
				if mode == "timeout" && code != 504 {
					t.Fatalf("timeout status %d", code)
				}
			case <-time.After(12 * time.Second):
				t.Fatal("HTTP capture unbounded")
			}
			deadline = time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if syscall.Kill(pid, 0) == syscall.ESRCH {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if syscall.Kill(pid, 0) != syscall.ESRCH {
				t.Fatal("Git process survived cancel")
			}
			cands, err := f.store.ListCandidates(context.Background(), repo, "", 50)
			if err != nil || len(cands) != 0 {
				t.Fatal("cancel persisted candidate")
			}
			cp, err := f.store.GetCheckpoint(context.Background(), repo, info.RepoID, info.CurrentRef)
			if err != nil {
				t.Fatal(err)
			}
			if cp != nil {
				t.Fatal("cancel advanced checkpoint")
			}
			mems, err := f.store.ListMemories(context.Background(), repo)
			if err != nil || len(mems) != 0 {
				t.Fatal("cancel wrote formal memory")
			}
		})
	}
}
