package opencode

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// SCN-001: 驗證取消時整組 PGID 進程樹均在 2 秒內完全終止無殘留
func TestProcessCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 啟動一個會衍生背景孫進程的腳本
	proc, err := StartProcess(ctx, "sh", []string{"-c", "sleep 15 & sleep 15 & wait"}, "")
	if err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	pgid := proc.PGID()
	if pgid <= 0 {
		t.Fatalf("expected positive PGID, got %d", pgid)
	}

	// 確保子進程與孫進程已經啟動
	time.Sleep(100 * time.Millisecond)

	// 確認此時進程組存活
	if !IsProcessGroupAlive(pgid) {
		t.Fatalf("expected process group %d to be alive before cancel", pgid)
	}

	// 觸發取消
	cancelDone := make(chan error, 1)
	go func() {
		cancel()
		cancelDone <- proc.Wait()
	}()

	select {
	case <-cancelDone:
		// 進程在期限內結束
	case <-time.After(2 * time.Second):
		t.Fatalf("process group did not terminate within 2 seconds after cancellation")
	}

	// 稍等以確保 OS 回收
	time.Sleep(100 * time.Millisecond)

	// 驗證衍生子程序與進程組無殘留
	if IsProcessGroupAlive(pgid) {
		t.Fatalf("process group %d still alive after cancellation, orphan processes remain", pgid)
	}
}

func TestProcessGroup(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	ConfigureProcessGroup(cmd)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("expected SysProcAttr.Setpgid to be true")
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start cmd: %v", err)
	}

	pid := cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("failed to get pgid: %v", err)
	}

	if pgid != pid {
		t.Errorf("expected pgid == pid (%d), got %d", pid, pgid)
	}

	// 測試 KillProcessGroup
	if err := KillProcessGroup(pgid); err != nil {
		t.Fatalf("KillProcessGroup failed: %v", err)
	}

	_ = cmd.Wait()

	time.Sleep(50 * time.Millisecond)
	if IsProcessGroupAlive(pgid) {
		t.Errorf("expected pgid %d to be dead after KillProcessGroup", pgid)
	}
}

func TestIsTaskProcessGroup(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	taskID := "task-test-id-123"
	cmd.Env = append(cmd.Environ(), "ASURA_TASK_ID="+taskID)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start cmd: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("failed to get pgid: %v", err)
	}

	// 1. 身分匹配時應為 true
	if !IsTaskProcessGroup(pgid, taskID) {
		t.Errorf("expected IsTaskProcessGroup to be true for matching taskID")
	}

	// 2. 身分不匹配時應為 false
	if IsTaskProcessGroup(pgid, "different-task-id") {
		t.Errorf("expected IsTaskProcessGroup to be false for mismatched taskID")
	}

	// 3. 殺死進程後應為 false
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	time.Sleep(20 * time.Millisecond)
	if IsTaskProcessGroup(pgid, taskID) {
		t.Errorf("expected IsTaskProcessGroup to be false after process termination")
	}
}

