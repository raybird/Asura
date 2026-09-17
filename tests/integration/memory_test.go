package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/storage"
)

// P1-T2 固定多語文字真相來源 (Single Source of Truth)
const (
	fixedMemoryContentA = "專案A固定核心記憶：不可洩漏給B (Project Alpha Core Memory: Confidential - 2026-09-14)"
	fixedMemoryContentB = "專案B固定核心記憶：不可洩漏給A (Project Beta Core Memory: Top Secret - 2026-09-14)"
)

// memoryFixture 封裝 P1-T2.a 要求之真實 IPC 測試環境：
// 同一 root 下 A/B 專案、A 子目錄 C、有效別名與越界符號連結，
// 以及具備計數功能的受控 runner 與真實 Daemon / Client。
type memoryFixture struct {
	rootDir     string
	projA       string
	projB       string
	subDirC     string
	aliasA      string
	outsideDir  string
	escapedLink string
	dbPath      string
	sockPath    string
	store       *storage.SQLiteStore
	policy      *auth.Policy
	daemon      *daemon.Daemon
	client      *daemon.Client
	runnerCalls *int32
}

func setupMemoryFixture(t *testing.T) *memoryFixture {
	t.Helper()

	rootDir := t.TempDir()
	outsideDir := t.TempDir()

	projA := filepath.Join(rootDir, "projectA")
	projB := filepath.Join(rootDir, "projectB")
	if err := os.MkdirAll(projA, 0755); err != nil {
		t.Fatalf("failed to create projA: %v", err)
	}
	if err := os.MkdirAll(projB, 0755); err != nil {
		t.Fatalf("failed to create projB: %v", err)
	}

	subDirC := filepath.Join(projA, "subC")
	if err := os.MkdirAll(subDirC, 0755); err != nil {
		t.Fatalf("failed to create subDirC: %v", err)
	}

	aliasA := filepath.Join(rootDir, "aliasA")
	if err := os.Symlink(projA, aliasA); err != nil {
		t.Fatalf("failed to create symlink aliasA -> projA: %v", err)
	}

	escapedLink := filepath.Join(rootDir, "escaped")
	if err := os.Symlink(outsideDir, escapedLink); err != nil {
		t.Fatalf("failed to create escaped symlink: %v", err)
	}

	dbPath := filepath.Join(rootDir, "asura_memory_test.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	policy := auth.NewPolicy([]string{rootDir})

	var runnerCalls int32
	testRunner := func(ctx context.Context, workspace string, prompt string) (string, error) {
		atomic.AddInt32(&runnerCalls, 1)
		return "done: " + prompt, nil
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(testRunner))
	sockPath := filepath.Join(rootDir, "asura_mem.sock")

	d := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
	}, store, policy, daemon.WithEngine(eng))

	if err := d.Start(); err != nil {
		_ = store.Close()
		t.Fatalf("failed to start daemon: %v", err)
	}

	waitForSocket(t, sockPath)

	client, err := daemon.NewClient(sockPath)
	if err != nil {
		_ = d.Close()
		_ = store.Close()
		t.Fatalf("failed to create client: %v", err)
	}

	f := &memoryFixture{
		rootDir:     rootDir,
		projA:       projA,
		projB:       projB,
		subDirC:     subDirC,
		aliasA:      aliasA,
		outsideDir:  outsideDir,
		escapedLink: escapedLink,
		dbPath:      dbPath,
		sockPath:    sockPath,
		store:       store,
		policy:      policy,
		daemon:      d,
		client:      client,
		runnerCalls: &runnerCalls,
	}

	t.Cleanup(func() {
		_ = client.Close()
		_ = d.Close()
		_ = store.Close()
	})

	return f
}

// TestP1T2a_FixtureAndRunnerBaseline (P1-T2.a 產出驗證)
// 驗證真實 IPC 測試 fixture 建立完成，且既有任務成功路徑使 runner 計數器可由 0 變 1。
func TestP1T2a_FixtureAndRunnerBaseline(t *testing.T) {
	f := setupMemoryFixture(t)

	// 初始 runner 計數器為 0
	if initial := atomic.LoadInt32(f.runnerCalls); initial != 0 {
		t.Fatalf("expected initial runnerCalls=0, got %d", initial)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	task, err := f.client.Submit(ctx, engine.SubmitRequest{
		Workspace: f.projA,
		Prompt:    "baseline runner verification task",
	})
	if err != nil {
		t.Fatalf("submit baseline task failed: %v", err)
	}
	if task == nil {
		t.Fatalf("expected task response, got nil")
	}

	// 輪詢等待任務完成
	deadline := time.Now().Add(3 * time.Second)
	var completed bool
	var lastTask *storage.Task
	for time.Now().Before(deadline) {
		tResp, err := f.client.GetTask(ctx, task.ID)
		if err == nil && tResp != nil {
			lastTask = tResp
			if tResp.Status == storage.StatusCompleted {
				completed = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !completed {
		t.Fatalf("timed out waiting for baseline task to complete, lastTask=%+v", lastTask)
	}

	// 驗證 runner 計數器確由 0 轉為 1
	if calls := atomic.LoadInt32(f.runnerCalls); calls != 1 {
		t.Fatalf("expected runnerCalls to transition from 0 to 1, got %d (lastTask=%+v)", calls, lastTask)
	}
}

// TestMEM002: 授權 root 內仍隔離不同專案 (MEM-002 外迴圈)
// Given 同一授權 root 下 A 與 B 各有不同的固定記憶內容
// When 在 A 列出記憶或以 B 的 ID 查詢及刪除
// Then 清單不含 B 的記憶且查詢及刪除拒絕
// And B 的記憶內容與筆數不變
func TestMEM002(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. 寫入 A 固定記憶
	memA, err := f.client.AddMemory(ctx, f.projA, fixedMemoryContentA)
	if err != nil {
		t.Fatalf("外迴圈行為紅燈 (功能尚缺)：在 A 寫入記憶失敗：%v (預期 unknown action: memory_add，待 P2-T1 與 P2-T2 實作)", err)
	}
	if memA == nil || memA.ID == "" {
		t.Fatalf("expected memA with non-empty ID, got %v", memA)
	}

	// 2. 寫入 B 固定記憶
	memB, err := f.client.AddMemory(ctx, f.projB, fixedMemoryContentB)
	if err != nil {
		t.Fatalf("外迴圈行為紅燈 (功能尚缺)：在 B 寫入記憶失敗：%v", err)
	}
	if memB == nil || memB.ID == "" {
		t.Fatalf("expected memB with non-empty ID, got %v", memB)
	}

	// 3. 在 A 列出記憶：清單不應包含 B 的記憶
	listA, err := f.client.ListMemories(ctx, f.projA)
	if err != nil {
		t.Fatalf("在 A 列出記憶失敗：%v", err)
	}
	for _, m := range listA {
		if m.ID == memB.ID || m.Content == fixedMemoryContentB {
			t.Fatalf("跨專案隔離漏洞 (隔離缺口)：A 的記憶清單包含 B 的記憶 ID=%s Content=%s", m.ID, m.Content)
		}
	}

	// 4. 在 A 以 B 的 ID 查詢：應被拒絕
	_, err = f.client.GetMemory(ctx, f.projA, memB.ID)
	if err == nil {
		t.Fatalf("跨專案存取漏洞 (隔離缺口)：在 A 成功查得 B 的記憶 ID=%s", memB.ID)
	}

	// 5. 在 A 以 B 的 ID 刪除：應被拒絕
	err = f.client.DeleteMemory(ctx, f.projA, memB.ID)
	if err == nil {
		t.Fatalf("跨專案刪除漏洞 (隔離缺口)：在 A 成功刪除 B 的記憶 ID=%s", memB.ID)
	}

	// 6. 驗證 B 的記憶內容與筆數不變
	bMem, err := f.client.GetMemory(ctx, f.projB, memB.ID)
	if err != nil {
		t.Fatalf("查詢 B 自身記憶失敗：%v", err)
	}
	if bMem.Content != fixedMemoryContentB {
		t.Fatalf("B 的記憶內容遭竄改：expected %q, got %q", fixedMemoryContentB, bMem.Content)
	}

	listB, err := f.client.ListMemories(ctx, f.projB)
	if err != nil {
		t.Fatalf("查詢 B 記憶清單失敗：%v", err)
	}
	if len(listB) != 1 {
		t.Fatalf("B 的記憶筆數改變：expected 1, got %d", len(listB))
	}
}

// TestMEM003: 撤權及越界拒絕 (MEM-003 外迴圈)
// Given 專案未授權、已撤權或輸入符號連結指向授權範圍外
// When 請求寫入、列出、查詢或刪除記憶
// Then 請求拒絕且不回傳受保護內容或變更資料
func TestMEM003(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. 越界符號連結 (指向 root 外部的 escapedLink)
	t.Run("越界符號連結操作拒絕", func(t *testing.T) {
		_, err := f.client.AddMemory(ctx, f.escapedLink, "越界寫入測試")
		if err == nil {
			t.Fatalf("安全漏洞 (隔離缺口)：越界符號連結寫入未被拒絕")
		}
		// 遵循 P1-T2.b 與假綠燈防範：unknown action 僅為功能尚缺，不得誤判為防護生效
		if strings.Contains(err.Error(), "unknown action") {
			t.Fatalf("外迴圈行為紅燈 (功能尚缺)：AddMemory 回應 %v，尚未實作記憶授權守門 (待 P2-T2 實作)", err)
		}

		_, err = f.client.ListMemories(ctx, f.escapedLink)
		if err == nil {
			t.Fatalf("安全漏洞 (隔離缺口)：越界符號連結列出未被拒絕")
		}
		if strings.Contains(err.Error(), "unknown action") {
			t.Fatalf("外迴圈行為紅燈 (功能尚缺)：ListMemories 回應 %v", err)
		}

		_, err = f.client.GetMemory(ctx, f.escapedLink, "dummy-id")
		if err == nil {
			t.Fatalf("安全漏洞 (隔離缺口)：越界符號連結查詢未被拒絕")
		}
		if strings.Contains(err.Error(), "unknown action") {
			t.Fatalf("外迴圈行為紅燈 (功能尚缺)：GetMemory 回應 %v", err)
		}

		err = f.client.DeleteMemory(ctx, f.escapedLink, "dummy-id")
		if err == nil {
			t.Fatalf("安全漏洞 (隔離缺口)：越界符號連結刪除未被拒絕")
		}
		if strings.Contains(err.Error(), "unknown action") {
			t.Fatalf("外迴圈行為紅燈 (功能尚缺)：DeleteMemory 回應 %v", err)
		}
	})

	// 2. 完全未授權的外部目錄 (outsideDir)
	t.Run("未授權外部目錄操作拒絕", func(t *testing.T) {
		_, err := f.client.AddMemory(ctx, f.outsideDir, "未授權寫入測試")
		if err == nil {
			t.Fatalf("安全漏洞 (隔離缺口)：未授權目錄寫入未被拒絕")
		}
		if strings.Contains(err.Error(), "unknown action") {
			t.Fatalf("外迴圈行為紅燈 (功能尚缺)：AddMemory 回應 %v，尚未實作記憶授權守門 (待 P2-T2 實作)", err)
		}

		_, err = f.client.ListMemories(ctx, f.outsideDir)
		if err == nil {
			t.Fatalf("安全漏洞 (隔離缺口)：未授權目錄列出未被拒絕")
		}
		if strings.Contains(err.Error(), "unknown action") {
			t.Fatalf("外迴圈行為紅燈 (功能尚缺)：ListMemories 回應 %v", err)
		}

		_, err = f.client.GetMemory(ctx, f.outsideDir, "dummy-id")
		if err == nil {
			t.Fatalf("安全漏洞 (隔離缺口)：未授權目錄查詢未被拒絕")
		}
		if strings.Contains(err.Error(), "unknown action") {
			t.Fatalf("外迴圈行為紅燈 (功能尚缺)：GetMemory 回應 %v", err)
		}

		err = f.client.DeleteMemory(ctx, f.outsideDir, "dummy-id")
		if err == nil {
			t.Fatalf("安全漏洞 (隔離缺口)：未授權目錄刪除未被拒絕")
		}
		if strings.Contains(err.Error(), "unknown action") {
			t.Fatalf("外迴圈行為紅燈 (功能尚缺)：DeleteMemory 回應 %v", err)
		}
	})
}

// TestMEM004: 精確 workspace 歸屬 (MEM-004 外迴圈)
// Given A 的有效別名路徑解析至 A 且 C 是 A 的子目錄
// When 分別以別名及 C 查詢 A 的記憶
// Then 別名可取得 A 的記憶而 C 不會繼承 A 的記憶
func TestMEM004(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. 在 A 寫入記憶
	memA, err := f.client.AddMemory(ctx, f.projA, fixedMemoryContentA)
	if err != nil {
		t.Fatalf("外迴圈行為紅燈 (功能尚缺)：在 A 寫入記憶失敗：%v (預期 unknown action: memory_add，待 P2-T1 與 P2-T2 實作)", err)
	}

	// 2. 以有效別名 aliasA 查詢：應可取得相同的記憶
	aliasMem, err := f.client.GetMemory(ctx, f.aliasA, memA.ID)
	if err != nil {
		t.Fatalf("別名查詢失敗：%v (預期別名應解析至相同專案 canonical scope)", err)
	}
	if aliasMem == nil || aliasMem.Content != fixedMemoryContentA {
		t.Fatalf("別名查詢內容不符：expected %q, got %v", fixedMemoryContentA, aliasMem)
	}

	// 3. 以子目錄 C 查詢 A 的記憶 ID：子目錄不得繼承父目錄記憶，應回傳錯誤
	_, err = f.client.GetMemory(ctx, f.subDirC, memA.ID)
	if err == nil {
		t.Fatalf("隔離漏洞：子目錄 C 不應繼承父目錄 A 的記憶，但成功取得記憶 ID=%s", memA.ID)
	}

	// 4. 以子目錄 C 列出記憶：清單應為空
	cList, err := f.client.ListMemories(ctx, f.subDirC)
	if err != nil {
		t.Fatalf("查詢子目錄 C 記憶清單失敗：%v", err)
	}
	if len(cList) != 0 {
		t.Fatalf("子目錄 C 繼承了父目錄記憶：expected 0 items, got %d", len(cList))
	}
}

// TestMEM007: 撤權保留資料並於重新授權後恢復操作 (MEM-007 外迴圈)
// Given A 有已保存記憶且其授權已撤除
// When 使用者嘗試讀取、寫入或刪除
// Then 全部操作拒絕且原記憶保持不變
// When A 重新獲得授權
// Then 可再次查得原記憶並執行授權範圍內的寫入與刪除
func TestMEM007(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. 在 A 寫入記憶
	memA, err := f.client.AddMemory(ctx, f.projA, fixedMemoryContentA)
	if err != nil {
		t.Fatalf("外迴圈行為紅燈 (功能尚缺)：在 A 寫入記憶失敗：%v (預期 unknown action: memory_add，待 P2-T1 與 P2-T2 實作)", err)
	}

	// 2. 模擬撤除 A 授權：關閉原 Daemon，啟動新 Daemon 但只授權 projB（撤除 projA）
	_ = f.client.Close()
	_ = f.daemon.Close()

	revokedPolicy := auth.NewPolicy([]string{f.projB})
	sockPath2 := filepath.Join(f.rootDir, "asura_mem_revoked.sock")
	daemonRevoked := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath2,
		MaxWorkers: 2,
	}, f.store, revokedPolicy)

	if err := daemonRevoked.Start(); err != nil {
		t.Fatalf("failed to start revoked daemon: %v", err)
	}
	defer daemonRevoked.Close()

	waitForSocket(t, sockPath2)
	clientRevoked, err := daemon.NewClient(sockPath2)
	if err != nil {
		t.Fatalf("failed to create client for revoked daemon: %v", err)
	}
	defer clientRevoked.Close()

	// 撤權期間所有操作皆應拒絕
	if _, err := clientRevoked.AddMemory(ctx, f.projA, "撤權中寫入"); err == nil {
		t.Fatalf("撤權安全漏洞：撤權專案寫入未被拒絕")
	}
	if _, err := clientRevoked.ListMemories(ctx, f.projA); err == nil {
		t.Fatalf("撤權安全漏洞：撤權專案列出未被拒絕")
	}
	if _, err := clientRevoked.GetMemory(ctx, f.projA, memA.ID); err == nil {
		t.Fatalf("撤權安全漏洞：撤權專案查詢未被拒絕")
	}
	if err := clientRevoked.DeleteMemory(ctx, f.projA, memA.ID); err == nil {
		t.Fatalf("撤權安全漏洞：撤權專案刪除未被拒絕")
	}

	// 3. 模擬重新授權：關閉撤權 Daemon，啟動重新授權 rootDir 的 Daemon
	_ = clientRevoked.Close()
	_ = daemonRevoked.Close()

	restoredPolicy := auth.NewPolicy([]string{f.rootDir})
	sockPath3 := filepath.Join(f.rootDir, "asura_mem_restored.sock")
	daemonRestored := daemon.NewDaemon(daemon.Config{
		SocketPath: sockPath3,
		MaxWorkers: 2,
	}, f.store, restoredPolicy)

	if err := daemonRestored.Start(); err != nil {
		t.Fatalf("failed to start restored daemon: %v", err)
	}
	defer daemonRestored.Close()

	waitForSocket(t, sockPath3)
	clientRestored, err := daemon.NewClient(sockPath3)
	if err != nil {
		t.Fatalf("failed to create client for restored daemon: %v", err)
	}
	defer clientRestored.Close()

	// 重新授權後，原記憶應完整保留並可正常查詢
	restoredMem, err := clientRestored.GetMemory(ctx, f.projA, memA.ID)
	if err != nil {
		t.Fatalf("重新授權後查詢原記憶失敗：%v", err)
	}
	if restoredMem.Content != fixedMemoryContentA {
		t.Fatalf("重新授權後記憶內容不符：expected %q, got %q", fixedMemoryContentA, restoredMem.Content)
	}

	// 且可執行刪除
	if err := clientRestored.DeleteMemory(ctx, f.projA, memA.ID); err != nil {
		t.Fatalf("重新授權後刪除失敗：%v", err)
	}
}

func buildAsuraBinary(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	asuraBin := filepath.Join(binDir, "asura")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(wd))
	cmdDir := filepath.Join(projectRoot, "cmd", "asura")
	buildCmd := exec.Command("go", "build", "-race", "-o", asuraBin, cmdDir)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build asura binary: %v, output: %s", err, string(out))
	}
	return asuraBin
}

// TestMEM001: 可追溯寫入與持久化 (MEM-001, P3-T1 外迴圈真實 CLI 測試)
func TestMEM001(t *testing.T) {
	f := setupMemoryFixture(t)
	asuraBin := buildAsuraBinary(t)

	// 1. 透過真實 CLI binary 寫入純文字記憶
	addCmd := exec.Command(asuraBin, "memory", "add",
		"--workspace", f.projA,
		"--content", fixedMemoryContentA,
		"--socket", f.sockPath,
	)
	var addOut, addErr bytes.Buffer
	addCmd.Stdout = &addOut
	addCmd.Stderr = &addErr

	if err := addCmd.Run(); err != nil {
		t.Fatalf("CLI memory add failed: %v, stderr: %s", err, addErr.String())
	}

	addOutput := addOut.String()
	// 驗證輸出包含五欄位且內容完整未被截斷
	if !strings.Contains(addOutput, "ID:") ||
		!strings.Contains(addOutput, "Workspace:") ||
		!strings.Contains(addOutput, "Content:") ||
		!strings.Contains(addOutput, "Source:") ||
		!strings.Contains(addOutput, "CreatedAt:") {
		t.Fatalf("memory add output missing one of five required fields:\n%s", addOutput)
	}
	if !strings.Contains(addOutput, fixedMemoryContentA) {
		t.Fatalf("memory add output truncated or mismatched:\n%s", addOutput)
	}
	if !strings.Contains(addOutput, "manual") {
		t.Fatalf("memory add output missing manual source:\n%s", addOutput)
	}

	// 擷取 Memory ID
	var memID string
	for _, line := range strings.Split(addOutput, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ID:") {
			memID = strings.TrimSpace(strings.TrimPrefix(line, "ID:"))
			break
		}
	}
	if memID == "" {
		t.Fatalf("failed to extract memory ID from output:\n%s", addOutput)
	}

	// 2. 模擬重啟 Daemon
	_ = f.client.Close()
	_ = f.daemon.Close()

	d2 := daemon.NewDaemon(daemon.Config{
		SocketPath: f.sockPath,
		MaxWorkers: 2,
	}, f.store, f.policy)
	if err := d2.Start(); err != nil {
		t.Fatalf("failed to restart daemon: %v", err)
	}
	defer d2.Close()
	waitForSocket(t, f.sockPath)

	// 3. 透過真實 CLI binary 依 ID 查詢該記憶
	getCmd := exec.Command(asuraBin, "memory", "get",
		"--workspace", f.projA,
		memID,
		"--socket", f.sockPath,
	)
	var getOut, getErr bytes.Buffer
	getCmd.Stdout = &getOut
	getCmd.Stderr = &getErr

	if err := getCmd.Run(); err != nil {
		t.Fatalf("CLI memory get failed: %v, stderr: %s", err, getErr.String())
	}

	getOutput := getOut.String()
	if !strings.Contains(getOutput, memID) {
		t.Errorf("get output expected to contain ID %s, got:\n%s", memID, getOutput)
	}
	if !strings.Contains(getOutput, fixedMemoryContentA) {
		t.Errorf("get output expected to contain full content %s, got:\n%s", fixedMemoryContentA, getOutput)
	}
	if !strings.Contains(getOutput, "manual") {
		t.Errorf("get output expected to contain manual source, got:\n%s", getOutput)
	}
	if !strings.Contains(getOutput, f.projA) {
		t.Errorf("get output expected to contain workspace %s, got:\n%s", f.projA, getOutput)
	}
}

// TestMEM005: 指定刪除與恢復 (MEM-005, P3-T1 外迴圈真實 CLI 測試)
func TestMEM005(t *testing.T) {
	f := setupMemoryFixture(t)
	asuraBin := buildAsuraBinary(t)

	// 1. 在專案 A 寫入兩筆記憶
	mem1, err := f.client.AddMemory(context.Background(), f.projA, "專案A第一筆記憶 (Memory 1)")
	if err != nil {
		t.Fatalf("AddMemory 1 failed: %v", err)
	}
	mem2, err := f.client.AddMemory(context.Background(), f.projA, "專案A第二筆記憶 (Memory 2)")
	if err != nil {
		t.Fatalf("AddMemory 2 failed: %v", err)
	}

	// 2. 透過 CLI 刪除其中一筆 (mem1)
	removeCmd := exec.Command(asuraBin, "memory", "remove",
		"--workspace", f.projA,
		mem1.ID,
		"--socket", f.sockPath,
	)
	var remOut, remErr bytes.Buffer
	removeCmd.Stdout = &remOut
	removeCmd.Stderr = &remErr

	if err := removeCmd.Run(); err != nil {
		t.Fatalf("CLI memory remove failed: %v, stderr: %s", err, remErr.String())
	}
	if !strings.Contains(remOut.String(), mem1.ID) {
		t.Errorf("expected remove output to mention %s, got: %s", mem1.ID, remOut.String())
	}

	// 3. 重啟 Daemon
	_ = f.client.Close()
	_ = f.daemon.Close()

	d2 := daemon.NewDaemon(daemon.Config{
		SocketPath: f.sockPath,
		MaxWorkers: 2,
	}, f.store, f.policy)
	if err := d2.Start(); err != nil {
		t.Fatalf("failed to restart daemon: %v", err)
	}
	defer d2.Close()
	waitForSocket(t, f.sockPath)

	// 4. 查詢已刪除之 mem1：預期失敗且 exit code 非 0
	get1Cmd := exec.Command(asuraBin, "memory", "get",
		"--workspace", f.projA,
		mem1.ID,
		"--socket", f.sockPath,
	)
	var get1Out, get1Err bytes.Buffer
	get1Cmd.Stdout = &get1Out
	get1Cmd.Stderr = &get1Err
	if err := get1Cmd.Run(); err == nil {
		t.Fatalf("expected error getting deleted memory %s, but succeeded:\n%s", mem1.ID, get1Out.String())
	}
	if !strings.Contains(get1Err.String(), "not found") {
		t.Errorf("expected 'not found' in stderr, got: %s", get1Err.String())
	}

	// 5. 查詢未刪除之 mem2：預期成功且內容完好
	get2Cmd := exec.Command(asuraBin, "memory", "get",
		"--workspace", f.projA,
		mem2.ID,
		"--socket", f.sockPath,
	)
	var get2Out, get2Err bytes.Buffer
	get2Cmd.Stdout = &get2Out
	get2Cmd.Stderr = &get2Err
	if err := get2Cmd.Run(); err != nil {
		t.Fatalf("failed getting remaining memory %s: %v, stderr: %s", mem2.ID, err, get2Err.String())
	}
	if !strings.Contains(get2Out.String(), mem2.ID) || !strings.Contains(get2Out.String(), "專案A第二筆記憶") {
		t.Errorf("remaining memory output unexpected:\n%s", get2Out.String())
	}
}

// TestMEM006: 失敗不冒充成功且管理不啟動模型 (MEM-006, P3-T1 外迴圈真實 CLI 測試)
func TestMEM006(t *testing.T) {
	f := setupMemoryFixture(t)
	asuraBin := buildAsuraBinary(t)

	// 1. 健康對照：正常 Daemon 下寫入成功，且 runnerCalls 保持 0
	healthCmd := exec.Command(asuraBin, "memory", "add",
		"--workspace", f.projA,
		"--content", fixedMemoryContentA,
		"--socket", f.sockPath,
	)
	var hOut, hErr bytes.Buffer
	healthCmd.Stdout = &hOut
	healthCmd.Stderr = &hErr
	if err := healthCmd.Run(); err != nil {
		t.Fatalf("健康對照失敗：%v, stderr: %s", err, hErr.String())
	}
	if calls := atomic.LoadInt32(f.runnerCalls); calls != 0 {
		t.Fatalf("安全違規：正常記憶管理啟動了 AI 後端/runner，runnerCalls=%d", calls)
	}

	// 2. 失敗對照（Daemon 不可用 / socket 不存在）：
	nonExistentSocket := filepath.Join(f.rootDir, "non-existent.sock")
	failCmd := exec.Command(asuraBin, "memory", "add",
		"--workspace", f.projA,
		"--content", "失敗測試內容",
		"--socket", nonExistentSocket,
	)
	var fOut, fErr bytes.Buffer
	failCmd.Stdout = &fOut
	failCmd.Stderr = &fErr
	err := failCmd.Run()
	if err == nil {
		t.Fatalf("預期 Daemon 不可用時 CLI 失敗，但得到成功 exit code 0")
	}
	// stderr 必須有明確錯誤訊息
	if !strings.Contains(fErr.String(), "daemon unavailable") {
		t.Errorf("stderr 預期包含 'daemon unavailable'，實際為: %s", fErr.String())
	}
	// stdout 不得冒充已保存
	if strings.Contains(fOut.String(), "ID:") || strings.Contains(fOut.String(), "Created") || strings.Contains(fOut.String(), "Saved") {
		t.Fatalf("安全漏洞：失敗時 stdout 冒充成功字樣:\n%s", fOut.String())
	}
	// 驗證未改走其他儲存方式（檢查沒有在專案或 root 產生任何額外 db/json/memory 檔案）
	entries, _ := os.ReadDir(f.projA)
	if len(entries) > 1 { // 原本只有 subC
		t.Fatalf("安全違規：CLI 在 daemon 失敗時疑似產生了替代儲存檔案: %v", entries)
	}
	// runner 計數仍應為 0
	if calls := atomic.LoadInt32(f.runnerCalls); calls != 0 {
		t.Fatalf("安全違規：失敗情境啟動了 AI 後端/runner，runnerCalls=%d", calls)
	}

	// 3. 驗證既有任務提交不自動加入記憶內容 (MEM-006):
	var receivedPrompt string
	var promptCaptured = make(chan struct{})
	captureRunner := func(ctx context.Context, ws, p string) (string, error) {
		receivedPrompt = p
		close(promptCaptured)
		return "ack", nil
	}
	// 暫停原 daemon，啟動帶 captureRunner 的 daemon
	_ = f.client.Close()
	_ = f.daemon.Close()

	engWithCapture := engine.NewEngine(f.store, f.policy, engine.WithRunner(captureRunner))
	dCapture := daemon.NewDaemon(daemon.Config{
		SocketPath: f.sockPath,
		MaxWorkers: 1,
	}, f.store, f.policy, daemon.WithEngine(engWithCapture))
	if err := dCapture.Start(); err != nil {
		t.Fatalf("failed to start capture daemon: %v", err)
	}
	defer dCapture.Close()
	waitForSocket(t, f.sockPath)

	clientCapture, err := daemon.NewClient(f.sockPath)
	if err != nil {
		t.Fatalf("failed to connect to capture daemon: %v", err)
	}
	defer clientCapture.Close()

	rawPrompt := "請幫我分析程式碼"
	_, err = clientCapture.Submit(context.Background(), engine.SubmitRequest{
		Workspace: f.projA,
		Prompt:    rawPrompt,
	})
	if err != nil {
		t.Fatalf("submit task failed: %v", err)
	}

	select {
	case <-promptCaptured:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for runner execution")
	}

	if receivedPrompt != rawPrompt {
		t.Fatalf("安全違規：既有任務提交被自動篡改或注入記憶內容！expected %q, got %q", rawPrompt, receivedPrompt)
	}
}
