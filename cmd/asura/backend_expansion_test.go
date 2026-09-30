package main

import (
	"asura/internal/storage"
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// 2026-09-30：BADAPT-003／005，指定新 ID 應通過 CLI 身份檢查，再檢查 socket。
func TestAskAcceptsExpandedBackendIDs(t *testing.T) {
	t.Setenv("ASURA_SOCKET", "")
	for _, id := range []string{"omp", "antigravity"} {
		var out, err bytes.Buffer
		handleAsk([]string{"--backend", id, "--socket", filepath.Join(t.TempDir(), "missing.sock"), "probe"}, &out, &err)
		if strings.Contains(err.String(), "unknown backend") {
			t.Fatalf("%s rejected: %s", id, err.String())
		}
		if !strings.Contains(err.String(), "connect") {
			t.Fatalf("missing socket preflight: %s", err.String())
		}
	}
}

// 2026-09-30：BADAPT-008，status 採保存的 backend 與 trace version，不猜目前安裝版本。
func TestExpandedBackendStatus(t *testing.T) {
	for _, id := range []string{"omp", "antigravity"} {
		t.Run(id, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "status.db")
			store, e := storage.NewSQLiteStore(path)
			if e != nil {
				t.Fatal(e)
			}
			defer store.Close()
			task := &storage.Task{ID: "status-backend", Backend: id, Status: storage.StatusCompleted, PipelineTrace: `{"mode":"direct","backend_id":"` + id + `","stages":[{"name":"striker","status":"completed","backend_id":"` + id + `","backend_version":"fixed-version"}]}`}
			if e = store.CreateTask(context.Background(), task); e != nil {
				t.Fatal(e)
			}
			var out, err bytes.Buffer
			if code := handleStatus([]string{"--db", path, task.ID}, &out, &err); code != 0 {
				t.Fatalf("status %s", err.String())
			}
			if !strings.Contains(out.String(), "Backend:   "+id) || !strings.Contains(out.String(), "Backend version (striker): fixed-version") {
				t.Fatalf("output %s", out.String())
			}
		})
	}
}
