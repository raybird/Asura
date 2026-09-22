package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"asura/internal/daemon"
	"asura/internal/storage"
)

func TestAskRecall(t *testing.T) {
	for _, mode := range []string{"off", "bounded"} {
		t.Run(mode, func(t *testing.T) {
			sock := filepath.Join(t.TempDir(), "cli.sock")
			l, err := net.Listen("unix", sock)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			received := make(chan map[string]any, 1)
			go func() {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				var req map[string]any
				json.NewDecoder(conn).Decode(&req)
				received <- req
				json.NewEncoder(conn).Encode(daemon.IPCResponse{Task: &storage.Task{ID: "task"}})
			}()
			var out, stderr bytes.Buffer
			code := handleAsk([]string{"--socket", sock, "--memory-recall", mode, "--memory-id", "explicit", "SQLite"}, &out, &stderr)
			if code != 0 {
				t.Fatal(stderr.String())
			}
			req := <-received
			if mode == "off" {
				if _, ok := req["memory_recall"]; ok {
					t.Fatal("off changed wire payload")
				}
			} else if req["memory_recall"] != "bounded" {
				t.Fatal("mode not forwarded")
			}
		})
	}
	var out, stderr bytes.Buffer
	if handleAsk([]string{"--memory-recall", "unknown", "SQLite"}, &out, &stderr) == 0 || !strings.Contains(stderr.String(), "memory-recall") {
		t.Fatal("unknown mode not rejected")
	}
}

func TestStatusRecallAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task.db")
	s, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	task := &storage.Task{ID: "audit", Workspace: "/a", Prompt: "SQLite", Status: storage.StatusCompleted, ResolvedContext: `{"recall":{"mode":"bounded","policy_version":"lexical-v1","explicit_ids":["manual"],"auto_ids":["auto"],"selected":[{"id":"auto","score":2,"reason":"lexical_overlap"}],"skipped":{"no_match":1},"budget":{"estimated_prompt_tokens":33,"estimation_method":"heuristic-char-ratio-v1"}}}`}
	if err = s.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	s.Close()
	var out, stderr bytes.Buffer
	if handleStatus([]string{"--db", path, "audit"}, &out, &stderr) != 0 {
		t.Fatal(stderr.String())
	}
	for _, want := range []string{"Recall:", "lexical-v1", `"auto_ids":["auto"]`, `"no_match":1`, `"estimated_prompt_tokens":33`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("audit missing %s: %s", want, out.String())
		}
	}
}
