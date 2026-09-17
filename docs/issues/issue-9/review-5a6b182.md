# 程式碼審查報告 (Issue #9: 專案級基礎記憶與取消狀態完整傳播)

## 變更摘要
本次審查涵蓋 Issue #9（專案級基礎記憶）之整體實作，以及針對 PR #10 第四輪審查回饋中提出的 3 項 MUST FIX（專案互斥鎖過早釋放、Cron 重疊略過遺漏 cancelling、重啟復原遺漏 cancelling）與 1 項建議項目（狀態轉移失敗處理）之完整修復。主要變更包含：
1. **專案記憶 CLI 與 IPC 架構**：`cmd/asura/memory.go` 實作 `memory`（`add`, `list`, `get`, `remove`）命令列介面，透過 Unix socket IPC 與 Daemon 溝通，杜絕直連資料庫或本地臨時儲存。
2. **同次正規化授權防禦**：`internal/auth/policy.go` 實作 `AuthorizeCanonicalWorkspace`，同次完成符號連結解析、路徑正規化與授權檢驗，消除 TOCTOU 時間差競爭。
3. **專案隔離 SQLite 儲存層**：`internal/storage/sqlite.go` 加法式擴充 `memories` 資料表與索引；`internal/storage/sqlite_memory.go` 嚴格限定 `WHERE workspace = ? AND id = ?` 進行 CRUD 操作，無全域讀取捷徑。
4. **專案互斥鎖在 cancelling 期間的嚴格保護 (PR #10 第四輪 MUST FIX 1)**：
   - 修復缺陷：原先 Daemon 的 `SubmitAsyncClassifiedWithDone` 在 `SubmitTask()` 返回後立即釋放 workspace lock，若取消協調端 `CancelTask()` 隨後遭遇進程驗證失敗或逾時，會導致同 workspace 的下一個排隊任務被喚醒並執行，破壞同專案互斥保證。
   - 解決方案：在 `dispatchTask` 與 `dispatchQueuedTask` 的 done callback 中加入查驗，若任務仍處於 `StatusCancelling` 狀態，**嚴禁釋放 workspace lock**；改由 `CancelTask()` 在成功確認進程安全回收（`StatusCancelled`）後，方透過 `releaseWorkspaceAndWakeNext()` 釋放鎖並喚醒排隊任務。
   - 測試覆蓋：新增 `internal/daemon/daemon_test.go:TestDaemon_CancellingTaskKeepsWorkspaceLockedUntilReapConfirmed`，模擬 active runner 產生未知 orphan PGID，Cancel 驗證失敗後，斷言 Task A 維持 cancelling 且保留 PGID，同專案之 Task B 保持 queued 且 runner 執行次數為 0（在未修復前紅燈重現，修復後綠燈通過）。
5. **Cron 排程重疊檢查納入 cancelling 狀態 (PR #10 第四輪 MUST FIX 2)**：
   - 修復缺陷：`internal/cron/scheduler.go` 判斷前次任務是否 active 時遺漏了 `StatusCancelling`，導致上一輪任務尚未回收時下個 tick 誤認為非 active 並重複建立 queued 任務。
   - 解決方案：將 `storage.StatusCancelling` 正式納入 overlap 判斷集合，前次任務處於 cancelling 時嚴格依 CRON-004 略過（記錄 skipped run 並推進下次執行時間）。
   - 測試覆蓋：新增 `internal/cron/scheduler_test.go:TestScheduler_Trigger_OverlapSkip_Cancelling`，驗證前次任務 cancelling 時略過觸發且不派發新任務（實測 PASS）。
6. **重啟復原機制納入 cancelling 狀態 (PR #10 第四輪 MUST FIX 3)**：
   - 修復缺陷：`internal/storage/sqlite.go` 的 `RecoverInterruptedTasks` 只掃描 `running`、`pending`、`queued`，若中樞在任務 cancelling 期間重啟，該任務將永久停留在 cancelling。
   - 解決方案：SQL 更新條件擴充為 `WHERE status IN (?, ?, ?, ?)`，包含 `StatusCancelling`，重啟時將殘留的 cancelling 孤兒任務正確標記為中斷失敗（`StatusFailed`）。
   - 測試覆蓋：新增 `internal/storage/sqlite_test.go:TestRecoverInterruptedTasks_Cancelling`，驗證 cancelling 任務於重啟復原時被更新為 `StatusFailed` 並具備非空中斷訊息（實測 PASS）。
7. **取消狀態轉移失敗之防禦處理 (建議項目併入)**：
   - `internal/engine/cancel.go` 中，將任務狀態更新為 `StatusCancelling` 若失敗，立即返回錯誤，嚴禁在持久化失敗前發出 `cancel()`，防止未持久化成功即發送信號引發之競爭與狀態錯亂。

## 審查版本
- Reviewed BASE SHA：`202f1b30e0c56aa087fd1c8f877c09bec1475019`
- Reviewed HEAD SHA：`5a6b182080837523cc4db0be5f3cd657f6a44573`
- Reviewed patch-id：`341a973babe571a6c32bba69634918c9381c6945`
- 獨立 reviewer：Independent Reviewer (獨立審查員)
- Review artifact：`docs/issues/issue-9/review-5a6b182.md`
- 審查日期：2026-09-16

## 整體評估
本次審查給予 **PASS**。
PR #10 第四輪審查回饋提出之「`StatusCancelling` 新狀態向外層編排層（Orchestration Layer）完整傳播」的三項 MUST FIX 與狀態轉移錯誤處理已全面獲得解決：
1. Workspace lock 的持有時鐘與取消進程驗證的確認時鐘完全對齊，在進程未確認回收前絕不提前交接鎖給同專案任務。
2. Cron 排程重疊邏輯與重啟復原機制均完整支援 `StatusCancelling`，無孤兒狀態懸空或異常重複排隊。
3. 全專案 7 項 Gherkin 驗收劇本、針對各修復點之專案單元測試、全套端到端整合測試與 `-race` 並發競態檢測全數通過。

## 需求符合度
- 需求描述：Issue #9 專案級基礎記憶（Phase 3A）及 PR #10 第四輪審查 MUST FIX 修復成果。
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

## 狀態機完整傳播查核 (PR #10 第四輪 MUST FIX)
1. **專案互斥鎖時序防禦**：
   - 查核 `internal/daemon/daemon.go`：已確認 `dispatchTask` 與 `dispatchQueuedTask` 的 done callback 在任務處於 `StatusCancelling` 時返回而不釋放 workspace lock。
   - 查核 IPC `case "cancel":`：已確認在 `CancelTask()` 成功返回後，才調用 `d.releaseWorkspaceAndWakeNext()`，保證只有在進程組確認死亡（`StatusCancelled`）後才交接鎖。
   - 實測 `TestDaemon_CancellingTaskKeepsWorkspaceLockedUntilReapConfirmed`：未修復前 Task B 搶跑（紅燈）；修復後 Task B 保持 queued 且 runner 執行次數為 0（綠燈 PASS）。
2. **Cron 排程重疊防禦**：
   - 查核 `internal/cron/scheduler.go`：前次任務處於 `StatusCancelling` 時精確進入 switch 分支略過本次觸發。
   - 實測 `TestScheduler_Trigger_OverlapSkip_Cancelling`：PASS (0.01s)。
3. **Daemon 重啟復原**：
   - 查核 `internal/storage/sqlite.go`：`RecoverInterruptedTasks` 正確涵蓋 `StatusCancelling`。
   - 實測 `TestRecoverInterruptedTasks_Cancelling`：PASS (0.01s)。
4. **狀態轉換錯誤處理**：
   - 查核 `internal/engine/cancel.go`：`UpdateTaskStatus` 寫入 `cancelling` 失敗時返回錯誤，不發出 `cancel()`。

## 測試真實性與驗證結果
- **測試命令與結果**：
  1. `go test -v -count=1 ./internal/daemon -run '^TestDaemon_CancellingTaskKeepsWorkspaceLockedUntilReapConfirmed$'`：PASS (0.17s)
  2. `go test -v -count=1 ./internal/cron -run '^TestScheduler_Trigger_OverlapSkip'`：PASS (0.02s)
  3. `go test -v -count=1 ./internal/storage -run '^TestRecoverInterruptedTasks_Cancelling$'`：PASS (0.01s)
  4. `go test -race -count=1 ./internal/engine ./internal/storage ./internal/cron ./internal/daemon ./cmd/asura`：PASS（無任何 data race）
  5. `go test -v -count=1 ./tests/integration`：PASS（全套端到端整合測試通過，63.88s）
- **真實性查核**：
  - 核心修復項均有對應且真實紅燈重現之單元／整合測試作為檢驗基線。
  - 查驗 `git diff`：無任何 `t.Skip`、`if true { return }` 或同義反覆之假斷言。

## 結論
PR #10 第四輪審查要求之 3 項 MUST FIX 與建議項均已完整修復並經全套測試驗證，`StatusCancelling` 狀態機已徹底融入整體架構。本獨立審查報告判定結果為 **PASS**，具備完整合併條件。
