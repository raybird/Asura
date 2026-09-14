package integration

import (
	"context"
	"os"
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

// TestMEM001: 可追溯寫入與持久化 (主責 P2-T1、P3-T1 外迴圈預留骨架)
func TestMEM001(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := f.client.AddMemory(ctx, f.projA, fixedMemoryContentA)
	if err != nil {
		t.Fatalf("外迴圈行為紅燈 (功能尚缺)：AddMemory 失敗：%v (待 P2-T1 實作)", err)
	}
}

// TestMEM005: 指定刪除與恢復 (主責 P2-T1、P3-T1 外迴圈預留骨架)
func TestMEM005(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := f.client.AddMemory(ctx, f.projA, fixedMemoryContentA)
	if err != nil {
		t.Fatalf("外迴圈行為紅燈 (功能尚缺)：AddMemory 失敗：%v (待 P2-T1 實作)", err)
	}
}

// TestMEM006: 失敗不冒充成功且管理不啟動模型 (主責 P2-T2、P3-T1 外迴圈預留骨架)
func TestMEM006(t *testing.T) {
	f := setupMemoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 記憶管理操作不應呼叫 runner (runner 計數應保持 0)
	_, _ = f.client.AddMemory(ctx, f.projA, fixedMemoryContentA)
	if calls := atomic.LoadInt32(f.runnerCalls); calls != 0 {
		t.Fatalf("安全違規：記憶管理啟動了 AI 後端/runner，runnerCalls=%d", calls)
	}
}
