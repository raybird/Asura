package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"asura/internal/auth"
	"asura/internal/backend/opencode"
	"asura/internal/engine"
	"asura/internal/storage"
)

// readTaskLockMetadata 自鎖檔讀取執行端登記的不可變核心身分資訊
func readTaskLockMetadata(lockPath string) opencode.TaskProcessMetadata {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return opencode.TaskProcessMetadata{}
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return opencode.TaskProcessMetadata{}
	}
	parts := strings.Split(content, ":")
	meta := opencode.TaskProcessMetadata{}
	if len(parts) >= 1 {
		meta.PGID, _ = strconv.Atoi(parts[0])
	}
	if len(parts) >= 2 {
		meta.StartTime = parts[1]
	}
	if len(parts) >= 3 {
		meta.RunnerPID, _ = strconv.Atoi(parts[2])
	}
	if len(parts) >= 4 {
		meta.RunnerStartTime = parts[3]
	}
	return meta
}

// safeKillTaskProcessGroup 進行三態安全回收 (修復 J-01, K-01, L-01):
// - 確認屬於此任務：發送 SIGKILL，等待確認進程組終結，確認後清零 PGID，回傳 nil
// - 確認不屬於此任務或已結束：不發信號，清零 PGID，回傳 nil (無副作用的成功)
// - 無法確認：回傳錯誤，絕不清零 PGID，絕不宣告成功
func safeKillTaskProcessGroup(store *storage.SQLiteStore, taskID string, targetPGID int, meta opencode.TaskProcessMetadata) error {
	if targetPGID <= 0 {
		return nil
	}

	matched, err := opencode.VerifyTaskProcessGroup(targetPGID, meta, taskID)
	if err != nil {
		// 無法確認身分，嚴禁清空 PGID 或視為已回收 (修復 K-01)
		return fmt.Errorf("unable to verify process identity: %w", err)
	}

	if matched {
		_ = opencode.KillProcessGroup(targetPGID)
		// 等候並確認進程組確實退出（最多等待 1 秒）
		for i := 0; i < 50; i++ {
			if !opencode.IsProcessGroupAlive(targetPGID) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if opencode.IsProcessGroupAlive(targetPGID) {
			_ = opencode.KillProcessGroup(targetPGID)
			time.Sleep(20 * time.Millisecond)
		}
		if opencode.IsProcessGroupAlive(targetPGID) {
			return fmt.Errorf("process group %d still alive after kill", targetPGID)
		}
	}

	// 確認原進程組已被回收，或進程組已死／已被系統重用：清空 PGID 標記回收完成
	_ = store.UpdateTaskPGID(context.Background(), taskID, 0)
	return nil
}

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

	// 進行跨行程排他握手：若執行端正處於啟動區間 (cmd.Start)，等候其完成 PGID 登記 (修復 G-01, H-01, I-01)
	lockPath := taskLockPath(*dbPath, taskID)
	lf, lErr := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if lErr != nil {
		// 開鎖失敗時嘗試盡力回收已登記之 PGID
		meta := readTaskLockMetadata(lockPath)
		if t, err := store.GetTask(context.Background(), taskID); err == nil && t.PGID > 0 {
			_ = safeKillTaskProcessGroup(store, taskID, t.PGID, meta)
		}
		fmt.Fprintf(stderr, "error: failed to open task lock: %v\n", lErr)
		return 1
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		_ = lf.Close()
		meta := readTaskLockMetadata(lockPath)
		if t, err := store.GetTask(context.Background(), taskID); err == nil && t.PGID > 0 {
			_ = safeKillTaskProcessGroup(store, taskID, t.PGID, meta)
		}
		fmt.Fprintf(stderr, "error: failed to acquire task lock: %v\n", err)
		return 1
	}
	_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	_ = lf.Close()

	// 握手完成：從鎖檔讀取不可變身分元資料，並重查最新 PGID
	meta := readTaskLockMetadata(lockPath)
	targetPGID := task.PGID
	if t, err := store.GetTask(context.Background(), taskID); err == nil && t.PGID > 0 {
		targetPGID = t.PGID
	}
	if targetPGID <= 0 && meta.PGID > 0 {
		targetPGID = meta.PGID
	}

	// 實施三態進程組身分檢驗與安全回收 (修復 J-01, K-01, L-01)
	if targetPGID > 0 {
		if killErr := safeKillTaskProcessGroup(store, taskID, targetPGID, meta); killErr != nil {
			fmt.Fprintf(stderr, "error: failed to recycle process: %v\n", killErr)
			return 1
		}
	}

	fmt.Fprintf(stdout, "Task %s cancelled successfully\n", taskID)
	return 0
}
