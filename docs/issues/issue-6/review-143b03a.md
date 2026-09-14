# 程式碼審查報告

## 變更摘要

本次變更為 Issue #6（Phase 2B 容量政策與動態 Cron 排程管理）之複審（Re-review），審查範圍為 `2530375f641a0aa8a3503bee65a93a65599dc58c...143b03a6ad5ae6dc2890c71843d8d58fcc6e36e7`，重點覆核提交 `143b03a` 針對前次審查報告（`docs/issues/issue-6/review-a42c088.md`）所提出的 2 項 MUST FIX、1 項 SHOULD FIX 與 1 項 NICE TO HAVE 缺失之修復成果：
1. **排程任務派發機制解耦與調度閉環**（`internal/cron/scheduler.go`, `internal/daemon/daemon.go`）：
   - `cron.Scheduler` 以 `TaskDispatcher` 回調介面取代直接相依 `engine.Engine`，徹底解耦排程時鐘與任務派發。
   - `daemon.Daemon` 實作 `dispatchCronTask`，將排程觸發任務納入同專案互斥鎖（`WorkspaceQueue`）與容量限流器（`WorkerPool`，以 `TaskClassCron` 派發），確保同專案嚴格互斥依序排隊，且背景任務嚴格受限於 `MaxCronWorkers`，完全落實 CRON-001 與 CRON-002 規格契約。
2. **不可達排程日期安全防護**（`internal/cron/parser.go`, `internal/daemon/daemon.go`, `cmd/asura/cron.go`）：
   - 實作 `Schedule.IsReachable(from time.Time) bool`。
   - 在 CLI `asura cron add` 與 Daemon IPC `cron_add` / `cron_resume` 加入不可達日期（如 2 月 31 日）攔截，直接拒絕建立或恢復。
   - 在 `cronLoop` 輪詢條件中加入 `!sched.NextRunAt.IsZero()` 防禦，並於 `AlignSchedules` 與 `Scheduler.Trigger` 中將零值排程自動標記為 `paused`（理由為 `unreachable schedule date`），徹底杜絕每秒瘋狂觸發之雪崩循環。
3. **規範說明與 CLI 體驗優化**（`README.md`, `cmd/asura/cron.go`）：
   - 於 `README.md` 明確補充日（DOM）與週（DOW）同時指定時採交集（AND）匹配之語意說明。
   - 於 `cmd/asura/cron.go` 補齊 `pause`, `resume`, `remove`, `runs` 缺少 schedule id 時之子命令使用指引。
4. **端到端邊界整合測試補齊**（`tests/integration/cron_test.go`）：
   - 新增 `TestCRON001_CronAndInteractiveWorkspaceMutex`、`TestCRON003_UnreachableDateRejection` 與 `TestCRON002_DaemonCronCapacityLimit` 整合測試，全套整合回歸與競態檢測（`-race`）全綠通過。

## 審查版本

- Reviewed HEAD SHA：`143b03a6ad5ae6dc2890c71843d8d58fcc6e36e7`
- 獨立 reviewer：Independent Code Reviewer (Subagent)
- Review artifact：`docs/issues/issue-6/review-143b03a.md`
- Reviewed patch-id：`f12e8a9dbf083454091c583256eddc5f44ab9486`

## 整體評估

本次修復提交 `143b03a` 展現了高度嚴謹的架構素質與問題處理能力。前次審查所指出的兩大核心缺陷（排程觸發繞過專案鎖與容量池、不可達日期零值雪崩循環）均已從架構根源與防禦層深層修復：
1. `TaskDispatcher` 回調設計優雅地維持了 `cron` 與 `daemon` 之間的單向依賴，並使排程任務完整受制於專案佇列（`WorkspaceQueue`）與容量池（`WorkerPool`），消除了多任務檔案競爭與互動通道被佔滿的重大風險。
2. 不可達日期在邊界輸入（CLI / IPC）、生命週期輪詢（`cronLoop`）與狀態對齊（`AlignSchedules` / `Trigger`）三層均布下完整防禦，徹底杜絕雪崩風險。
3. 新增的端到端整合測試精確驗證了併發排隊、背景限流與不可達拒絕，全套測試在 `-race` 下穩定通過（ok 132.713s，0 FAIL，0 DATA RACE）。
4. 規格修訂比對無未核准更動，紅綠重構證據真實完備。

經獨立審查員全面覆核，所有先前缺陷均已圓滿解決，無新引入之風險，判定為 **`PASS`**。

## 需求符合度

- 需求描述：Issue #6 - Phase 2B 容量政策與動態 Cron 排程管理
- 是否符合需求：**是（完全符合）**
- 規格核對與覆蓋情況：
  - **CRON-001（同專案互斥排隊與依序執行）**：已完整覆蓋。透過 `dispatchCronTask` 接入 `d.wsQueue.TryAcquire`，排程觸發之任務與人類互動任務享有相同的專案互斥排隊語意。整合測試 `TestCRON001_CronAndInteractiveWorkspaceMutex` 證實互動任務執行時，定時排程進入 `queued`，前任務完成後自動喚醒執行，最大並發嚴格為 1。
  - **CRON-002（保留互動通道與背景容量上限）**：已完整覆蓋。排程觸發任務以 `TaskClassCron` 提交至 `WorkerPool`，受 `MaxCronWorkers` 限制。整合測試 `TestCRON002_DaemonCronCapacityLimit` 證實多排程並發時背景任務嚴格受限，即時互動任務可立即可用不受阻礙。
  - **CRON-003（動態建立與管理排程）**：已完整覆蓋。支援 CLI/IPC 動態新增、查詢、暫停、恢復與移除，並具備不可達日期防禦。
  - **CRON-004（排程週期觸發產生可追蹤任務）**：已完整覆蓋。週期觸發產生標準 Task 實體與 `cron_runs` 歷史。
  - **CRON-005（排程執行重疊時略過不重複堆疊）**：已完整覆蓋。前任務處於 `StatusRunning`、`StatusPending` 或 `StatusQueued` 時，自動略過（`skipped`）且不堆疊。
  - **CRON-006（重啟或中斷後不補跑過期排程）**：已完整覆蓋。服務重啟時 `AlignSchedules` 自動對齊未來時間，絕不補跑歷史。
  - **CRON-007（觸發時非授權目錄嚴格拒絕並自動暫停）**：已完整覆蓋。定時觸發時複查授權目錄，未授權則拒絕執行、記錄 `unauthorized` 並自動將排程轉為 `paused`。
  - **CRON-008（排程與觸發紀錄於服務重啟後完整持久化）**：已完整覆蓋。排程定義與觸發歷史均持久化於 SQLite，重啟後完整還原。
- 缺漏或過度實作說明：無。

## 規格修訂查核

依據 `docs/AGENTS.md` 規範執行規格修訂比對：
- 比對指令：`git diff df5b48a..HEAD docs/issues/issue-6/README.md`
- 查核結果：
  - Gherkin 驗收劇本（CRON-001～CRON-008）原文內容完全零變動，無任何新增、刪除或修改。
  - 核准表記錄完整，8 項 Scenario 全數為「已核准」，核准 commit `df5b48a`，核准日期 2026-09-13。
  - 僅進度狀態自「待實作」更新為「已實作，待獨立審查」，Timeline 補充了實作與審查修復時序。

## 問題與風險

嚴重度標籤說明：
- 🔴 **MUST FIX** — 邏輯錯誤、安全漏洞，必須在 merge 前修正
- 🟡 **SHOULD FIX** — 潛在問題、邊界未處理，建議修正
- 🟢 **NICE TO HAVE** — 可讀性、最佳實踐，可列入後續 backlog

### 前次審查問題修復覆核清單：

- ✅ **MUST FIX 1：排程觸發繞過專案鎖與容量池**
  - **修復位置**：`internal/cron/scheduler.go:13-32, 131-135`、`internal/daemon/daemon.go:165, 176-218`
  - **覆核結果**：`Scheduler` 透過 `TaskDispatcher` 回呼，`Daemon.dispatchCronTask` 統一接管容量取得、初始任務建立、`wsQueue.TryAcquire` 與 `dispatchTask(..., TaskClassCron)`。任務完成時於 `defer` 自動觸發 `wakeQueuedTask` 喚醒同專案下一個排隊任務。邏輯嚴密，端到端整合測試已證實行為完全符合規格。問題已徹底解決。

- ✅ **MUST FIX 2：不可達日期零值雪崩循環**
  - **修復位置**：`internal/cron/parser.go:188-192`、`cmd/asura/cron.go:44-51`、`internal/daemon/daemon.go:296, 563-566, 631-634`、`internal/cron/scheduler.go:55-58, 159-163`
  - **覆核結果**：新增 `IsReachable` 檢查；CLI 與 IPC 於新增/恢復排程時攔截不可達表達式；`cronLoop` 加入 `!sched.NextRunAt.IsZero()` 防禦；`AlignSchedules` 與 `Trigger` 將零值排程標記為 `paused`。多層防禦到位，單元測試與整合測試均已驗證。問題已徹底解決。

- ✅ **SHOULD FIX 3：日（DOM）與週（DOW）交集條件說明**
  - **修復位置**：`README.md:133`
  - **覆核結果**：已在核心特性中清楚註記採用交集（AND）條件匹配。問題已解決。

- ✅ **NICE TO HAVE 4：CLI 子命令缺少參數時用法指引**
  - **修復位置**：`cmd/asura/cron.go:131, 163, 195, 227`
  - **覆核結果**：`pause`, `resume`, `remove`, `runs` 缺少 schedule id 時皆已輸出清楚之子命令 Usage 指引。問題已解決。

### 本次審查新增問題：
- 無新增之 MUST FIX、SHOULD FIX 或 NICE TO HAVE 問題。

## 驗收標準與測試證據

- **驗收標準覆蓋與紅綠燈證據**：
  - 各階段（Phase 1～Phase 4）及本次修復階段（`remediation`）之測試證據日誌完備存放於 `docs/issues/issue-6/evidence/`。
  - 修復階段紅綠燈證據查核：
    - `docs/issues/issue-6/evidence/remediation/inner-red.log`：驗證 `sched.IsReachable` 未定義之編譯期失敗。
    - `docs/issues/issue-6/evidence/remediation/inner-green.log`：驗證 `TestCronParser_UnreachableDate` 通過（0.007s）。
    - `docs/issues/issue-6/evidence/remediation/outer-red.log`：驗證 `d.Scheduler()` 未定義之編譯期失敗。
    - `docs/issues/issue-6/evidence/remediation/outer-green.log`：驗證 `TestCRON001_CronAndInteractiveWorkspaceMutex`、`TestCRON003_UnreachableDateRejection` 與 `TestCRON002_DaemonCronCapacityLimit` 通過（1.468s）。
    - `docs/issues/issue-6/evidence/remediation/final-regression.log`：全套測試日誌。
- **誠實回報與防作弊檢查**：
  - 斷言具有明確且獨立的真相來源（非同義反覆）：
    - 斷言 `concurrent <= 1` 檢驗真實原子計數器。
    - 斷言 `maxCronConcurrent <= 1` 檢驗真實並行背景數。
    - 斷言 `cronTask.Status == storage.StatusQueued` 檢驗專案鎖排隊狀態。
    - 斷言 `feb31-job` 新增時回傳 error。
    - 斷言保留通道不因背景任務阻塞（2 秒 timeout 檢驗）。
  - 獨立審查員於本機實測全套測試（包含競態檢測）：`go test -race ./...`，全套通過（ok 132.713s，0 FAIL，0 DATA RACE）。
- **Gate 豁免查核**：無 Gate 豁免紀錄。

## 破壞性邊界條件

- **Edge Case 1：使用者透過 CLI/Web 正在專案 A 執行任務時，排程 A 同時到達觸發點**
  - **輸入／狀態**：專案 A 正在執行長任務；排程 A 綁定專案 A 並抵達觸發時間。
  - **預期結果**：排程任務被專案互斥鎖阻擋，任務狀態標記為 `queued`，待前任務完成後自動喚醒執行，最大並發嚴格為 1。
  - **現有覆蓋**：`TestCRON001_CronAndInteractiveWorkspaceMutex` 已完整覆蓋。
  - **判定**：通過。

- **Edge Case 2：多個排程同時觸發，其數量大於 `MaxCronWorkers`**
  - **輸入／狀態**：2 個排程同時觸發，`MaxWorkers=2, MaxCronWorkers=1`。
  - **預期結果**：背景任務最多只有 1 個在執行，其餘排隊；隨時保留 1 個 Worker 給即時互動任務。
  - **現有覆蓋**：`TestCRON002_DaemonCronCapacityLimit` 已完整覆蓋。
  - **判定**：通過。

- **Edge Case 3：使用者新增不可能抵達的排程（例如 `0 0 31 2 *`）**
  - **輸入／狀態**：表達式為 2 月 31 日。
  - **預期結果**：CLI 與 Daemon 建立或恢復排程時即刻攔截並拒絕；若有舊數據存在，自動標記為 `paused`，絕不陷入每秒雪崩循環。
  - **現有覆蓋**：`TestCronParser_UnreachableDate` 與 `TestCRON003_UnreachableDateRejection` 已完整覆蓋。
  - **判定**：通過。

- **Edge Case 4：未授權符號連結逃逸**
  - **輸入／狀態**：排程目錄指向合法根目錄下的符號連結，但符號連結外部目標不在授權清單中。
  - **預期結果**：`AuthorizeWorkspace` 透過 `filepath.EvalSymlinks` 遞迴解析真實路徑，觸發時嚴格拒絕並自動暫停排程。
  - **現有覆蓋**：`TestCRON005_UnauthorizedWorkspaceAutoPause` 已完整覆蓋。
  - **判定**：通過。

- **Edge Case 5：排隊中的排程任務在喚醒前遭取消或服務中斷**
  - **輸入／狀態**：任務處於 `StatusQueued` 時收到取消請求。
  - **預期結果**：`daemon.go:411-414` 於 `cancel` 處理中主動呼叫 `wsQueue.Remove` 與 `releaseCapacity`，確保專案佇列不發生卡死，容量槽位精確歸還。
  - **現有覆蓋**：代碼邏輯已有明確防禦分支。
  - **判定**：通過。

## 架構符合度

- **分層與依賴方向**：
  - 嚴格遵守 `CLI -> Daemon -> Engine / Cron -> Policy / Storage` 分層架構。
  - `cron.Scheduler` 僅處理時鐘排程、表達式解析與重疊略過判定，透過 `TaskDispatcher` 回調交由 `Daemon` 負責具體調度。
  - `Daemon` 中樞統一管理專案互斥鎖（`WorkspaceQueue`）與容量限流池（`WorkerPool`），調度責任高度內聚。
  - 無跨層反向呼叫，無循環依賴。

## 安全性檢查

- **存取控制與授權檢查**：
  - 建立了完整的動態雙重授權機制（建立時驗證 + 每次定時觸發時複查）。
  - 若目錄移出授權清單，排程自動進入 `paused` 並記錄 `unauthorized` 失敗歷史，防護嚴密。
  - 符號連結解析防逃逸機制健全。
- **輸入驗證與 Injection 防護**：
  - Cron 5 欄位表達式嚴格解析各欄位範圍與語法。
  - 日曆不可達日期在邊界直接拒絕。
  - SQLite 儲存層所有 SQL 查詢均採用參數化綁定，無 SQL injection 疑慮。
- **資料庫完整性與連線管理**：
  - SQLite 外鍵級聯刪除（`ON DELETE CASCADE`）運作正常，刪除排程時無殘留孤兒觸發記錄。
  - 連線池配置單一連線（`SetMaxOpenConns(1)`）搭配 WAL 模式，併發讀寫無鎖死風險。

## 程式品質與效能

- **可讀性與命名**：Go 慣用語法良好，註解詳盡標記對應之 BDD 規格編號（CRON-001～CRON-008）。
- **資源管理與併發安全**：
  - 互斥鎖與容量槽位取得與釋放採用 `defer` 模式，無洩漏路徑。
  - 全套測試通過 Go 官方 `-race` 競態檢測器檢查（0 data race）。

## 改進建議

- 無強制修改項目。
- 後續維護建議（可列入未來 backlog）：
  - 未來若有需求支援標準 POSIX 的日/週聯集（OR）語意，可將 `parser.go` 擴充為支援語法標記或相容模式切換。

## 流程判定

- **`PASS`**
  - **判定依據**：獨立審查員已完成當前最新 HEAD `143b03a6ad5ae6dc2890c71843d8d58fcc6e36e7` 之完整獨立審查。前次審查提出之 2 項 MUST FIX、1 項 SHOULD FIX 與 1 項 NICE TO HAVE 已全數確認徹底修復。本次變更無任何 MUST FIX 問題，架構分層合理，安全防護完備，破壞性邊界條件已全數覆蓋，紅綠重構證據真實存在且無假綠燈，全套整合測試在 `-race` 下全數通過。審查報告已持久化保存於 `docs/issues/issue-6/review-143b03a.md`。
