package main

import (
	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/storage"
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestLifecycleCLI(t *testing.T) {
	ws := t.TempDir()
	s, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateMemory(context.Background(), storage.Memory{ID: "a", Workspace: ws, Content: "old"}); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "d.sock")
	d := daemon.NewDaemon(daemon.Config{SocketPath: sock}, s, auth.NewPolicy([]string{ws}))
	if err = d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	run := func(args ...string) string {
		t.Helper()
		var out, stderr bytes.Buffer
		code := handleMemory(append(args, "--workspace", ws, "--socket", sock), &out, &stderr)
		if code != 0 {
			t.Fatalf("CLI %v exit=%d: %s", args, code, stderr.String())
		}
		return out.String()
	}
	run("add", "--content", "new", "--supersedes", "a", "--operation-id", "cli-op", "--durable")
	out := run("detail", "a")
	var detail storage.MemoryDetail
	if err = json.Unmarshal([]byte(out), &detail); err != nil || detail.Successor == "" || detail.Memory.Lifecycle != "superseded" {
		t.Fatalf("L7 detail %s %v", out, err)
	}
	run("durable", detail.Successor)
	run("detail", detail.Successor)
	var stdout, stderr bytes.Buffer
	if handleMemory([]string{"add", "--workspace", ws, "--socket", sock, "--content", "bad", "--supersedes", detail.Successor}, &stdout, &stderr) == 0 {
		t.Fatal("CLI allowed supersedes without operation ID")
	}
}
