package integration

import (
	"bytes"
	"context"
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
