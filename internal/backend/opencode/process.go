package opencode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

// Process 代表一個被管理且擁有獨立進程組 (PGID) 的子進程
type Process struct {
	cmd       *exec.Cmd
	pgid      int
	stdout    io.ReadCloser
	stderr    io.ReadCloser
	done      chan struct{}
	closeOnce sync.Once
}

// ConfigureProcessGroup 為命令配置獨立進程組 (PGID = PID)
func ConfigureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// KillProcessGroup 向指定 PGID 發送 SIGKILL，徹底清除整個進程樹
func KillProcessGroup(pgid int) error {
	if pgid <= 0 {
		return errors.New("invalid pgid")
	}
	err := syscall.Kill(-pgid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		// 進程組已完全退出
		return nil
	}
	return err
}

// IsProcessGroupAlive 探測指定進程組是否仍有任何存活成員
func IsProcessGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil
}

// IsTaskProcessGroup 驗證指定進程組是否確實屬於該任務，防止 PID 重用導致誤殺不相關程序 (修復 J-01)
func IsTaskProcessGroup(pgid int, taskID string) bool {
	if pgid <= 0 || taskID == "" {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pgid))
	if err != nil {
		return false
	}
	expected := []byte(fmt.Sprintf("ASURA_TASK_ID=%s", taskID))
	for _, entry := range bytes.Split(data, []byte{0}) {
		if bytes.Equal(entry, expected) {
			return true
		}
	}
	return false
}

// GetProcessStartTime 讀取指定 PID 的核心啟動時間戳記（/proc/<pid>/stat 欄位 22），不受受控命令環境變數竄改影響 (修復 K-01)
func GetProcessStartTime(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("invalid pid")
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	lastIdx := bytes.LastIndexByte(data, ')')
	if lastIdx == -1 || lastIdx+2 >= len(data) {
		return "", errors.New("malformed /proc/stat")
	}
	fields := strings.Fields(string(data[lastIdx+2:]))
	// fields[0] 為 state (第 3 欄)，starttime 為第 22 欄，故索引為 22 - 3 = 19
	if len(fields) <= 19 {
		return "", errors.New("insufficient fields in /proc/stat")
	}
	return fields[19], nil
}

// VerifyTaskProcess 嚴格進行三態進程身分檢驗：
// - (true, nil): 確認進程存在且身分（StartTime 或 TaskID 標記）吻合，確屬該任務。
// - (false, nil): 確認進程不存在，或身分明確不匹配（PID 重用）。
// - (false, err): 遭遇權限不足或其他系統錯誤，無法確認身分。
func VerifyTaskProcess(pid int, expectedStartTime string, taskID string) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	st, err := GetProcessStartTime(pid)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// 進程已不存在，確認已結束
			return false, nil
		}
		// 無法確認身分，回傳錯誤（防禦 K-01）
		return false, err
	}

	// 若有記錄 expectedStartTime，以核心不可變 StartTime 比對
	if expectedStartTime != "" {
		if st == expectedStartTime {
			return true, nil
		}
		// StartTime 不一致，確認 PID 已被系統重新分配給不相關工作（防禦 J-01）
		return false, nil
	}

	// 若無 StartTime 元資料，回退檢查環境變數 ASURA_TASK_ID
	if taskID != "" && IsTaskProcessGroup(pid, taskID) {
		return true, nil
	}

	return false, nil
}

// StartProcess 在獨立進程組中啟動指定可執行檔，並在 ctx 取消時強殺整組 PGID
func StartProcess(ctx context.Context, name string, args []string, dir string) (*Process, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}

	ConfigureProcessGroup(cmd)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdoutPipe.Close()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		return nil, err
	}

	pid := cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		// 若無法獲取 PGID，退化以 PID 為 PGID
		pgid = pid
	}

	proc := &Process{
		cmd:    cmd,
		pgid:   pgid,
		stdout: stdoutPipe,
		stderr: stderrPipe,
		done:   make(chan struct{}),
	}

	// 監聽 Context 取消，若觸發則發送信號直達整組 PGID
	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				_ = KillProcessGroup(proc.pgid)
			case <-proc.done:
			}
		}()
	}

	return proc, nil
}

// PGID 返回此進程所屬的進程組 ID
func (p *Process) PGID() int {
	return p.pgid
}

// PID 返回主進程 PID
func (p *Process) PID() int {
	if p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

// Stdout 返回標準輸出串流
func (p *Process) Stdout() io.Reader {
	return p.stdout
}

// Stderr 返回標準錯誤串流
func (p *Process) Stderr() io.Reader {
	return p.stderr
}

// Wait 等待進程執行完畢並釋放資源
func (p *Process) Wait() error {
	defer p.closeOnce.Do(func() {
		close(p.done)
	})
	return p.cmd.Wait()
}

// Kill 主動強殺整個進程組
func (p *Process) Kill() error {
	defer p.closeOnce.Do(func() {
		close(p.done)
	})
	return KillProcessGroup(p.pgid)
}
