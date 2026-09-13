# Implementation Plan Decomposition (Issue #6)

建立日期：2026-09-13。承接 [Issue 6 README](./README.md) 與 [實作計畫](./implementation-plan.md)。

## Risk Decision

- **最大風險**：排程設定之目錄不在授權清單內卻於背景定時觸發本機執行；或同專案多任務並行讀寫造成檔案衝突與程式庫毀損。
- **風險等級與理由**：High。涉及無人值守自主觸發本機 AI 副作用、目錄互斥鎖與授權防逃逸。
- **首要驗證**：先建立 SQLite 儲存層遷移，隨後建立「同專案互斥排隊等待（`queued`）」與「未授權專案排程觸發即刻拒絕並自動暫停」的外迴圈紅燈。
- **選擇理由**：在實作排程定時器前，優先確保檔案寫入排他性與授權防護邊界，最直接阻斷最大的安全與資料破壞風險。
- **完成證據**：同專案並行請求回傳 `queued` 並依序執行；清單外目錄排程觸發記錄為 `unauthorized`、後端零啟動、排程自動轉為 `paused`。

---

## Phase 1 — 儲存層 Schema 擴充與排程資料庫支援

### Goal
擴充 SQLite 資料模型，新增 `cron_schedules` 與 `cron_runs` 資料表，提供排程定義、啟用狀態、下次觸發時刻與歷史觸發紀錄的完整持久化。

### Deliverables
- `internal/storage/sqlite.go`：新增排程與觸發資料表 Schema 遷移及 CRUD 操作方法。
- `internal/storage/sqlite_cron_test.go`：單元測試驗證排程增刪查改、狀態變更與外鍵串聯刪除。
- `tests/integration/cron_test.go`：CRON-008 外迴圈整合測試框架。

### Dependencies
- 既有 SQLiteStore 基礎設施。

### Tasks

#### Task 1.1：SQLite 排程資料表遷移與結構體定義 ✅
- **任務說明**：在 SQLiteStore 建立 `cron_schedules` 與 `cron_runs` 資料表，並定義 Go 結構體（`CronSchedule`, `CronRun`）。（2026-09-13 實作完成，內迴圈綠燈見 `evidence/phase-1/inner-cron-green.log`）
- **預期輸出**：`internal/storage/sqlite.go` 擴充 DDL 遷移與資料型別。
- **涉及模組或檔案**：`internal/storage/sqlite.go`
- **完成判準**：
  1. 資料庫連線初始化時自動建立 `cron_schedules` 與 `cron_runs` 兩張表。
  2. 外鍵約束成立（刪除排程自動串聯清理或約束相關 runs）。
- **驗證方式**：`go test -count=1 -v ./internal/storage -run '^TestSQLiteStore_CronSchemaMigration$'`
- **覆蓋項目**：S-03 觸發持久化基礎、CRON-008 儲存基底
- **責任角色**：唯一負責
- **Scenario ID**：CRON-008
- **改變可觀察行為**：是
- **BDD 外迴圈**：
  - 測試檔案：`tests/integration/cron_test.go:TestCRON008_PersistenceAcrossRestart`
  - 命令：`go test -count=1 -v ./tests/integration -run '^TestCRON008_PersistenceAcrossRestart$'`
  - 預期失敗原因：找不到排程相關儲存介面或資料表不存在（紅燈見 `evidence/phase-1/outer-cron-red.log`）。
- **TDD 內迴圈**：
  - 測試檔案：`internal/storage/sqlite_cron_test.go:TestSQLiteStore_CronSchemaMigration`
  - 命令：`go test -count=1 -v ./internal/storage -run '^TestSQLiteStore_CronSchemaMigration$'`
  - 預期失敗原因：`cron_schedules` table not found（紅燈見 `evidence/phase-1/inner-cron-red.log`）。
- **最小實作與回歸**：在 `internal/storage/sqlite.go` 加入 DDL 執行；回歸全套儲存測試 `go test -count=1 -race ./internal/storage`（綠燈見 `evidence/phase-1/final-regression.log`）。

#### Task 1.2：排程與觸發歷史 CRUD 方法實作與重啟持久化 ✅
- **任務說明**：實作排程的新增、查詢清單、單筆查詢、更新狀態（active/paused）、記錄觸發結果與刪除排程的方法。（2026-09-13 實作完成，外迴圈綠燈見 `evidence/phase-1/outer-cron-green.log`）
- **預期輸出**：`internal/storage/sqlite.go` 新增排程資料庫 API。
- **涉及模組或檔案**：`internal/storage/sqlite.go`, `internal/storage/sqlite_cron_test.go`
- **完成判準**：
  1. 新增排程成功寫入並能透過 ID 或清單完整查回。
  2. 暫停與恢復狀態更新持久化至磁碟，重啟 Store 仍正確讀取。
  3. 刪除排程確實自資料庫清除。
- **驗證方式**：`go test -count=1 -v ./internal/storage -run '^TestSQLiteStore_CronCRUD$'`
- **覆蓋項目**：CRON-008 排程持久化
- **責任角色**：唯一負責
- **Scenario ID**：CRON-008
- **改變可觀察行為**：是
- **BDD 外迴圈**：
  - 測試檔案：`tests/integration/cron_test.go:TestCRON008_PersistenceAcrossRestart`
  - 命令：`go test -count=1 -v ./tests/integration -run '^TestCRON008_PersistenceAcrossRestart$'`
  - 預期失敗原因：尚未串接 Daemon 與真實重啟恢復。
- **TDD 內迴圈**：
  - 測試檔案：`internal/storage/sqlite_cron_test.go:TestSQLiteStore_CronCRUD`
  - 命令：`go test -count=1 -v ./internal/storage -run '^TestSQLiteStore_CronCRUD$'`
  - 預期失敗原因：Store 未實作 `CreateCronSchedule`、`ListCronSchedules` 等方法。
- **最小實作與回歸**：在 `internal/storage/sqlite.go` 實作相關 SQL 查詢；執行 `go test -count=1 -race ./internal/storage` 全綠（日誌見 `evidence/phase-1/final-regression.log`）。

---

## Phase 2 — 容量調度器升級（同專案互斥佇列與保留通道）

### Goal
落實 CRON-001（同專案互斥排隊與依序執行）與 CRON-002（保留互動通道與背景容量上限），阻斷並行寫入衝突並保障人類即時互動能力。

### Deliverables
- `internal/daemon/pool.go`：擴充 WorkerPool 支援背景專用容量上限（`maxCronWorkers = 1`）與保留互動通道。
- `internal/daemon/workspace_queue.go`：實作專案級別排他鎖與佇列狀態機。
- `internal/daemon/daemon.go`：整合同專案互斥排隊至 `Submit` 流程，任務回傳 `queued` 狀態。

### Dependencies
- Phase 1 儲存層支援。

### Tasks

#### Task 2.1：WorkerPool 保留互動通道與配額控制
- **任務說明**：在 WorkerPool 內劃分互動任務（interactive）與背景任務（cron），限制背景任務同時執行的 Worker 上限為 1，保留其餘 Workers 專門服務即時互動任務。
- **預期輸出**：`internal/daemon/pool.go` 支援任務來源分類與背景配額守門。
- **涉及模組或檔案**：`internal/daemon/pool.go`, `internal/daemon/pool_test.go`
- **完成判準**：
  1. 當 1 個背景 Cron 任務在執行時，後續背景任務進入等待，不得佔用第 2 個 Worker。
  2. 此時提交互動任務，可立即取得第 2 個 Worker 啟動執行，不受背景任務阻礙。
- **驗證方式**：`go test -count=1 -v ./internal/daemon -run '^TestWorkerPool_InteractiveChannelReserved$'`
- **覆蓋項目**：S-01 容量與寫入政策、CRON-002
- **責任角色**：唯一負責
- **Scenario ID**：CRON-002
- **改變可觀察行為**：是
- **BDD 外迴圈**：
  - 測試檔案：`tests/integration/cron_test.go:TestCRON002_InteractiveChannelReserved`
  - 命令：`go test -count=1 -v ./tests/integration -run '^TestCRON002_InteractiveChannelReserved$'`
  - 預期失敗原因：背景任務佔用 Worker 後，互動任務被阻塞或逾時。
- **TDD 內迴圈**：
  - 測試檔案：`internal/daemon/pool_test.go:TestWorkerPool_InteractiveChannelReserved`
  - 命令：`go test -count=1 -v ./internal/daemon -run '^TestWorkerPool_InteractiveChannelReserved$'`
  - 預期失敗原因：WorkerPool 未提供依 TaskType 分配之配額機制。
- **最小實作與回歸**：修改 `internal/daemon/pool.go` 加入通道計數保護；執行 `go test -count=1 -race ./internal/daemon` 全綠。

#### Task 2.2：同專案互斥排隊（Workspace Serialization Queue）
- **任務說明**：在 Daemon 中樞實作專案目錄排他鎖。當 Workspace 內已有執行中任務時，同目錄新任務標記為 `queued` 狀態進入專案佇列，待前一任務結束後依序自動派工。
- **預期輸出**：`internal/daemon/workspace_queue.go` 專案鎖管理邏輯，並串入 `daemon.go` 的 `Submit` 與任務結束回呼。
- **涉及模組或檔案**：`internal/daemon/daemon.go`, `internal/daemon/workspace_queue.go`, `internal/daemon/workspace_queue_test.go`
- **完成判準**：
  1. 同 Workspace 並發提交 2 個任務，任務 1 為 `running`，任務 2 為 `queued`。
  2. 兩者受控後端絕不並發執行（執行次數時序完全不重疊）。
  3. 任務 1 結束後，任務 2 自動被喚醒並執行完畢。
- **驗證方式**：`go test -count=1 -v ./tests/integration -run '^TestCRON001_WorkspaceMutexQueue$'`
- **覆蓋項目**：S-01 同專案互斥、CRON-001
- **責任角色**：唯一負責
- **Scenario ID**：CRON-001
- **改變可觀察行為**：是
- **BDD 外迴圈**：
  - 測試檔案：`tests/integration/cron_test.go:TestCRON001_WorkspaceMutexQueue`
  - 命令：`go test -count=1 -v ./tests/integration -run '^TestCRON001_WorkspaceMutexQueue$'`
  - 預期失敗原因：同專案兩個任務同時進入 running 且並發執行。
- **TDD 內迴圈**：
  - 測試檔案：`internal/daemon/workspace_queue_test.go:TestWorkspaceQueue_SerializeExecution`
  - 命令：`go test -count=1 -v ./internal/daemon -run '^TestWorkspaceQueue_SerializeExecution$'`
  - 預期失敗原因：未實作專案排他鎖或未回傳 queued 狀態。
- **最小實作與回歸**：在 `internal/daemon/` 實作專案鎖；執行 `go test -count=1 -race ./internal/daemon` 與外迴圈整合測試全綠。

---

## Phase 3 — 內嵌 Cron 排程引擎核心

### Goal
在 Daemon 常駐進程內實作 Cron 排程調度引擎，支援標準 5 欄位 Cron、本地時區計算、定時觸發任務、重疊略過（Skip）、重啟不補跑（No Catchup）與非授權目錄嚴格回絕自動暫停。

### Deliverables
- `internal/daemon/scheduler.go`：內嵌排程調度器核心，支援假時鐘（Clock interface）注入測試。
- `internal/daemon/scheduler_test.go`：時鐘演算、重疊保護與未授權拒絕之單元測試。
- 端到端整合測試覆蓋 CRON-004、CRON-005、CRON-006、CRON-007。

### Dependencies
- Phase 1（儲存）與 Phase 2（容量與專案互斥）。

### Tasks

#### Task 3.1：標準 5 欄位 Cron 解析與週期觸發（CRON-004 & CRON-006）
- **任務說明**：實作標準 5 欄位 Cron 表達式解析（分 時 日 月 星期），依 `time.Local` 計算下次觸發時間；每當預定時間抵達時，自動建立標準 Task 並送入中樞執行。重啟時直接以當前時間計算下個未來點，不補跑過期任務。
- **預期輸出**：`internal/daemon/scheduler.go`
- **涉及模組或檔案**：`internal/daemon/scheduler.go`, `internal/daemon/scheduler_test.go`
- **完成判準**：
  1. 正確解析標準 5 欄位表達式（如 `*/5 * * * *`）。
  2. 假時鐘推進時觸發任務，並正確更新 `last_run_at` 與 `next_run_at`。
  3. 模擬離線 2 小時後重啟，未補跑過去漏掉的觸發次數，直接以重啟時刻推算下一次未來時間。
- **驗證方式**：`go test -count=1 -v ./internal/daemon -run '^TestScheduler_PeriodicTrigger_And_NoCatchup$'`
- **覆蓋項目**：S-02 排程語意、CRON-004、CRON-006
- **責任角色**：唯一負責
- **Scenario ID**：CRON-004, CRON-006
- **改變可觀察行為**：是
- **BDD 外迴圈**：
  - 測試檔案：`tests/integration/cron_test.go:TestCRON004_PeriodicTriggerCreatesTask`、`TestCRON006_RestartNoCatchup`
  - 命令：`go test -count=1 -v ./tests/integration -run '^TestCRON00[46]'`
  - 預期失敗原因：Daemon 尚未內建排程輪詢調度器。
- **TDD 內迴圈**：
  - 測試檔案：`internal/daemon/scheduler_test.go:TestScheduler_PeriodicTrigger_And_NoCatchup`
  - 命令：`go test -count=1 -v ./internal/daemon -run '^TestScheduler_PeriodicTrigger_And_NoCatchup$'`
  - 預期失敗原因：未實作 `Scheduler` 結構體與時鐘推進觸發。
- **最小實作與回歸**：在 `internal/daemon/scheduler.go` 實作排程計時器與下次時間推算；回歸全套測試。

#### Task 3.2：排程執行重疊偵測與略過保護（CRON-005）
- **任務說明**：當排程之觸發時刻抵達時，檢查該排程先前建立的任務是否仍在 `running` 或 `queued`。若是，直接略過（Skip）本次觸發，不建立新任務，並在 `cron_runs` 記錄狀態為 `skipped` 與原因。
- **預期輸出**：`internal/daemon/scheduler.go` 加入重疊防護。
- **涉及模組或檔案**：`internal/daemon/scheduler.go`, `internal/daemon/scheduler_test.go`
- **完成判準**：
  1. 前次任務仍在執行中時，觸發時間抵達不產生新 Task。
  2. `cron_runs` 記錄該次為 `skipped`，說明前次任務尚未完成。
- **驗證方式**：`go test -count=1 -v ./tests/integration -run '^TestCRON005_OverlapSkipped$'`
- **覆蓋項目**：S-02 重疊保護、CRON-005
- **責任角色**：唯一負責
- **Scenario ID**：CRON-005
- **改變可觀察行為**：是
- **BDD 外迴圈**：
  - 測試檔案：`tests/integration/cron_test.go:TestCRON005_OverlapSkipped`
  - 命令：`go test -count=1 -v ./tests/integration -run '^TestCRON005_OverlapSkipped$'`
  - 預期失敗原因：重複觸發產生了第二個任務或引發並發錯誤。
- **TDD 內迴圈**：
  - 測試檔案：`internal/daemon/scheduler_test.go:TestScheduler_OverlapSkipped`
  - 命令：`go test -count=1 -v ./internal/daemon -run '^TestScheduler_OverlapSkipped$'`
  - 預期失敗原因：排程器未檢查既有任務狀態直接發起觸發。
- **最小實作與回歸**：實作排程重疊狀態檢查；執行 `go test -count=1 -race ./internal/daemon`。

#### Task 3.3：非授權目錄排程觸發嚴格拒絕與自動暫停（CRON-007）
- **任務說明**：排程觸發當下複查 Workspace 是否在 Daemon 之 `allowed-roots` 內。若否，回絕啟動後端，在 `cron_runs` 標記為 `unauthorized`，並自動將該排程設為 `paused` 且記錄原因。
- **預期輸出**：`internal/daemon/scheduler.go` 授權防逃逸檢查。
- **涉及模組或檔案**：`internal/daemon/scheduler.go`, `internal/daemon/scheduler_test.go`
- **完成判準**：
  1. 目錄不在授權清單時，受控後端執行次數嚴格為 0。
  2. `cron_runs` 記錄狀態為 `unauthorized`。
  3. `cron_schedules` 狀態自動變更為 `paused`，`pause_reason` 明確記載目錄未獲授權。
- **驗證方式**：`go test -count=1 -v ./tests/integration -run '^TestCRON007_UnauthorizedDirectoryRejection$'`
- **覆蓋項目**：S-04 授權邊界、CRON-007
- **責任角色**：唯一負責
- **Scenario ID**：CRON-007
- **改變可觀察行為**：是
- **BDD 外迴圈**：
  - 測試檔案：`tests/integration/cron_test.go:TestCRON007_UnauthorizedDirectoryRejection`
  - 命令：`go test -count=1 -v ./tests/integration -run '^TestCRON007_UnauthorizedDirectoryRejection$'`
  - 預期失敗原因：觸發時未檢查 `policy.AuthorizeWorkspace` 或後端仍被啟動。
- **TDD 內迴圈**：
  - 測試檔案：`internal/daemon/scheduler_test.go:TestScheduler_UnauthorizedDirectoryRejection`
  - 命令：`go test -count=1 -v ./internal/daemon -run '^TestScheduler_UnauthorizedDirectoryRejection$'`
  - 預期失敗原因：未實作授權複查或自動暫停邏輯。
- **最小實作與回歸**：在排程執行前加入 `AuthorizeWorkspace` 檢查；執行 `go test -count=1 -race ./internal/daemon`。

---

## Phase 4 — CLI 子命令 `asura cron` 與 IPC 接線

### Goal
實作 `asura cron` 系列子命令（add, list, pause, resume, remove），透過 Daemon IPC 進行排程的動態管理與狀態查詢。

### Deliverables
- `internal/daemon/daemon.go`：新增 cron 相關 IPC 操作（`cron_add`, `cron_list`, `cron_pause`, `cron_resume`, `cron_remove`）。
- `cmd/asura/cron.go`：新增 `handleCron` 子命令分派與終端表格化輸出。
- `cmd/asura/run_test.go`：CLI 參數校驗單元測試。

### Dependencies
- Phase 1～3 核心排程與儲存能力。

### Tasks

#### Task 4.1：Daemon IPC 擴充（Cron 管理通訊協定）
- **任務說明**：在 Daemon 的 IPC 處理器加入排程管理的請求路由，提供新增、清單查詢、暫停、恢復與刪除的 IPC 介面。
- **預期輸出**：`internal/daemon/daemon.go` 支援 cron IPC 操作。
- **涉及模組或檔案**：`internal/daemon/daemon.go`, `internal/daemon/daemon_test.go`
- **完成判準**：
  1. 客戶端透過 IPC 可成功新增排程並取得指派的 Cron ID。
  2. 支援查詢排程清單、暫停指定排程、恢復指定排程與刪除排程。
- **驗證方式**：`go test -count=1 -v ./internal/daemon -run '^TestDaemon_CronIPC$'`
- **覆蓋項目**：S-04 操作介面、CRON-003
- **責任角色**：唯一負責
- **Scenario ID**：CRON-003
- **改變可觀察行為**：是
- **BDD 外迴圈**：
  - 測試檔案：`tests/integration/cron_test.go:TestCRON003_CLICronManagement`
  - 命令：`go test -count=1 -v ./tests/integration -run '^TestCRON003_CLICronManagement$'`
  - 預期失敗原因：IPC 回報 unknown operation `cron_add`。
- **TDD 內迴圈**：
  - 測試檔案：`internal/daemon/daemon_test.go:TestDaemon_CronIPC`
  - 命令：`go test -count=1 -v ./internal/daemon -run '^TestDaemon_CronIPC$'`
  - 預期失敗原因：Daemon 缺少 cron IPC 處理方法。
- **最小實作與回歸**：在 `internal/daemon/daemon.go` 實作 IPC 分發；執行 `go test -count=1 -race ./internal/daemon`。

#### Task 4.2：CLI `asura cron` 系列子命令與輸入校驗
- **任務說明**：實作 `asura cron` 進入點，解析 `add`, `list`, `pause`, `resume`, `remove` 參數。`add` 檢查必填欄位與 5 欄位表達式合法性；`list` 以對齊表格輸出排程名稱、目錄、表達式、狀態、下次觸發時間等。
- **預期輸出**：`cmd/asura/cron.go`, `cmd/asura/run.go`
- **涉及模組或檔案**：`cmd/asura/cron.go`, `cmd/asura/run.go`, `cmd/asura/run_test.go`
- **完成判準**：
  1. 參數缺少或表達式格式錯誤時，返回非 0 狀態碼並輸出清楚的錯誤提示。
  2. 格式正確時透過 IPC 呼叫 Daemon 成功執行，並印出友善的成功訊息或表格。
- **驗證方式**：`go test -count=1 -v ./cmd/asura -run '^TestRunCLI_CronValidation$'`
- **覆蓋項目**：CRON-003
- **責任角色**：唯一負責
- **Scenario ID**：CRON-003
- **改變可觀察行為**：是
- **BDD 外迴圈**：
  - 測試檔案：`tests/integration/cron_test.go:TestCRON003_CLICronManagement`
  - 命令：`go test -count=1 -v ./tests/integration -run '^TestCRON003_CLICronManagement$'`
  - 預期失敗原因：CLI 未實作 `cron` 子命令。
- **TDD 內迴圈**：
  - 測試檔案：`cmd/asura/run_test.go:TestRunCLI_CronValidation`
  - 命令：`go test -count=1 -v ./cmd/asura -run '^TestRunCLI_CronValidation$'`
  - 預期失敗原因：`cron` 未知子命令錯誤。
- **最小實作與回歸**：在 `cmd/asura/` 接線；執行 `go test -count=1 -race ./cmd/asura`。

---

## Phase 5 — 全套整合回歸、邊界驗證與使用文件更新

### Goal
完成 CRON-001～CRON-008 全套驗收劇本的端到端整合驗證，進行 `-race` 競爭條件檢測，並更新使用者操作手冊與測試日誌。

### Deliverables
- `tests/integration/cron_test.go`：CRON-001～008 全數通過。
- `README.md`：補充 `asura cron` 的命令語法與排程操作說明。
- `docs/issues/issue-6/evidence/`：完整留存各階段外紅、內紅、最小綠燈與全套回歸日誌。

### Dependencies
- Phase 1～4 全部完成。

### Tasks

#### Task 5.1：端到端整合全套回歸與 Race 檢測
- **任務說明**：執行全部 8 個套件的完整回歸測試，確認容量互斥、排程觸發、重啟持久化無任何資料競態（0 FAIL, 0 SKIP）。
- **預期輸出**：`docs/issues/issue-6/evidence/phase-5/final-regression.log`
- **涉及模組或檔案**：`tests/integration/cron_test.go`, 所有 Go 原始碼
- **完成判準**：`go test -count=1 -race -v ./...` 全部通過，退出碼為 0。
- **驗證方式**：`go test -count=1 -race ./...`
- **覆蓋項目**：CRON-001～CRON-008 全套驗收
- **責任角色**：唯一負責
- **Scenario ID**：CRON-001, CRON-002, CRON-003, CRON-004, CRON-005, CRON-006, CRON-007, CRON-008
- **改變可觀察行為**：否
- **等價證據**：`go test -count=1 -race -v ./...` 全套回歸測試輸出，退出碼 0，零資料競態。
- **最小實作與回歸**：無 production code 修改，純回歸驗收。

#### Task 5.2：專案操作手冊與使用文件更新
- **任務說明**：在根目錄 `README.md` 中新增 `asura cron` 子命令的語法格式、5 欄位 Cron 範例、狀態查詢與暫停/恢復說明。
- **預期輸出**：`README.md`
- **涉及模組或檔案**：`README.md`
- **完成判準**：文件包含清晰的 `asura cron add/list/pause/resume/remove` 範例與排程注意事項。
- **驗證方式**：人工檢視與 Markdown 語法檢查。
- **覆蓋項目**：S-04 使用者文件
- **責任角色**：唯一負責
- **Scenario ID**：CRON-003
- **改變可觀察行為**：否
- **等價證據**：文件靜態檢查通過，格式正確無壞連線。
- **最小實作與回歸**：更新 `README.md`。

---

## 4. 完整覆蓋核對表

| Scenario ID | 核准狀態 | 責任 Task | BDD 外迴圈測試 | TDD 內迴圈測試 |
|---|---|---|---|---|
| **CRON-001** | 已核准 | Task 2.2 | `tests/integration/cron_test.go:TestCRON001_WorkspaceMutexQueue` | `internal/daemon/workspace_queue_test.go` |
| **CRON-002** | 已核准 | Task 2.1 | `tests/integration/cron_test.go:TestCRON002_InteractiveChannelReserved` | `internal/daemon/pool_test.go` |
| **CRON-003** | 已核准 | Task 4.2 (支援 Task 4.1, 5.2) | `tests/integration/cron_test.go:TestCRON003_CLICronManagement` | `cmd/asura/run_test.go` |
| **CRON-004** | 已核准 | Task 3.1 | `tests/integration/cron_test.go:TestCRON004_PeriodicTriggerCreatesTask` | `internal/daemon/scheduler_test.go` |
| **CRON-005** | 已核准 | Task 3.2 | `tests/integration/cron_test.go:TestCRON005_OverlapSkipped` | `internal/daemon/scheduler_test.go` |
| **CRON-006** | 已核准 | Task 3.1 | `tests/integration/cron_test.go:TestCRON006_RestartNoCatchup` | `internal/daemon/scheduler_test.go` |
| **CRON-007** | 已核准 | Task 3.3 | `tests/integration/cron_test.go:TestCRON007_UnauthorizedDirectoryRejection` | `internal/daemon/scheduler_test.go` |
| **CRON-008** | 已核准 | Task 1.2 (支援 Task 1.1) | `tests/integration/cron_test.go:TestCRON008_PersistenceAcrossRestart` | `internal/storage/sqlite_cron_test.go` |
