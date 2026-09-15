# 程式碼審查報告 (Issue #9: 專案級基礎記憶與取消契約修復)

## 變更摘要
本次審查涵蓋 Issue #9（專案級基礎記憶）之整體實作，以及針對 PR #10 審查回饋所進行之關鍵修復。主要變更包含：
1. **專案記憶 CLI 與 IPC 架構**：`cmd/asura/memory.go` 實作 `memory`（`add`, `list`, `get`, `remove`）命令列介面，透過 Unix socket IPC 與 Daemon 溝通，杜絕直連資料庫或本地臨時儲存。
2. **同次正規化授權防禦**：`internal/auth/policy.go` 實作 `AuthorizeCanonicalWorkspace`，同次完成符號連結解析、路徑正規化與授權檢驗，消除 TOCTOU 時間差競爭。
3. **專案隔離 SQLite 儲存層**：`internal/storage/sqlite.go` 加法式擴充 `memories` 資料表與索引；`internal/storage/sqlite_memory.go` 嚴格限定 `WHERE workspace = ? AND id = ?` 進行 CRUD 操作，無全域讀取捷徑。
4. **取消狀態機與進程組安全回收契約 (PR #10 MUST FIX 1 修復)**：
   - 移除 `internal/engine/cancel.go` 中於進程組身分驗證前「提前標記 StatusCancelled」之邏輯。
   - 嚴格落實取消狀態機時序契約：`cancel context` -> `verify/reap process group` -> `confirm dead` -> `persist StatusCancelled` -> `return success`。
   - 當進程組身分無法確認（identity unknown）或回收超時時，`CancelTask()` 立即回傳錯誤，完整保留 PGID 追蹤，且絕對不將狀態標記為 `cancelled`。
   - `internal/engine/engine.go` 於執行端失敗路徑使用 `UpdateTaskStatusIfRunning`，防止覆寫已被外部取消之終態。
5. **建議項目改善 (PR #10 SHOULD FIX 修復)**：
   - `internal/daemon/daemon.go` 的 `generateMemoryID()` 增加對 `rand.Read` 錯誤之處理，並將隨機熵源由 6 bytes 提升至 8 bytes，同時改用奈秒級時間戳記（`UnixNano`）。
   - `.github/workflows/ci.yml` 將 Go 版本設定由 `stable` 改為 `go-version-file: go.mod`。
6. **外迴圈整合與驗收測試**：`tests/integration/memory_test.go` 提供 MEM-001～MEM-007 之完整端到端驗證；`internal/engine/cancel_test.go` 與 `tests/integration/cli_test.go` 強化身分未驗證時不標記 cancelled 之嚴格斷言。

## 審查版本
- Reviewed BASE SHA：`202f1b30e0c56aa087fd1c8f877c09bec1475019`
- Reviewed HEAD SHA：`6e45b91c9c83c27ba14a49576394e5ed2c43ae69`
- Reviewed patch-id：`78d9898e2b4ddbda12d28395e39e483882c8eccf`
- 獨立 reviewer：Independent Reviewer (獨立審查員)
- Review artifact：`docs/issues/issue-9/review-6e45b91.md`
- 審查日期：2026-09-16

## 整體評估
本次審查給予 **PASS**。
PR #10 審查回饋之核心要求已獲得完整且精確的修正：取消狀態機在驗證進程組身分前提前寫入 `StatusCancelled` 的邏輯已徹底移除，嚴格遵守「cancel -> verify/reap -> confirm dead -> persist cancelled -> return success」之時序契約，未知進程組身分時完好保留 PGID 且絕不標記 cancelled；相關單元與整合測試斷言完備。兩項 SHOULD FIX（記憶 ID 熵源強化與錯誤處理、CI 使用 `go-version-file: go.mod`）亦均正確落實。全專案 7 項 Gherkin 驗收劇本無缺漏、無假綠燈，全專案回歸與 Race 檢測全數通過。

## 需求符合度
- 需求描述：Issue #9 專案級基礎記憶（Phase 3A）及 PR #10 審查回饋修復成果。
- 是否符合需求：**是**。
- 7 項核准 Gherkin 劇本 (MEM-001 ~ MEM-007) 符合度查核：
  - **MEM-001 (可追溯寫入與持久化)**：CLI binary 寫入純文字記憶，Daemon 重啟後，依 ID 取得完全相同內容、正規化專案路徑、`manual` 來源與建立時間；五欄位完整且繁中多語無截斷。符合。
  - **MEM-002 (授權 root 內仍隔離不同專案)**：同 root 下 A/B 專案各自獨立，在 A scope 下列出不含 B 記憶，以 B 之 ID 查詢或刪除皆回傳 `memory not found`，B 的記憶內容與筆數完全不變。符合。
  - **MEM-003 (撤權及越界拒絕)**：指向授權 root 外部之符號連結或未授權外部目錄，寫入、列出、查詢、刪除四項操作全數遭伺服端拒絕（`unauthorized workspace`），無資料洩漏或異動。符合。
  - **MEM-004 (精確 workspace 歸屬)**：有效別名路徑解析命中專案 A 之記憶；子目錄 C 解析為獨立 scope，查詢 A 記憶遭拒且清單為空，子目錄不繼承父目錄記憶。符合。
  - **MEM-005 (指定刪除與恢復)**：專案 A 刪除指定一筆記憶並重啟 Daemon，已刪除 ID 查無資料（exit code 1），未刪除之另一筆資料內容完好。符合。
  - **MEM-006 (失敗不冒充成功且管理不啟動模型)**：Daemon 不可用時 CLI 報錯輸出至 stderr 並返回 code 1，stdout 無偽造成功字樣；不寫入本地臨時或替代檔案；記憶管理操作 AI 模型調用保持為 0；既有任務 prompt 原樣傳遞不被篡改注入記憶。符合。
  - **MEM-007 (撤權保留資料並於重新授權後恢復操作)**：撤權期間所有操作拒絕且既有資料完整保留；重新授權後立即可查得原資料並恢復寫入與刪除。符合。
- 缺漏或過度實作說明：無。

## 取消狀態機與競態修復查核 (PR #10 MUST FIX 1)
1. **取消契約嚴格順序**：
   - 查核 `internal/engine/cancel.go`：已確認完全移除信號發送前提前寫入 `StatusCancelled` 之程式碼。
   - 現行順序嚴格遵循：
     1. 觸發 context 取消 (`cancel()`)
     2. 等候活躍 runner 退出 (`<-doneCh` 或 2s timeout)
     3. 取得最新 PGID 並實施三態進程組身分驗證 (`VerifyTaskProcessGroup`)
     4. 發送 SIGKILL 並輪詢確認進程組死亡 (`!IsProcessGroupAlive`)
     5. 確認死亡後清零 PGID (`UpdateTaskPGID(..., 0)`)
     6. **最後步驟**才持久化終態為 cancelled (`UpdateTaskStatus(..., StatusCancelled, ...)`)
     7. 回傳成功 (`return nil`)
2. **身分未確認與超時保護**：
   - 當 `targetPGID > 0` 但身分驗證回傳錯誤時，立即回傳 `unable to verify process identity: %w`。
   - 此時不清除 PGID，且因尚未執行持久化步驟，任務狀態絕不標記為 `StatusCancelled`。
   - 當進程組經 SIGKILL 後仍存活（超時），立即回傳 `process group %d still alive after cancellation timeout`，同樣不清除 PGID 且不標記 `StatusCancelled`。
3. **測試斷言覆蓋**：
   - `internal/engine/cancel_test.go` 新增 `TestEngineCancel_UnknownIdentityDoesNotMarkCancelled`，建立孤兒進程組且身分無法確認，斷言 `CancelTask` 報錯、PGID 完好保留且狀態維持 `StatusRunning` 而非 `StatusCancelled`（實測 PASS）。
   - `tests/integration/cli_test.go` 中的 `TestCLI_LatestReview_M01_OrphanUnknownIdentityFailsSafely` 斷言新增 `finalTask.Status != StatusCancelled` 與 `finalTask.Status == StatusRunning`（實測 PASS）。
4. **執行端失敗狀態覆寫防禦**：
   - `internal/engine/engine.go` 中的 `SubmitTask` 在 `runErr != nil` 或空輸出路徑採用 `UpdateTaskStatusIfRunning(..., StatusFailed, ...)`。
   - 若狀態已被取消端標記為 `StatusCancelled`，則條件更新失敗，系統讀取最新狀態並回傳 `context.Canceled`，絕不將 cancelled 覆寫為 failed。

## 建議項目修復查核 (PR #10 SHOULD FIX)
1. **記憶 ID 產生器強化 (`internal/daemon/daemon.go:792`)**：
   - 隨機 buffer 增加至 8 bytes（64 bits 熵）。
   - 增加 `if _, err := rand.Read(b); err != nil` 錯誤檢查與 fallback 機制。
   - 時間戳記升級至奈秒級 `time.Now().UnixNano()`，格式為 `mem-<UnixNano>-<hex>`。已修復。
2. **CI 流程設定 (`.github/workflows/ci.yml:20`)**：
   - `actions/setup-go@v5` 中的 `go-version: 'stable'` 已修正為 `go-version-file: go.mod`，確保 CI 環境與專案宣告之 Go 版本嚴格鎖定一致。已修復。

## 問題與風險

### 🔴 MUST FIX (阻塞合併問題)
- **無**。

### 🟡 SHOULD FIX (建議改進項目)
- **無**。先前提出的 SHOULD FIX 項目均已獲得妥善修復。

### 🟢 NICE TO HAVE (可列入後續演進之改進項目)
1. **分頁查詢支援**：目前 `memory list` 回傳該 workspace 下全部記憶，後續專案記憶資料量膨脹時，可評估支援 `--limit` 與 `--offset`。
2. **多行與檔案輸入**：目前 `memory add` 僅支援 `--content <text>` 命令列字串，未來可擴充標準輸入管道（stdin）或 `--file` 旗標以支援長篇筆記。

## 驗收標準與測試證據 (Proof of Test)

核對 7 項核准驗收劇本之實證，由獨立審查員於 2026-09-16 獨立執行：

| 驗收編號 | 可觀察結果摘要 | 規格依據 (commit:path) | 測試證據連結與指令 | 驗證結果 |
|---|---|---|---|---|
| **MEM-001** | CLI binary 寫入純文字記憶，重啟 Daemon 後依 ID 取得相同內容、專案、manual 來源與時間 | `7eb9655:docs/issues/issue-9/README.md#L42-L46` | `go test -v -count=1 ./tests/integration -run '^TestMEM001$'` | **PASS** (4.03s) |
| **MEM-002** | 同 root 下 A/B 專案隔離，清單不含 B 記憶且以 B 之 ID 查詢/刪除皆被拒絕，B 內容不變 | `7eb9655:docs/issues/issue-9/README.md#L48-L54` | `go test -v -count=1 ./tests/integration -run '^TestMEM002$'` | **PASS** (0.03s) |
| **MEM-003** | 未授權、已撤權或越界符號連結請求寫入、列出、查詢或刪除記憶全數拒絕，無資料洩漏或異動 | `7eb9655:docs/issues/issue-9/README.md#L56-L60` | `go test -v -count=1 ./tests/integration -run '^TestMEM003$'` | **PASS** (0.02s) |
| **MEM-004** | 有效別名解析至同一專案並可查得記憶；子目錄 C 不繼承父目錄記憶 | `7eb9655:docs/issues/issue-9/README.md#L62-L66` | `go test -v -count=1 ./tests/integration -run '^TestMEM004$'` | **PASS** (0.03s) |
| **MEM-005** | 指定刪除其中一筆記憶並重啟 Daemon，已刪 ID 查無資料且另一筆內容完好 | `7eb9655:docs/issues/issue-9/README.md#L68-L72` | `go test -v -count=1 ./tests/integration -run '^TestMEM005$'` | **PASS** (4.09s) |
| **MEM-006** | Daemon 不可用時非零退出且 stderr 顯誤、stdout 無冒充成功、無替代儲存、零 AI 啟動、原 prompt 不變 | `7eb9655:docs/issues/issue-9/README.md#L74-L81` | `go test -v -count=1 ./tests/integration -run '^TestMEM006$'` | **PASS** (2.10s) |
| **MEM-007** | 撤權期間所有操作拒絕且資料保留，重新授權後原記憶立即可查且可恢復寫入與刪除 | `7eb9655:docs/issues/issue-9/README.md#L83-L89` | `go test -v -count=1 ./tests/integration -run '^TestMEM007$'` | **PASS** (2.03s) |
| **PR-10 M1** | 進程組身分未知時取消失敗，保留 PGID 且絕不標記 cancelled | `6e45b91:internal/engine/cancel.go` | `go test -v -count=1 ./internal/engine -run '^TestEngineCancel_UnknownIdentityDoesNotMarkCancelled$'`<br>`go test -v -count=1 ./tests/integration -run '^TestCLI_LatestReview_M01_OrphanUnknownIdentityFailsSafely$'` | **PASS** (0.03s)<br>**PASS** (1.56s) |

### 全專案回歸與競態檢測
- 獨立執行命令：`go test -count=1 -race ./...`
- 執行結果：全專案無任何錯誤或 Data Race 警告，全部 PASS：
  - `asura/cmd/asura`：PASS (1.057s)
  - `asura/internal/auth`：PASS (1.062s)
  - `asura/internal/backend/opencode`：PASS (14.944s)
  - `asura/internal/cron`：PASS (1.618s)
  - `asura/internal/daemon`：PASS (12.391s)
  - `asura/internal/engine`：PASS (4.206s)
  - `asura/internal/storage`：PASS (3.179s)
  - `asura/internal/web`：PASS (1.084s)
  - `asura/tests/integration`：PASS (129.620s)

- 誠實回報與防作弊查核：
  - 測試無假綠燈：斷言均與實際外部可觀察行為掛鉤，非同義反覆。
  - MEM-003 明確排除了 `unknown action` 之假綠燈，要求真正由授權模組拒絕。
  - MEM-006 嚴格檢驗 stderr、stdout、磁碟目錄無替代檔案及 runner 調用計數器為 0。
- Gate 豁免查核：無豁免項目。

## 破壞性邊界條件
- **Edge Case 1 (未知身分之孤兒進程組取消)**：
  - 輸入／狀態：任務登記了 PGID，但 Leader 進程已 exit 0 且組內殘留之子進程缺乏 `ASURA_TASK_ID` 環境變數或無法確認身分。
  - 預期結果：`CancelTask` 回傳錯誤，PGID 不被清零，且資料庫中任務狀態嚴禁標記為 `StatusCancelled`。
  - 現有覆蓋：`internal/engine/cancel_test.go:TestEngineCancel_UnknownIdentityDoesNotMarkCancelled` 與 `tests/integration/cli_test.go:TestCLI_LatestReview_M01_OrphanUnknownIdentityFailsSafely`。
  - 判定：**通過**。
- **Edge Case 2 (取消回收逾時之進程組保護)**：
  - 輸入／狀態：被取消的任務進程組陷入卡死狀態（如不可中斷之 I/O），SIGKILL 後 2 秒輪詢仍存活。
  - 預期結果：`CancelTask` 回報超時錯誤，終態不寫入 `StatusCancelled`，PGID 完好保留供後續重試或人工介入。
  - 現有覆蓋：`internal/engine/cancel.go` 第 51-53 行之超時判斷與早退邏輯。
  - 判定：**通過**。
- **Edge Case 3 (取消與執行端失敗之併發時序競態)**：
  - 輸入／狀態：`CancelTask` 與執行端 runner 因 SIGKILL/context 取消而返回失敗，兩者同時更新資料庫狀態。
  - 預期結果：執行端採用 `UpdateTaskStatusIfRunning`，若已被取消端持久化為 `StatusCancelled` 則更新被拒，讀取最新狀態並回傳 `context.Canceled`，不覆寫為 `StatusFailed`。
  - 現有覆蓋：`internal/engine/engine.go` 第 283 行與第 304 行之條件更新防禦。
  - 判定：**通過**。
- **Edge Case 4 (跨專案記憶存取探測)**：
  - 輸入／狀態：用戶在專案 A scope 下嘗試查詢或刪除專案 B 的記憶 ID。
  - 預期結果：儲存層雙鍵限定 `WHERE workspace = ? AND id = ?`，Daemon 回傳 `memory not found`，不洩漏存在性且專案 B 資料不變。
  - 現有覆蓋：`tests/integration/memory_test.go:TestMEM002`。
  - 判定：**通過**。

## 架構符合度
- **分層邊界**：
  - CLI（`cmd/asura/memory.go`）僅負責參數解析與格式化輸出，不直連 SQLite、不觸碰檔案系統私藏記憶。
  - 通訊層（IPC）嚴格透過 Unix domain socket 傳遞。
  - 授權層（`internal/auth/policy.go`）於伺服端同次正規化解析與授權，杜絕客戶端竄改 scope。
  - 儲存層（`internal/storage/sqlite_memory.go`）無跨專案讀寫捷徑。
  - 執行引擎（`internal/engine`）完全不參與記憶管理，維持乾淨的責任分離。
- **依據與限制**：符合 `docs/issues/issue-9/README.md` 與專案既有架構設計。

## 安全性檢查
- **存取控制與授權**：所有記憶管理請求皆由伺服端 `AuthorizeCanonicalWorkspace` 檢驗，未授權目錄或越界符號連結一律被擋在 IPC 入口。
- **輸入驗證與 Injection 防護**：SQL 操作一律使用參數化查詢（`?` 佔位符），無字串拼接；內容長度與欄位均作非空檢驗。
- **敏感資料與資料殘留**：刪除記憶在 SQLite 層執行標準 `DELETE`；跨專案探測回傳一致之 `not found`，杜絕 enumeration 漏洞。
- **AI 模型隔離**：記憶管理不觸發任何模型調用，既有任務提交不注入記憶，避免 Prompt Injection 風險。

## 程式品質與效能
- **可讀性與命名**：遵循 Go 慣例，命名清晰（如 `AuthorizeCanonicalWorkspace`、`UpdateTaskStatusIfRunning`），註解詳細標註關聯驗收編號與修復來源。
- **效能考量**：`memories` 資料表建立 `idx_memories_workspace` 索引，專案查詢均命中索引；記憶 ID 使用奈秒時間戳與 8 bytes 隨機數，碰撞機率極低。

## 流程判定
- **判定結果**：`PASS`
- **判定說明**：
  獨立審查員已完成當前 HEAD（`6e45b91c9c83c27ba14a49576394e5ed2c43ae69`）之全面審查。PR #10 審查回饋之 MUST FIX（取消契約順序與身分未知時之狀態保護）及 SHOULD FIX（記憶 ID 隨機性與錯誤處理、CI Go 版本設定）已全數正確落實，7 項核准 Gherkin 驗收劇本均具備堅實測試證據，全專案 Race 回歸測試全數通過，無任何殘留 MUST FIX 或架構漏洞。審查報告已持久化至 `docs/issues/issue-9/review-6e45b91.md`，准予通過。
