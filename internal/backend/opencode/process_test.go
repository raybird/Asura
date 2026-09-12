package opencode

import (
	"context"
	"fmt"
	"os"
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

	time.Sleep(20 * time.Millisecond)
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

func TestVerifyTaskProcess_StartTime(t *testing.T) {
	// 啟動一個執行 exec env -i 清空環境變數的進程
	cmd := exec.Command("sh", "-c", "exec env -i sleep 5")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start cmd: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	pid := cmd.Process.Pid

	// 1. 取得核心 StartTime
	st, err := GetProcessStartTime(pid)
	if err != nil {
		t.Fatalf("GetProcessStartTime failed: %v", err)
	}
	if st == "" {
		t.Errorf("expected non-empty start time")
	}

	// 2. 身分校驗：雖然環境變數已被清空，但核心 StartTime 一致時必須驗證成功 (修復 K-01)
	matched, err := VerifyTaskProcess(pid, st, "any-task-id")
	if err != nil {
		t.Fatalf("VerifyTaskProcess unexpected error: %v", err)
	}
	if !matched {
		t.Errorf("expected VerifyTaskProcess to match by startTime even with clean env")
	}

	// 3. StartTime 不匹配時必須返回 false (防禦 J-01 PID 重用)
	matched, err = VerifyTaskProcess(pid, "mismatched-start-time", "any-task-id")
	if err != nil {
		t.Fatalf("VerifyTaskProcess unexpected error: %v", err)
	}
	if matched {
		t.Errorf("expected VerifyTaskProcess to be false for mismatched startTime")
	}

	// 4. 進程退出後必須返回 (false, nil)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	time.Sleep(20 * time.Millisecond)
	matched, err = VerifyTaskProcess(pid, st, "any-task-id")
	if err != nil {
		t.Fatalf("VerifyTaskProcess unexpected error on dead process: %v", err)
	}
	if matched {
		t.Errorf("expected VerifyTaskProcess to be false for dead process")
	}
}

func TestVerifyTaskProcessGroup_OrphanGroup(t *testing.T) {
	// 建立包含背景孤兒進程的命令：Leader 率先 exit 0，背景 sleep 繼續執行持有同一 PGID
	taskID := "task-orphan-unit-test"
	cmd := exec.Command("sh", "-c", "(sleep 5) & exit 0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(cmd.Environ(), "ASURA_TASK_ID="+taskID)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start cmd: %v", err)
	}
	leaderPID := cmd.Process.Pid
	pgid, err := syscall.Getpgid(leaderPID)
	if err != nil {
		t.Fatalf("failed to get pgid: %v", err)
	}

	leaderST, _ := GetProcessStartTime(leaderPID)
	runnerPID := os.Getpid()
	runnerST, _ := GetProcessStartTime(runnerPID)

	meta := TaskProcessMetadata{
		PGID:            pgid,
		StartTime:       leaderST,
		RunnerPID:       runnerPID,
		RunnerStartTime: runnerST,
	}

	// 等候 Leader 主進程 exit 0
	_ = cmd.Wait()

	// 確保 Leader 進程已不存在
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", leaderPID)); os.IsNotExist(err) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 1. 進程組依然存活（有孤兒 sleep 5）
	if !IsProcessGroupAlive(pgid) {
		t.Fatalf("expected orphan process group to be alive")
	}

	// 2. 身分校驗：Leader 已死，但有 Runner 監護憑證，必須識別為真 (修復 L-01)
	matched, err := VerifyTaskProcessGroup(pgid, meta, taskID)
	if err != nil {
		t.Fatalf("VerifyTaskProcessGroup unexpected error: %v", err)
	}
	if !matched {
		t.Errorf("expected VerifyTaskProcessGroup to match orphan group by runner metadata")
	}

	// 3. 身分校驗：即使無 Runner 憑證，藉由組內孤兒成員之 ASURA_TASK_ID 也必須識別為真
	metaWithoutRunner := TaskProcessMetadata{
		PGID:      pgid,
		StartTime: leaderST,
	}
	matchedNoRunner, err := VerifyTaskProcessGroup(pgid, metaWithoutRunner, taskID)
	if err != nil {
		t.Fatalf("VerifyTaskProcessGroup unexpected error: %v", err)
	}
	if !matchedNoRunner {
		t.Errorf("expected VerifyTaskProcessGroup to match orphan group by child environ")
	}

	// 4. 身分校驗：若 TaskID 與 Runner 憑證皆不匹配，必須識別為偽 (防禦 PID/PGID 重用)
	matchedFake, err := VerifyTaskProcessGroup(pgid, TaskProcessMetadata{PGID: pgid, StartTime: leaderST, RunnerPID: 9999999}, "wrong-task-id")
	if err != nil {
		t.Fatalf("VerifyTaskProcessGroup unexpected error on fake meta: %v", err)
	}
	if matchedFake {
		t.Errorf("expected VerifyTaskProcessGroup to be false for wrong metadata")
	}

	// 5. 終結整組孤兒進程
	if err := KillProcessGroup(pgid); err != nil {
		t.Fatalf("KillProcessGroup failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// 6. 進程組完全死後，IsProcessGroupAlive 應為 false，VerifyTaskProcessGroup 應返回 (false, nil)
	if IsProcessGroupAlive(pgid) {
		t.Errorf("expected process group to be dead after KillProcessGroup")
	}
	matchedDead, err := VerifyTaskProcessGroup(pgid, meta, taskID)
	if err != nil {
		t.Fatalf("VerifyTaskProcessGroup unexpected error on dead group: %v", err)
	}
	if matchedDead {
		t.Errorf("expected VerifyTaskProcessGroup to be false for dead group")
	}
}


