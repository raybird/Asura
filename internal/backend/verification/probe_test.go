package verification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
// inGroup 時命令跑在 runtime 的 process group 裡，group 被殺時前景命令一起結束，只有命令自己另開 session 的子行程留下。
type cancelBackend struct {
	shellBackend
	started   chan int
	inProcess bool
	inGroup   bool
	leaked    []*exec.Cmd
	commands  []string
}

// cleanup 只清除這個 backend 執行過的命令留下的 sleep，不碰同一台機器上的其他行程（2026-10-28 PR #82 審查）。
func (b *cancelBackend) cleanup() {
	for _, c := range b.leaked {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		_ = c.Wait()
	}
	for _, command := range b.commands {
		for _, m := range regexp.MustCompile(`sleep (\d+)`).FindAllStringSubmatch(command, -1) {
			for _, pid := range sleepPIDs(m[1]) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
}

func (b *cancelBackend) Run(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	runtime := exec.Command("sleep", "600")
	runtime.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := runtime.Start(); err != nil {
		return nil, err
	}
	b.started <- runtime.Process.Pid
	_, command, _ := strings.Cut(req.Prompt, "\n\n")
	b.commands = append(b.commands, command)
	if b.inProcess {
		_, rest, ok := strings.Cut(command, "> ")
		target, _, _ := strings.Cut(rest, " ")
		if err := os.WriteFile(target, []byte("started"), 0o600); !ok || err != nil {
			_ = syscall.Kill(-runtime.Process.Pid, syscall.SIGKILL)
			_ = runtime.Wait()
			return nil, fmt.Errorf("built-in shell found no start marker in %q", command)
		}
	} else {
		cmd := exec.Command("sh", "-c", command)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if b.inGroup {
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: runtime.Process.Pid}
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		if b.inGroup {
			// 像 runtime 自己的 shell，結束後立即回收，不讓殭屍行程留在 group 裡。
			go func() { _ = cmd.Wait() }()
		} else {
			b.leaked = append(b.leaked, cmd)
		}
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
	t.Cleanup(leaky.cleanup)
	if err := run(leaky); err == nil || !strings.Contains(err.Error(), "survived cancellation") {
		t.Errorf("leaked command: err = %v, want process tree survived", err)
	}

	// 2026-10-28：issue-74 SCN-004，只回收 process group 的 runtime 留下命令另開 session 的背景子行程，probe 必須失敗。
	groupOnly := &cancelBackend{started: make(chan int, 1), inGroup: true}
	t.Cleanup(groupOnly.cleanup)
	if err := run(groupOnly); err == nil || !strings.Contains(err.Error(), "survived cancellation") {
		t.Errorf("detached background command: err = %v, want process tree survived", err)
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

// fakeSandbox 模擬 runtime 自己的 OS 沙箱：ran 為 false 時命令沒有執行，blocks 為 false 時唯讀也擋不住寫入，
// writes 為 false 時 workspace-write 也寫不進去。
type fakeSandbox struct{ ran, blocks, writes, echo bool }

func (f fakeSandbox) run(ctx context.Context, workspace string, readOnly bool, command ...string) (string, error) {
	if f.echo {
		return "error: cannot start: " + strings.Join(command, " "), errors.New("exit status 2")
	}
	if !f.ran {
		return "unknown permission profile", errors.New("exit status 2")
	}
	if (readOnly && f.blocks) || (!readOnly && !f.writes) {
		return sandboxMarker + "\nsh: Read-only file system", errors.New("exit status 2")
	}
	out, err := exec.CommandContext(ctx, command[0], command[1:]...).CombinedOutput()
	return string(out), err
}

// 2026-10-27：PR #77 審查 Q17（SCN-005），唯讀沙箱 probe 不經模型：沙箱正常時 Codex 的模型在唯讀角色下不會嘗試寫入，
// 唯讀 probe 分不出 OS 沙箱是否真的生效。唯讀模式下命令必須確實執行且寫不進 workspace，workspace-write 下必須寫得進去。
func TestSandboxProbe(t *testing.T) {
	cases := []struct {
		name string
		box  fakeSandbox
		want string
	}{
		{"sandbox holds", fakeSandbox{ran: true, blocks: true, writes: true}, ""},
		{"read-only sandbox lets the write through", fakeSandbox{ran: true, writes: true}, "security violation"},
		{"command never ran", fakeSandbox{}, "inconclusive"},
		// 2026-10-28：第二輪審查 NICE 3，啟動失敗時把命令參數印出來的 runner 不能被當成命令有執行。
		{"runner only echoes the command", fakeSandbox{echo: true}, "inconclusive"},
		{"workspace-write cannot write either", fakeSandbox{ran: true, blocks: true}, "positive control"},
	}
	for _, c := range cases {
		var probe Probe
		for _, p := range Probes(Options{ReadOnlyRoles: true, ReadOnlySandbox: c.box.run}) {
			if p.Name == ProbeSandbox {
				probe = p
			}
		}
		if probe.Run == nil {
			t.Fatal("read-only sandbox probe not registered")
		}
		err := runOne(context.Background(), nil, t.TempDir(), probe)
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	for _, p := range Probes(Options{ReadOnlyRoles: true}) {
		if p.Name == ProbeSandbox {
			t.Error("sandbox probe registered without a sandbox runner")
		}
	}
}
