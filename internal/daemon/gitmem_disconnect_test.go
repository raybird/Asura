package daemon_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/storage"
)

// 2026-09-30 WEBGIT-002/011：真實 Git process 與 Unix 斷線，先查核既有取消契約。
func TestWEBGITIPCDisconnectProbe(t *testing.T) {
	root := t.TempDir()
	repo := initTestGitRepoInDaemon(t, root)
	store, err := storage.NewSQLiteStore(filepath.Join(root, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sock := filepath.Join(root, "d.sock")
	d := daemon.NewDaemon(daemon.Config{SocketPath: sock, MaxWorkers: 1}, store, auth.NewPolicy([]string{repo}))
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "pid")
	script := fmt.Sprintf("#!/bin/sh\necho $$ > '%s'\nexec /bin/sleep 60\n", marker)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	call := func(ws string) net.Conn {
		c, e := net.Dial("unix", sock)
		if e != nil {
			t.Fatal(e)
		}
		if e = json.NewEncoder(c).Encode(daemon.IPCRequest{Action: "git_memory_capture", Workspace: ws, Range: "HEAD~1..HEAD"}); e != nil {
			t.Fatal(e)
		}
		return c
	}
	denied := call(root)
	var response daemon.IPCResponse
	if err := json.NewDecoder(denied).Decode(&response); err != nil {
		t.Fatal(err)
	}
	denied.Close()
	if !strings.Contains(response.Error, "unauthorized workspace") {
		t.Fatalf("denied: %+v", response)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unauthorized capture spawned Git")
	}
	c := call(repo)
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
		c.Close()
		t.Fatal("successful authorization did not start subprocess")
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("positive control process missing: %v", err)
	}
	c.Close()
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) != syscall.ESRCH {
		t.Fatalf("capture subprocess %d survived disconnect", pid)
	}
	cands, e := store.ListCandidates(context.Background(), repo, "", 50)
	if e != nil || len(cands) != 0 {
		t.Fatalf("cancel wrote candidates: %v %v", cands, e)
	}
	mems, e := store.ListMemories(context.Background(), repo)
	if e != nil || len(mems) != 0 {
		t.Fatalf("cancel wrote memories: %v %v", mems, e)
	}
	// 2026-09-30：換回真實 Git，確認斷線不使後續正常擷取失效。
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec '"+realGit+"' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c = call(repo)
	response = daemon.IPCResponse{}
	defer c.Close()
	if err := json.NewDecoder(c).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "" || response.Candidate == nil {
		t.Fatalf("positive capture: %+v", response)
	}
}
