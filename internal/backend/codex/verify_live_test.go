package codex

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"asura/internal/backend"
	"asura/internal/backend/verification"
)

// 2026-10-21：issue-73 Phase 1；2026-10-22 改走 verification.Verify，與 asura runtime verify 相同的放行路徑：
// 未驗證的已安裝版本只透過暫時紀錄放行。binary 非空時改用該執行檔（失效對照組的 shim）。
func liveCodex(t *testing.T, binary string, only ...string) []verification.Result {
	t.Helper()
	if !verification.LiveEnabled() {
		t.Skip("set ASURA_VERIFY_LIVE=1 to run model-calling probes")
	}
	if binary == "" {
		real, err := codexBinary()
		if err != nil {
			t.Skip("codex not installed")
		}
		binary = real
	}
	spec, results, err := verification.Verify(context.Background(), func(records string, onStarted func(context.Context, int)) backend.Backend {
		return NewAdapter(AdapterConfig{BinaryPath: binary, VerificationPath: records, OnProcessStarted: onStarted})
	}, only...)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Builtin {
		t.Skipf("%s %s is a built-in verified identity; Verify runs no probes", spec.Record.Runtime, spec.Record.Version)
	}
	summary, _ := verification.Summary(results)
	t.Logf("identity %+v builtin=%v\nresults:\n%s", spec.Record.Identity, spec.Builtin, summary)
	return results
}

// ASURA_VERIFY_CODEX_BIN 指定另一份未驗證的安裝做對照（內建身分不執行 probe）。
func codexBinary() (string, error) {
	if p := os.Getenv("ASURA_VERIFY_CODEX_BIN"); p != "" {
		return p, nil
	}
	return exec.LookPath("codex")
}

func TestVerifyLive_Codex(t *testing.T) {
	if _, ok := verification.Summary(liveCodex(t, "")); !ok {
		t.Error("codex probes failed")
	}
}

// 2026-10-27：PR #77 審查 Q17，只跑不經模型的唯讀沙箱 probe（Codex 新版本的完整 probe 目前因 #74 失敗）。
func TestVerifyLive_CodexSandbox(t *testing.T) {
	if _, ok := verification.Summary(liveCodex(t, "", verification.ProbeSandbox)); !ok {
		t.Error("codex read-only sandbox probe failed")
	}
}

// 失效對照組：shim 把 read-only 換成 workspace-write、把 --approve-for-me 換成完全不設沙箱，
// codex sandbox 的 :read-only 換成 :workspace（2026-10-27），對應的 probe 必須失敗，證明 probe 抓得到沙箱失效。
func TestVerifyLive_CodexControls(t *testing.T) {
	if !verification.LiveEnabled() {
		t.Skip("set ASURA_VERIFY_LIVE=1 to run model-calling probes")
	}
	real, err := codexBinary()
	if err != nil {
		t.Skip("codex not installed")
	}
	shim, err := verification.WriteShim(t.TempDir(), "codex", real, `for a; do shift
  case "$a" in
    read-only) set -- "$@" workspace-write ;;
    :read-only) set -- "$@" :workspace ;;
    --approve-for-me) set -- "$@" --dangerously-bypass-approvals-and-sandbox ;;
    *) set -- "$@" "$a" ;;
  esac
done
exec "$REAL" "$@"`)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range liveCodex(t, shim, verification.ProbeReadOnly, verification.ProbeSandbox, verification.ProbeWorkspace) {
		if r.Err == nil || !strings.Contains(r.Err.Error(), "security violation") {
			t.Errorf("control %s did not detect the degraded sandbox: %v", r.Name, r.Err)
		}
	}
}

// 2026-10-28：issue-74 SCN-006（#73 TBD-7），read-only-sandbox 驗的是 codex sandbox -P :read-only；
// 正式任務的 exec -s read-only 交給 codex-linux-sandbox 的權限設定必須與它相同，workspace 也不可寫。
func TestVerifyLive_CodexExecReadOnlyProfile(t *testing.T) {
	if !verification.LiveEnabled() {
		t.Skip("set ASURA_VERIFY_LIVE=1 to run model-calling probes")
	}
	bin, err := codexBinary()
	if err != nil {
		t.Skip("codex not installed")
	}
	ws := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	n := 3000 + int(time.Now().UnixNano()%5000)

	direct := strconv.Itoa(n)
	sb := exec.CommandContext(ctx, bin, "sandbox", "-P", ":read-only", "-C", ws, "--", "sleep", direct)
	sb.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := sb.Start(); err != nil {
		t.Fatal(err)
	}
	want := sandboxProfile(ctx, t, direct)
	_ = backend.KillProcessTree(sb.Process.Pid)
	_ = sb.Wait()

	b, _, err := verification.Prepare(ctx, t.TempDir(), func(records string) backend.Backend {
		return NewAdapter(AdapterConfig{BinaryPath: bin, VerificationPath: records})
	})
	if err != nil {
		t.Fatal(err)
	}
	viaExec := strconv.Itoa(n + 1)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = b.Run(runCtx, &backend.ExecutionRequest{Role: backend.RoleOracle, Workspace: ws,
			Prompt: "Use your shell tool to run exactly this command once, without modification, then reply DONE. " +
				"If the command is blocked, reply BLOCKED and do not try anything else.\n\nsleep " + viaExec})
	}()
	got := sandboxProfile(ctx, t, viaExec)
	stop()
	<-done

	var w, g any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("codex sandbox profile: %v: %s", err, want)
	}
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("exec profile: %v: %s", err, got)
	}
	t.Logf("codex sandbox -P :read-only: %s\nexec -s read-only:          %s", want, got)
	if !reflect.DeepEqual(w, g) {
		t.Error("exec -s read-only passes a different permission profile than codex sandbox -P :read-only")
	}
	if strings.Contains(got, `"access":"write"`) {
		t.Error("exec -s read-only grants write access")
	}
}

// sandboxProfile 等到執行 sleep marker 的 codex-linux-sandbox 出現，回傳它的 --permission-profile。
func sandboxProfile(ctx context.Context, t *testing.T, marker string) string {
	t.Helper()
	for ctx.Err() == nil {
		entries, _ := os.ReadDir("/proc")
		for _, e := range entries {
			data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
			if err != nil {
				continue
			}
			args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
			if filepath.Base(args[0]) != "codex-linux-sandbox" || !strings.Contains(args[len(args)-1], "sleep "+marker) && args[len(args)-1] != marker {
				continue
			}
			for i, a := range args[:len(args)-1] {
				if a == "--permission-profile" {
					return args[i+1]
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no codex-linux-sandbox ran sleep %s", marker)
	return ""
}
