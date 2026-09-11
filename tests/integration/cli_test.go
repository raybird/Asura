package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
