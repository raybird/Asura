# 程式碼審查報告 (Issue #9: 專案級基礎記憶與取消狀態機完備修復)

## 變更摘要
本次審查涵蓋 Issue #9（專案級基礎記憶）之整體實作，以及針對 PR #10 第三輪審查回饋中提出的關鍵 MUST FIX（`SubmitTask` 繞過取消協調端提前將任務標記為 cancelled 之旁路問題）進行之架構級修復。主要變更包含：
1. **專案記憶 CLI 與 IPC 架構**：`cmd/asura/memory.go` 實作 `memory`（`add`, `list`, `get`, `remove`）命令列介面，透過 Unix socket IPC 與 Daemon 溝通，杜絕直連資料庫或本地臨時儲存。
2. **同次正規化授權防禦**：`internal/auth/policy.go` 實作 `AuthorizeCanonicalWorkspace`，同次完成符號連結解析、路徑正規化與授權檢驗，消除 TOCTOU 時間差競爭。
3. **專案隔離 SQLite 儲存層**：`internal/storage/sqlite.go` 加法式擴充 `memories` 資料表與索引；`internal/storage/sqlite_memory.go` 嚴格限定 `WHERE workspace = ? AND id = ?` 進行 CRUD 操作，無全域讀取捷徑。
4. **正式引入 StatusCancelling 狀態機（修復第三輪 MUST FIX 核心旁路）**：
   - `internal/storage/sqlite.go` 新增 `StatusCancelling TaskStatus = "cancelling"`。
   - 明確劃分「停止意圖（`context.Canceled` / `cancelling`）」與「安全回收完成終態（`cancelled`）」：
     `running -> cancelling -> process verification -> reap confirmed -> cancelled`。
   - `internal/engine/cancel.go` 作為取消唯一的 Coordinator，在發起取消前將狀態原子轉為 `StatusCancelling`，發送信號並等候 runner 退出後，進行三態進程組驗證與死亡確認，只有在驗證成功、進程確認死亡且清零 PGID 後，才由 `CancelTask()` 持久化終態為 `StatusCancelled`。
   - 若進程組身分無法確認（identity unknown）或回收超時，`CancelTask()` 立即回傳錯誤，完整保留 PGID 追蹤，且狀態精確維持為 `cancelling`，嚴禁標記為 `cancelled`。
   - `internal/engine/engine.go` 中的 `SubmitTask()` 在 runner 因 context 取消退出時，若資料庫已處於 `StatusCancelling` 或 `StatusCancelled`，僅持久化保存 partial output（若有），**嚴禁自行宣告或升級為 StatusCancelled**，將終態判定與持久化權限完全交由 `CancelTask()`。
   - runner 啟動前查核與 runner 結束後的條件式更新（`UpdateTaskStatusIfRunning`）全面納入 `StatusCancelling` 防禦，防止並發競爭下將 cancelling 覆寫為 failed 或 completed。
5. **前兩輪已修正項目持續維持**：
   - `internal/daemon/daemon.go` 的 `generateMemoryID()` 保持 8-byte 隨機熵源與 `rand.Read` 錯誤防禦。
   - `.github/workflows/ci.yml` 保持 `go-version-file: go.mod`。
6. **精準測試與迴歸覆蓋**：
   - 新增 `TestEngineCancel_ActiveRunnerUnknownIdentityDoesNotMarkCancelled`：在 active runner 執行中登記未知孤兒進程組時調用 `CancelTask`，先行紅燈重現（SubmitTask 提前寫 cancelled 的 critical bug），修復後綠燈通過，斷言報錯、PGID 保留且狀態維持 `cancelling` 絕非 cancelled。
   - 更新既有單元測試 `TestEngineCancel_UnknownIdentityDoesNotMarkCancelled` 與 CLI 整合測試 `TestCLI_LatestReview_M01_OrphanUnknownIdentityFailsSafely` 斷言狀態為 `StatusCancelling`。

## 審查版本
- Reviewed BASE SHA：`202f1b30e0c56aa087fd1c8f877c09bec1475019`
- Reviewed HEAD SHA：`dfe7045bfab38634b0c4bcc1c881952120ce5189`
- Reviewed patch-id：`f38af492d4d8df133da1c94630a5976f49166841`
- 獨立 reviewer：Independent Reviewer (獨立審查員)
- Review artifact：`docs/issues/issue-9/review-dfe7045.md`
- 審查日期：2026-09-16

## 整體評估
本次審查給予 **PASS**。
PR #10 第三輪審查回饋指出之跨 `SubmitTask()` 與 `CancelTask()` 取消契約旁路（active runner 被取消時 `SubmitTask()` 搶先寫入 `StatusCancelled`）已透過正式引入 `StatusCancelling` 狀態機徹底修復。現在取消終態 `StatusCancelled` 的持久化權力完全收攏於協調器 `CancelTask()`，任何未經驗證或超時殘留的進程組均無法將任務標記為已取消；新增的 active-runner + unknown-identity 重現測試成功由紅轉綠，全專案 7 項 Gherkin 驗收劇本、單元測試、CLI 端到端測試與 `-race` 並發檢測全數通過。

## 需求符合度
- 需求描述：Issue #9 專案級基礎記憶（Phase 3A）及 PR #10 第三輪審查 MUST FIX 修復成果。
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

## 取消狀態機與旁路修復查核 (PR #10 第三輪 MUST FIX)
1. **取消旁路徹底消除**：
   - 原先缺陷：`SubmitTask()` 在 runner 因 context 取消返回後，於 defer 關閉 `doneCh` 之前，自行將資料庫任務狀態更新為 `StatusCancelled`。導致後續 `CancelTask()` 在收到 `doneCh` 進行 `VerifyTaskProcessGroup` 遇到身分未驗證報錯時，DB 早已被標記為 `cancelled`。
   - 本次修復：
     1. 引入 `storage.StatusCancelling`。
     2. `CancelTask()` 發起時先將狀態更新為 `StatusCancelling`，再調用 `cancel()`。
     3. `SubmitTask()` runner 返回後檢查資料庫狀態，若已為 `StatusCancelling`，僅透過 `UpdateTaskResult` 保存 partial output，**嚴禁自行寫入 `StatusCancelled`**，直接回傳 `context.Canceled`。
     4. `CancelTask()` 在 runner 退出後執行身分核驗與 SIGKILL 確認死亡，最後步驟才將狀態更新為終態 `StatusCancelled`。
2. **身分未確認與超時保護**：
   - 當 `targetPGID > 0` 且 `VerifyTaskProcessGroup` 回傳錯誤時，`CancelTask()` 回傳 `unable to verify process identity`。PGID 完好保留，資料庫狀態保持 `StatusCancelling`，絕不標記 `StatusCancelled`。
   - 當進程組經 SIGKILL 後仍存活（超時），`CancelTask()` 回傳 `process group %d still alive after cancellation timeout`。PGID 完好保留，狀態保持 `StatusCancelling`，絕不標記 `StatusCancelled`。
3. **精準重現測試證明**：
   - 新增 `internal/engine/cancel_test.go:TestEngineCancel_ActiveRunnerUnknownIdentityDoesNotMarkCancelled`。
   - 模擬真實情境：SubmitTask 正在 active running、已登記 PGID、進程組內包含 unknown identity orphan，CancelTask 發起取消。
   - **實測證據**：未修復前跑此測試，精確觸發 `CRITICAL BUG: task status in DB was marked as cancelled despite verification failure!`（紅燈）；套用 `StatusCancelling` 修正後測試綠燈通過（PASS 0.02s）。
   - 同時既有單元測試 `TestEngineCancel_UnknownIdentityDoesNotMarkCancelled` 與 CLI 整合測試 `TestCLI_LatestReview_M01_OrphanUnknownIdentityFailsSafely` 斷言狀態精確維持 `StatusCancelling`（實測全數 PASS）。

## 測試真實性與驗證結果
- **測試命令與結果**：
  1. `go test -v -count=1 ./internal/engine -run '^TestEngineCancel_'`：
     - `TestEngineCancel_SynchronouslyTerminatesProcessGroup`：PASS (0.03s)
     - `TestEngineCancel_UnknownIdentityDoesNotMarkCancelled`：PASS (0.02s)
     - `TestEngineCancel_ActiveRunnerUnknownIdentityDoesNotMarkCancelled`：PASS (0.02s)
  2. `go test -v -count=1 ./tests/integration -run '^TestCLI_LatestReview_M01_OrphanUnknownIdentityFailsSafely$'`：
     - PASS (0.56s)
  3. `go test -v -count=1 ./internal/engine ./internal/storage ./internal/daemon ./cmd/asura`：
     - PASS（全包單元測試無任何失敗）
  4. `go test -race -count=1 ./internal/engine ./internal/storage ./internal/daemon ./cmd/asura`：
     - PASS（並發無任何 data race）
  5. `go test -v -count=1 ./tests/integration`：
     - PASS（全專案所有端到端整合測試全數通過）
- **真實性查核**：
  - 測試直接調用真實 SQLite 資料庫與 Linux 操作系統進程組信號（`syscall.Kill`、`/proc/<pid>` 狀態查驗）。
  - 新增之 active runner 重現測試在修復前有明確紅燈重現基線，絕非同義反覆。
  - 查驗 `git diff`：無任何 `t.Skip`、`if true { return }`、mocking bypass 或假綠燈情事。

## 風險評估與後續追蹤
- **本次變更風險判定**：低。
  `StatusCancelling` 為中繼狀態，不破壞既有以終態為停止條件之查詢（如 `ask.go` 依舊等待至 `completed`, `failed`, `cancelled` 終態；`daemon.go` 阻擋重新分發 `cancelling` 或 `cancelled` 任務）。
- **後續建議**：
  未來若有定時排程（Cron）或 Web API 取消需求，可直接沿用此 `StatusCancelling` 協調器架構，維持全專案進程組回收的一致安全性。

## 結論
PR #10 第三輪審查中唯一的 MUST FIX 已透過乾淨、嚴謹的狀態機重構獲得徹底修復，證據鏈完整且可重複驗證。本獨立審查報告判定結果為 **PASS**，具備完整合併條件。
