# 審查報告 (PR #12 / Issue #11 複審)

- **審查日期**：2026-09-17
- **範圍**：完整 PR (`987e47a36c893fd2c896f65eeedf689b89c5ce6f..f65d4f4f3eba8d5f548e3bf24b997d633e50ff5f`)
- **Reviewed BASE SHA**：`987e47a36c893fd2c896f65eeedf689b89c5ce6f`
- **Reviewed HEAD SHA**：`f65d4f4f3eba8d5f548e3bf24b997d633e50ff5f`
- **Reviewed patch-id**：`3280aaebfdd82ea0b5a2efdf326abccc3e38cac6`
- **獨立 reviewer**：Independent Reviewer (Antigravity)
- **Review artifact**：`docs/issues/issue-11/review-f65d4f4.md`

---

## 審查總結

PR #12（Issue #11：專案記憶與 AI 任務 Context 整合 Phase 3B）在前回審查報告（`docs/issues/issue-11/review-ac9e960.md`）中，因測試程式碼 `internal/daemon/memory_test.go` 於開啟 Race Detector 時觸發並發讀寫資料競爭（DATA RACE），依品質門檻判定退回 `execute-task`。

實作團隊於 Commit `f65d4f40f09a807d4b4a9a0ea12b591b93f1bc4a` 針對前回報告所列之所有項目進行了精準且嚴謹的手術式修復：
1. **MUST FIX**：在 `TestDaemon_MemoryScopeRejection` 中為共享變數 `receivedPrompt` 引入 `sync.Mutex` 保護，徹底消除 Data Race。
2. **SHOULD FIX**：在 `tests/integration/memory_context_test.go` 中補充了 `TestCTXMEM_QueuedTaskPreservesSealedContextOnWake`，完整覆蓋任務因專案互斥鎖進入 `StatusQueued` 後喚醒並還原 Sealed Context 的端到端流程。
3. **NICE TO HAVE**：於 Daemon 入口層 `internal/daemon/daemon.go` 實施了 `deduplicateStrings` 去重防禦，與 CLI 層既有去重形成雙層保護，防範重複 Memory ID 浪費 Context 與 Token。
4. **回歸驗證**：經本複審獨立執行 `go test -race -count=1 ./...`，全專案（包含 CLI、daemon、engine、storage、integration 等所有套件）全數通過，0 DATA RACE、0 失敗。

經獨立審查員嚴格查核，所有先前指出的阻塞問題與風險均已徹底消除，本 PR 符合所有品質與架構規範，正式判定為 **PASS**。

---

## 問題與風險追蹤

### 前回審查項目複審結論

| 項目分類 | 前回提出問題 | 修復位置與機制 | 複審驗證方式與證據 | 判定 |
|---|---|---|---|---|
| **MUST FIX** | `TestDaemon_MemoryScopeRejection` 並發讀寫 `receivedPrompt` 觸發 DATA RACE | [memory_test.go#L278-L418](file:///home/kevin/Documents/RCodes/Asura/internal/daemon/memory_test.go#L278-L418)<br>引入 `promptMu sync.Mutex`，於 runner 寫入及測試斷言讀取時加鎖保護 | 執行 `go test -v -race -run TestDaemon_MemoryScopeRejection ./internal/daemon/...`<br>結果：PASS (0.06s)，0 DATA RACE | ✅ **徹底修復 (RESOLVED)** |
| **SHOULD FIX** | 缺乏排隊任務（`StatusQueued`）喚醒後的 Context 記憶保留測試 | [memory_context_test.go#L345-L403](file:///home/kevin/Documents/RCodes/Asura/tests/integration/memory_context_test.go#L345-L403)<br>新增 `TestCTXMEM_QueuedTaskPreservesSealedContextOnWake`，驗證排隊喚醒後 runner 取得記憶 Markdown 且 SQLite 完好保存 | 執行 `go test -v -race -run TestCTXMEM_QueuedTaskPreservesSealedContextOnWake ./tests/integration/...`<br>結果：PASS (0.06s) | ✅ **完整補齊 (RESOLVED)** |
| **NICE TO HAVE** | 記憶 ID 請求去重以防重複 Prompt 注入與 Token 浪費 | [daemon.go#L463,L1374](file:///home/kevin/Documents/RCodes/Asura/internal/daemon/daemon.go#L463)<br>實作 `deduplicateStrings`，於 IPC 請求進入時先去重再計算指紋與查詢記憶 | 檢視 CLI (`cmd/asura/ask.go`) 與 Daemon 雙重防禦機制；執行 `go test -v -run TestHandleAsk_MemoryFlags ./cmd/asura/...`<br>結果：PASS | ✅ **雙層落實 (RESOLVED)** |

---

## 本次審查新發現問題與風險

- **MUST FIX**：無。
- **SHOULD FIX**：無。
- **NICE TO HAVE**：無。

---

## 已查核維度

### 1. 驗收與證據符合性

| 驗收劇本 | 規格重點 | 覆蓋測試位置 | 審查判定 | 查核說明 |
|---|---|---|---|---|
| **CTXMEM-001** | 同專案記憶可被任務使用，Prompt 拼裝且原始 Prompt 不被改寫 | `tests/integration/memory_context_test.go:TestCTXMEM001_...`<br>`internal/engine/engine_test.go:TestEngine_SubmitTaskWithMemoryContext` | ✅ PASS | 斷言 runner 確實收到 `### Project Context` 區塊，且 SQLite `tasks.prompt` 與記憶欄位分流隔離，未受污染。 |
| **CTXMEM-002** | 跨專案記憶不可注入，runner 啟動前拒絕，呼叫為 0 且無洩漏 | `tests/integration/memory_context_test.go:TestCTXMEM002_...`<br>`internal/storage/sqlite_memory_test.go:TestResolveMemoriesForTask` | ✅ PASS | 嚴格比對 canonical workspace 與記憶 ID，跨專案回傳 `ErrMemoryNotFound`，runner 調用為 0，錯誤訊息未洩漏機密記憶內容。 |
| **CTXMEM-003** | 撤權與非法 workspace 不可使用記憶，runner 呼叫為 0 | `tests/integration/memory_context_test.go:TestCTXMEM003_...`<br>`internal/auth/policy_test.go:TestAuthorizeCanonicalWorkspace` | ✅ PASS | Daemon 入口同步調用 `AuthorizeCanonicalWorkspace`，非法路徑於記憶解析與容量獲取前直接攔截拒絕。 |
| **CTXMEM-004** | 任務接受後 Context 密封不可變，記憶異動/刪除或重送不變且無二次執行 | `tests/integration/memory_context_test.go:TestCTXMEM004_...`<br>`internal/daemon/memory_test.go:TestDaemon_TaskContextSealedAndImmutable` | ✅ PASS | 接受時寫入 `ResolvedContext` 快照；重送同 Request ID 時直接回傳既有密封任務，不重新查詢底層記憶，runner 呼叫次數嚴格為 1。 |
| **CTXMEM-005** | 不存在或已刪除記憶於 runner 啟動前拒絕，runner 不啟動 | `tests/integration/memory_context_test.go:TestCTXMEM005_...`<br>`internal/storage/sqlite_memory_test.go:TestResolveMemoriesForTask` | ✅ PASS | 查詢不存在或刪除後 ID 直接回傳 `ErrMemoryNotFound`，CLI 輸出錯誤並非零退出，runner 呼叫為 0。 |
| **CTXMEM-006** | 記憶 Context 歷史可追溯，可查回 MemoryIDs 與 workspace | `tests/integration/memory_context_test.go:TestCTXMEM006_...`<br>`internal/storage/sqlite_test.go:TestSQLiteStore_MemoryContextColumnsMigration` | ✅ PASS | 查詢 Task 可準確取得持久化的 `MemoryRefs` 與 `ResolvedContext`，無需依當前記憶狀態回推歷史。 |
| **CTXMEM-007** | 未選取記憶時維持既有任務語意，Prompt 完全原樣無 Markdown 區塊 | `tests/integration/memory_context_test.go:TestCTXMEM007_...`<br>`internal/engine/engine_test.go:TestComposePrompt` | ✅ PASS | 未傳入 `--memory-id` 時，Prompt 原封不動直通 runner，不注入任何額外字元。 |
| **排隊喚醒** | 任務排隊（`StatusQueued`）喚醒後仍保留 Sealed Context | `tests/integration/memory_context_test.go:TestCTXMEM_QueuedTaskPreservesSealedContextOnWake` | ✅ PASS | 專案互斥鎖釋放後，喚醒之排隊任務其 prompt 正確組裝記憶區塊，資料庫欄位持久化完整。 |

---

### 2. 相關失敗面查核

| 輸入 / 狀態情境 | 預期防禦行為 | 實作機制與現有覆蓋 | 判定 |
|---|---|---|---|
| **跨專案 Memory ID** | 於進入 WorkerPool 前拒絕，runner 0 調用，記憶內容不洩漏 | `daemon.go:497` 調用 `ResolveMemoriesForTask`，比對 canonical workspace 失敗即中斷；測試覆蓋於 `memory_context_test.go`。 | ✅ 符合 |
| **已刪除 / 不存在 Memory ID** | 拒絕任務，0 後端啟動，明確報錯 | `sqlite_memory.go:138` 檢測不到資料回傳 `ErrMemoryNotFound`；測試覆蓋於 `sqlite_memory_test.go`。 | ✅ 符合 |
| **越界符號連結 / 未授權 Workspace** | 入口立即 fail-closed 拒絕 | `daemon.go:452` 調用 `AuthorizeCanonicalWorkspace`；測試覆蓋於 `auth_test.go` 與整合測試。 | ✅ 符合 |
| **同 Request ID 帶不同 Memory IDs 重送** | 拒絕重送，防範 Payload 錯置衝突 | `calculateTaskFingerprint` 將去重排序後之 `MemoryIDs` 納入 SHA-256 指紋計算，指紋不符即拒絕 `request ID reused with different payload`。 | ✅ 符合 |
| **同 Request ID 在底層記憶刪除後重送** | 冪等回傳原始已接受之密封任務，無二次執行 | `daemon.go:471` 在記憶解析前優先檢查既有任務，直接回傳既有任務快照；測試覆蓋於 `memory_test.go`。 | ✅ 符合 |
| **重複 Memory ID 提交** | 自動去重，防範重複組裝與 Token 膨脹 | `daemon.go:463` 透過 `deduplicateStrings` 確保送入指紋與記憶解析之 ID 不重複。 | ✅ 符合 |
| **舊版 SQLite 資料庫平滑遷移** | 既有 tasks 表加法式擴充，無損升級 | `sqlite.go:169` `initSchema` 透過 `getTableColumns` 動態補齊 `memory_refs` 與 `resolved_context`；測試覆蓋於 `sqlite_test.go`。 | ✅ 符合 |

---

### 3. 需求、架構、安全與代碼品質

- **需求符合性**：TBD-1（資料表擴充）、TBD-2（CLI 多值與逗點分隔）、TBD-3（Markdown Prompt 組裝）、TBD-4（CTXMEM 劇本與排隊喚醒場景）100% 落實。
- **架構邊界**：
  - 各層責任分明：CLI 參數解析去重 -> Daemon 授權驗證與去重封裝 -> Storage 嚴格隔離查詢 -> Engine 安全組裝派工。
  - 無過度設計，充分契合既有 Workspace 鎖機制與 WorkerPool 排程體系。
- **資料安全與權限**：
  - 貫徹 Fail-Closed 原則，任何非授權路徑或記憶存取均於排入 Worker 佇列前被精確攔截。
  - 記憶內容不外洩於錯誤訊息中。
- **代碼品質與並發安全**：
  - 測試程式碼之並發讀寫瑕疵已完全透過 `sync.Mutex` 同步化，在 `-race` 下展現最高等級的執行期安全保證。

---

### 4. 豁免、待確認與限制揭露

- **豁免事項**：無。
- **限制揭露**：
  - 本階段（Phase 3B）採確定性明確選取（Explicit Selection，`--memory-id`），未選取者維持既有 Prompt 行為，不執行未經授權的自動檢索注入。
  - 資料庫欄位採用加法式遷移（Additive Migration），完全向後相容舊版資料庫。

---

## 流程判定

**判定**：🟢 **PASS**

**判定理由**：
1. 前回審查唯一阻塞之 MUST FIX（`internal/daemon/memory_test.go` DATA RACE）已徹底修復，經 `-race` 執行驗證為 0 data race 且全綠。
2. SHOULD FIX（排隊任務喚醒驗證）與 NICE TO HAVE（記憶去重防禦）均已高品質實作並附帶完整測試驗證。
3. 全專案執行 `go test -race -count=1 ./...` 全模組全數通過，無任何失敗或並發競爭。
4. 審查報告已持久化存檔於 `docs/issues/issue-11/review-f65d4f4.md`。

PR #12 (Issue #11) 正式通過獨立代碼審查，具備交付與合併條件。
