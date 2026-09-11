package opencode

import (
	"context"
	"errors"
	"io"
	"os/exec"
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
