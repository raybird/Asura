# Issue 5 實作計畫 (Implementation Plan)

建立日期：2026-09-13。

## 1. 實作步驟排序原則與風險優先考量

依據專案核心規則《風險決定驗證順序》：
- **首要風險**：未授權專案目錄在背景被定時觸發並執行本機 AI，造成安全漏洞；或多任務並行讀寫同一專案造成原始碼損壞。
- **排序原則**：
  1. 先建立 SQLite 儲存與 Schema 遷移，提供狀態與紀錄基底。
  2. 接著實作同專案互斥鎖與容量分流控制（優先降低寫入衝突風險）。
  3. 再實作排程調度核心、時鐘計算、授權複查與重疊/漏跑保護。
  4. 最後實作 CLI 子命令接線與端到端全套整合測試。

---

## 2. 分階段實作步驟

### Phase 1：儲存層 Schema 擴充與排程資料庫支援
- **目標**：支援 `cron_schedules` 與 `cron_runs` 的 CRUD 與狀態持久化。
- **外迴圈（BDD 整合）**：
  - `tests/integration/cron_test.go`：驗證排程儲存與重啟後狀態完整性（CRON-008 外紅）。
- **內迴圈（TDD 單元）**：
  - `internal/storage/sqlite_cron_test.go`：驗證建立排程、更新下次觸發時間、記錄觸發歷史、查詢清單與外鍵串聯刪除（內紅 -> 實作 -> 內綠）。
- **產出檔案**：
  - `internal/storage/sqlite.go`（Schema 遷移與 CRUD 方法）
  - `internal/storage/sqlite_cron_test.go`

### Phase 2：容量調度器升級（同專案互斥佇列與保留通道）
- **目標**：落實 CRON-001（同專案排隊等待）與 CRON-002（保留互動通道）。
- **外迴圈（BDD 整合）**：
  - `tests/integration/cron_test.go`：同專案並發提交任務 1 與 2，驗證任務 2 進入 `queued` 且依序執行；背景任務佔用時，即時互動任務不受阻礙（CRON-001、CRON-002 外紅）。
- **內迴圈（TDD 單元）**：
  - `internal/daemon/pool_test.go`：擴充 WorkerPool 分流配額控制單元測試。
  - `internal/daemon/workspace_queue_test.go`：同專案任務狀態轉移單元測試。
- **產出檔案**：
  - `internal/daemon/pool.go`
  - `internal/daemon/daemon.go`

### Phase 3：內嵌 Cron 排程引擎核心
- **目標**：落實 CRON-004（週期觸發）、CRON-005（重疊略過）、CRON-006（重啟不補跑）與 CRON-007（未授權拒絕與自動暫停）。
- **外迴圈（BDD 整合）**：
  - `tests/integration/cron_test.go`：模擬時鐘前進觸發任務、重疊時略過、重啟後計算下次時間不補跑、清單外目錄觸發回絕並標示 paused。
- **內迴圈（TDD 單元）**：
  - `internal/daemon/scheduler_test.go`：以假時鐘驗證 5 欄位 Cron 表達式計算、重疊偵測、未授權拒絕邏輯。
- **產出檔案**：
  - `internal/daemon/scheduler.go`
  - `internal/daemon/scheduler_test.go`

### Phase 4：CLI 子命令 `asura cron` 與 IPC 接線
- **目標**：落實 CRON-003（動態建立與管理排程），提供 CLI 操作體驗。
- **外迴圈（BDD 整合）**：
  - `tests/integration/cron_test.go`：透過 CLI 執行 `cron add`、`cron list`、`cron pause`、`cron resume`、`cron remove` 與 Daemon IPC 交互驗證。
- **內迴圈（TDD 單元）**：
  - `cmd/asura/run_test.go`：測試 `asura cron` 參數缺少、表達式無效等輸入驗證。
- **產出檔案**：
  - `cmd/asura/cron.go`
  - `cmd/asura/run.go`
  - `cmd/asura/run_test.go`

### Phase 5：全套整合回歸、邊界驗證與使用手冊更新
- **目標**：通過 CRON-001～CRON-008 全套測試、`-race` 檢測與說明文件更新。
- **驗證項目**：
  - 全套 `go test -count=1 -race ./...` 回歸。
  - 更新 `README.md` 說明 `asura cron` 的使用方法與範例。
- **產出檔案**：
  - `README.md`
  - 留存測試證據於 `docs/issues/issue-5/evidence/`

---

## 3. Gherkin 與測試雙迴圈映射表

| Scenario ID | 場景名稱 | 外迴圈測試 (Integration) | 內迴圈測試 (Unit) |
|---|---|---|---|
| **CRON-001** | 同專案互斥排隊與依序執行 | `tests/integration/cron_test.go:TestCRON001_WorkspaceMutexQueue` | `internal/daemon/workspace_queue_test.go` |
| **CRON-002** | 保留互動通道與背景容量上限 | `tests/integration/cron_test.go:TestCRON002_InteractiveChannelReserved` | `internal/daemon/pool_test.go` |
| **CRON-003** | 動態建立與管理排程 | `tests/integration/cron_test.go:TestCRON003_CLICronManagement` | `cmd/asura/run_test.go` |
| **CRON-004** | 排程週期觸發產生可追蹤任務 | `tests/integration/cron_test.go:TestCRON004_PeriodicTriggerCreatesTask` | `internal/daemon/scheduler_test.go` |
| **CRON-005** | 排程執行重疊時略過不重複堆疊 | `tests/integration/cron_test.go:TestCRON005_OverlapSkipped` | `internal/daemon/scheduler_test.go` |
| **CRON-006** | 重啟或中斷後不補跑過期排程 | `tests/integration/cron_test.go:TestCRON006_RestartNoCatchup` | `internal/daemon/scheduler_test.go` |
| **CRON-007** | 觸發時非授權目錄嚴格拒絕並自動暫停 | `tests/integration/cron_test.go:TestCRON007_UnauthorizedDirectoryRejection` | `internal/daemon/scheduler_test.go` |
| **CRON-008** | 排程與觸發紀錄於服務重啟後完整持久化 | `tests/integration/cron_test.go:TestCRON008_PersistenceAcrossRestart` | `internal/storage/sqlite_cron_test.go` |
