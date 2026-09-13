# Issue 6 技術方案取捨 (Technical Analysis)

建立日期：2026-09-13。

## 1. 架構方案評估與取捨

針對排程器與容量調度的架構定位，評估以下兩個可行方案：

| 評估維度 | 方案 A：內嵌於 Daemon 常駐進程（採用） | 方案 B：獨立 `asura-cron` 外部守護行程 |
|---|---|---|
| **系統拓撲** | 單一 Daemon 進程管理排程、容量、SQLite 與 WorkerPool | 額外新增獨立的 Cron 守護進程，需獨立啟動與維護 |
| **通訊成本** | 內部 Go Channel 與記憶體調度，零 IPC 額外開銷 | 每次觸發需透過 Unix Domain Socket 呼叫 Daemon IPC |
| **容量與狀態協調** | 排程器可直接感知當前 Worker 佔用與專案互斥鎖 | 外部行程無法精確感知 Daemon 內部 Worker 分佈，協商複雜 |
| **重疊與死鎖風險** | 在 Daemon 內部單一 Lock 內完成重疊檢查，無競態漏洞 | 需依賴分散式鎖或來回查詢，容易在併發時產生重疊任務 |
| **維運複雜度** | 極簡：`asura daemon` 啟動即具備排程能力 | 繁瑣：需同時管理兩個系統服務的啟停與重啟 |

**決策**：採**方案 A**。符合「簡單優先」原則，由單一 Daemon 掌握完整的全域狀態、工作階段與專案鎖。

---

## 2. 核心技術設計

### 2.1 同專案互斥鎖（Workspace Serialization Queue）
- 在 `internal/daemon` 引入專案級別的狀態追蹤器：
  ```go
  type WorkspaceScheduler struct {
      mu       sync.Mutex
      active   map[string]string         // workspace path -> running taskID
      queues   map[string][]*QueuedTask  // workspace path -> waiting tasks FIFO
  }
  ```
- 當提交任務時，若目標 Workspace 已有 `running` 任務，該任務狀態標記為 `queued` 並記入 `queues[ws]`。
- 當前任務執行完畢（無論成功、失敗或取消）釋放專案鎖時，自動從該專案的 `queues[ws]` 取出下一個任務投入 WorkerPool。

### 2.2 容量分流與保留互動通道（Capacity Partitioning）
- 預設 `MaxWorkers = 2`：
  - **背景 Cron 任務上限**：嚴格限制最多佔用 1 個 Worker（`maxCronWorkers = 1`）。
  - **人類互動任務（CLI / Web）**：可使用剩餘的所有 Workers，確保隨時有至少 1 個專用 Worker 保持即時響應，杜絕背景工作佔滿系統資源。

### 2.3 SQLite Schema 擴充

在 `internal/storage/sqlite.go` 中新增兩張資料表：

```sql
CREATE TABLE IF NOT EXISTS cron_schedules (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    workspace TEXT NOT NULL,
    schedule TEXT NOT NULL,         -- 5-field cron expression
    prompt TEXT NOT NULL,
    status TEXT NOT NULL,           -- 'active' | 'paused'
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    last_run_at TIMESTAMP,
    next_run_at TIMESTAMP,
    pause_reason TEXT
);

CREATE TABLE IF NOT EXISTS cron_runs (
    id TEXT PRIMARY KEY,
    cron_id TEXT NOT NULL,
    scheduled_time TIMESTAMP NOT NULL,
    triggered_time TIMESTAMP NOT NULL,
    task_id TEXT,                   -- 關聯建立的 tasks.id（若略過或拒絕則為空）
    status TEXT NOT NULL,           -- 'triggered' | 'skipped' | 'unauthorized' | 'failed'
    reason TEXT,
    FOREIGN KEY (cron_id) REFERENCES cron_schedules(id) ON DELETE CASCADE
);
```

### 2.4 Cron 表達式解析器
- 遵循 Go 簡潔標準，可使用經過社群廣泛驗證、輕量級且零額外 CGO 依賴的標準 Cron 解析模組（或標準庫時間計算邏輯），嚴格解析標準 5 欄位（分 時 日 月 星期），依 `time.Local` 計算下次觸發時間。
