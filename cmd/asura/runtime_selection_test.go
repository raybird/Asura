package main

import (
	"asura/internal/backend/opencode"
	"asura/internal/engine"
	"asura/internal/storage"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
	var runOut, runErr safeBuffer
	go func() {
		finished <- handleRun([]string{"--db", db, "--workspace", root, "--id", "cancel-auto", "--backend", "auto", "probe"}, &runOut, &runErr)
	}()
	// 2026-10-13：issue-48，失敗時保存 runner 端任務終態與輸出，供 CI 偶發失敗定位。
	defer func() {
		if !t.Failed() {
			return
		}
		if s, err := storage.NewSQLiteStore(db); err == nil {
			if tk, err := s.GetTask(context.Background(), "cancel-auto"); err == nil {
				t.Logf("runner task status=%s pgid=%d err=%q result=%q", tk.Status, tk.PGID, tk.ErrorMessage, tk.Result)
			}
			s.Close()
		}
		t.Logf("runner stdout=%q stderr=%q", runOut.String(), runErr.String())
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

// 2026-10-28：issue-74 SCN-002，外部 asura cancel 也要回收 runtime 另開 session 的子孫（模擬模型啟動的命令）。
func TestRuntimeSelectionLocalCancelReapsDetachedCommand(t *testing.T) {
	t.Setenv("ASURA_SOCKET", "")
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.Mkdir(bin, 0700)
	marker := strconv.Itoa((40000+os.Getpid()%9000)*10 + 7)
	os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\necho 1.18.23\n"), 0700)
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'codex-cli 0.155.0'; exit 0; fi\n/usr/bin/setsid /bin/sleep "+marker+" &\nwhile true; do /bin/sleep 1; done\n"), 0700)
	t.Setenv("PATH", bin)
	t.Setenv("ASURA_RUNTIME_PRIORITY", "codex")
	sleepPIDs := func() []int {
		var pids []int
		entries, _ := os.ReadDir("/proc")
		for _, e := range entries {
			data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
			if err == nil && string(data) == "/bin/sleep\x00"+marker+"\x00" {
				pid, _ := strconv.Atoi(e.Name())
				pids = append(pids, pid)
			}
		}
		return pids
	}
	t.Cleanup(func() {
		for _, pid := range sleepPIDs() {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	db := filepath.Join(root, "tasks.db")
	finished := make(chan int, 1)
	var runOut, runErr safeBuffer
	go func() {
		finished <- handleRun([]string{"--db", db, "--workspace", root, "--id", "cancel-detached", "--backend", "auto", "probe"}, &runOut, &runErr)
	}()
	var store *storage.SQLiteStore
	var task *storage.Task
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if store == nil {
			store, _ = storage.NewSQLiteStore(db)
		}
		if store != nil {
			task, _ = store.GetTask(context.Background(), "cancel-detached")
			if task != nil && task.PGID > 0 && len(sleepPIDs()) == 1 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if store == nil || task == nil || task.PGID <= 0 || len(sleepPIDs()) != 1 {
		t.Fatalf("runtime did not start the detached command: task=%+v sleeps=%v", task, sleepPIDs())
	}
	defer store.Close()
	defer opencode.KillProcessGroup(task.PGID)
	var out, stderr bytes.Buffer
	if code := handleCancel([]string{"--db", db, "cancel-detached"}, &out, &stderr); code != 0 {
		t.Fatalf("cancel: %s", stderr.String())
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime survived cancel")
	}
	deadline = time.Now().Add(5 * time.Second)
	for len(sleepPIDs()) != 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pids := sleepPIDs(); len(pids) != 0 {
		t.Errorf("detached command survived asura cancel: %v", pids)
	}
	saved, _ := store.GetTask(context.Background(), "cancel-detached")
	if saved.Status != storage.StatusCancelled {
		t.Fatalf("status=%s", saved.Status)
	}
}

// 2026-10-04：取消發生在 runtime version 探測中，成功取消後不得啟動 prompt 程式。
func TestRuntimeSelectionCancelDuringVersionProbe(t *testing.T) {
	t.Setenv("ASURA_SOCKET", "")
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.Mkdir(bin, 0700)
	os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\necho 1.18.23\n"), 0700)
	marker := filepath.Join(root, "version-blocked")
	release := filepath.Join(root, "release-version")
	side := filepath.Join(root, "execution-side-effect")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then
 if [ ! -f '%s/admission-seen' ]; then /usr/bin/touch '%s/admission-seen'; else
  if [ ! -f '%s' ]; then /usr/bin/touch '%s'; while [ ! -f '%s' ]; do /bin/sleep 0.01; done; fi
 fi
 echo 'codex-cli 0.155.0'; exit 0
fi
/usr/bin/touch '%s'
printf '%%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"done"}}'
`, root, root, marker, marker, release, side)
	os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0700)
	t.Setenv("PATH", bin)
	t.Setenv("ASURA_RUNTIME_PRIORITY", "codex")
	db := filepath.Join(root, "tasks.db")
	done := make(chan int, 1)
	go func() {
		var out, stderr bytes.Buffer
		done <- handleRun([]string{"--db", db, "--workspace", root, "--id", "probe-cancel", "--backend", "auto", "probe"}, &out, &stderr)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("version probe not blocked")
	}
	var out, stderr bytes.Buffer
	if code := handleCancel([]string{"--db", db, "probe-cancel"}, &out, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	lf, err := os.OpenFile(taskLockPath(db, "probe-cancel"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	syscall.Flock(int(lf.Fd()), syscall.LOCK_EX)
	os.WriteFile(release, []byte("release"), 0600)
	time.Sleep(100 * time.Millisecond)
	_, sideErr := os.Stat(side)
	syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	lf.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled admission did not finish")
	}
	if !os.IsNotExist(sideErr) {
		t.Fatal("runtime executed prompt after successful cancellation")
	}
}

// 2026-10-04：clisandbox 的 BeforeStart→OnProcessStarted→release 順序必須共用同一 flock。
func TestRuntimeSelectionHeldStartLock(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "tasks.db")
	store, err := storage.NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.CreateTask(context.Background(), &storage.Task{ID: "held", Status: storage.StatusRunning})
	child := exec.Command("/bin/sleep", "10")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { opencode.KillProcessGroup(child.Process.Pid); child.Wait() }()
	lf, err := os.OpenFile(taskLockPath(db, "held"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	syscall.Flock(int(lf.Fd()), syscall.LOCK_EX)
	done := make(chan struct{})
	go func() {
		recordRuntimeProcessStart(context.Background(), store, db, "held", child.Process.Pid, lf)
		close(done)
	}()
	completed := false
	select {
	case <-done:
		completed = true
	case <-time.After(200 * time.Millisecond):
	}
	syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	<-done
	if !completed {
		t.Fatal("startup callback reacquired held flock and deadlocked")
	}
	metadata := readTaskLockMetadata(taskLockPath(db, "held"))
	if metadata.PGID != child.Process.Pid || metadata.StartTime == "" || metadata.RunnerStartTime == "" {
		t.Fatalf("metadata=%+v", metadata)
	}
}

// 2026-10-13：issue-48，PGID 登記後才看到外部取消意圖時，runner 先標記 cancelling 再回收，任務不得被記為 failed。
func TestRuntimeSelectionStartSeesCancelIntent(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "tasks.db")
	store, err := storage.NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.CreateTask(context.Background(), &storage.Task{ID: "intent", Status: storage.StatusRunning})
	child := exec.Command("/bin/sleep", "10")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer opencode.KillProcessGroup(child.Process.Pid)
	if err = os.WriteFile(taskCancelIntentPath(db, "intent"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	recordRuntimeProcessStart(context.Background(), store, db, "intent", child.Process.Pid, nil)
	if err = child.Wait(); err == nil || !strings.Contains(err.Error(), "killed") {
		t.Fatalf("runtime not reclaimed: %v", err)
	}
	task, err := store.GetTask(context.Background(), "intent")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != storage.StatusCancelling {
		t.Fatalf("status=%s, want %s", task.Status, storage.StatusCancelling)
	}
}
