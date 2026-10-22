package verification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"asura/internal/backend"
)

// shellBackend 執行 probe prompt 裡的 shell 命令；sandbox 不為空時，把 credential 目錄改寫到該處，模擬沙箱內的 tmpfs HOME。
type shellBackend struct {
	credential, sandbox string
	refuse              bool
}

func (b *shellBackend) ID() string                              { return "shell" }
func (b *shellBackend) Version(context.Context) (string, error) { return "1", nil }
func (b *shellBackend) Capabilities() []backend.Capability      { return nil }
func (b *shellBackend) HasCapability(backend.Capability) bool   { return false }
func (b *shellBackend) Run(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	if b.refuse {
		return &backend.ExecutionResult{Output: "BLOCKED"}, nil
	}
	_, command, _ := strings.Cut(req.Prompt, "\n\n")
	if b.sandbox != "" {
		command = strings.ReplaceAll(command, b.credential, b.sandbox)
	}
	out, err := exec.CommandContext(ctx, "sh", "-c", command).CombinedOutput()
	return &backend.ExecutionResult{Output: string(out) + "DONE"}, err
}

// 2026-10-21：issue-73 Task 1.3，憑證邊界 probe 只在憑證目錄真的被寫到時失敗，模型沒執行命令時判為無法判定。
func TestCredentialProbe(t *testing.T) {
	run := func(b *shellBackend) error {
		t.Helper()
		var probe Probe
		for _, p := range Probes(Options{CredentialDir: b.credential}) {
			if p.Name == ProbeCredential {
				probe = p
			}
		}
		if probe.Run == nil {
			t.Fatal("credential probe not registered")
		}
		before, _ := os.ReadDir(b.credential)
		err := runOne(context.Background(), b, t.TempDir(), probe)
		if after, _ := os.ReadDir(b.credential); len(after) != len(before) {
			t.Errorf("probe left files in the credential directory: %v", after)
		}
		return err
	}
	if err := run(&shellBackend{credential: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "security violation") {
		t.Errorf("writable credential directory: err = %v, want security violation", err)
	}
	sandbox := t.TempDir()
	if err := run(&shellBackend{credential: t.TempDir(), sandbox: sandbox}); err != nil {
		t.Errorf("sandboxed credential directory: %v", err)
	}
	if err := run(&shellBackend{credential: t.TempDir(), refuse: true}); err == nil || !strings.Contains(err.Error(), "positive control") {
		t.Errorf("command not executed: err = %v, want positive control failure", err)
	}
	if entries, _ := os.ReadDir(sandbox); len(entries) == 0 {
		t.Error("sandboxed run never wrote the redirected sentinel")
	}
}

// cancelBackend 模擬 runtime 的行程：runtime 本身是一個以 pgid 回報的 sleep 600。
// inProcess 時像 OMP 的內建 shell，自己寫出 prompt 命令的啟動標記、不產生 sleep 行程；
// 否則以 sh 在另一個 session 執行命令（像 codex-linux-sandbox），取消時只殺 runtime 的 process group。
type cancelBackend struct {
	shellBackend
	started   chan int
	inProcess bool
	leaked    []*exec.Cmd
}

func (b *cancelBackend) Run(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	runtime := exec.Command("sleep", "600")
	runtime.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := runtime.Start(); err != nil {
		return nil, err
	}
	b.started <- runtime.Process.Pid
	_, command, _ := strings.Cut(req.Prompt, "\n\n")
	if b.inProcess {
		_, rest, ok := strings.Cut(command, "> ")
		target, _, _ := strings.Cut(rest, ";")
		if err := os.WriteFile(strings.TrimSpace(target), []byte("started"), 0o600); !ok || err != nil {
			_ = syscall.Kill(-runtime.Process.Pid, syscall.SIGKILL)
			_ = runtime.Wait()
			return nil, fmt.Errorf("built-in shell found no start marker in %q", command)
		}
	} else {
		cmd := exec.Command("sh", "-c", command)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		b.leaked = append(b.leaked, cmd)
	}
	<-ctx.Done()
	_ = syscall.Kill(-runtime.Process.Pid, syscall.SIGKILL)
	_ = runtime.Wait()
	return &backend.ExecutionResult{FailureClass: backend.FailureCancelled}, ctx.Err()
}

// 2026-10-22：issue-73 Task 1.3，取消 probe 以命令寫出的啟動標記判定命令已開始，不依賴看得到獨立的 sleep 行程；
// 模型啟動的行程在取消後存活時仍必須失敗。
func TestCancelProbe(t *testing.T) {
	run := func(b *cancelBackend) error {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for _, p := range Probes(Options{Started: b.started}) {
			if p.Name == ProbeCancel {
				return runOne(ctx, b, t.TempDir(), p)
			}
		}
		t.Fatal("cancellation probe not registered")
		return nil
	}
	if err := run(&cancelBackend{started: make(chan int, 1), inProcess: true}); err != nil {
		t.Errorf("in-process shell: %v", err)
	}
	leaky := &cancelBackend{started: make(chan int, 1)}
	t.Cleanup(func() {
		for _, c := range leaky.leaked {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
			_ = c.Wait()
		}
	})
	if err := run(leaky); err == nil || !strings.Contains(err.Error(), "survived cancellation") {
		t.Errorf("leaked command: err = %v, want process tree survived", err)
	}
}

// readOnlyBackend 模擬唯讀角色：reads 時讀出 workspace 內的 nonce 檔並放進回覆，writes 時沙箱失效、寫入目標檔。
type readOnlyBackend struct {
	shellBackend
	reads, writes bool
	err           error
}

func (b *readOnlyBackend) Run(_ context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	if b.err != nil {
		return &backend.ExecutionResult{FailureClass: backend.FailureAuth}, b.err
	}
	reply := "BLOCKED"
	if b.reads {
		entries, _ := os.ReadDir(req.Workspace)
		for _, e := range entries {
			if data, err := os.ReadFile(filepath.Join(req.Workspace, e.Name())); err == nil {
				reply = string(data) + "\n" + reply
			}
		}
	}
	if b.writes {
		_, command, _ := strings.Cut(req.Prompt, "without modification: ")
		command, _, _ = strings.Cut(command, "\n")
		if out, err := exec.Command("sh", "-c", command).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("%v: %s", err, out)
		}
	}
	return &backend.ExecutionResult{Output: reply}, nil
}

// 2026-10-22：issue-73 Task 3.1（SCN-005），唯讀 probe 的正向對照：模型必須在同一個唯讀回合讀出 Asura 放在
// workspace 的 nonce，否則「沒有寫入」可能只是模型沒做事，不能當成唯讀成立。
func TestReadOnlyProbe(t *testing.T) {
	var probe Probe
	for _, p := range Probes(Options{ReadOnlyRoles: true}) {
		if p.Name == ProbeReadOnly {
			probe = p
		}
	}
	if probe.Run == nil {
		t.Fatal("read-only probe not registered")
	}
	cases := []struct {
		name string
		b    *readOnlyBackend
		want string
	}{
		{"sandbox holds", &readOnlyBackend{reads: true}, ""},
		{"read-only role can write", &readOnlyBackend{reads: true, writes: true}, "security violation"},
		{"model did nothing", &readOnlyBackend{}, "positive control"},
		{"runtime failed", &readOnlyBackend{err: errors.New("login expired")}, "inconclusive"},
	}
	for _, c := range cases {
		err := runOne(context.Background(), c.b, t.TempDir(), probe)
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}
