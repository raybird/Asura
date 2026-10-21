package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"asura/internal/backend"
)

var (
	// ErrNoBinary 代表找不到合格的 Codex binary
	ErrNoBinary = errors.New("codex binary not found")
	// ErrVersionMismatch 代表 Codex binary 版本不符合固定契約
	ErrVersionMismatch = errors.New("unsupported codex version")
	// ErrExecutionFailed 代表 Codex 執行非零退出
	ErrExecutionFailed = errors.New("codex execution failed")
)

// AdapterConfig 代表 Codex 適配器配置
type AdapterConfig struct {
	BeforeStart      func(context.Context) (func(), error)
	BinaryPath       string
	ExpectedVersion  string
	VerificationPath string
	OnProcessStarted func(ctx context.Context, pgid int)
	OnProcessExited  func(ctx context.Context, pgid int)
}

// Adapter 負責調度 Codex CLI 執行任務 (MBACK-002, MBACK-007)
type Adapter struct {
	cfg AdapterConfig
}

// 確保 Adapter 實作 backend.Backend 介面
var _ backend.Backend = (*Adapter)(nil)

// NewAdapter 建立新的 Codex Adapter 實例
func NewAdapter(cfg AdapterConfig) *Adapter {
	if cfg.ExpectedVersion == "" {
		cfg.ExpectedVersion = "0.155.0"
	}
	if cfg.BinaryPath == "" {
		if path, err := exec.LookPath("codex"); err == nil {
			cfg.BinaryPath = path
		}
	}
	return &Adapter{cfg: cfg}
}

// ID 回傳後端唯一標識
func (a *Adapter) ID() string {
	return "codex"
}

// Version 回傳目前 Codex 版本
func (a *Adapter) Version(ctx context.Context) (string, error) {
	binPath := a.cfg.BinaryPath
	if binPath == "" {
		p, err := exec.LookPath("codex")
		if err != nil {
			return "", fmt.Errorf("%w: codex not found in PATH", ErrNoBinary)
		}
		binPath = p
	}
	out, err := exec.CommandContext(ctx, binPath, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("execute codex --version: %w", err)
	}
	ver := strings.TrimSpace(string(out))
	return ver, nil
}

// NormalizeCodexVersion 正規化 Codex 版本字串，去除前綴 "codex-cli " 與前後空白
func NormalizeCodexVersion(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "codex-cli ")
	return strings.TrimSpace(raw)
}

// ValidateVersion 校驗目前 Codex binary：內建 ExpectedVersion 精確比對，其他版本需本機驗證紀錄（issue-73）
func (a *Adapter) ValidateVersion(ctx context.Context) error {
	raw, err := a.Version(ctx)
	if err != nil {
		return err
	}
	if err := a.admit(raw, a.ProbeExecutable()); err != nil {
		return fmt.Errorf("%w: %v", ErrVersionMismatch, err)
	}
	return nil
}

// Capabilities 回傳 Codex CLI 支援的能力清單 (MBACK-008)
func (a *Adapter) Capabilities() []backend.Capability {
	return []backend.Capability{
		backend.CapReadOnlySandbox,
		backend.CapWorkspaceWrite,
		backend.CapStructuredEvents,
		backend.CapProcessCancellation,
	}
}

// HasCapability 檢查後端是否具備指定能力
func (a *Adapter) HasCapability(cap backend.Capability) bool {
	for _, c := range a.Capabilities() {
		if c == cap {
			return true
		}
	}
	return false
}

// Run 實作 backend.Backend 介面，以 non-interactive 方式執行 codex exec (MBACK-002, MBACK-006, MBACK-007)
func (a *Adapter) Run(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	if req == nil {
		return nil, errors.New("execution request cannot be nil")
	}

	startedAt := time.Now()

	// 嚴格校驗版本 (MUST FIX 4)：版本不符時立即終止，0 執行進程調用
	if a.cfg.ExpectedVersion != "" {
		if err := a.ValidateVersion(ctx); err != nil {
			return &backend.ExecutionResult{
				BackendID:    "codex",
				FailureClass: backend.FailureUnavailable,
				StartedAt:    startedAt,
				CompletedAt:  time.Now(),
			}, backend.NewExecutionError("codex", backend.FailureUnavailable, err, "")
		}
	}

	binPath := a.cfg.BinaryPath
	if binPath == "" {
		p, err := exec.LookPath("codex")
		if err != nil {
			return &backend.ExecutionResult{
				BackendID:    "codex",
				FailureClass: backend.FailureUnavailable,
				StartedAt:    startedAt,
				CompletedAt:  time.Now(),
			}, backend.NewExecutionError("codex", backend.FailureUnavailable, ErrNoBinary, "")
		}
		binPath = p
	}

	args := []string{
		"exec",
		"--json",
		"--ephemeral",
		"--skip-git-repo-check",
	}

	if req.Workspace != "" {
		args = append(args, "-C", req.Workspace)
	}

	// 依角色設定沙箱邊界：Oracle / Critic 嚴格唯讀，Striker 允許 workspace-write，未知角色 fail-closed
	switch req.Role {
	case backend.RoleOracle, backend.RoleCritic:
		args = append(args, "-s", "read-only")
	case backend.RoleStriker:
		args = append(args, "--approve-for-me")
	default:
		return nil, fmt.Errorf("unsupported backend role: %q", req.Role)
	}

	args = append(args, req.Prompt)

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// 2026-10-04：完成版本/準備後，在真正啟動前握手取消與進程身分鎖。
	var releaseStart func()
	if a.cfg.BeforeStart != nil {
		var startErr error
		releaseStart, startErr = a.cfg.BeforeStart(ctx)
		if startErr != nil {
			return nil, startErr
		}
	}
	release := func() {
		if releaseStart != nil {
			releaseStart()
			releaseStart = nil
		}
	}
	defer release()
	if err := cmd.Start(); err != nil {
		return &backend.ExecutionResult{
			BackendID:    "codex",
			FailureClass: backend.FailureExecution,
			StartedAt:    startedAt,
			CompletedAt:  time.Now(),
		}, backend.NewExecutionError("codex", backend.FailureExecution, fmt.Errorf("start codex process: %w", err), "")
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid
	}

	if a.cfg.OnProcessStarted != nil {
		a.cfg.OnProcessStarted(ctx, pgid)
	}
	release()

	// 進程回收監控：Context 取消時發送 SIGKILL 至整組進程組 (MBACK-007)
	doneChan := make(chan struct{})
	var closeOnce sync.Once
	defer closeOnce.Do(func() { close(doneChan) })

	go func() {
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		case <-doneChan:
		}
	}()

	waitErr := cmd.Wait()
	closeOnce.Do(func() { close(doneChan) })

	if a.cfg.OnProcessExited != nil {
		a.cfg.OnProcessExited(ctx, pgid)
	}

	rawStdout := stdoutBuf.String()
	rawStderr := stderrBuf.String()
	completedAt := time.Now()

	ver, _ := a.Version(ctx)
	output, partialOutput, events, failureClass, usage, parseErr := ExtractResultAndEvents(rawStdout, rawStderr)

	usageSource := backend.ProvenanceUnknown
	if usage != nil {
		usageSource = backend.ProvenanceExact
	}

	result := &backend.ExecutionResult{
		BackendID:      "codex",
		BackendVersion: ver,
		Output:         output,
		PartialOutput:  partialOutput,
		Events:         events,
		FailureClass:   failureClass,
		Usage:          usage,
		UsageSource:    usageSource,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
	}

	// 檢查 Context 取消
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.FailureClass = backend.FailureCancelled
		return result, backend.NewExecutionError("codex", backend.FailureCancelled, ctx.Err(), partialOutput)
	}

	if waitErr != nil && failureClass == backend.FailureNone {
		result.FailureClass = backend.FailureExecution
		errMsg := strings.TrimSpace(rawStderr)
		if errMsg == "" {
			errMsg = waitErr.Error()
		}
		return result, backend.NewExecutionError("codex", backend.FailureExecution, fmt.Errorf("%w: %s", ErrExecutionFailed, errMsg), partialOutput)
	}

	if parseErr != nil {
		return result, backend.NewExecutionError("codex", failureClass, parseErr, partialOutput)
	}

	return result, nil
}
