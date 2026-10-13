package main

import (
	"asura/internal/backend/opencode"
	"asura/internal/storage"
	"context"
	"fmt"
	"os"
	"syscall"
)

// 2026-10-04：登記 local runtime 進程身分與外部取消意圖。
func recordRuntimeProcessStart(c context.Context, store *storage.SQLiteStore, dbPath, taskID string, pgid int, startLock *os.File) {
	lf := startLock
	if lf == nil {
		var e error
		lf, e = os.OpenFile(taskLockPath(dbPath, taskID), os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			_ = opencode.KillProcessGroup(pgid)
			return
		}
		defer lf.Close()
		if e = syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); e != nil {
			_ = opencode.KillProcessGroup(pgid)
			return
		}
		defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	}
	st, e := opencode.GetProcessStartTime(pgid)
	runnerST, runnerErr := opencode.GetProcessStartTime(os.Getpid())
	if e != nil || runnerErr != nil {
		_ = opencode.KillProcessGroup(pgid)
		return
	}
	_ = lf.Truncate(0)
	_, e = fmt.Fprintf(lf, "%d:%s:%d:%s", pgid, st, os.Getpid(), runnerST)
	if e != nil {
		_ = opencode.KillProcessGroup(pgid)
		return
	}
	_ = store.UpdateTaskPGID(context.Background(), taskID, pgid)
	task, _ := store.GetTask(context.Background(), taskID)
	_, intentErr := os.Stat(taskCancelIntentPath(dbPath, taskID))
	// 2026-10-13：issue-48，外部取消意圖先於狀態寫入；比照 shell runner 先標記 cancelling，避免回收後被記為 failed。
	if intentErr == nil && task != nil && task.Status == storage.StatusRunning {
		_, _ = store.UpdateTaskStatusIfRunning(context.Background(), taskID, storage.StatusCancelling, task.Result, "task cancellation requested")
	}
	if c.Err() != nil || intentErr == nil || task != nil && (task.Status == storage.StatusCancelled || task.Status == storage.StatusCancelling) {
		_ = opencode.KillProcessGroup(pgid)
	}
}
