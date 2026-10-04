package main

import (
	"asura/internal/storage"
	"bytes"
	"context"
	"encoding/json"
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
