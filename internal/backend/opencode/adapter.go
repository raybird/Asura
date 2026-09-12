package opencode

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

var (
	// ErrNoBinary 代表找不到合格的 OpenCode binary
	ErrNoBinary = errors.New("opencode binary not found")
	// ErrExecutionFailed 代表 OpenCode 執行非零退出且無可恢復輸出
	ErrExecutionFailed = errors.New("opencode execution failed")
	// ErrEmptyResult 代表執行未產生任何有效文字
	ErrEmptyResult = errors.New("opencode produced empty result")
)

// AdapterConfig 定義 OpenCode Adapter 的執行設定
type AdapterConfig struct {
	BinaryPath       string
	ExpectedVersion  string
	ConfigDir        string
	Permission       string // JSON string for OPENCODE_PERMISSION
	DisableAuto      bool
	OnProcessStarted func(ctx context.Context, pgid int)
	OnProcessExited  func(ctx context.Context, pgid int)
}

// Adapter 負責調度固定版本 OpenCode CLI 執行 AI 任務並解析事件串流
type Adapter struct {
	cfg AdapterConfig
}

// ValidateOpenCodeVersion 檢查二進位檔版本是否符合預期
func ValidateOpenCodeVersion(binPath string, expectedVersion string) error {
	out, err := exec.Command(binPath, "--version").Output()
	if err != nil {
		return fmt.Errorf("execute opencode --version: %w", err)
	}
	ver := strings.TrimSpace(string(out))
	if expectedVersion != "" && !strings.HasPrefix(ver, expectedVersion) {
		return fmt.Errorf("unsupported opencode version: expected %s, got %s", expectedVersion, ver)
	}
	return nil
}

// ResolveOpenCodeBinary 解析並驗證 OpenCode 二進位檔路徑與版本
func ResolveOpenCodeBinary(preferredPath string, expectedVersion string) (string, error) {
	if preferredPath != "" {
		if err := ValidateOpenCodeVersion(preferredPath, expectedVersion); err != nil {
			return "", err
		}
		return preferredPath, nil
	}

	// 1. 優先檢查隔離安裝目錄
	home, err := os.UserHomeDir()
	if err == nil {
		isolatedBin := filepath.Join(home, ".cache/asura-probe-issue-2/install/node_modules/.bin/opencode")
		if _, err := os.Stat(isolatedBin); err == nil {
			if vErr := ValidateOpenCodeVersion(isolatedBin, expectedVersion); vErr == nil {
				return isolatedBin, nil
			}
		}
	}

	// 2. 檢查 PATH
	pathBin, err := exec.LookPath("opencode")
	if err == nil {
		vErr := ValidateOpenCodeVersion(pathBin, expectedVersion)
		if vErr == nil {
			return pathBin, nil
		}
		return "", fmt.Errorf("opencode found in PATH but version invalid: %w", vErr)
	}

	return "", fmt.Errorf("%w: valid opencode %s not found in probe path or PATH", ErrNoBinary, expectedVersion)
}

// NewAdapter 建立新的 OpenCode Adapter 實例
func NewAdapter(cfg AdapterConfig) *Adapter {
	if cfg.ExpectedVersion == "" {
		cfg.ExpectedVersion = "1.18.23"
	}
	if cfg.BinaryPath == "" {
		if resolved, err := ResolveOpenCodeBinary("", cfg.ExpectedVersion); err == nil {
			cfg.BinaryPath = resolved
		}
	}
	return &Adapter{cfg: cfg}
}

// ExtractResultFromEvents 從 JSON 事件串流中解析並萃取最終回答文字或錯誤
func (a *Adapter) ExtractResultFromEvents(output string) (string, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "", ErrEmptyResult
	}

	var sb strings.Builder
	var lastErr string
	hasEvents := false

	scanner := bufio.NewScanner(strings.NewReader(trimmed))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		evt, err := ParseEventLine(line)
		if err != nil || evt == nil {
			continue
		}
		hasEvents = true

		if evt.Text != "" {
			if sb.Len() > 0 && !strings.HasSuffix(sb.String(), "\n") {
				sb.WriteString("\n")
			}
			sb.WriteString(evt.Text)
		}

		if evt.IsRateLimit {
			lastErr = "rate limit exceeded"
		}
		if evt.Type == "error" && evt.ErrorMessage != "" {
			lastErr = evt.ErrorMessage
		}
	}

	result := strings.TrimSpace(sb.String())
	// 遵循審查建議：保留 partial output + terminal error，避免先收到 text、後收到 429/error 時被判成成功
	if lastErr != "" {
		return result, fmt.Errorf("backend error: %s", lastErr)
	}

	if result != "" {
		return result, nil
	}

	if !hasEvents {
		// 非 JSON 格式，保留原始標準輸出
		return trimmed, nil
	}

	return "", ErrEmptyResult
}

// Run 執行自然語言任務，解析事件串流並回傳文字結果 (INT-001, INT-007)
// 自然語言 prompt 直接作為參數傳入，不經過 shell 解譯。
func (a *Adapter) Run(ctx context.Context, workspace string, prompt string) (string, error) {
	expectedVer := a.cfg.ExpectedVersion
	if expectedVer == "" {
		expectedVer = "1.18.23"
	}

	binPath, err := ResolveOpenCodeBinary(a.cfg.BinaryPath, expectedVer)
	if err != nil {
		return "", err
	}

	args := []string{"run", "--format", "json", "--dir", workspace}
	if !a.cfg.DisableAuto {
		// 依 Phase 1 探測與核准決策：在核准專案內允許自動修改/建置/測試
		args = append(args, "--auto")
	}
	// 自然語言 prompt 作為獨立參數傳遞，絕對不經由 sh -c
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, binPath, args...)
	ConfigureProcessGroup(cmd)

	// 配置環境變數，包含權限控制與隔離設定
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "OPENCODE_DISABLE_AUTOUPDATE=1")
	if a.cfg.ConfigDir != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("OPENCODE_CONFIG_DIR=%s", a.cfg.ConfigDir))
	}
	if a.cfg.Permission != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("OPENCODE_PERMISSION=%s", a.cfg.Permission))
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start opencode process: %w", err)
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid
	}

	if a.cfg.OnProcessStarted != nil {
		a.cfg.OnProcessStarted(ctx, pgid)
	}

	// 支援 Context 取消時發送信號並清理進程組
	doneChan := make(chan struct{})
	var closeOnce sync.Once
	defer closeOnce.Do(func() { close(doneChan) })

	go func() {
		select {
		case <-ctx.Done():
			_ = KillProcessGroup(pgid)
		case <-doneChan:
		}
	}()

	waitErr := cmd.Wait()
	closeOnce.Do(func() { close(doneChan) })

	if a.cfg.OnProcessExited != nil {
		a.cfg.OnProcessExited(ctx, pgid)
	}

	rawStdout := stdoutBuf.String()
	extracted, extractErr := a.ExtractResultFromEvents(rawStdout)

	if waitErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return extracted, ctx.Err()
		}
		// 若已有部分抽取文字，保留之，同時回傳錯誤
		if extracted != "" {
			return extracted, fmt.Errorf("%w: %v", ErrExecutionFailed, waitErr)
		}
		errMsg := strings.TrimSpace(stderrBuf.String())
		if errMsg == "" {
			errMsg = waitErr.Error()
		}
		return "", fmt.Errorf("%w: %s", ErrExecutionFailed, errMsg)
	}

	if extractErr != nil {
		// 若抽取失敗但有原始輸出，回傳原始輸出
		if rawStdout != "" {
			return rawStdout, nil
		}
		return "", extractErr
	}

	return extracted, nil
}
