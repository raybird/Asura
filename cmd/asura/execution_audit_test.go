package main

import (
	"asura/internal/audit"
	"asura/internal/daemon"
	"asura/internal/storage"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

// 2026-10-04：EXAUD-012/013/014，CLI 讀 legacy Task 不虛構歷史、不輸出 result。
func TestExecutionAuditCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateTask(context.Background(), &storage.Task{ID: "legacy", Status: storage.StatusCompleted, Prompt: "secret-sentinel", Result: "secret-sentinel"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	var out, stderr bytes.Buffer
	if code := RunCLI([]string{"audit", "--db", path, "--id", "legacy", "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("audit command missing code=%d %s", code, stderr.String())
	}
	var r map[string]any
	if json.Unmarshal(out.Bytes(), &r) != nil || r["completeness"] != "legacy" || strings.Contains(out.String(), "secret-sentinel") {
		t.Fatalf("unsafe legacy projection %s", out.String())
	}
}

// 2026-10-04：獨立 review R3，ask 必須保留 IPC immediate rejection/digest。
func TestExecutionAuditCLIAdmissionRejection(t *testing.T) {
	root := t.TempDir()
	socket := filepath.Join(root, "reject.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		var req daemon.IPCRequest
		if err = json.NewDecoder(conn).Decode(&req); err != nil {
			done <- err
			return
		}
		rejection := audit.AdmissionRejection(req.RequestID, req.Mode, req.Backend, nil)
		done <- json.NewEncoder(conn).Encode(daemon.IPCResponse{Error: "legacy-safe-error", AdmissionRejection: rejection})
	}()
	var out, stderr bytes.Buffer
	code := handleAsk([]string{"--socket", socket, "--workspace", root, "--id", "request", "--backend", "codex", "private-prompt"}, &out, &stderr)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if code != 1 || !strings.Contains(stderr.String(), audit.Correlation("request")) || !strings.Contains(stderr.String(), "admission_rejected") || strings.Contains(stderr.String(), "private-prompt") {
		t.Fatalf("rejection summary missing: %s", stderr.String())
	}
}

// 2026-10-04：review R3 local run 真 admission 拒絕不建立 Task，摘要不帶 prompt。
func TestExecutionAuditLocalRejection(t *testing.T) {
	t.Setenv("ASURA_SOCKET", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ASURA_RUNTIME_PRIORITY", "")
	root := t.TempDir()
	db := filepath.Join(root, "db")
	var out, stderr bytes.Buffer
	code := handleRun([]string{"--db", db, "--workspace", root, "--id", "local-rejected", "--backend", "omp", "private-prompt"}, &out, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), audit.Correlation("local-rejected")) || !strings.Contains(stderr.String(), "admission_rejected") || strings.Contains(stderr.String(), "private-prompt") {
		t.Fatalf("local rejection summary missing %s", stderr.String())
	}
	store, err := storage.NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.GetTask(context.Background(), "local-rejected"); err == nil {
		t.Fatal("rejected Task persisted")
	}
}
