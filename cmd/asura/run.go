package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"asura/internal/auth"
	"asura/internal/backend/opencode"
	"asura/internal/engine"
	"asura/internal/storage"
)

// taskLockPath 根據 DB 路徑與 Task ID 產生跨行程平坦互斥鎖路徑，防止 Task ID 特殊字元導致路徑跳脫或目錄不存在 (修復 H-01)
func taskLockPath(dbPath, taskID string) string {
	h := sha256.Sum256([]byte(taskID))
	safeName := fmt.Sprintf(".task-%s.lock", hex.EncodeToString(h[:16]))
	return filepath.Join(filepath.Dir(dbPath), safeName)
}

// RunCLI 為 CLI 總進入點函式
func RunCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 1
	}

	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "run":
		return handleRun(subargs, stdout, stderr)
	case "status":
		return handleStatus(subargs, stdout, stderr)
	case "cancel":
		return handleCancel(subargs, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "error: unknown command %q\n\n", subcmd)
		printUsage(stderr)
		return 1
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: asura <command> [options] [arguments]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  run     Submit and execute a task in a workspace")
	fmt.Fprintln(w, "  status  Query the execution status and result of a task")
	fmt.Fprintln(w, "  cancel  Cancel a pending or running task")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run 'asura <command> -h' for details on each command.")
}

// safeBuffer 提供線程安全的緩衝區，杜絕 stdout 與 stderr 併發寫入的 DATA RACE (修復 R-04)
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func handleRun(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)

	dbPath := fs.String("db", "asura.db", "path to SQLite database")
	customID := fs.String("id", "", "optional custom task ID")
	workspace := fs.String("workspace", "", "target workspace directory")
	allowedRoots := fs.String("allowed-roots", "", "comma-separated list of allowed root directories")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(stderr, "error: prompt is required")
		return 1
	}
	prompt := strings.Join(remaining, " ")

	ws := *workspace
	if ws == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "error: failed to get working directory: %v\n", err)
			return 1
		}
		ws = cwd
	}

	var roots []string
	if *allowedRoots != "" {
		for _, r := range strings.Split(*allowedRoots, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				roots = append(roots, r)
			}
		}
	} else {
		// 預設允許當前指定的 workspace
		roots = []string{ws}
	}

	policy := auth.NewPolicy(roots)

	store, err := storage.NewSQLiteStore(*dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: failed to initialize storage: %v\n", err)
		return 1
	}
	defer store.Close()

	var currentTaskID string

	runner := func(ctx context.Context, dir string, p string) (string, error) {
		cmd := exec.CommandContext(ctx, "sh", "-c", p)
		cmd.Dir = dir
		var buf safeBuffer
		cmd.Stdout = io.MultiWriter(stdout, &buf)
		cmd.Stderr = io.MultiWriter(stderr, &buf)

		// 配置獨立進程組
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if currentTaskID != "" {
			cmd.Env = append(os.Environ(), fmt.Sprintf("ASURA_TASK_ID=%s", currentTaskID))
		}

		var lockFile *os.File
		if currentTaskID != "" {
			lockPath := taskLockPath(*dbPath, currentTaskID)
			lf, lErr := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
			if lErr != nil {
				return "", fmt.Errorf("acquire task lock open: %w", lErr)
			}
			if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
				_ = lf.Close()
				return "", fmt.Errorf("acquire task lock flock: %w", err)
			}
			lockFile = lf
		}

		// 在啟動排他鎖保護下檢查是否已被取消，徹底防禦啟動已取消之任務與外部副作用 (修復 F-02, G-01)
		if currentTaskID != "" {
			t, err := store.GetTask(context.Background(), currentTaskID)
			if err == nil && t.Status == storage.StatusCancelled {
				if lockFile != nil {
					_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
					_ = lockFile.Close()
				}
				return buf.String(), context.Canceled
			}
		}

		if err := cmd.Start(); err != nil {
			if lockFile != nil {
				_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
				_ = lockFile.Close()
			}
			return buf.String(), err
		}

		var pgid int
		if cmd.Process != nil {
			p, err := syscall.Getpgid(cmd.Process.Pid)
			if err == nil {
				pgid = p
				if currentTaskID != "" {
					_ = store.UpdateTaskPGID(ctx, currentTaskID, pgid)
					// 記錄不可變核心 StartTime 與執行端 Runner 憑證至鎖檔中供取消端嚴格核驗身分 (修復 K-01, L-01)
					runnerPID := os.Getpid()
					runnerST, _ := opencode.GetProcessStartTime(runnerPID)
					if st, stErr := opencode.GetProcessStartTime(cmd.Process.Pid); stErr == nil && lockFile != nil {
						_ = lockFile.Truncate(0)
						_, _ = lockFile.Seek(0, 0)
						_, _ = lockFile.WriteString(fmt.Sprintf("%d:%s:%d:%s", pgid, st, runnerPID, runnerST))
					}
					// 檢查此任務是否在啟動前或啟動當下已被外部標記取消（修復 R-03）
					t, err := store.GetTask(context.Background(), currentTaskID)
					if err == nil && t.Status == storage.StatusCancelled {
						if lockFile != nil {
							_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
							_ = lockFile.Close()
						}
						_ = opencode.KillProcessGroup(pgid)
						_ = cmd.Wait()
						_ = store.UpdateTaskPGID(context.Background(), currentTaskID, 0)
						return buf.String(), context.Canceled
					}
				}
			}
		}

		// 啟動完成且已登記 PGID，釋放啟動排他鎖
		if lockFile != nil {
			_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
			_ = lockFile.Close()
		}

		err := cmd.Wait()
		if currentTaskID != "" {
			// 若任務已被標記取消，確保殘留之背景孤兒進程組一併被擊殺 (修復 L-01)
			t, _ := store.GetTask(context.Background(), currentTaskID)
			if t != nil && t.Status == storage.StatusCancelled && pgid > 0 {
				_ = opencode.KillProcessGroup(pgid)
			}
			// 僅在進程組已無存活成員時才清空 PGID 與鎖檔
			if !opencode.IsProcessGroupAlive(pgid) {
				_ = store.UpdateTaskPGID(context.Background(), currentTaskID, 0)
				lockPath := taskLockPath(*dbPath, currentTaskID)
				_ = os.WriteFile(lockPath, []byte(""), 0600)
			}
		}
		return buf.String(), err
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))

	ctx := context.Background()
	reqID := *customID
	if reqID == "" {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		reqID = fmt.Sprintf("task-%d-%s", time.Now().Unix(), hex.EncodeToString(b))
	}
	currentTaskID = reqID

	// 在任務提交執行前揭露 Task ID，確保執行中任務可取得 ID 進行取消，失敗任務亦可追蹤（修復 R-07）
	fmt.Fprintf(stdout, "Task ID: %s\n", reqID)

	task, err := eng.SubmitTask(ctx, engine.SubmitRequest{
		ID:        reqID,
		Workspace: ws,
		Prompt:    prompt,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Status:  %s\n", task.Status)

	if task.Status == storage.StatusFailed {
		return 1
	}
	return 0
}
