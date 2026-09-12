package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"syscall"

	"asura/internal/auth"
	"asura/internal/backend/opencode"
	"asura/internal/engine"
	"asura/internal/storage"
)

func handleCancel(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cancel", flag.ContinueOnError)
	fs.SetOutput(stderr)

	dbPath := fs.String("db", "asura.db", "path to SQLite database")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(stderr, "error: task_id is required")
		return 1
	}
	taskID := remaining[0]

	store, err := storage.NewSQLiteStore(*dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: failed to open storage: %v\n", err)
		return 1
	}
	defer store.Close()

	// 檢查任務是否存在
	task, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	policy := auth.NewPolicy([]string{task.Workspace})
	eng := engine.NewEngine(store, policy)

	if err := eng.CancelTask(taskID); err != nil {
		fmt.Fprintf(stderr, "error: failed to cancel task: %v\n", err)
		return 1
	}

	// 進行跨行程排他握手：若執行端正處於啟動區間 (cmd.Start)，等候其完成 PGID 登記 (修復 G-01, H-01)
	lockPath := taskLockPath(*dbPath, taskID)
	lf, lErr := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if lErr != nil {
		fmt.Fprintf(stderr, "error: failed to open task lock: %v\n", lErr)
		return 1
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		_ = lf.Close()
		fmt.Fprintf(stderr, "error: failed to acquire task lock: %v\n", err)
		return 1
	}
	_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	_ = lf.Close()

	// 握手完成：此時執行端要麼在鎖內檢查到 cancelled 而放棄啟動，要麼已經完成 cmd.Start() 並登記 PGID
	targetPGID := task.PGID
	if targetPGID == 0 {
		if t, err := store.GetTask(context.Background(), taskID); err == nil && t.PGID > 0 {
			targetPGID = t.PGID
		}
	}

	// 若任務具有運行的進程組 (PGID)，發送 SIGKILL 乾淨回收整個進程樹（D-05 規範）
	if targetPGID > 0 {
		_ = opencode.KillProcessGroup(targetPGID)
	}

	fmt.Fprintf(stdout, "Task %s cancelled successfully\n", taskID)
	return 0
}
