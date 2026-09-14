# 程式碼審查報告

## 變更摘要

本次變更為 Issue #6（Phase 2B 容量政策與動態 Cron 排程管理）之全量實作，累計 46 個檔案異動（+4593 / -118 行），核心涵蓋：
1. **儲存層擴充**（`internal/storage/sqlite.go`）：新增 `cron_schedules` 與 `cron_runs` 資料表，外鍵級聯刪除（ON DELETE CASCADE）與索引優化，支援任務狀態 `StatusQueued` 與 `UpdateTaskStatusIfPendingOrQueued`。
2. **容量調度器與專案鎖**（`internal/daemon/pool.go`, `internal/daemon/workspace_queue.go`）：實作分類容量調度（`TaskClassInteractive` 與 `TaskClassCron`）、保留互動通道機制（`MaxCronWorkers`）與同專案嚴格互斥佇列（`WorkspaceQueue`）。
3. **內嵌 Cron 排程引擎**（`internal/cron/parser.go`, `internal/cron/scheduler.go`）：實作標準 5 欄位表達式解析、本地時區跳躍搜尋、重疊略過（Overlap Skip）、重啟漏跑不補（No Catchup）與未授權目錄自動暫停（Auto Pause）。
4. **IPC 協議與 CLI 子命令**（`internal/daemon/daemon.go`, `cmd/asura/cron.go`）：新增 `asura cron`（`add`, `list`, `pause`, `resume`, `remove`, `runs`）子命令與 Daemon IPC 處理分支。
5. **整合測試與驗證日誌**（`tests/integration/cron_test.go`, `docs/issues/issue-6/evidence/`）：覆蓋 CRON-001～CRON-008 各階段之雙迴圈測試日誌與使用手冊。

## 審查版本

- Reviewed HEAD SHA：`a42c088a87b4b1b806babdc32d78e99cc83d9266`
- 獨立 reviewer：Independent Code Reviewer (Subagent)
- Review artifact：`docs/issues/issue-6/review-a42c088.md`
- Reviewed patch-id：`4f7ccacaea556777dd2369a794f114a6bc7cd60a`

## 整體評估

本次提交架構清晰、模組邊界分明、全套單元測試與回歸測試在 `-race` 下全部通過（91.959s，0 FAIL，0 DATA RACE）。然而，獨立審查員在深入比對排程觸發鏈路與破壞性邊界時，發現了兩個嚴重的核心缺陷：
1. **排程觸發繞過容量調度器（WorkerPool）與專案鎖（WorkspaceQueue）**：`Scheduler.Trigger` 在觸發時直接透過 goroutine 呼叫 `engine.SubmitTask`，完全未接入 `Daemon` 的容量調度（`TaskClassCron`）與同專案互斥鎖（`WorkspaceQueue`），導致 CRON-001（同專案互斥）與 CRON-002（保留互動通道）在真實排程觸發路徑下完全被擊穿！
2. **無效/無法匹配日期（如 2 月 31 日）導致 `Next()` 返回零值引發 `cronLoop` 每秒雪崩觸發**：當表達式在日曆上無法成立時，`Next()` 返回 `time.Time{}`（0001 年），導致 Daemon 每秒輪詢皆判定過期而瘋狂觸發任務。

依據品質守門原則，判定為 **`RETURN TO execute-task`**，必須修正上述 MUST FIX 問題並補強端到端整合測試後重新提審。

## 需求符合度

- 需求描述：Issue #6 - Phase 2B 容量政策與動態 Cron 排程管理
- 是否符合需求：**部分符合（存在核心邏輯斷鏈漏洞）**
- 缺漏或過度實作說明：
  - 規格定義之 CRON-001 要求「使用者或排程提交同專案 A 任務時互斥排隊」，CRON-002 要求「背景 Cron 佔用達到上限時不阻塞互動通道」。但目前排程觸發引擎 `Scheduler.Trigger` 並未串接 `WorkspaceQueue` 與 `WorkerPool`，使排程自主觸發時無法履行該二項規格契約。
  - 無過度實作，所有變更均收斂於既定之邊界內。

## 問題與風險

嚴重度標籤說明：
- 🔴 **MUST FIX** — 邏輯錯誤、安全漏洞，必須在 merge 前修正
- 🟡 **SHOULD FIX** — 潛在問題、邊界未處理，建議修正
- 🟢 **NICE TO HAVE** — 可讀性、最佳實踐，可列入後續 backlog

- 🔴 **問題 1：排程觸發直接呼叫 `engine.SubmitTask`，繞過 `WorkspaceQueue` 與 `WorkerPool`（破壞 CRON-001 與 CRON-002）**
  - **位置**：`internal/cron/scheduler.go:161-166` 與 `internal/daemon/daemon.go:165`
  - **影響**：
    在 `Scheduler.Trigger` 中，正常觸發時使用獨立 goroutine 直接執行：
    ```go
    go func() {
        ...
        _, _ = s.engine.SubmitTask(context.Background(), engine.SubmitRequest{
            ID:        taskID,
            Workspace: sched.Workspace,
            Prompt:    sched.Prompt,
        })
    }()
    ```
    該路徑直接進入 `Engine`，**完全沒有經過 `Daemon` 的 `wsQueue.TryAcquire` 與 `pool.SubmitAsyncClassified`**！
    這產生兩個致命破壞：
    1. 若使用者正透過 CLI/Web 在專案 A 執行任務，排程觸發專案 A 的定時任務時，會直接與使用者任務並行執行，破壞 CRON-001 的同專案互斥原則，造成原始碼或 git 狀態衝突！
    2. 多個排程同時觸發時，完全不受 `MaxCronWorkers` 限制，會無上限佔用背景資源，甚至奪取全部 Worker，使 CRON-002 的「隨時保留互動通道」徹底失效！
    3. `internal/daemon/pool.go` 雖然實作了 `TaskClassCron` 的分類調度，但在生產代碼中完全是孤兒邏輯（僅在單元測試中被手動呼叫）。

- 🔴 **問題 2：無法匹配之日期（如 2 月 31 日）導致 `Schedule.Next()` 返回零值，使 `cronLoop` 陷入每秒瘋狂觸發之雪崩循環**
  - **位置**：`internal/cron/parser.go:185`、`internal/daemon/daemon.go:512`、`internal/cron/scheduler.go:149`
  - **影響**：
    若使用者建立 `0 0 31 2 *`（2 月 31 日）等語法合法但日曆上不可能出現的排程，`Schedule.Next()` 在搜尋 5 年後返回零值 `time.Time{}`（即 `0001-01-01 00:00:00 UTC`）。
    此零值作為 `NextRunAt` 存入 SQLite。隨後 `daemon.go:246` 的 `cronLoop` 每秒檢查：
    `if sched.Status == storage.CronStatusActive && sched.NextRunAt != nil && !sched.NextRunAt.After(now)`
    因為 `0001-01-01` 永遠在 `now` 之前，條件恆成立，導致 Daemon **每一秒鐘都在建立並觸發一次該排程任務**，迅速耗盡系統進程與資料庫空間！

- 🟡 **問題 3：日（Day of Month）與週（Day of Week）同時指定時強制為交集（AND），偏離標準 Cron 規範**
  - **位置**：`internal/cron/parser.go:160-163`
  - **說明**：
    在標準 POSIX / Vixie cron 中，若日與週同時指定（皆非 `*`），兩者關係為**聯集（OR）**（符合日或符合週任一即可觸發）。當前實作使用 `!dayMatch || !weekdayMatch` 進行跳躍快進，實質為**交集（AND）**。例如 `0 0 13 * 5` 只會在「13 號且剛好是週五」時觸發，而非「每月 13 號以及每週五」。建議在說明文件與使用手冊中明確備註此項語意差異，或依標準重構為聯集支援。

- 🟢 **問題 4：CLI `cron pause/resume/remove/runs` 缺少參數時未提供子命令用法指引**
  - **位置**：`cmd/asura/cron.go:124, 156, 188, 220`
  - **說明**：
    當使用者未輸入 schedule id 時，僅輸出 `error: schedule id is required`，建議比照標準 CLI 附上用法提示（例如 `Usage: asura cron pause <schedule-id>`）。

## 驗收標準與測試證據

- **CRON-001～CRON-008 覆蓋查核**：
  - CRON-001（同專案互斥排隊）：`TestCRON001_WorkspaceMutexQueue` 通過，但測試僅覆蓋「Client 提交之任務」，未覆蓋「排程觸發之任務」。（存在實作漏洞，見問題 1）
  - CRON-002（保留互動通道）：`TestCRON002_InteractiveChannelReserved` 通過，但測試僅對 `WorkerPool` 進行內部單元測試，排程觸發引擎未串接。（存在實作漏洞，見問題 1）
  - CRON-003（標準 5 欄位解析與本地時區）：`TestCRON003_StandardFieldParsingAndNextRun` 通過。
  - CRON-004（重疊略過與重啟不補跑）：`TestCRON004_OverlapSkipAndNoCatchup` 通過。
  - CRON-005（未授權目錄拒絕與自動暫停）：`TestCRON005_UnauthorizedWorkspaceAutoPause` 通過。
  - CRON-006（CLI 排程管理）：`TestCRON006_CLICronManagement` 通過。
  - CRON-007（排程歷史查詢）：`TestCRON007_CLICronHistoryQuery` 通過。
  - CRON-008（服務重啟後持久化）：`TestCRON008_PersistenceAcrossRestart` 通過。
- **紅綠重構證據查核**：
  - Phase 1 至 Phase 4 之 outer-red, inner-red, outer-green, inner-green 與 regression 日誌皆真實存在於 `docs/issues/issue-6/evidence/`。
  - 紅燈為編譯期型別或函式未定義、未實作引發之失敗，綠燈由目標行為驅動。
- **誠實回報與防作弊檢查**：
  - 無假綠燈，無同義反覆斷言。
  - 獨立審查員於本機實測全套測試（含 `-race` 檢測）：`go test -race ./...`，全數通過（ok 91.959s，0 FAIL，0 DATA RACE）。
- **Gate 豁免查核**：無 Gate 豁免紀錄。

## 破壞性邊界條件

- **Edge Case 1：使用者透過 CLI/Web 正在專案 A 執行任務時，排程 A 同時到達觸發點**
  - **輸入／狀態**：專案 A 正在執行長任務；排程 A 綁定專案 A 並抵達觸發時間。
  - **預期結果**：排程 A 應被專案互斥鎖阻擋，任務狀態應為 `queued` 或判定為 `skipped`，絕對不得並行啟動執行器。
  - **現有覆蓋**：未覆蓋。當前程式碼 `Scheduler.Trigger` 直接呼叫 `engine.SubmitTask`，繞過 `wsQueue`，兩者會並行修改檔案！
  - **判定**：🔴 **MUST FIX**。

- **Edge Case 2：多個排程同時觸發，其數量大於 `MaxWorkers`**
  - **輸入／狀態**：設定 3 個同時觸發的排程，`MaxWorkers=2, MaxCronWorkers=1`。
  - **預期結果**：背景任務最多只能有 1 個在執行，其餘進入排隊；隨時保留 1 個 Worker 給即時互動任務。
  - **現有覆蓋**：未覆蓋。`Scheduler.Trigger` 直接發起 goroutine，完全繞過 `WorkerPool`，3 個背景任務同時並發執行，互動通道被完全擠佔。
  - **判定**：🔴 **MUST FIX**。

- **Edge Case 3：使用者新增不可能抵達的排程（例如 `0 0 31 2 *`）**
  - **輸入／狀態**：表達式為 2 月 31 日。
  - **預期結果**：新增時應檢測並拒絕；或 `NextRunAt` 設為無效並自動暫停，不可持續觸發。
  - **現有覆蓋**：未覆蓋。`Next()` 返回零值 `time.Time{}`，存入資料庫後使 `cronLoop` 每一秒都在建立並觸發該任務。
  - **判定**：🔴 **MUST FIX**。

- **Edge Case 4：未授權符號連結逃逸**
  - **輸入／狀態**：排程目錄指向合法根目錄下的符號連結，但符號連結外部目標不在授權清單中。
  - **預期結果**：`AuthorizeWorkspace` 解析符號連結後嚴格拒絕。
  - **現有覆蓋**：已覆蓋。`auth.Policy.AuthorizeWorkspace` 透過 `filepath.EvalSymlinks` 遞迴解析真實路徑，並成功攔截。
  - **判定**：通過。

## 架構符合度

- **分層與依賴方向**：
  - 目前架構大體符合 `CLI -> Daemon -> Engine / Cron -> Policy / Storage`。
  - 但因 `cron` 套件與 `daemon` 套件之間避免循環相依，原實作者直接將 `engine.Engine` 傳入 `cron.Scheduler`，導致調度責任斷裂（`Scheduler` 繞過了 `Daemon` 的 `WorkerPool` 與 `WorkspaceQueue`）。
  - **改進依據**：排程任務的「派發（Dispatching）」屬於 `Daemon` 中樞之核心職責，`Scheduler` 應透過回呼函式（`TaskDispatcher` 回調介面）將任務交由 `Daemon` 統一進行互斥排隊與分類容量調度。

## 安全性檢查

- **存取控制與授權檢查**：
  - `AuthorizeWorkspace` 於 `asura cron add` 與 `Scheduler.Trigger` 時進行雙重檢查，防範目錄移出白名單後被非授權觸發。
  - 符號連結防逃逸機制完整。
- **輸入驗證與 Injection 防護**：
  - Cron 5 欄位表達式嚴格解析各數值範圍與格式，非法輸入立即拒絕。
- **敏感資料與資料庫完整性**：
  - SQLite 外鍵約束已啟用（`PRAGMA foreign_keys=ON`），`cron_runs` 設有 `ON DELETE CASCADE`，防止孤兒觸發歷史殘留。

## 程式品質與效能

- **可讀性與命名**：Go 慣用語法良好，註解詳盡且標註規格條款。
- **併發安全**：
  - SQLite 配置單一連線池（`SetMaxOpenConns(1)`）與 WAL 模式，消除跨連線鎖死風險。
  - 既有單元與整合測試均通過 `-race` 競態檢測。

## 改進建議

1. **重構排程任務派發機制（修復問題 1）**：
   - 在 `internal/cron/scheduler.go` 中定義派發函式型別：
     ```go
     type TaskDispatcher func(ctx context.Context, cronID, workspace, prompt string) error
     ```
   - `Scheduler` 初始化時接收此 `dispatcher`，在 `Trigger` 時呼叫它，而非直接呼叫 `s.engine.SubmitTask`。
   - 在 `internal/daemon/daemon.go` 中實作此派發器：
     1. 產生 `taskID`，建立 `storage.StatusPending` 之任務記錄。
     2. 呼叫 `d.wsQueue.TryAcquire(workspace, taskID)`：
        - 若未取得專案鎖：將任務標記為 `storage.StatusQueued`，等待前一任務完成喚醒。
        - 若取得專案鎖：呼叫 `d.dispatchTask(taskID, workspace, prompt, TaskClassCron)`，經由 `d.pool.SubmitAsyncClassified(..., TaskClassCron, ...)` 受到背景並行上限管制。
     3. 任務結束時透過 defer 執行 `d.releaseCapacity()` 與 `d.wsQueue.Release`。
2. **防範無效/無法匹配之排程時間（修復問題 2）**：
   - 在 `cron_add` 與 `cron.Parse` 時檢查 `next := sched.Next(now)`；若 `next.IsZero()`，直接拒絕建立並提示無效排程。
   - 在 `cronLoop` 與 `Scheduler.Trigger` 中，若計算之 `next.IsZero()`，將 `NextRunAt` 設為 `nil` 並將狀態轉為 `storage.CronStatusPaused`，理由為 `unreachable schedule date`，避免每秒無限觸發。
3. **補充端到端併發與容量整合測試**：
   - 新增 `TestCRON001_CronAndInteractiveWorkspaceMutex`：驗證排程觸發與 CLI 同專案任務之間的互斥排隊。
   - 新增 `TestCRON002_DaemonCronCapacityLimit`：驗證多個排程觸發時受限於 `MaxCronWorkers`，且不阻礙即時互動任務。

## 流程判定

- **`RETURN TO execute-task`**
  - **理由**：存在兩項 🔴 **MUST FIX** 問題（排程觸發繞過容量調度器與專案鎖、無法匹配之日期引發每秒瘋狂觸發）。必須完成修復並補充測試證據後再次審查。
