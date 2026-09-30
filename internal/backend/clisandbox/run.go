// 2026-09-30：新 CLI adapter 共用的 Linux 隔離程序邊界。
package clisandbox

import (
	"asura/internal/backend"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type Authentication struct {
	Payload []byte
	Expiry  time.Time
	Secrets []string
}

type PinnedFile struct{ Path, SHA256 string }

type Config struct {
	PinnedFiles                       []PinnedFile
	PinnedTrees                       []PinnedFile
	AuthReadOnly                      bool
	ID, Version, Home, AuthTarget     string
	Auth                              func(context.Context) (Authentication, error)
	Program                           []string
	ReadOnlyPaths                     []string
	Settings                          []byte
	Args                              func(string) []string
	Parse                             func(string, string) (*backend.ExecutionResult, error)
	OnProcessStarted, OnProcessExited func(context.Context, int)
	BeforeStart                       func(context.Context) (func(), error)
}

// VerifyFile 2026-09-30：僅接受真實 probe 的固定檔案，不執行未驗 binary。
func VerifyFile(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	if fmt.Sprintf("%x", h.Sum(nil)) != want {
		return errors.New("binary differs from fixed contract")
	}
	return nil
}

func Run(ctx context.Context, req *backend.ExecutionRequest, cfg Config) (*backend.ExecutionResult, error) {
	result := &backend.ExecutionResult{BackendID: cfg.ID, BackendVersion: cfg.Version, StartedAt: time.Now()}
	fail := func(class backend.FailureClassification, err error) (*backend.ExecutionResult, error) {
		result.FailureClass = class
		result.CompletedAt = time.Now()
		return result, backend.NewExecutionError(cfg.ID, class, err, result.PartialOutput)
	}
	if req == nil {
		return fail(backend.FailureExecution, errors.New("execution request cannot be nil"))
	}
	if req.Role != backend.RoleStriker {
		return fail(backend.FailureUnsupported, backend.ErrUnsupportedCapability)
	}
	if ctx.Err() != nil {
		return fail(backend.FailureCancelled, ctx.Err())
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return fail(backend.FailureUnavailable, err)
	}
	if cfg.Auth == nil || cfg.Parse == nil || cfg.Args == nil || len(cfg.Program) == 0 {
		return fail(backend.FailureUnavailable, errors.New("incomplete adapter configuration"))
	}
	workspace, err := filepath.Abs(req.Workspace)
	if err != nil {
		return fail(backend.FailureExecution, err)
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return fail(backend.FailureExecution, err)
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return fail(backend.FailureExecution, errors.New("workspace must be a directory"))
	}
	// 2026-09-30：工作區不可覆蓋被遮蔽的主機登入或固定 runtime。
	for _, path := range append([]string{cfg.Home}, cfg.ReadOnlyPaths...) {
		rel, e := filepath.Rel(workspace, path)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fail(backend.FailureUnsupported, errors.New("workspace overlaps protected runtime or HOME"))
		}
	}
	for _, path := range cfg.ReadOnlyPaths {
		rel, e := filepath.Rel(path, workspace)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fail(backend.FailureUnsupported, errors.New("workspace overlaps protected runtime"))
		}
	}
	probe := exec.CommandContext(ctx, bwrap, "--unshare-pid", "--unshare-ipc", "--ro-bind", "/", "/", "--proc", "/proc", "--die-with-parent", "--", "/usr/bin/true")
	if err = probe.Run(); err != nil {
		if ctx.Err() != nil {
			return fail(backend.FailureCancelled, ctx.Err())
		}
		return fail(backend.FailureUnavailable, errors.New("bwrap namespaces unavailable"))
	}
	auth, err := cfg.Auth(ctx)
	expiry := auth.Expiry
	if err != nil {
		if ctx.Err() != nil {
			return fail(backend.FailureCancelled, ctx.Err())
		}
		return fail(backend.FailureAuth, err)
	}
	if !expiry.After(time.Now()) {
		return fail(backend.FailureAuth, errors.New("CLI login expired; update login in the original CLI"))
	}
	fd, err := unix.MemfdCreate("asura-cli-auth", unix.MFD_CLOEXEC)
	if err != nil {
		return fail(backend.FailureUnavailable, err)
	}
	authFile := os.NewFile(uintptr(fd), "asura-cli-auth")
	defer authFile.Close()
	if _, err = authFile.Write(auth.Payload); err != nil {
		return fail(backend.FailureUnavailable, err)
	}
	if _, err = authFile.Seek(0, io.SeekStart); err != nil {
		return fail(backend.FailureUnavailable, err)
	}
	root, err := os.MkdirTemp("", "asura-cli-")
	if err != nil {
		return fail(backend.FailureUnavailable, err)
	}
	defer os.RemoveAll(root)

	pinnedTrees := make(map[string]string)
	for i, tree := range cfg.PinnedTrees {
		path := filepath.Join(root, fmt.Sprintf("tree-%d", i))
		hash, e := SnapshotTree(tree.Path, path)
		if e != nil || hash != tree.SHA256 {
			return fail(backend.FailureUnavailable, errors.New("dependency snapshot differs from fixed contract"))
		}
		pinnedTrees[tree.Path] = path
	}
	pinned := make(map[string]string, len(cfg.PinnedFiles))
	for i, file := range cfg.PinnedFiles {
		source, e := os.Open(file.Path)
		if e != nil {
			return fail(backend.FailureUnavailable, e)
		}
		path := filepath.Join(root, fmt.Sprintf("runtime-%d", i))
		copy, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0500)
		if e != nil {
			source.Close()
			return fail(backend.FailureUnavailable, e)
		}
		digest := sha256.New()
		_, e = io.Copy(io.MultiWriter(copy, digest), source)
		source.Close()
		closeErr := copy.Close()
		if e != nil || closeErr != nil || fmt.Sprintf("%x", digest.Sum(nil)) != file.SHA256 {
			return fail(backend.FailureUnavailable, errors.New("runtime snapshot differs from fixed contract"))
		}
		pinned[file.Path] = path
	}
	if err = os.WriteFile(filepath.Join(root, "config.json"), cfg.Settings, 0600); err != nil {
		return fail(backend.FailureUnavailable, err)
	}
	args := []string{"--unshare-pid", "--unshare-ipc", "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp", "--tmpfs", "/var/tmp", "--tmpfs", "/run/user", "--tmpfs", cfg.Home}
	for _, path := range cfg.ReadOnlyPaths {
		args = append(args, "--dir", filepath.Dir(path), "--ro-bind", path, path)
	}
	authBind := "--bind-data"
	if cfg.AuthReadOnly {
		authBind = "--ro-bind-data"
	}
	args = append(args, "--dir", filepath.Dir(cfg.AuthTarget), authBind, "3", cfg.AuthTarget, "--bind", root, root, "--bind", workspace, workspace, "--die-with-parent", "--chdir", workspace, "--clearenv", "--setenv", "HOME", cfg.Home, "--setenv", "PATH", "/usr/bin:/bin", "--")

	// 2026-09-30：overlay 在可寫 scratch mount 之後；封住原路徑與副本別名。
	mountEnd := 0
	for i, arg := range args {
		if arg == "--die-with-parent" {
			mountEnd = i
			break
		}
	}
	mountArgs := append([]string(nil), args[:mountEnd]...)
	for _, tree := range cfg.PinnedTrees {
		path := pinnedTrees[tree.Path]
		mountArgs = append(mountArgs, "--ro-bind", path, path, "--ro-bind", path, tree.Path)
	}
	for _, file := range cfg.PinnedFiles {
		path := pinned[file.Path]
		mountArgs = append(mountArgs, "--ro-bind", path, path, "--ro-bind", path, file.Path)
	}
	args = append(mountArgs, args[mountEnd:]...)
	args = append(args, cfg.Program...)
	args = append(args, cfg.Args(filepath.Join(root, "config.json"))...)
	// 2026-09-30：到期即終止快照執行，不讓模型或背景工具跨越登入期限。
	runCtx, cancel := context.WithDeadline(ctx, expiry)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bwrap, args...)
	cmd.ExtraFiles = []*os.File{authFile}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	var unlock func()
	if cfg.BeforeStart != nil {
		unlock, err = cfg.BeforeStart(runCtx)
		if err != nil {
			if ctx.Err() == nil && runCtx.Err() != nil {
				return fail(backend.FailureAuth, errors.New("CLI login expired before execution"))
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return fail(backend.FailureCancelled, err)
			}
			return fail(backend.FailureExecution, err)
		}
	}
	release := func() {
		if unlock != nil {
			unlock()
			unlock = nil
		}
	}
	defer release()
	if ctx.Err() != nil {
		return fail(backend.FailureCancelled, ctx.Err())
	}
	if runCtx.Err() != nil {
		return fail(backend.FailureAuth, errors.New("CLI login expired before execution"))
	}
	if err = cmd.Start(); err != nil {
		return fail(backend.FailureUnavailable, err)
	}
	pgid := cmd.Process.Pid
	if cfg.OnProcessStarted != nil {
		cfg.OnProcessStarted(ctx, pgid)
	}
	release()
	waitErr := cmd.Wait()
	if cfg.OnProcessExited != nil {
		cfg.OnProcessExited(ctx, pgid)
	}
	rawOut, rawErr := stdout.String(), stderr.String()
	for _, secret := range auth.Secrets {
		if secret != "" {
			rawOut = strings.ReplaceAll(rawOut, secret, "<redacted>")
			rawErr = strings.ReplaceAll(rawErr, secret, "<redacted>")
		}
	}
	parsed, parseErr := cfg.Parse(rawOut, rawErr)
	if parsed != nil {
		result.Output = parsed.Output
		result.PartialOutput = parsed.PartialOutput
		result.Events = parsed.Events
		result.Usage = parsed.Usage
		result.UsageSource = parsed.UsageSource
		result.FailureClass = parsed.FailureClass
	}
	result.CompletedAt = time.Now()
	if ctx.Err() != nil {
		result.Output = ""
		return fail(backend.FailureCancelled, ctx.Err())
	}
	if runCtx.Err() != nil {
		result.Output = ""
		return fail(backend.FailureAuth, errors.New("CLI login expired during execution"))
	}
	if waitErr != nil && result.FailureClass == backend.FailureNone {
		result.Output = ""
		return fail(backend.FailureExecution, errors.New("CLI exited unsuccessfully"))
	}
	if parseErr != nil {
		return fail(result.FailureClass, parseErr)
	}
	return result, nil
}
