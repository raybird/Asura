package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

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

	targetPGID := task.PGID
	if targetPGID == 0 {
		// 若任務處於啟動過渡期尚未寫入 PGID，短暫等待其完成登記以確保強殺回收（修復 R-03）
		for i := 0; i < 15; i++ {
			time.Sleep(10 * time.Millisecond)
			if t, err := store.GetTask(context.Background(), taskID); err == nil && t.PGID > 0 {
				targetPGID = t.PGID
				break
			}
		}
	}

	// 若任務具有運行的進程組 (PGID)，發送 SIGKILL 乾淨回收整個進程樹（D-05 規範）
	if targetPGID > 0 {
		_ = opencode.KillProcessGroup(targetPGID)
	}

	fmt.Fprintf(stdout, "Task %s cancelled successfully\n", taskID)
	return 0
}
