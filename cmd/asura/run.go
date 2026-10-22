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
	"asura/internal/backend"
	"asura/internal/backend/antigravity"
	"asura/internal/backend/codex"
	"asura/internal/backend/omp"
	"asura/internal/backend/opencode"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
)

// taskLockPath 根據 DB 路徑與 Task ID 產生跨行程平坦互斥鎖路徑，防止 Task ID 特殊字元導致路徑跳脫或目錄不存在 (修復 H-01)
func taskLockPath(dbPath, taskID string) string {
	h := sha256.Sum256([]byte(taskID))
	safeName := fmt.Sprintf(".task-%s.lock", hex.EncodeToString(h[:16]))
	return filepath.Join(filepath.Dir(dbPath), safeName)
}

func taskCancelIntentPath(dbPath, taskID string) string {
	h := sha256.Sum256([]byte(taskID))
	safeName := fmt.Sprintf(".task-%s.cancel", hex.EncodeToString(h[:16]))
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

	// 需要 socket／DB 的指令先載入 operator config；壞檔明確失敗，不退回內建預設。
	switch subcmd {
	case "daemon", "run", "ask", "runtime", "audit", "status", "cancel", "web", "cron", "memory", "service", "setup":
		cc, err := loadCLIConfig()
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		activeConfig = cc
		defer func() { activeConfig = nil }()
	}

	switch subcmd {
	case "daemon":
		return handleDaemon(subargs, stdout, stderr)
	case "run":
		return handleRun(subargs, stdout, stderr)
	case "ask":
		return handleAsk(subargs, stdout, stderr)
	case "runtime":
		return handleRuntime(subargs, stdout, stderr)
	case "audit":
		return handleAudit(subargs, stdout, stderr)
	case "status":
		return handleStatus(subargs, stdout, stderr)
	case "cancel":
		return handleCancel(subargs, stdout, stderr)
	case "web":
		return handleWeb(subargs, stdout, stderr)
	case "cron":
		return handleCron(subargs, stdout, stderr)
	case "memory":
		return handleMemory(subargs, stdout, stderr)
	case "service":
		return handleService(subargs, stdout, stderr)
	case "setup":
		return handleSetup(subargs, stdout, stderr)
	case "version":
		return handleVersion(subargs, stdout, stderr)
	case "doctor":
		return handleDoctor(subargs, stdout, stderr)
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
	fmt.Fprintln(w, "  daemon  Start the central execution daemon")
	fmt.Fprintln(w, "  run     Submit and execute a task in a workspace")
	fmt.Fprintln(w, "  ask     Submit a natural language AI task via Daemon")
	fmt.Fprintln(w, "  audit   Query persisted execution provenance")
	fmt.Fprintln(w, "  status  Query the execution status and result of a task")
	fmt.Fprintln(w, "  cancel  Cancel a pending or running task")
	fmt.Fprintln(w, "  web     Start the local web remote entry console")
	fmt.Fprintln(w, "  cron    Manage periodic scheduled tasks")
	fmt.Fprintln(w, "  memory  Manage project-level long-term memories")
	fmt.Fprintln(w, "  runtime Query runtime inventory and verify installed runtime versions")
	fmt.Fprintln(w, "  version Print build version, commit and platform")
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

	dbPath := fs.String("db", activeConfig.dbPath(), "path to SQLite database")
	customID := fs.String("id", "", "optional custom task ID")
	workspace := fs.String("workspace", "", "target workspace directory")
	allowedRoots := fs.String("allowed-roots", activeConfig.allowedRoots(), "comma-separated list of allowed root directories")
	mode := fs.String("mode", string(pipeline.ModeDirect), "execution mode (direct or three-face)")
	backendFlag := fs.String("backend", "opencode", "execution backend (opencode, codex, omp, antigravity, auto)")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	if !pipeline.IsValidExecutionMode(pipeline.ExecutionMode(*mode)) {
		fmt.Fprintf(stderr, "error: invalid mode %q (must be direct or three-face)\n", *mode)
		return 1
	}

	contextCfg, err := activeConfig.contextConfig()
	if err != nil {
		fmt.Fprintf(stderr, "error: invalid context configuration: %v\n", err)
		return 1
	}

	bID := *backendFlag
	if bID == "" {
		bID = "opencode"
	}
	if bID != "opencode" && bID != "codex" && bID != "omp" && bID != "antigravity" && bID != "auto" {
		fmt.Fprintf(stderr, "error: unknown backend %q (supported: opencode, codex, omp, antigravity, auto)\n", bID)
		return 1
	}

	var runtimePriority []string
	if bID == "auto" {
		runtimePriority, err = backend.ParseRuntimePriority(activeConfig.runtimePriority())
		if err != nil {
			fmt.Fprintf(stderr, "error: runtime priority: %v\n", err)
			return 1
		}
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

	if err := activeConfig.prepareDirs(*dbPath); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
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
			cancelIntentPath := taskCancelIntentPath(*dbPath, currentTaskID)
			if _, statErr := os.Stat(cancelIntentPath); statErr == nil {
				current, _ := store.GetTask(context.Background(), currentTaskID)
				currentResult := ""
				if current != nil {
					currentResult = current.Result
				}
				_, _ = store.UpdateTaskStatusIfRunning(
					context.Background(), currentTaskID, storage.StatusCancelling,
					currentResult, "task cancellation requested",
				)
				if lockFile != nil {
					_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
					_ = lockFile.Close()
				}
				return buf.String(), context.Canceled
			} else if !os.IsNotExist(statErr) {
				if lockFile != nil {
					_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
					_ = lockFile.Close()
				}
				return "", fmt.Errorf("check task cancellation intent: %w", statErr)
			}
			t, err := store.GetTask(context.Background(), currentTaskID)
			if err == nil && (t.Status == storage.StatusCancelled || t.Status == storage.StatusCancelling) {
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
					if err == nil && (t.Status == storage.StatusCancelled || t.Status == storage.StatusCancelling) {
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
			if t != nil && (t.Status == storage.StatusCancelled || t.Status == storage.StatusCancelling) {
				if pgid > 0 {
					_ = opencode.KillProcessGroup(pgid)
				}
				if !opencode.IsProcessGroupAlive(pgid) {
					_ = store.UpdateTaskPGID(context.Background(), currentTaskID, 0)
					lockPath := taskLockPath(*dbPath, currentTaskID)
					_ = os.WriteFile(lockPath, []byte(""), 0600)
				}
				return buf.String(), context.Canceled
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

	oracleStage := pipeline.NewOracleStage(runner)
	strikerStage := pipeline.NewStrikerStage(runner)
	criticStage := pipeline.NewCriticStage(runner)
	orc := pipeline.NewOrchestrator(oracleStage, strikerStage, criticStage).WithContextBudget(contextCfg)
	// 2026-09-30：新增 CLI adapter 在本地 composition root 接線；保留既有 shell runner。
	if bID == "omp" || bID == "antigravity" || bID == "auto" {
		var startLock *os.File
		beforeStart := func(c context.Context) (func(), error) {
			lf, e := os.OpenFile(taskLockPath(*dbPath, currentTaskID), os.O_CREATE|os.O_RDWR, 0600)
			if e != nil {
				return nil, e
			}
			release := func() { startLock = nil; _ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN); _ = lf.Close() }
			if e = syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); e != nil {
				release()
				return nil, e
			}
			if _, e = os.Stat(taskCancelIntentPath(*dbPath, currentTaskID)); e == nil {
				release()
				return nil, context.Canceled
			} else if !os.IsNotExist(e) {
				release()
				return nil, e
			}
			task, e := store.GetTask(c, currentTaskID)
			if e != nil {
				release()
				return nil, e
			}
			if task.Status == storage.StatusCancelling || task.Status == storage.StatusCancelled {
				release()
				return nil, context.Canceled
			}
			startLock = lf
			return release, nil
		}
		onStarted := func(c context.Context, pgid int) {
			if bID != "auto" {
				_ = store.UpdateTaskPGID(context.Background(), currentTaskID, pgid)
				return
			}
			recordRuntimeProcessStart(c, store, *dbPath, currentTaskID, pgid, startLock)
		}
		onExited := func(c context.Context, pgid int) {
			if bID != "auto" || !opencode.IsProcessGroupAlive(pgid) {
				_ = store.UpdateTaskPGID(context.Background(), currentTaskID, 0)
			}
		}
		records := verificationPath()
		var selected backend.Backend
		if bID == "omp" {
			selected = omp.NewAdapter(omp.AdapterConfig{VerificationPath: records, OnProcessStarted: onStarted, OnProcessExited: onExited, BeforeStart: beforeStart})
		} else if bID == "antigravity" {
			selected = antigravity.NewAdapter(antigravity.AdapterConfig{VerificationPath: records, OnProcessStarted: onStarted, OnProcessExited: onExited, BeforeStart: beforeStart})
		}
		reg := backend.NewRegistry()
		if bID == "auto" {
			configDir, cleanup, e := opencode.CreateIsolatedReadOnlyConfigDir()
			if e != nil {
				fmt.Fprintf(stderr, "error: read-only runtime config: %v\n", e)
				return 1
			}
			defer cleanup()
			readOnly := opencode.NewAdapter(opencode.AdapterConfig{ConfigDir: configDir, Permission: daemon.DefaultReadOnlyPermissionPolicy, DisableAuto: true, Role: opencode.RoleOracle, Agent: opencode.AsuraReadOnlyAgentName, Pure: true, DisableProjectConfig: true, Isolated: true, VerificationPath: records, OnProcessStarted: onStarted, OnProcessExited: onExited, BeforeStart: beforeStart})
			writer := opencode.NewAdapter(opencode.AdapterConfig{Permission: daemon.DefaultApprovedPermissionPolicy, Role: opencode.RoleStriker, VerificationPath: records, OnProcessStarted: onStarted, OnProcessExited: onExited, BeforeStart: beforeStart})
			selected = opencode.NewRoleRoutingBackend(readOnly, writer)
			reg.Register(selected)
			reg.Register(codex.NewAdapter(codex.AdapterConfig{VerificationPath: records, OnProcessStarted: onStarted, OnProcessExited: onExited, BeforeStart: beforeStart}))
			reg.Register(omp.NewAdapter(omp.AdapterConfig{VerificationPath: records, OnProcessStarted: onStarted, OnProcessExited: onExited, BeforeStart: beforeStart}))
			reg.Register(antigravity.NewAdapter(antigravity.AdapterConfig{VerificationPath: records, OnProcessStarted: onStarted, OnProcessExited: onExited, BeforeStart: beforeStart}))
		} else {
			reg.Register(selected)
		}
		oracleStage = pipeline.NewOracleStageWithBackend(selected)
		strikerStage = pipeline.NewStrikerStageWithBackend(selected)
		criticStage = pipeline.NewCriticStageWithBackend(selected)
		orc = pipeline.NewOrchestrator(oracleStage, strikerStage, criticStage).WithRegistry(reg).WithContextBudget(contextCfg).WithRuntimePriority(runtimePriority)
	}

	eng := engine.NewEngine(store, policy,
		engine.WithRunner(runner),
		engine.WithPipelineRunner(orc),
	)

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
		Mode:      *mode,
		Backend:   bID,
	})
	if err != nil {
		printAdmissionRejection(stderr, err, reqID, *mode, bID)
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Status:  %s\n", task.Status)

	if task.Status == storage.StatusFailed {
		return 1
	}
	return 0
}
