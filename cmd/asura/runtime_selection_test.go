package main

import (
	"asura/internal/backend/opencode"
	"asura/internal/engine"
	"asura/internal/storage"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 2026-10-03：RTSEL-001/011/015，真 CLI/adapter/SQLite，prompt 不執行 shell。
func TestRuntimeSelectionLocalAuto(t *testing.T) {
	t.Setenv("ASURA_SOCKET", "")
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.Mkdir(bin, 0700)
	for _, id := range []string{"opencode"} {
		os.WriteFile(filepath.Join(bin, id), []byte("#!/bin/sh\necho 1.18.23\n"), 0700)
	}
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then echo 'codex-cli 0.155.0'; exit 0; fi
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"auto-runtime-proof"}}'
`
	os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0700)
	t.Setenv("PATH", bin)
	t.Setenv("ASURA_RUNTIME_PRIORITY", "codex,opencode,antigravity,omp")
	db := filepath.Join(root, "tasks.db")
	var out, stderr bytes.Buffer
	code := handleRun([]string{"--db", db, "--workspace", root, "--id", "auto", "--backend", "auto", "touch shell-side-effect"}, &out, &stderr)
	if code != 0 {
		t.Fatalf("code=%d: %s", code, stderr.String())
	}
	store, err := storage.NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	task, err := store.GetTask(context.Background(), "auto")
	if err != nil {
		t.Fatal(err)
	}
	if task.Backend != "codex" || task.Result != "auto-runtime-proof" {
		t.Fatalf("task=%+v", task)
	}
	if _, err = os.Stat(filepath.Join(root, "shell-side-effect")); !os.IsNotExist(err) {
		t.Fatal("prompt executed as shell")
	}
	var sealed engine.ExecutionContext
	json.Unmarshal([]byte(task.ResolvedContext), &sealed)
	if sealed.ExecutionContract == nil || sealed.ExecutionContract.RequestedBackend != "auto" {
		t.Fatal("selection not sealed")
	}
	out.Reset()
	if code = handleStatus([]string{"--db", db, "auto"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "Selection policy: deterministic-priority v1") {
		t.Fatalf("audit=%s %s", out.String(), stderr.String())
	}
}
func TestRuntimeSelectionCLIBoundaries(t *testing.T) {
	var out, stderr bytes.Buffer
	handleAsk([]string{"--backend", "auto", "--socket", filepath.Join(t.TempDir(), "missing.sock"), "probe"}, &out, &stderr)
	if strings.Contains(stderr.String(), "unknown backend") {
		t.Fatal(stderr.String())
	}
	stderr.Reset()
	t.Setenv("ASURA_RUNTIME_PRIORITY", "codex,codex")
	if handleDaemon([]string{"--db", filepath.Join(t.TempDir(), "db")}, &out, &stderr) == 0 || !strings.Contains(stderr.String(), "runtime priority") {
		t.Fatalf("invalid setting: %s", stderr.String())
	}
}

// 2026-10-03：RTSEL-011/012，新 local auto 路徑沿既有跨進程取消契約。
func TestRuntimeSelectionLocalCancel(t *testing.T) {
	t.Setenv("ASURA_SOCKET", "")
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.Mkdir(bin, 0700)
	os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\necho 1.18.23\n"), 0700)
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'codex-cli 0.155.0'; exit 0; fi\nwhile true; do /bin/sleep 1; done\n"), 0700)
	t.Setenv("PATH", bin)
	t.Setenv("ASURA_RUNTIME_PRIORITY", "codex")
	db := filepath.Join(root, "tasks.db")
	finished := make(chan int, 1)
	go func() {
		var out, stderr bytes.Buffer
		finished <- handleRun([]string{"--db", db, "--workspace", root, "--id", "cancel-auto", "--backend", "auto", "probe"}, &out, &stderr)
	}()
	var store *storage.SQLiteStore
	var task *storage.Task
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if store == nil {
			store, _ = storage.NewSQLiteStore(db)
		}
		if store != nil {
			task, _ = store.GetTask(context.Background(), "cancel-auto")
			if task != nil && task.PGID > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if store == nil || task == nil || task.PGID <= 0 {
		t.Fatal("runtime did not start")
	}
	defer store.Close()
	defer opencode.KillProcessGroup(task.PGID)
	var out, stderr bytes.Buffer
	if code := handleCancel([]string{"--db", db, "cancel-auto"}, &out, &stderr); code != 0 {
		t.Fatalf("cancel: %s", stderr.String())
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("auto runtime survived cancel")
	}
	saved, _ := store.GetTask(context.Background(), "cancel-auto")
	if saved.Status != storage.StatusCancelled {
		t.Fatalf("status=%s", saved.Status)
	}
}
