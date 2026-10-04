package backend

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type probeOutput struct {
	bytes.Buffer
	overflow bool
}

func (w *probeOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := MaxRuntimeVersionBytes - w.Len()
	if len(p) > remaining {
		w.overflow = true
		p = p[:remaining]
	}
	w.Buffer.Write(p)
	return n, nil
}

// 2026-10-04：版本探測只保留有界 stdout，stderr 不進 audit；整個進程組可取消。
func ProbeVersionCommand(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
		return "", &ProbeError{Reason: ExecutableNotFound}
	}
	var out probeOutput
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	err = cmd.Run()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil || out.overflow {
		return "", &ProbeError{Reason: VersionFailed}
	}
	version := strings.TrimSpace(out.String())
	if !validVersion(version) {
		return "", &ProbeError{Reason: VersionFailed}
	}
	return version, nil
}
