# CLI 參考

`asura` 各子命令的用法與行為契約。Runtime 選擇與稽核語意見 [Runtime 與執行控制](runtimes.md)，記憶相關命令見 [專案記憶](memory.md)，Web 入口見 [Web Console](web-console.md)。

## daemon

```bash
# 啟動常駐執行中樞（監聽 Unix Domain Socket，集中管理排程與執行容量）
./bin/asura daemon --socket /tmp/asura.sock --allowed-roots /path/to/project --workers 2 --queue 10
```

2026-09-13：經 Daemon 提交、查詢、取消或重送任務時，都套用服務端 `--allowed-roots`。專案移出清單後，其歷史任務仍保存在資料庫，但不能透過 Daemon 讀取或取消；指向清單外的符號連結亦遭拒絕。此清單是入口授權政策，不是作業系統沙箱。

Daemon 讀取的環境變數：

| 變數 | 用途 | 說明 |
|---|---|---|
| `ASURA_RUNTIME_PRIORITY` | `auto` 選擇的 runtime 偏好順序 | [Runtime 與執行控制](runtimes.md#自動選擇與優先序) |
| `ASURA_CONTEXT_LIMIT`／`ASURA_CONTEXT_POLICY` | 上下文窗口限制與 `observe`／`reject` 政策 | [Runtime 與執行控制](runtimes.md#上下文窗口管理與-token-審計) |
| `ASURA_MEMORY_UTILITY_POLICY` | 記憶效用 `observe`／`apply` | [專案記憶](memory.md#記憶效用回饋與稽核) |

## ask

```bash
# 透過 Daemon 中樞提交 AI 任務（非同步接受立即回傳 Task ID，斷線不中斷，預設 direct 模式、opencode 後端）
./bin/asura ask --socket /tmp/asura.sock --workspace /path/to/project "分析專案代碼並整理函式呼叫圖"

# 啟用 Three-Face 固定管線模式（Oracle 規劃 → Striker 執行 → Critic 審查）
./bin/asura ask --socket /tmp/asura.sock --workspace /path/to/project --mode three-face "重構認證模組並補齊測試"

# 指定後端執行器（opencode、codex、omp、antigravity 或 auto，預設 opencode）
./bin/asura ask --socket /tmp/asura.sock --workspace /path/to/project --backend codex "修復單元測試"

# 提交並等待任務完成輸出結果
./bin/asura ask --socket /tmp/asura.sock --workspace /path/to/project --wait "執行測試並回報結果"

# 啟用有界記憶召回（見 memory.md）
./bin/asura ask --socket /tmp/asura.sock --workspace /path/to/project \
  --memory-recall bounded --memory-id explicit-id "檢查 SQLite 隔離規則"
```

## run

```bash
# 本機直接執行指令並追蹤任務（預設 direct 模式、opencode 後端）
./bin/asura run \
  --workspace /path/to/project \
  --allowed-roots /path/to/project \
  "echo 'Hello Asura'"

# 指定以 Three-Face 模式與指定後端執行
./bin/asura run \
  --workspace /path/to/project \
  --allowed-roots /path/to/project \
  --backend codex \
  --mode three-face \
  "make test"
```

2026-10-03：本機 `run --backend opencode/codex` 保留既有 shell 命令執行，不呼叫 runtime 或封存其能力；CLI status 與 Web 稽核明示 `legacy/unavailable`。`run --backend auto` 的參數則是 AI prompt，見 [自動選擇與優先序](runtimes.md#自動選擇與優先序)。

## `ask` 與 `run` 的後端行為

2026-09-30：`ask` 與 `run` 支援 `--backend opencode|codex|omp|antigravity` 旗標（未指定預設為 `opencode`）。指定未知後端時將立即 fail-closed 拒絕，0 模型調用且絕不自動切換。Three-Face 模式下後端須支援唯讀沙箱（`CapReadOnlySandbox`），若能力不符立即拒絕。任務執行期間若部分失敗，保存現場並標記 Failed，絕無第二次自動容錯嘗試（No Automatic Failover）。

各 runtime 的版本與隔離條件見 [Runtime 與執行控制](runtimes.md#runtime-adapter)。

## status

```bash
# 向 Daemon 或儲存層查詢指定任務狀態、執行模式與 Pipeline 各階段歷程
./bin/asura status <task-id>
```

`status` 顯示的欄位（封存 inventory、stage binding、Recall、Utility）見 [Runtime 與執行控制](runtimes.md) 與 [專案記憶](memory.md)。

## audit

```bash
./bin/asura audit --id <task-id> --db asura.db
./bin/asura audit --id <task-id> --socket /path/to/asura.sock --json
```

輸出內容與語意見 [執行稽核](runtimes.md#執行稽核)。

## cancel

```bash
# 從共同入口發送取消請求，終止任務進程組並確保進程樹安全回收
./bin/asura cancel <task-id>
```

## runtime inventory

```bash
# 2026-10-04：查詢當下 fresh inventory；指定 socket 時優先使用 daemon
./bin/asura runtime inventory
./bin/asura runtime inventory --socket /path/to/asura.sock
```

語意見 [可用性清冊](runtimes.md#可用性清冊)。

## cron

```bash
# 新增定時排程（標準 5 欄位 Cron：分 時 日 月 週）
./bin/asura cron add \
  --name "nightly-audit" \
  --workspace /path/to/project \
  --schedule "0 3 * * *" \
  --prompt "執行靜態檢查與測試"

# 查詢所有排程清單與下次執行時間
./bin/asura cron list

# 查詢特定排程歷史觸發紀錄
./bin/asura cron runs <cron-id> --limit 10

# 暫停排程（不再自動觸發）
./bin/asura cron pause <cron-id>

# 恢復排程（重新計算下次執行時間並啟用）
./bin/asura cron resume <cron-id>

# 刪除排程（連帶清理歷史觸發紀錄）
./bin/asura cron remove <cron-id>
```

2026-09-13：
- **同專案排他佇列**：排程任務與互動任務共用專案互斥鎖，同一工作目錄絕不並行執行，避免檔案衝突。
- **保留互動通道**：保留獨立執行槽位供互動任務（`ask`），排程暴增時 CLI 互動請求絕不被餓死。
- **重疊略過與漏跑不補**：前次任務執行中若再度到期自動略過（`skipped`）；Daemon 重啟時過期任務自動重新對齊未來時刻，不補跑歷史。
- **未授權目錄自動暫停**：排程工作目錄若未在 Daemon `--allowed-roots` 內，觸發時嚴格拒絕並自動轉為 `paused`。
- **不可達日期安全防護**：如 2 月 31 日等日曆上不可達之排程在新增時即刻拒絕，防止計時器陷入零值雪崩循環。
- **日與週匹配語意**：若表達式中同時指定「日（Day of Month）」與「週（Day of Week）」，採用交集（AND）條件匹配（即「當日且當週」同時成立才觸發）。

## memory

見 [專案記憶](memory.md)。

## web

見 [Web Console](web-console.md)。

## 執行核心保證

- **POSIX 進程樹隔離與精確回收**：獨立進程組（PGID）管理，取消任務時直達整組進程樹強殺（SIGKILL）；不可變核心時間戳記（`StartTime`）身分驗證，防禦 Linux PID 重用誤殺；三態進程組身分合約追蹤組內孤兒進程生命週期，杜絕假成功與殭屍進程殘留。
- **跨行程平坦排他鎖與狀態引擎**：啟動區間平坦排他鎖（`.task-<hash>.lock`）消除啟動與取消間的競態；單一 SQLite 狀態引擎統一管理任務佇列、執行狀態、日誌與追蹤資料。
- **後端異常防禦**：速率限制（429）錯誤辨識、空輸出防禦與部分失敗保護；失敗即 fail-closed，不自動切換 runtime。
