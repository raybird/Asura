# 審查報告 (PR #12 / Issue #11)

- **審查日期**：2026-09-17
- **範圍**：完整 PR (`987e47a36c893fd2c896f65eeedf689b89c5ce6f..ac9e9606862683e0b253654d8542e92c6fd47d6b`)
- **Reviewed BASE SHA**：`987e47a36c893fd2c896f65eeedf689b89c5ce6f` (`987e47a`)
- **Reviewed HEAD SHA**：`ac9e9606862683e0b253654d8542e92c6fd47d6b` (`ac9e960`)
- **Reviewed patch-id**：`9714d0f240fed25148721a6003af315becbad3b1`
- **獨立 reviewer**：Independent Reviewer (Antigravity)
- **Review artifact**：`docs/issues/issue-11/review-ac9e960.md`

---

## 審查總結

PR #12（Issue #11：專案記憶與 AI 任務 Context 整合 Phase 3B）在規格落實度、授權隔離防禦（Fail-Closed）、資料庫加法相容遷移、原始 Prompt 保護以及不可變 Sealed Context 設計上均表現出極高水準，完整達成了核准劇本 CTXMEM-001 ～ CTXMEM-007 的所有規格需求。

然而，在進行完整的品質與回歸查核時，執行 `go test -v -race ./internal/daemon/...` 發現測試檔案 `internal/daemon/memory_test.go` 存在**真實的資料競爭（DATA RACE）**，導致開啟 Race Detector 時測試直接報錯退出（exit code 1）。由於實作計畫與驗證承諾中明確宣稱「`go test -race ./...` 0 data race 全綠」，此項問題屬於嚴重的真實紅燈，依據專案審查準則，必須列為 **MUST FIX** 並退回 `execute-task` 修復後方可判定 PASS。

---

## 問題與風險

### MUST FIX（阻塞合併項目）

1. **`internal/daemon/memory_test.go` 測試程式碼存在真實資料競爭 (DATA RACE)**
   - **具體位置**：
     - [memory_test.go#L278-L283](file:///home/kevin/Documents/RCodes/Asura/internal/daemon/memory_test.go#L278-L283)：
       ```go
       var runnerCalls int32
       var receivedPrompt string
       runner := func(ctx context.Context, ws, prompt string) (string, error) {
           atomic.AddInt32(&runnerCalls, 1)
           receivedPrompt = prompt
           return "runner output", nil
       }
       ```
     - [memory_test.go#L413](file:///home/kevin/Documents/RCodes/Asura/internal/daemon/memory_test.go#L413)：
       ```go
       if !strings.Contains(receivedPrompt, "### Project Context") || !strings.Contains(receivedPrompt, "專案 B 記憶內容") {
       ```
   - **影響**：
     - 在 worker goroutine 執行 `runner` 寫入 `receivedPrompt` 的同時，主測試 goroutine 於 subtest `CTXMEM-001` 中未加鎖直接讀取 `receivedPrompt`，造成並發讀寫衝突。
     - 執行 `go test -v -race -run TestDaemon_MemoryScopeRejection ./internal/daemon/...` 會直接觸發 Go runtime `WARNING: DATA RACE` 警告並使測試套件失敗（exit code 1）。
     - 這直接違背了實作計畫 Task 4.2 宣稱之「`go test -race ./...` 全綠，0 data race」之驗收保證。
   - **建議修復方式**：
     - 參考同檔案中 `TestDaemon_TaskContextSealedAndImmutable`（第 439 行）的正確做法，宣告 `var promptMu sync.Mutex`，在 runner 寫入 `receivedPrompt` 與測試斷言讀取 `receivedPrompt` 時均透過 Mutex 進行同步保護，徹底消除 Data Race。

---

### SHOULD FIX（強烈建議改進項目）

1. **補充排隊任務（`StatusQueued`）喚醒後的 Context 記憶保留測試**
   - **具體位置**：[tests/integration/memory_context_test.go](file:///home/kevin/Documents/RCodes/Asura/tests/integration/memory_context_test.go)
   - **影響**：
     - 雖然目前 `internal/engine/engine.go` 在第 310 行具有 `else if task.ResolvedContext != ""` 的還原邏輯，確保任務在被排隊喚醒時仍能從資料庫還原 Sealed Context；但在現有的外迴圈與單元測試中，缺乏針對「因專案互斥鎖而進入 `StatusQueued` 的任務，在被喚醒執行後，傳入 runner 的 prompt 依然帶有記憶 Markdown 區塊」的直接斷言。
   - **建議修復方式**：
     - 在 `tests/integration/memory_context_test.go` 中新增一組互斥排隊與喚醒測試，驗證排隊任務喚醒後 Context 依然正確組裝。

---

### NICE TO HAVE（後續精煉項目）

1. **記憶 ID 請求去重（Deduplication）**
   - **具體位置**：[internal/daemon/daemon.go#L489-L497](file:///home/kevin/Documents/RCodes/Asura/internal/daemon/daemon.go#L489-L497) 與 [internal/storage/sqlite_memory.go#L129-L144](file:///home/kevin/Documents/RCodes/Asura/internal/storage/sqlite_memory.go#L129-L144)
   - **說明**：
     - 若使用者在 CLI 輸入重複的 ID（例如 `--memory-id mem-1 --memory-id mem-1` 或 `--memory-id mem-1,mem-1`），目前系統會對同一記憶解析兩次，並在 Prompt 中組裝出兩筆重複的 Markdown 記憶行。
     - 建議在解析前或解析時對 `MemoryIDs` 進行去重，以節省 LLM context window 與 token 成本。
2. **更新實作計畫真實測試紀錄**
   - **具體位置**：[docs/issues/issue-11/implementation-plan.md](file:///home/kevin/Documents/RCodes/Asura/docs/issues/issue-11/implementation-plan.md)
   - **說明**：
     - 在修復 MUST FIX 後，重新執行 `go test -race ./...`，將真實的全綠日誌回寫覆蓋實作計畫檔案中的測試證據。

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

---

### 2. 相關失敗面查核

| 輸入 / 狀態情境 | 預期防禦行為 | 實作機制與現有覆蓋 | 判定 |
|---|---|---|---|
| **跨專案 Memory ID** | 於進入 WorkerPool 前拒絕，runner 0 調用，記憶內容不洩漏 | `daemon.go:492` 調用 `ResolveMemoriesForTask`，比對 canonical workspace 失敗即中斷；測試覆蓋於 `memory_context_test.go`。 | ✅ 符合 |
| **已刪除 / 不存在 Memory ID** | 拒絕任務，0 後端啟動，明確報錯 | `sqlite_memory.go:138` 檢測不到資料回傳 `ErrMemoryNotFound`；測試覆蓋於 `sqlite_memory_test.go`。 | ✅ 符合 |
| **越界符號連結 / 未授權 Workspace** | 入口立即 fail-closed 拒絕 | `daemon.go:452` 調用 `AuthorizeCanonicalWorkspace`；測試覆蓋於 `auth_test.go` 與整合測試。 | ✅ 符合 |
| **同 Request ID 帶不同 Memory IDs 重送** | 拒絕重送，防範 Payload 錯置衝突 | `calculateTaskFingerprint` 將排序後之 `MemoryIDs` 納入 SHA-256 指紋計算，指紋不符即拒絕 `request ID reused with different payload`。 | ✅ 符合 |
| **同 Request ID 在底層記憶刪除後重送** | 冪等回傳原始已接受之密封任務，無二次執行 | `daemon.go:466` 在記憶解析前優先檢查既有任務，直接回傳既有任務快照；測試覆蓋於 `memory_test.go`。 | ✅ 符合 |
| **舊版 SQLite 資料庫平滑遷移** | 既有 tasks 表加法式擴充，無損升級 | `sqlite.go:169` `initSchema` 透過 `getTableColumns` 動態補齊 `memory_refs` 與 `resolved_context`；測試覆蓋於 `sqlite_test.go`。 | ✅ 符合 |

---

### 3. 需求、架構、安全與代碼品質

- **需求符合性**：TBD-1（資料表擴充）、TBD-2（CLI 多值與逗點分隔）、TBD-3（Markdown Prompt 組裝）、TBD-4（CTXMEM 劇本）100% 落實。
- **架構邊界**：
  - 各層分工清楚：`cmd/asura` 處理參數切分 -> `daemon` 處理授權與 Scope 密封 -> `storage` 處理持久化與查詢隔離 -> `engine` 處理 Prompt 拼裝與調度。
  - 無不必要的抽象層與過度設計，充分複用既有組件。
- **資料安全**：
  - Fail-closed 原則貫徹徹底，非法請求在接觸 WorkerPool 前被阻擋。
  - 錯誤訊息僅回傳記憶 ID，未洩漏機密記憶內容。
- **代碼品質**：
  - 生產程式碼品質優秀，原始 Prompt 保護嚴密。
  - **測試代碼因缺乏 Mutex 同步保護引發 DATA RACE，屬於必須修正的瑕疵**。

---

### 4. 豁免、待確認與限制揭露

- **豁免事項**：無。
- **限制揭露**：
  - 任務記憶注入第一版採確定性／明確選取（Explicit Selection），未指定 `--memory-id` 之任務維持既有相容行為，不進行自動召回注入。
  - `tasks` 表擴充欄位採用 SQLite `ALTER TABLE ADD COLUMN` 加法式相容遷移，向後相容舊版資料庫。

---

## 流程判定

**判定**：🛑 **RETURN TO execute-task**

**原因說明**：
儘管業務邏輯與架構設計完全符合規格，但在執行 `go test -v -race ./internal/daemon/...` 時，`TestDaemon_MemoryScopeRejection` 觸發了真實的 **DATA RACE** 導致測試失敗。依據品質門檻與專案規範，不得在包含 MUST FIX 或測試未通過 `-race` 檢驗的情況下給予 PASS。請修復上述 MUST FIX 後重新提交審查。
