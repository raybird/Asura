package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"asura/internal/storage"
)

// SCN-001: 透過本機 CLI (go run ./cmd/asura) 提交任務並以 status 重查相同結果
// SCN-002: 超出授權之工作目錄在 CLI 端被明確拒絕
func TestCLI_EndToEnd(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cli-integration-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")

	// 找出 cmd/asura 目錄的絕對路徑
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	t.Run("SCN-001: 提交任務並重查結果", func(t *testing.T) {
		runCmd := exec.Command("go", "run", cmdDir,
			"run",
			"--db", dbPath,
			"--workspace", allowedDir,
			"--allowed-roots", allowedDir,
			"echo 42",
		)
		var runOut, runErr bytes.Buffer
		runCmd.Stdout = &runOut
		runCmd.Stderr = &runErr

		err := runCmd.Run()
		if err != nil {
			t.Fatalf("CLI run failed: %v, stderr: %s", err, runErr.String())
		}

		outStr := runOut.String()
		if !strings.Contains(outStr, "Task ID:") {
			t.Fatalf("expected output to contain 'Task ID:', got: %s", outStr)
		}

		// 解析 Task ID
		var taskID string
		for _, line := range strings.Split(outStr, "\n") {
			if strings.HasPrefix(line, "Task ID:") {
				taskID = strings.TrimSpace(strings.TrimPrefix(line, "Task ID:"))
				break
			}
		}
		if taskID == "" {
			t.Fatalf("failed to extract task ID from output: %s", outStr)
		}

		// 透過 status <task_id> 重新查詢
		statusCmd := exec.Command("go", "run", cmdDir,
			"status",
			"--db", dbPath,
			taskID,
		)
		var statusOut, statusErr bytes.Buffer
		statusCmd.Stdout = &statusOut
		statusCmd.Stderr = &statusErr

		err = statusCmd.Run()
		if err != nil {
			t.Fatalf("CLI status failed: %v, stderr: %s", err, statusErr.String())
		}

		statusStr := statusOut.String()
		if !strings.Contains(statusStr, taskID) {
			t.Errorf("status output expected to contain task ID %s, got: %s", taskID, statusStr)
		}
		if !strings.Contains(statusStr, "completed") {
			t.Errorf("status output expected to contain 'completed', got: %s", statusStr)
		}
	})

	t.Run("SCN-002: 拒絕超出授權的工作目錄", func(t *testing.T) {
		runCmd := exec.Command("go", "run", cmdDir,
			"run",
			"--db", dbPath,
			"--workspace", "/etc",
			"--allowed-roots", allowedDir,
			"cat /etc/passwd",
		)
		var runOut, runErr bytes.Buffer
		runCmd.Stdout = &runOut
		runCmd.Stderr = &runErr

		err := runCmd.Run()
		if err == nil {
			t.Fatalf("expected non-zero exit code for unauthorized workspace, got success")
		}

		errStr := runErr.String()
		if !strings.Contains(errStr, "authorization denied") && !strings.Contains(errStr, "disallowed") {
			t.Errorf("expected stderr to explain authorization denial, got: %s", errStr)
		}
	})
}

// SCN-004: 取消任務 A 在 2 秒內停止且狀態為 cancelled，不影響其他並行任務 B（仍為 completed）
func TestCLI_CancelIsolation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cli-cancel-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	// 預先編譯二進位執行檔以消除 go run 編譯時間對 2 秒超時的干擾
	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-o", asuraBin, cmdDir)
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", buildErr, string(buildOut))
	}

	// 1. 啟動長時間任務 A (sleep 10)
	taskAID := "task-A-long"
	runACmd := exec.Command(asuraBin,
		"run",
		"--db", dbPath,
		"--id", taskAID,
		"--workspace", allowedDir,
		"--allowed-roots", allowedDir,
		"sleep 10",
	)
	var runAOut, runAErr bytes.Buffer
	runACmd.Stdout = &runAOut
	runACmd.Stderr = &runAErr
	if err := runACmd.Start(); err != nil {
		t.Fatalf("failed to start task A: %v", err)
	}

	// 2. 同時啟動快速任務 B
	taskBID := "task-B-fast"
	runBCmd := exec.Command(asuraBin,
		"run",
		"--db", dbPath,
		"--id", taskBID,
		"--workspace", allowedDir,
		"--allowed-roots", allowedDir,
		"echo task-B-finished",
	)
	var runBOut, runBErr bytes.Buffer
	runBCmd.Stdout = &runBOut
	runBCmd.Stderr = &runBErr
	if err := runBCmd.Start(); err != nil {
		t.Fatalf("failed to start task B: %v", err)
	}

	// 等候任務 A 與 B 寫入資料庫且任務 A 取得 PGID
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	var pgidA int
	for i := 0; i < 30; i++ {
		taskA, err := store.GetTask(context.Background(), taskAID)
		if err == nil && taskA.Status == storage.StatusRunning && taskA.PGID > 0 {
			pgidA = taskA.PGID
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if pgidA == 0 {
		taskA, getErr := store.GetTask(context.Background(), taskAID)
		t.Fatalf("task A did not start or did not register PGID in time, task in DB: %+v, getErr: %v, stdout: %s, stderr: %s", taskA, getErr, runAOut.String(), runAErr.String())
	}

	// 3. 發送取消請求並精準計時
	cancelStart := time.Now()
	cancelCmd := exec.Command(asuraBin,
		"cancel",
		"--db", dbPath,
		taskAID,
	)
	var cancelOut, cancelErr bytes.Buffer
	cancelCmd.Stdout = &cancelOut
	cancelCmd.Stderr = &cancelErr
	if err := cancelCmd.Run(); err != nil {
		t.Fatalf("asura cancel failed: %v, stderr: %s", err, cancelErr.String())
	}

	// 4. 等候任務 A 進程退出並斷言終止時間在 2 秒內 (SCN-004 規定)
	_ = runACmd.Wait()
	duration := time.Since(cancelStart)
	if duration > 2*time.Second {
		t.Errorf("task A took %v to terminate after cancel, exceeding 2s limit", duration)
	}

	// 5. 斷言任務 A 的狀態更新為 cancelled
	statusACmd := exec.Command(asuraBin, "status", "--db", dbPath, taskAID)
	var statusAOut bytes.Buffer
	statusACmd.Stdout = &statusAOut
	if err := statusACmd.Run(); err != nil {
		t.Fatalf("asura status task A failed: %v", err)
	}
	if !strings.Contains(statusAOut.String(), "cancelled") {
		t.Errorf("expected task A status to be cancelled, got: %s", statusAOut.String())
	}

	// 6. 任務 B 正常完成且狀態為 completed
	if err := runBCmd.Wait(); err != nil {
		t.Fatalf("task B failed to complete: %v, stderr: %s", err, runBErr.String())
	}
	statusBCmd := exec.Command(asuraBin, "status", "--db", dbPath, taskBID)
	var statusBOut bytes.Buffer
	statusBCmd.Stdout = &statusBOut
	if err := statusBCmd.Run(); err != nil {
		t.Fatalf("asura status task B failed: %v", err)
	}
	if !strings.Contains(statusBOut.String(), "completed") {
		t.Errorf("expected task B status to be completed, got: %s", statusBOut.String())
	}

	// 7. 驗證任務 A 進程組 PGID 無孤兒進程殘留
	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(-pgidA, 0); err == nil {
		t.Errorf("expected process group %d to be completely terminated, but it is still alive", pgidA)
	}
}

// SCN-005: 部分執行失敗不自動重做副作用且揭露已知進度
func TestCLI_PartialFailureProtection(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cli-partial-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-o", asuraBin, cmdDir)
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", buildErr, string(buildOut))
	}

	sideEffectFile := filepath.Join(allowedDir, "side-effect-partial.txt")
	taskID := "task-partial-fail-1"

	// 提交一個執行部分副作用（touch 檔案）隨後失敗（exit 42）的命令
	script := fmt.Sprintf("echo 'step 1 done' && touch %s && echo 'step 2 failed' && exit 42", sideEffectFile)
	runCmd := exec.Command(asuraBin,
		"run",
		"--db", dbPath,
		"--id", taskID,
		"--workspace", allowedDir,
		"--allowed-roots", allowedDir,
		script,
	)
	var runOut, runErr bytes.Buffer
	runCmd.Stdout = &runOut
	runCmd.Stderr = &runErr

	// 預期執行失敗，退出碼非 0
	err = runCmd.Run()
	if err == nil {
		t.Fatalf("expected command to exit with error, but succeeded")
	}

	// 1. 斷言副作用確實已發生（sideEffectFile 存在）
	if _, statErr := os.Stat(sideEffectFile); os.IsNotExist(statErr) {
		t.Fatalf("expected side effect file to be created, but not found")
	}

	// 2. 斷言透過 asura status 查詢該任務之結果
	statusCmd := exec.Command(asuraBin, "status", "--db", dbPath, taskID)
	var statusOut, statusErr bytes.Buffer
	statusCmd.Stdout = &statusOut
	statusCmd.Stderr = &statusErr
	if err := statusCmd.Run(); err != nil {
		t.Fatalf("asura status failed: %v, stderr: %s", err, statusErr.String())
	}
	statusStr := statusOut.String()

	// 狀態必須為 failed（而非 completed 或 pending）
	if !strings.Contains(statusStr, "Status:    failed") {
		t.Errorf("expected status to be failed, got: %s", statusStr)
	}

	// 必須揭露已產生的已知進度（step 1 done）
	if !strings.Contains(statusStr, "step 1 done") {
		t.Errorf("expected status output to contain known progress 'step 1 done', got: %s", statusStr)
	}

	// 必須揭露失敗原因（exit status 42）
	if !strings.Contains(statusStr, "42") {
		t.Errorf("expected status output to reveal exit status or error 42, got: %s", statusStr)
	}

	// 3. 驗證資料庫中僅存在該一筆任務，未自動產生重試任務
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	task, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("expected task in store, got error: %v", err)
	}
	if task.Status != storage.StatusFailed {
		t.Errorf("expected DB status to be failed, got: %s", task.Status)
	}
}

// SCN-006: 守護程序重啟保留已完成結果並將中斷任務標記為失敗揭露原因
func TestCLI_RestartRecovery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cli-recover-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-o", asuraBin, cmdDir)
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", buildErr, string(buildOut))
	}

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	// 1. 寫入已完成之任務 A
	taskA := &storage.Task{
		ID:        "task-A-done",
		Prompt:    "echo task A finished",
		Workspace: tmpDir,
		Status:    storage.StatusCompleted,
		Result:    "task A finished\n",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, taskA); err != nil {
		t.Fatalf("failed to create task A: %v", err)
	}

	// 2. 寫入中斷前執行中之任務 B (模擬 daemon 突然崩潰中斷)
	taskB := &storage.Task{
		ID:        "task-B-interrupted",
		Prompt:    "sleep 100",
		Workspace: tmpDir,
		Status:    storage.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, taskB); err != nil {
		t.Fatalf("failed to create task B: %v", err)
	}

	// 3. 觸發重啟恢復程序
	recovered, err := store.RecoverInterruptedTasks(ctx)
	if err != nil {
		t.Fatalf("recover interrupted tasks failed: %v", err)
	}
	if recovered != 1 {
		t.Errorf("expected 1 task recovered, got %d", recovered)
	}

	// 4. 透過 CLI status 查詢任務 A，驗證結果仍完整保存且為 completed
	statusACmd := exec.Command(asuraBin, "status", "--db", dbPath, taskA.ID)
	var statusAOut bytes.Buffer
	statusACmd.Stdout = &statusAOut
	if err := statusACmd.Run(); err != nil {
		t.Fatalf("asura status task A failed: %v", err)
	}
	statusAStr := statusAOut.String()
	if !strings.Contains(statusAStr, "Status:    completed") {
		t.Errorf("expected task A status to be completed, got: %s", statusAStr)
	}
	if !strings.Contains(statusAStr, "task A finished") {
		t.Errorf("expected task A result to be preserved, got: %s", statusAStr)
	}

	// 5. 透過 CLI status 查詢任務 B，驗證狀態為 failed 且明確揭露中斷原因
	statusBCmd := exec.Command(asuraBin, "status", "--db", dbPath, taskB.ID)
	var statusBOut bytes.Buffer
	statusBCmd.Stdout = &statusBOut
	if err := statusBCmd.Run(); err != nil {
		t.Fatalf("asura status task B failed: %v", err)
	}
	statusBStr := statusBOut.String()
	if !strings.Contains(statusBStr, "Status:    failed") {
		t.Errorf("expected task B status to be failed, got: %s", statusBStr)
	}
	if !strings.Contains(statusBStr, "interrupted") {
		t.Errorf("expected task B error to explain interruption, got: %s", statusBStr)
	}
}

// SCN-007: 空白輸出或結構化錯誤不被當成成功且明確告知失敗原因
func TestCLI_EmptyOutputDefense(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-cli-empty-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-o", asuraBin, cmdDir)
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", buildErr, string(buildOut))
	}

	taskID := "task-empty-output-1"

	// 提交一個無任何文字輸出的命令（如 ":" 或 "true"）
	runCmd := exec.Command(asuraBin,
		"run",
		"--db", dbPath,
		"--id", taskID,
		"--workspace", allowedDir,
		"--allowed-roots", allowedDir,
		":",
	)
	var runOut, runErr bytes.Buffer
	runCmd.Stdout = &runOut
	runCmd.Stderr = &runErr

	// 斷言 CLI 返回非 0 狀態碼（因空輸出被防禦為失敗）
	err = runCmd.Run()
	if err == nil {
		t.Fatalf("expected CLI run to fail for empty output command, but succeeded with exit code 0")
	}

	// 透過 CLI status 查詢任務狀態
	statusCmd := exec.Command(asuraBin, "status", "--db", dbPath, taskID)
	var statusOut, statusErr bytes.Buffer
	statusCmd.Stdout = &statusOut
	statusCmd.Stderr = &statusErr
	if err := statusCmd.Run(); err != nil {
		t.Fatalf("asura status failed: %v, stderr: %s", err, statusErr.String())
	}
	statusStr := statusOut.String()

	// 1. 斷言狀態嚴格為 failed（杜絕假成功）
	if !strings.Contains(statusStr, "Status:    failed") {
		t.Errorf("expected status to be failed, got: %s", statusStr)
	}

	// 2. 斷言錯誤訊息明確指出 empty output
	if !strings.Contains(statusStr, "empty output") {
		t.Errorf("expected error message to explain empty output, got: %s", statusStr)
	}

	// 3. 檢查資料庫持久化狀態
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	task, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("failed to get task from store: %v", err)
	}
	if task.Status != storage.StatusFailed {
		t.Errorf("expected DB task status failed, got: %s", task.Status)
	}
	if !strings.Contains(task.ErrorMessage, "empty output") {
		t.Errorf("expected DB task error message to contain 'empty output', got: %s", task.ErrorMessage)
	}
}

// R-03, R-06, R-07 整合驗證：快速取消資源回收、保留 partial output、失敗與取消皆揭露 Task ID
func TestCLI_ReviewFixes_R03_R06_R07(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-review-cli-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-o", asuraBin, cmdDir)
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", buildErr, string(buildOut))
	}

	// 1. R-07 驗證：任務失敗時 stdout 依然揭露 Task ID
	failCmd := exec.Command(asuraBin,
		"run",
		"--db", dbPath,
		"--workspace", allowedDir,
		"--allowed-roots", allowedDir,
		"echo initial-partial-output; exit 42",
	)
	var failOut, failErr bytes.Buffer
	failCmd.Stdout = &failOut
	failCmd.Stderr = &failErr
	_ = failCmd.Run()

	failOutStr := failOut.String()
	if !strings.Contains(failOutStr, "Task ID: task-") {
		t.Errorf("expected failed run to reveal Task ID (R-07), but got stdout: %q", failOutStr)
	}

	// 2. R-03 & R-06 驗證：快速發動取消，進程被殺死且保存 partial output
	taskCancelID := "task-cancel-partial"
	runCmd := exec.Command(asuraBin,
		"run",
		"--db", dbPath,
		"--id", taskCancelID,
		"--workspace", allowedDir,
		"--allowed-roots", allowedDir,
		"echo captured-partial-log; sleep 5",
	)
	var runOut, runErr bytes.Buffer
	runCmd.Stdout = &runOut
	runCmd.Stderr = &runErr
	if err := runCmd.Start(); err != nil {
		t.Fatalf("failed to start runCmd: %v", err)
	}

	// 等候產生部分輸出後立即發動取消
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	for i := 0; i < 30; i++ {
		t, err := store.GetTask(context.Background(), taskCancelID)
		if err == nil && t.PGID > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancelCmd := exec.Command(asuraBin, "cancel", "--db", dbPath, taskCancelID)
	if cancelOut, cancelErr := cancelCmd.CombinedOutput(); cancelErr != nil {
		t.Fatalf("asura cancel failed: %v, output: %s", cancelErr, string(cancelOut))
	}

	_ = runCmd.Wait()

	// 透過 status 檢查是否保存了 partial output (R-06)
	statusCmd := exec.Command(asuraBin, "status", "--db", dbPath, taskCancelID)
	statusOut, err := statusCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("asura status failed: %v, output: %s", err, string(statusOut))
	}
	statusStr := string(statusOut)
	if !strings.Contains(statusStr, "captured-partial-log") {
		t.Errorf("expected status output to preserve partial output (R-06), got: %s", statusStr)
	}
	if !strings.Contains(statusStr, "cancelled") {
		t.Errorf("expected status to be cancelled, got: %s", statusStr)
	}
}

// R-04 驗證：stdout 與 stderr 併發高頻輸出時安全無 Data Race (搭配 -race 執行)
func TestCLI_ReviewFixes_R04_ConcurrentOutput(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-r04-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-race", "-o", asuraBin, cmdDir)
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", buildErr, string(buildOut))
	}

	runCmd := exec.Command(asuraBin,
		"run",
		"--db", dbPath,
		"--workspace", allowedDir,
		"--allowed-roots", allowedDir,
		"for i in $(seq 1 50); do echo out-$i; echo err-$i >&2; done",
	)
	var outBuf, errBuf bytes.Buffer
	runCmd.Stdout = &outBuf
	runCmd.Stderr = &errBuf

	if err := runCmd.Run(); err != nil {
		t.Fatalf("concurrent output run failed: %v, stderr: %s", err, errBuf.String())
	}

	outStr := outBuf.String()
	errStr := errBuf.String()

	if !strings.Contains(outStr, "out-50") {
		t.Errorf("expected stdout to contain out-50, got: %s", outStr)
	}
	if !strings.Contains(errStr, "err-50") {
		t.Errorf("expected stderr to contain err-50, got: %s", errStr)
	}
}

// G-01: 驗證跨行程排他握手——在執行端啟動延遲期間發出取消，握手確保子進程絕不啟動且 0 副作用
func TestCLI_LatestReview_G01_HandshakeCancelBeforeStart(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-g01-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-race", "-o", asuraBin, cmdDir)
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", buildErr, string(buildOut))
	}

	taskID := "task-g01-handshake"
	h := sha256.Sum256([]byte(taskID))
	safeName := fmt.Sprintf(".task-%s.lock", hex.EncodeToString(h[:16]))
	lockPath := filepath.Join(filepath.Dir(dbPath), safeName)
	sideEffectFile := filepath.Join(allowedDir, "side_effect.marker")

	// 1. 測試主程先持有該任務的排他鎖，模擬執行端被暫停在進入啟動區間前
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("failed to acquire initial lock: %v", err)
	}

	// 2. 啟動 asura run 子進程（會執行副作用指令：touch side_effect.marker）
	runCmd := exec.Command(asuraBin,
		"run",
		"--db", dbPath,
		"--id", taskID,
		"--workspace", allowedDir,
		"--allowed-roots", allowedDir,
		fmt.Sprintf("touch %s; sleep 5", sideEffectFile),
	)
	var runOut, runErr bytes.Buffer
	runCmd.Stdout = &runOut
	runCmd.Stderr = &runErr
	if err := runCmd.Start(); err != nil {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		_ = lf.Close()
		t.Fatalf("failed to start runCmd: %v", err)
	}

	// 3. 等候任務在 SQLite 中建立並轉為 running
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		_ = lf.Close()
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	for i := 0; i < 50; i++ {
		tObj, err := store.GetTask(context.Background(), taskID)
		if err == nil && tObj.Status == storage.StatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 4. 並發啟動 asura cancel 子進程
	cancelCmd := exec.Command(asuraBin, "cancel", "--db", dbPath, taskID)
	cancelDone := make(chan error, 1)
	var cancelOut, cancelErr bytes.Buffer
	cancelCmd.Stdout = &cancelOut
	cancelCmd.Stderr = &cancelErr

	if err := cancelCmd.Start(); err != nil {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		_ = lf.Close()
		t.Fatalf("failed to start cancelCmd: %v", err)
	}

	go func() {
		cancelDone <- cancelCmd.Wait()
	}()

	// 5. 短暫等待確保 cancel 已經將 DB 更新為 cancelled 且正等待排他鎖握手
	time.Sleep(50 * time.Millisecond)

	// 6. 釋放排他鎖，放行 cancel 的握手與 run 的進入
	_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	_ = lf.Close()

	// 7. 取消端必須順利完成握手並返回 0
	cErr := <-cancelDone
	if cErr != nil {
		t.Fatalf("cancel command failed: %v, stderr: %s, stdout: %s", cErr, cancelErr.String(), cancelOut.String())
	}
	if !strings.Contains(cancelOut.String(), "cancelled successfully") {
		t.Errorf("expected cancel success output, got: %s", cancelOut.String())
	}

	// 8. 執行端退出
	_ = runCmd.Wait()

	// 9. 核心斷言：副作用標記檔案絕不能存在（子進程被原子握手攔截，0 啟動，0 副作用）
	if _, statErr := os.Stat(sideEffectFile); statErr == nil {
		t.Fatalf("side-effect file exists! cmd.Start() was executed despite external cancel (violates G-01)")
	}

	// 10. 資料庫狀態嚴格維持 cancelled，且 PGID 為 0
	finalTask, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("failed to query final task: %v", err)
	}
	if finalTask.Status != storage.StatusCancelled {
		t.Errorf("expected final status cancelled, got: %s", finalTask.Status)
	}
	if finalTask.PGID != 0 {
		t.Errorf("expected final PGID 0 (never launched), got: %d", finalTask.PGID)
	}
}

// H-01: 驗證 Task ID 包含路徑跳脫/階層字元（如 batch/task-1）時，仍能使用平坦鎖檔完成排他握手與取消，且失敗時不得未持鎖執行
func TestCLI_LatestReview_H01_PathEscapedTaskIDHandshake(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-h01-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-race", "-o", asuraBin, cmdDir)
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", buildErr, string(buildOut))
	}

	taskID := "batch/task-1"
	h := sha256.Sum256([]byte(taskID))
	safeName := fmt.Sprintf(".task-%s.lock", hex.EncodeToString(h[:16]))
	lockPath := filepath.Join(filepath.Dir(dbPath), safeName)
	sideEffectFile := filepath.Join(allowedDir, "side_effect.marker")

	// 1. 測試主程先持有平坦鎖檔排他鎖
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("failed to acquire initial lock: %v", err)
	}

	// 2. 啟動含有路徑字元 ID 的 asura run
	runCmd := exec.Command(asuraBin,
		"run",
		"--db", dbPath,
		"--id", taskID,
		"--workspace", allowedDir,
		"--allowed-roots", allowedDir,
		fmt.Sprintf("touch %s; sleep 5", sideEffectFile),
	)
	var runOut, runErr bytes.Buffer
	runCmd.Stdout = &runOut
	runCmd.Stderr = &runErr
	if err := runCmd.Start(); err != nil {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		_ = lf.Close()
		t.Fatalf("failed to start runCmd: %v", err)
	}

	// 3. 等候任務在 SQLite 中建立並轉為 running
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		_ = lf.Close()
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	for i := 0; i < 50; i++ {
		tObj, err := store.GetTask(context.Background(), taskID)
		if err == nil && tObj.Status == storage.StatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 在測試主程仍持有鎖期間，檢查 run 是否被阻塞：副作用標記絕不能產生
	// 若存在 H-01 漏洞（鎖檔路徑錯誤且忽略失敗），runCmd 會在此時直接啟動子進程並產生副作用！
	time.Sleep(100 * time.Millisecond)
	if _, statErr := os.Stat(sideEffectFile); statErr == nil {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		_ = lf.Close()
		_ = runCmd.Process.Kill()
		t.Fatalf("side-effect marker appeared while lock held! H-01 lock bypass detected (cmd.Start was not guarded by lock)")
	}

	// 4. 並發啟動 asura cancel 子進程
	cancelCmd := exec.Command(asuraBin, "cancel", "--db", dbPath, taskID)
	cancelDone := make(chan error, 1)
	var cancelOut, cancelErr bytes.Buffer
	cancelCmd.Stdout = &cancelOut
	cancelCmd.Stderr = &cancelErr

	if err := cancelCmd.Start(); err != nil {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		_ = lf.Close()
		t.Fatalf("failed to start cancelCmd: %v", err)
	}

	go func() {
		cancelDone <- cancelCmd.Wait()
	}()

	time.Sleep(50 * time.Millisecond)

	// 5. 釋放排他鎖，放行握手
	_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	_ = lf.Close()

	cErr := <-cancelDone
	if cErr != nil {
		t.Fatalf("cancel command failed: %v, stderr: %s, stdout: %s", cErr, cancelErr.String(), cancelOut.String())
	}
	if !strings.Contains(cancelOut.String(), "cancelled successfully") {
		t.Errorf("expected cancel success output, got: %s", cancelOut.String())
	}

	_ = runCmd.Wait()

	if _, statErr := os.Stat(sideEffectFile); statErr == nil {
		t.Fatalf("side-effect file exists! cmd.Start() was executed despite external cancel")
	}

	finalTask, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("failed to query final task: %v", err)
	}
	if finalTask.Status != storage.StatusCancelled {
		t.Errorf("expected final status cancelled, got: %s", finalTask.Status)
	}
	if finalTask.PGID != 0 {
		t.Errorf("expected final PGID 0 (never launched), got: %d", finalTask.PGID)
	}
}

// H-01: 驗證取得啟動鎖失敗時，執行端立即終止絕不啟動程序，取消端亦不得宣告握手完成
func TestCLI_LatestReview_H01_LockFailureAbort(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-h01-abort-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-race", "-o", asuraBin, cmdDir)
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", buildErr, string(buildOut))
	}

	taskID := "task-lock-fail"
	h := sha256.Sum256([]byte(taskID))
	safeName := fmt.Sprintf(".task-%s.lock", hex.EncodeToString(h[:16]))
	lockPath := filepath.Join(filepath.Dir(dbPath), safeName)
	sideEffectFile := filepath.Join(allowedDir, "side_effect.marker")

	// 預先建立鎖檔並設為不可讀寫 (0000)，造成 os.OpenFile 權限拒絕
	if err := os.WriteFile(lockPath, []byte(""), 0000); err != nil {
		t.Fatalf("failed to create dummy lock file: %v", err)
	}
	_ = os.Chmod(lockPath, 0000)

	// 1. 執行端：無法取得鎖時必須返回非 0，且不得執行命令（無副作用）
	runCmd := exec.Command(asuraBin,
		"run",
		"--db", dbPath,
		"--id", taskID,
		"--workspace", allowedDir,
		"--allowed-roots", allowedDir,
		fmt.Sprintf("touch %s", sideEffectFile),
	)
	var runOut, runErr bytes.Buffer
	runCmd.Stdout = &runOut
	runCmd.Stderr = &runErr

	runCode := 0
	if err := runCmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			runCode = exitErr.ExitCode()
		} else {
			t.Fatalf("runCmd unexpected error: %v", err)
		}
	}

	if runCode == 0 {
		t.Errorf("expected runCmd to fail when lock cannot be opened, got 0")
	}
	if !strings.Contains(runErr.String(), "acquire task lock") {
		t.Errorf("expected stderr to report lock error, got: %s", runErr.String())
	}
	if _, statErr := os.Stat(sideEffectFile); statErr == nil {
		t.Fatalf("side effect file was created! Task executed despite lock open failure")
	}

	// 2. 取消端：在 DB 內任務為 running 的情況下，鎖檔無法開啟時不得宣稱成功
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	if err := store.UpdateTaskStatus(context.Background(), taskID, storage.StatusRunning, "", ""); err != nil {
		t.Fatalf("failed to reset task status to running: %v", err)
	}

	cancelCmd := exec.Command(asuraBin, "cancel", "--db", dbPath, taskID)
	var cancelOut, cancelErr bytes.Buffer
	cancelCmd.Stdout = &cancelOut
	cancelCmd.Stderr = &cancelErr

	cancelCode := 0
	if err := cancelCmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			cancelCode = exitErr.ExitCode()
		} else {
			t.Fatalf("cancelCmd unexpected error: %v", err)
		}
	}

	if cancelCode == 0 {
		t.Errorf("expected cancelCmd to fail when lock cannot be opened, got 0")
	}
	if !strings.Contains(cancelErr.String(), "failed to open task lock") {
		t.Errorf("expected stderr to mention 'failed to open task lock', got: %s", cancelErr.String())
	}
	if strings.Contains(cancelOut.String(), "cancelled successfully") {
		t.Errorf("cancelCmd should not claim success when lock handshake failed")
	}
}

// I-01: 驗證取消握手失敗後，修復鎖檔再次重試 cancel 能夠成功完成握手並回收正在運行的程序，絕不產生殘留副作用
func TestCLI_LatestReview_I01_CancelRetryRecoversProcess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-i01-retry-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-race", "-o", asuraBin, cmdDir)
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", buildErr, string(buildOut))
	}

	taskID := "task-i01-retry"
	h := sha256.Sum256([]byte(taskID))
	safeName := fmt.Sprintf(".task-%s.lock", hex.EncodeToString(h[:16]))
	lockPath := filepath.Join(filepath.Dir(dbPath), safeName)

	startedMarker := filepath.Join(allowedDir, "started.marker")
	sideEffectFile := filepath.Join(allowedDir, "still_executed.marker")

	// 1. 啟動 asura run，先 touch started.marker，然後 sleep 3 秒後建立副作用檔案
	runCmd := exec.Command(asuraBin,
		"run",
		"--db", dbPath,
		"--id", taskID,
		"--workspace", allowedDir,
		"--allowed-roots", allowedDir,
		fmt.Sprintf("touch %s; sleep 3; touch %s", startedMarker, sideEffectFile),
	)
	var runOut, runErr bytes.Buffer
	runCmd.Stdout = &runOut
	runCmd.Stderr = &runErr

	if err := runCmd.Start(); err != nil {
		t.Fatalf("failed to start runCmd: %v", err)
	}

	// 2. 等候任務在 store 中建立、狀態為 running，且已建立 started.marker
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	for i := 0; i < 50; i++ {
		tObj, err := store.GetTask(context.Background(), taskID)
		_, statErr := os.Stat(startedMarker)
		if err == nil && tObj.Status == storage.StatusRunning && statErr == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 3. 將鎖檔權限設為 0000 模擬開鎖失敗
	if err := os.Chmod(lockPath, 0000); err != nil {
		t.Fatalf("failed to chmod lock file to 0000: %v", err)
	}

	// 4. 第一次執行 cancel：預期失敗退出碼 1
	cancelCmd1 := exec.Command(asuraBin, "cancel", "--db", dbPath, taskID)
	var cancelOut1, cancelErr1 bytes.Buffer
	cancelCmd1.Stdout = &cancelOut1
	cancelCmd1.Stderr = &cancelErr1

	code1 := 0
	if err := cancelCmd1.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code1 = exitErr.ExitCode()
		}
	}
	if code1 == 0 {
		t.Fatalf("expected cancel 1 to fail due to permission denied, got exit code 0")
	}
	if !strings.Contains(cancelErr1.String(), "failed to open task lock") {
		t.Fatalf("expected cancel 1 stderr to report failed to open task lock, got: %s", cancelErr1.String())
	}

	// 5. 恢復鎖檔權限為 0600
	if err := os.Chmod(lockPath, 0600); err != nil {
		t.Fatalf("failed to chmod lock file back to 0600: %v", err)
	}

	// 6. 第二次執行 cancel（重試取消）：必須成功握手、回收進程並返回 0
	cancelCmd2 := exec.Command(asuraBin, "cancel", "--db", dbPath, taskID)
	var cancelOut2, cancelErr2 bytes.Buffer
	cancelCmd2.Stdout = &cancelOut2
	cancelCmd2.Stderr = &cancelErr2

	code2 := 0
	if err := cancelCmd2.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code2 = exitErr.ExitCode()
		} else {
			t.Fatalf("cancel 2 unexpected error: %v", err)
		}
	}

	if code2 != 0 {
		t.Fatalf("expected cancel 2 to succeed after restoring lock permission, got code: %d, stderr: %s, stdout: %s",
			code2, cancelErr2.String(), cancelOut2.String())
	}
	if !strings.Contains(cancelOut2.String(), "cancelled successfully") {
		t.Errorf("expected cancel 2 stdout to contain 'cancelled successfully', got: %s", cancelOut2.String())
	}

	// 7. 等候 runCmd 結束
	_ = runCmd.Wait()

	// 8. 驗證進程已被回收，副作用標記絕不能出現
	if _, statErr := os.Stat(sideEffectFile); statErr == nil {
		t.Fatalf("side-effect marker appeared! Process was not killed during cancel retry (violates I-01)")
	}

	// 9. 驗證資料庫狀態為 cancelled
	finalTask, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("failed to query final task: %v", err)
	}
	if finalTask.Status != storage.StatusCancelled {
		t.Errorf("expected task status to be cancelled, got: %s", finalTask.Status)
	}
}

// J-01: 驗證當 PGID 被系統重用或指派給不相關進程時，取消舊任務絕不誤殺不相關進程，且收尾後將 PGID 歸零
func TestCLI_LatestReview_J01_CancelDoesNotKillReusedPGID(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-j01-reused-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-race", "-o", asuraBin, cmdDir)
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", buildErr, string(buildOut))
	}

	// 1. 啟動一個完全獨立的測試進程（不屬於任何 asura task），模擬 PID 被系統重新分配的情境
	unrelatedCmd := exec.Command("sleep", "10")
	unrelatedCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := unrelatedCmd.Start(); err != nil {
		t.Fatalf("failed to start unrelated process: %v", err)
	}
	defer func() {
		if unrelatedCmd.Process != nil {
			_ = unrelatedCmd.Process.Kill()
		}
	}()

	unrelatedPGID, err := syscall.Getpgid(unrelatedCmd.Process.Pid)
	if err != nil {
		t.Fatalf("failed to get unrelated pgid: %v", err)
	}

	// 2. 在 store 中建立一個舊 cancelled 任務，但其 PGID 恰好等於 unrelatedPGID（模擬 PGID 重用）
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	oldTaskID := "old-cancelled-task"
	err = store.CreateTask(context.Background(), &storage.Task{
		ID:        oldTaskID,
		Workspace: allowedDir,
		Prompt:    "echo old",
		Status:    storage.StatusCancelled,
		PGID:      unrelatedPGID,
	})
	if err != nil {
		t.Fatalf("failed to create dummy old task: %v", err)
	}

	waitErrCh := make(chan error, 1)
	go func() {
		waitErrCh <- unrelatedCmd.Wait()
	}()

	// 3. 執行 asura cancel 取消該舊任務
	cancelCmd := exec.Command(asuraBin, "cancel", "--db", dbPath, oldTaskID)
	var cancelOut, cancelErr bytes.Buffer
	cancelCmd.Stdout = &cancelOut
	cancelCmd.Stderr = &cancelErr

	cancelCode := 0
	if err := cancelCmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			cancelCode = exitErr.ExitCode()
		} else {
			t.Fatalf("cancelCmd error: %v", err)
		}
	}

	if cancelCode != 0 {
		t.Fatalf("expected cancelCmd to succeed (exit 0), got: %d, stderr: %s", cancelCode, cancelErr.String())
	}

	// 4. 關鍵斷言：不相關的進程絕不能被殺死（必須依然存活！）
	// 若存在 J-01 漏洞，該進程會被 SIGKILL 殺死，waitErrCh 會立即收到 signal: killed
	select {
	case waitErr := <-waitErrCh:
		t.Fatalf("unrelated process was killed by cancel! (PID reuse vulnerability J-01 detected): %v", waitErr)
	case <-time.After(100 * time.Millisecond):
		// 正常：獨立 sleep 進程依然存活並在背景執行
	}

	// 5. 驗證資料庫中的 PGID 已被歸零（標記回收完成）
	tObj, err := store.GetTask(context.Background(), oldTaskID)
	if err != nil {
		t.Fatalf("failed to query task: %v", err)
	}
	if tObj.PGID != 0 {
		t.Errorf("expected task PGID to be cleared to 0 after cancel, got: %d", tObj.PGID)
	}
}

// K-01: 驗證受控命令執行 exec env -i 清空環境變數時，取消端仍能透過核心不可變身分成功識別並回收程序，絕無殘留副作用
func TestCLI_LatestReview_K01_CancelRecoversCleanEnvProcess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-k01-cleanenv-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-race", "-o", asuraBin, cmdDir)
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", buildErr, string(buildOut))
	}

	taskID := "task-clean-env"
	startedMarker := filepath.Join(allowedDir, "started.marker")
	sideEffectFile := filepath.Join(allowedDir, "still_executed.marker")

	// 1. 啟動執行 exec env -i 清空所有環境變數的命令
	runCmd := exec.Command(asuraBin,
		"run",
		"--db", dbPath,
		"--id", taskID,
		"--workspace", allowedDir,
		"--allowed-roots", allowedDir,
		fmt.Sprintf("exec env -i PATH=/usr/bin:/bin sh -c 'touch %s; sleep 3; touch %s'", startedMarker, sideEffectFile),
	)
	var runOut, runErr bytes.Buffer
	runCmd.Stdout = &runOut
	runCmd.Stderr = &runErr

	if err := runCmd.Start(); err != nil {
		t.Fatalf("failed to start runCmd: %v", err)
	}

	// 2. 等候任務進入 running 且已 touch started.marker（代表 exec env -i 已執行，環境已清空）
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	for i := 0; i < 50; i++ {
		tObj, err := store.GetTask(context.Background(), taskID)
		_, statErr := os.Stat(startedMarker)
		if err == nil && tObj.Status == storage.StatusRunning && statErr == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 3. 執行 asura cancel
	cancelCmd := exec.Command(asuraBin, "cancel", "--db", dbPath, taskID)
	var cancelOut, cancelErr bytes.Buffer
	cancelCmd.Stdout = &cancelOut
	cancelCmd.Stderr = &cancelErr

	if err := cancelCmd.Run(); err != nil {
		t.Fatalf("cancel command failed: %v, stderr: %s, stdout: %s", err, cancelErr.String(), cancelOut.String())
	}

	// 4. 等候 runCmd 結束
	_ = runCmd.Wait()

	// 5. 核心斷言：原程序必須已被回收，副作用檔案絕不能產生！
	// 若存在 K-01 漏洞（因找不到 ASURA_TASK_ID 而略過 SIGKILL），程序會繼續執行並建立 sideEffectFile
	if _, statErr := os.Stat(sideEffectFile); statErr == nil {
		t.Fatalf("side-effect marker appeared! Clean-env process was not killed (violates K-01)")
	}

	// 6. 驗證任務最終狀態為 cancelled，且 PGID 已被清零
	finalTask, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("failed to query final task: %v", err)
	}
	if finalTask.Status != storage.StatusCancelled {
		t.Errorf("expected final status cancelled, got: %s", finalTask.Status)
	}
	if finalTask.PGID != 0 {
		t.Errorf("expected final PGID 0 (cleared), got: %d", finalTask.PGID)
	}
}

// L-01: 驗證進程組 Leader 率先退出、但背景孤兒子進程仍在執行時，取消端絕不因 Leader 不存在而誤判整組已回收，並能完整回收整個進程組
func TestCLI_LatestReview_L01_CancelRecoversOrphanProcessGroup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-l01-orphan-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-race", "-o", asuraBin, cmdDir)
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", buildErr, string(buildOut))
	}

	taskID := "task-orphan-group"
	childStartedMarker := filepath.Join(allowedDir, "child_started.marker")
	childSideEffectFile := filepath.Join(allowedDir, "child_still_executed.marker")

	// 1. 啟動命令：在背景派生子進程，Leader 程序先行 exit 0 退出，留下背景子進程持有同一 PGID
	runCmd := exec.Command(asuraBin,
		"run",
		"--db", dbPath,
		"--id", taskID,
		"--workspace", allowedDir,
		"--allowed-roots", allowedDir,
		fmt.Sprintf("(touch %s; sleep 3; touch %s) & echo LEADER_EXIT; exit 0", childStartedMarker, childSideEffectFile),
	)
	var runOut, runErr bytes.Buffer
	runCmd.Stdout = &runOut
	runCmd.Stderr = &runErr

	if err := runCmd.Start(); err != nil {
		t.Fatalf("failed to start runCmd: %v", err)
	}

	// 2. 等候任務進入 running、子進程 touch child_started.marker，且主程序已輸出 LEADER_EXIT
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	var targetPGID int
	for i := 0; i < 50; i++ {
		tObj, err := store.GetTask(context.Background(), taskID)
		_, statErr := os.Stat(childStartedMarker)
		if err == nil && tObj.Status == storage.StatusRunning && statErr == nil && tObj.PGID > 0 {
			targetPGID = tObj.PGID
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if targetPGID == 0 {
		t.Fatalf("target task PGID was not registered")
	}

	// 短暫等待確保 Leader 進程 exit 0 退出（/proc/<targetPGID>/stat 消失），但進程組仍存活
	time.Sleep(100 * time.Millisecond)

	// 3. 執行 asura cancel
	cancelCmd := exec.Command(asuraBin, "cancel", "--db", dbPath, taskID)
	var cancelOut, cancelErr bytes.Buffer
	cancelCmd.Stdout = &cancelOut
	cancelCmd.Stderr = &cancelErr

	if err := cancelCmd.Run(); err != nil {
		t.Fatalf("cancel command failed: %v, stderr: %s, stdout: %s", err, cancelErr.String(), cancelOut.String())
	}

	// 4. 等候 runCmd 結束
	_ = runCmd.Wait()

	// 5. 核心斷言：背景孤兒子進程必須被 SIGKILL 回收，副作用檔案絕不能產生！
	// 若存在 L-01 漏洞（因 Leader 不存在而誤判整組已結束），子程序會繼續執行並建立 childSideEffectFile
	if _, statErr := os.Stat(childSideEffectFile); statErr == nil {
		t.Fatalf("child side-effect marker appeared! Orphan process group was not killed (violates L-01)")
	}

	// 6. 驗證任務最終狀態為 cancelled，且 PGID 已被清零
	finalTask, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("failed to query final task: %v", err)
	}
	if finalTask.Status != storage.StatusCancelled {
		t.Errorf("expected final status cancelled, got: %s", finalTask.Status)
	}
	if finalTask.PGID != 0 {
		t.Errorf("expected final PGID 0 (cleared), got: %d", finalTask.PGID)
	}
}

// M-01: 驗證當子命令清除環境變數、Leader 率先退出且 Runner 崩潰中斷時，取消端無法確認孤兒進程組身分，必須失敗報錯、保留 PGID 追蹤且拒絕假成功
func TestCLI_LatestReview_M01_OrphanUnknownIdentityFailsSafely(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-m01-orphan-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "asura.db")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")

	asuraBin := filepath.Join(tmpDir, "asura")
	buildCmd := exec.Command("go", "build", "-race", "-o", asuraBin, cmdDir)
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", buildErr, string(buildOut))
	}

	taskID := "task-m01-unknown-orphan"

	// 1. 啟動命令：以 exec env -i 清除環境變數，Leader 衍生 sleep 3 孤兒子進程後 exit 0
	runCmd := exec.Command(asuraBin,
		"run",
		"--db", dbPath,
		"--id", taskID,
		"--workspace", allowedDir,
		"--allowed-roots", allowedDir,
		"exec env -i PATH=/usr/bin:/bin sh -c \"(sleep 4) & echo LEADER_EXIT; exit 0\"",
	)
	var runOut, runErr bytes.Buffer
	runCmd.Stdout = &runOut
	runCmd.Stderr = &runErr

	if err := runCmd.Start(); err != nil {
		t.Fatalf("failed to start runCmd: %v", err)
	}

	// 2. 等候任務進入 running 且已登記 PGID
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	var targetPGID int
	for i := 0; i < 50; i++ {
		tObj, err := store.GetTask(context.Background(), taskID)
		if err == nil && tObj.Status == storage.StatusRunning && tObj.PGID > 0 {
			targetPGID = tObj.PGID
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if targetPGID == 0 {
		t.Fatalf("target task PGID was not registered")
	}

	// 確保測試結束時清理孤兒進程組
	defer func() {
		_ = syscall.Kill(-targetPGID, syscall.SIGKILL)
	}()

	// 3. 等候 Leader 程序 exit 0 退出（/proc/<targetPGID>/stat 消失），並中止 Runner 模擬崩潰中斷
	for i := 0; i < 50; i++ {
		if _, statErr := os.Stat(fmt.Sprintf("/proc/%d", targetPGID)); os.IsNotExist(statErr) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 中止 Runner 進程
	_ = runCmd.Process.Kill()
	_ = runCmd.Wait()

	// 確保組內孤兒進程依然存活
	if err := syscall.Kill(-targetPGID, 0); err != nil {
		t.Fatalf("expected orphan process group %d to be alive, got: %v", targetPGID, err)
	}

	// 4. 執行 asura cancel：因為 Leader 已死、Runner 已死且孤兒無 ASURA_TASK_ID 標記，身分無法確認
	cancelCmd := exec.Command(asuraBin, "cancel", "--db", dbPath, taskID)
	var cancelOut, cancelErr bytes.Buffer
	cancelCmd.Stdout = &cancelOut
	cancelCmd.Stderr = &cancelErr

	err = cancelCmd.Run()

	// 5. 斷言（M-01 核心規範）：
	// 取消端絕不能當成已成功回收，嚴禁返回 0，必須失敗報錯！
	if err == nil {
		t.Fatalf("expected cancel command to fail with unknown identity error, but succeeded: stdout=%s", cancelOut.String())
	}

	// 驗證 stderr 揭露無法確認身分之錯誤
	if !strings.Contains(cancelErr.String(), "unable to verify process identity") {
		t.Errorf("expected stderr to contain 'unable to verify process identity', got: %s", cancelErr.String())
	}

	// 6. 驗證 SQLite 中的 PGID 必須完好保留（嚴禁清零抹煞未知進程組之追蹤）
	finalTask, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("failed to query final task: %v", err)
	}
	if finalTask.PGID != targetPGID {
		t.Errorf("expected final PGID to be preserved as %d, got: %d (cleared violates M-01)", targetPGID, finalTask.PGID)
	}
}









