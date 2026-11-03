package opencode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"asura/internal/backend"
)

// requireBwrap 在主機無法建立 bwrap namespace 時跳過；寫入角色的沙箱只能以真實 bwrap 驗證。
func requireBwrap(t *testing.T) {
	t.Helper()
	path, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap unavailable")
	}
	if err := exec.Command(path, "--unshare-pid", "--ro-bind", "/", "/", "--", "/usr/bin/true").Run(); err != nil {
		t.Skip("bwrap namespaces unavailable")
	}
}

// sandboxRoot 建立不在 /tmp 底下的測試根目錄，並把 HOME 指到其中，讓 HOME overlay 與真實執行的掛載方式相同。
func sandboxRoot(t *testing.T) (root, home string) {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err = os.MkdirTemp(cache, "asura-opencode-sandbox-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	home = filepath.Join(root, "home")
	for _, dir := range []string{home + "/.config/opencode", home + "/.cache/opencode", root + "/ws", root + "/sibling", root + "/bin"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	return root, home
}

// runFakeStriker 以寫入角色執行假 opencode；script 是 --version 以外的命令內容。
func runFakeStriker(t *testing.T, ctx context.Context, root, script string) (*backend.ExecutionResult, error) {
	t.Helper()
	bin := filepath.Join(root, "bin", "opencode")
	content := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 1.18.31; exit 0; fi\n" + script +
		"\necho '{\"type\":\"text\",\"timestamp\":1,\"sessionID\":\"ses_sandbox\",\"part\":{\"type\":\"text\",\"text\":\"ok\"}}'\n"
	if err := os.WriteFile(bin, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	adapter := NewAdapter(AdapterConfig{BinaryPath: bin, ExpectedVersion: "1.18.31", Role: RoleStriker})
	return adapter.Run(ctx, &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: filepath.Join(root, "ws"), Prompt: "p"})
}

// 2026-11-03：issue-75 SCN-001、SCN-002，寫回主機的只有 workspace 與 OpenCode 的 data、state 目錄。
func TestStrikerSandbox_OnlyWorkspaceAndOpenCodeDataReachHost(t *testing.T) {
	requireBwrap(t)
	root, home := sandboxRoot(t)
	tmpFile := filepath.Join(os.TempDir(), filepath.Base(root)+".txt")
	t.Cleanup(func() { os.Remove(tmpFile) })
	outside := []string{
		filepath.Join(root, "sibling", "outside.txt"),
		filepath.Join(home, "home.txt"),
		filepath.Join(home, ".config", "opencode", "config.txt"),
		filepath.Join(home, ".cache", "opencode", "cache.txt"),
		filepath.Join(root, "outside-home.txt"),
		tmpFile,
	}
	inside := []string{
		filepath.Join(root, "ws", "inside.txt"),
		filepath.Join(home, ".local", "share", "opencode", "data.txt"),
		filepath.Join(home, ".local", "state", "opencode", "state.txt"),
	}
	script := ""
	for _, f := range append(append([]string{}, inside...), outside...) {
		script += fmt.Sprintf("mkdir -p %q 2>/dev/null; echo x > %q 2>/dev/null\n", filepath.Dir(f), f)
	}
	if _, err := runFakeStriker(t, context.Background(), root, script); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, f := range inside {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("write should reach host: %s: %v", f, err)
		}
	}
	for _, f := range outside {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("wrote outside the workspace: %s", f)
		}
	}
}

// 2026-11-03：issue-75 SCN-007，沙箱內看不到主機 /run 底下的 socket，名稱解析設定仍可讀。
func TestStrikerSandbox_HidesHostRunSockets(t *testing.T) {
	requireBwrap(t)
	root, _ := sandboxRoot(t)
	hidden := []string{"/run/docker.sock", "/var/run/docker.sock", fmt.Sprintf("/run/user/%d", os.Getuid())}
	visible := 0
	for _, p := range hidden {
		if _, err := os.Stat(p); err == nil {
			visible++
		}
	}
	if visible == 0 {
		t.Skip("host has none of the sockets to hide")
	}
	report := filepath.Join(root, "ws", "report.txt")
	script := ""
	for _, p := range hidden {
		script += fmt.Sprintf("[ -e %q ] && echo 'visible %s' >> %q\n", p, p, report)
	}
	script += fmt.Sprintf("cat /etc/resolv.conf >/dev/null 2>&1 || echo 'resolv.conf unreadable' >> %q\n", report)
	sentinel := filepath.Join(root, "ws", "checked")
	script += fmt.Sprintf("echo ok > %q\n", sentinel)
	if _, err := runFakeStriker(t, context.Background(), root, script); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("checks did not run in the workspace: %v", err)
	}
	if data, err := os.ReadFile(report); err == nil {
		t.Errorf("sandbox exposes host paths:\n%s", data)
	}
}

// 2026-11-03：issue-75 SCN-003（#74 TBD-1），取消前就已脫離行程樹的命令被 namespace 的 init 接手，取消時一併回收。
// 行程內取消與外部取消（KillProcessGroup，asura cancel 走的路徑）都要涵蓋。
func TestStrikerSandbox_CancelReapsCommandsDetachedBeforeCancel(t *testing.T) {
	requireBwrap(t)
	for i, external := range []bool{false, true} {
		name := map[bool]string{false: "in-process", true: "external"}[external]
		t.Run(name, func(t *testing.T) {
			root, _ := sandboxRoot(t)
			detached := strconv.Itoa((40000+os.Getpid()%9000)*10 + 7 + i)
			t.Cleanup(func() {
				for _, pid := range markerSleepPIDs(detached)[detached] {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			})
			started := filepath.Join(root, "ws", "started")
			// 子 shell 結束後，setsid 的 sleep 已不是 opencode 的子孫；它不持有 opencode 的 stdout，與 bash 工具啟動的命令相同。
			script := fmt.Sprintf("( setsid sleep %s </dev/null >/dev/null 2>&1 & ) ; echo ok > %q ; exec sleep 300", detached, started)
			pgids := make(chan int, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bin := filepath.Join(root, "bin", "opencode")
			content := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 1.18.31; exit 0; fi\n" + script + "\n"
			if err := os.WriteFile(bin, []byte(content), 0o755); err != nil {
				t.Fatal(err)
			}
			adapter := NewAdapter(AdapterConfig{BinaryPath: bin, ExpectedVersion: "1.18.31", Role: RoleStriker,
				OnProcessStarted: func(_ context.Context, pgid int) { pgids <- pgid }})
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = adapter.Run(ctx, &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: filepath.Join(root, "ws"), Prompt: "p"})
			}()
			var pgid int
			select {
			case pgid = <-pgids:
			case <-done:
				t.Fatal("Run returned before starting opencode")
			}
			ready := func(found map[string][]int) bool {
				_, err := os.Stat(started)
				return err == nil && len(found[detached]) == 1
			}
			if found := waitMarkers(t, 5*time.Second, ready, detached); !ready(found) {
				t.Fatalf("detached command did not start: %v", found)
			}
			if external {
				if err := KillProcessGroup(pgid); err != nil {
					t.Fatalf("KillProcessGroup: %v", err)
				}
			} else {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("Run did not return after cancellation")
			}
			if found := waitMarkers(t, 5*time.Second, func(f map[string][]int) bool { return len(f) == 0 }, detached); len(found) != 0 {
				t.Errorf("command detached before cancellation survived: %v", found)
			}
			if IsProcessGroupAlive(pgid) {
				t.Errorf("process group %d still alive", pgid)
			}
		})
	}
}

// 2026-11-03：issue-75 SCN-004，找不到 bwrap 時整個 OpenCode 判為 dependency_not_found；
// bwrap 存在但無法建立 namespace 時，寫入角色在啟動 opencode 前以 unavailable 失敗，兩者都不在沙箱外執行。
func TestStrikerSandbox_FailsClosedWithoutBwrap(t *testing.T) {
	root, _ := sandboxRoot(t)
	ws := filepath.Join(root, "ws")
	marker := filepath.Join(ws, "executed")
	bin := filepath.Join(root, "bin", "opencode")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n[ \"$1\" = --version ] && { echo 1.18.23; exit 0; }\n: > "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(config, "opencode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "opencode", "opencode.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	read := NewAdapter(AdapterConfig{BinaryPath: bin, ConfigDir: config, Isolated: true, DisableAuto: true, Pure: true, DisableProjectConfig: true})
	write := NewAdapter(AdapterConfig{BinaryPath: bin, Role: RoleStriker})
	logical := NewRoleRoutingBackend(read, write)
	runStriker := func() {
		t.Helper()
		res, err := logical.Run(context.Background(), &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: ws, Prompt: "p"})
		if err == nil || res == nil || res.FailureClass != backend.FailureUnavailable {
			t.Errorf("striker Run: res=%+v err=%v", res, err)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Error("opencode was executed outside the sandbox")
		}
	}

	t.Setenv("PATH", filepath.Join(root, "bin"))
	s, err := backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "opencode", Backend: logical}})
	if err != nil || s.Entries[0].ReasonCode != backend.DependencyNotFound {
		t.Errorf("inventory without bwrap: %+v %v", s, err)
	}
	runStriker()

	fake := filepath.Join(root, "fakebwrap")
	if err := os.MkdirAll(fake, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fake, "bwrap"), []byte("#!/bin/sh\necho 'bwrap: setting up uid map: Permission denied' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fake)
	runStriker()
	// bwrap 的錯誤訊息要帶進失敗原因，否則使用者看不出是版本還是 namespace 的問題（PR #86 審查 M1）。
	if _, err := logical.Run(context.Background(), &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: ws, Prompt: "p"}); err == nil || !strings.Contains(err.Error(), "setting up uid map") {
		t.Errorf("striker error does not carry bwrap stderr: %v", err)
	}
}

// 2026-11-03：issue-75 SCN-004（Q7），HOME overlay 需要 bwrap 0.10 以上；舊版 bwrap 時整個 OpenCode 判為 dependency_not_found。
func TestStrikerSandbox_InventoryRequiresOverlayCapableBwrap(t *testing.T) {
	root, _ := sandboxRoot(t)
	bin := filepath.Join(root, "bin")
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\necho 1.18.23\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	for version, want := range map[string]backend.ProbeReason{
		"bubblewrap 0.9.0":  backend.DependencyNotFound,
		"bubblewrap 0.10.0": backend.ProbeOK,
		"bubblewrap 0.11.1": backend.ProbeOK,
		"garbage":           backend.DependencyNotFound,
	} {
		if err := os.WriteFile(filepath.Join(bin, "bwrap"), []byte("#!/bin/sh\necho '"+version+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		logical := NewRoleRoutingBackend(NewAdapter(AdapterConfig{Role: RoleOracle}), NewAdapter(AdapterConfig{Role: RoleStriker}))
		s, err := backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "opencode", Backend: logical}})
		if err != nil || s.Entries[0].ReasonCode != want {
			t.Errorf("%s: reason=%v err=%v, want %s", version, s.Entries[0].ReasonCode, err, want)
		}
	}
}

// 2026-11-03：issue-75 SCN-008（Q8），workspace 等於或包含 HOME、/tmp、/run 時，掛載會蓋掉 overlay 或遮蔽；寫入角色拒絕執行。
func TestStrikerSandbox_RejectsWorkspaceCoveringMaskedPaths(t *testing.T) {
	requireBwrap(t)
	root, home := sandboxRoot(t)
	marker := filepath.Join(home, "executed")
	bin := filepath.Join(root, "bin", "opencode")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n[ \"$1\" = --version ] && { echo 1.18.31; exit 0; }\n: > "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	adapter := NewAdapter(AdapterConfig{BinaryPath: bin, ExpectedVersion: "1.18.31", Role: RoleStriker})
	// 遮蔽目錄用字面路徑各測一筆，不受 TMPDIR 影響；/ 同時蓋住 HOME，不能代表其他遮蔽目錄（PR #86 第二輪審查 N-A）。
	for _, ws := range []string{home, root, "/", "/tmp", "/var/tmp", "/run"} {
		res, err := adapter.Run(context.Background(), &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: ws, Prompt: "p"})
		if err == nil || res == nil || res.FailureClass != backend.FailureUnsupported {
			t.Errorf("workspace %s: res=%+v err=%v", ws, res, err)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Errorf("workspace %s: opencode was executed", ws)
			os.Remove(marker)
		}
	}
}

// linkBwrap 把主機的 bwrap 連進 dir：OpenCode 寫入角色只在 bwrap 內執行，PATH 只放假 runtime 的測試也要找得到它（issue-75）。
func linkBwrap(t *testing.T, dir string) {
	t.Helper()
	path, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap unavailable")
	}
	if err := os.Symlink(path, filepath.Join(dir, "bwrap")); err != nil {
		t.Fatal(err)
	}
}
