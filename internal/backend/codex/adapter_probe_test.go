package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func requireLiveCodex(t *testing.T, expectedVersion string) string {
	t.Helper()
	p, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex binary not found in PATH; skipping live contract probe")
	}
	out, err := exec.Command(p, "--version").Output()
	if err != nil {
		t.Skipf("codex --version failed: %v; skipping live contract probe", err)
	}
	verStr := strings.TrimSpace(string(out))
	if expectedVersion != "" && !strings.Contains(verStr, expectedVersion) {
		t.Skipf("codex version mismatch (expected %s, got %s); skipping live contract probe", expectedVersion, verStr)
	}
	return p
}

// TestCodexProbe_BinaryVersion 驗證本機已安裝固定版本 codex-cli 0.155.0
func TestCodexProbe_BinaryVersion(t *testing.T) {
	binPath := requireLiveCodex(t, "0.155.0")
	cmd := exec.Command(binPath, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to execute 'codex --version': %v", err)
	}
	verStr := strings.TrimSpace(string(out))
	t.Logf("codex version output: %s", verStr)
	if !strings.Contains(verStr, "0.155.0") {
		t.Errorf("expected codex version to contain '0.155.0', got %q", verStr)
	}
}

// TestCodexProbe_NonInteractive_JSONL 驗證 codex exec --json 輸出符合預期之 JSONL 事件串流
func TestCodexProbe_NonInteractive_JSONL(t *testing.T) {
	binPath := requireLiveCodex(t, "0.155.0")
	tmpDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, "exec",
		"--json",
		"--ephemeral",
		"--skip-git-repo-check",
		"-C", tmpDir,
		"Respond with exactly the word 'PONG'",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex exec failed: %v, output: %s", err, string(out))
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	foundThreadStarted := false
	foundTurnStarted := false
	foundTurnCompleted := false
	foundPong := false

	for scanner.Scan() {
		line := scanner.Text()
		var evt map[string]interface{}
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			// 可能夾帶非 JSON 的日誌行，略過
			continue
		}
		typ, _ := evt["type"].(string)
		switch typ {
		case "thread.started":
			foundThreadStarted = true
		case "turn.started":
			foundTurnStarted = true
		case "turn.completed":
			foundTurnCompleted = true
		case "item.completed":
			if item, ok := evt["item"].(map[string]interface{}); ok {
				if txt, ok := item["text"].(string); ok {
					if strings.Contains(strings.ToUpper(txt), "PONG") {
						foundPong = true
					}
				}
			}
		}
	}

	if !foundThreadStarted {
		t.Errorf("expected thread.started event in JSONL output")
	}
	if !foundTurnStarted {
		t.Errorf("expected turn.started event in JSONL output")
	}
	if !foundTurnCompleted {
		t.Errorf("expected turn.completed event in JSONL output")
	}
	if !foundPong {
		t.Errorf("expected response to contain 'PONG', raw output: %s", string(out))
	}
}

// TestCodexProbe_ReadOnlySandbox 驗證 -s read-only 模式能確實阻止檔案寫入副作用 (MBACK-008, MBACK-002)
func TestCodexProbe_ReadOnlySandbox(t *testing.T) {
	requireLiveCodex(t, "0.155.0")

	tmpDir := t.TempDir()
	targetFile := filepath.Join(tmpDir, "forbidden.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "codex", "exec",
		"--json",
		"--ephemeral",
		"--skip-git-repo-check",
		"-C", tmpDir,
		"-s", "read-only",
		"Create a file named forbidden.txt containing secret",
	)

	out, err := cmd.CombinedOutput()
	t.Logf("read-only output: %s", string(out))

	if ctx.Err() != nil {
		t.Fatalf("command timed out: %v", ctx.Err())
	}
	if len(out) == 0 && err != nil {
		t.Fatalf("codex process failed to produce output: %v", err)
	}

	// 驗證目標檔案絕對不可被建立
	if _, statErr := os.Stat(targetFile); statErr == nil {
		t.Fatalf("security violation: forbidden.txt was created under read-only sandbox!")
	}
}

// TestCodexProbe_WorkspaceWriteSandbox 驗證 --approve-for-me 模式允許 workspace 寫入 (Striker 角色)
func TestCodexProbe_WorkspaceWriteSandbox(t *testing.T) {
	requireLiveCodex(t, "0.155.0")

	tmpDir := t.TempDir()
	targetFile := filepath.Join(tmpDir, "allowed.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "codex", "exec",
		"--json",
		"--ephemeral",
		"--skip-git-repo-check",
		"-C", tmpDir,
		"--approve-for-me",
		"Create a file named allowed.txt containing 'ALLOWED_PAYLOAD'",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workspace write failed: %v, output: %s", err, string(out))
	}

	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("expected allowed.txt to be created, err: %v, output: %s", err, string(out))
	}
	if !strings.Contains(string(content), "ALLOWED_PAYLOAD") {
		t.Errorf("expected file content to contain 'ALLOWED_PAYLOAD', got %q", string(content))
	}
}

// TestCodexProbe_ContextCancellation 驗證 Context Cancel 時能迅速回收進程樹 (MBACK-007)
func TestCodexProbe_ContextCancellation(t *testing.T) {
	requireLiveCodex(t, "0.155.0")

	tmpDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "codex", "exec",
		"--json",
		"--ephemeral",
		"--skip-git-repo-check",
		"-C", tmpDir,
		"Run a bash command that sleeps for 60 seconds",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start codex: %v", err)
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("failed to get pgid: %v", err)
	}

	// 確保進程已啟動
	time.Sleep(300 * time.Millisecond)

	// 觸發取消並向進程組發送信號
	cancel()
	_ = syscall.Kill(-pgid, syscall.SIGKILL)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-done:
		// 正常終止
	case <-time.After(3 * time.Second):
		t.Fatalf("codex process group did not terminate within 3s after cancellation")
	}

	time.Sleep(100 * time.Millisecond)
	// 確認進程組無殘留
	if err := syscall.Kill(-pgid, 0); err == nil {
		t.Fatalf("codex process group %d is still alive after cancellation", pgid)
	}
}

