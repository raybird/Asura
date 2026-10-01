package main

import (
	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/storage"
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryUtilityCLI(t *testing.T) {
	ws := t.TempDir()
	s, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	d := daemon.NewDaemon(daemon.Config{SocketPath: sock}, s, auth.NewPolicy([]string{ws}))
	if err = d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = s.CreateMemory(context.Background(), storage.Memory{ID: "m", Workspace: ws, Content: "sqlite"}); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := handleMemory([]string{"utility", "m", "--workspace", ws, "--socket", sock, "--limit", "1"}, &out, &stderr); code != 0 {
		t.Fatalf("CLI %d %s", code, stderr.String())
	}
	var page storage.UtilityPage
	if err = json.Unmarshal(out.Bytes(), &page); err != nil || page.WeightPPM != 1000000 || page.Aggregate.Observed != 0 {
		t.Fatalf("neutral %s %v", out.String(), err)
	}
	for _, args := range [][]string{
		{"utility", "m", "--workspace", ws, "--socket", sock, "--limit", "101"},
		{"utility", "foreign", "--workspace", ws, "--socket", sock},
		{"utility", "m", "--workspace", ws, "--socket", sock, "--cursor", strings.Repeat("x", 513)},
		{"utility", "m", "--workspace", ws, "--socket", sock + "missing"},
	} {
		out.Reset()
		stderr.Reset()
		if handleMemory(args, &out, &stderr) == 0 || out.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("CLI false success %v %s %s", args, out.String(), stderr.String())
		}
	}
}
