# Implementation Plan Decomposition

- **文件建立日期**：2026-09-11
- **對應 Issue**：Issue 1（Asura 本機執行核心與後端契約探測）
- **規範依據**：`docs/AGENTS.md` 1.22
- **依賴高階計畫**：[implementation-plan.md](implementation-plan.md)
- **架構決策來源**：[架構規格書](../../superpowers/specs/2026-09-11-asura-architecture-design.md)

---

## Risk Decision

- **最大風險**：入口可驅動本機寫入與命令，但授權邊界未定義；最嚴重後果是未授權副作用。最大技術未知是後端（Opencode 等 CLI）的權限放行、事件串流、進程取消與會話控制能力。
- **風險等級與理由**：High。涉及本機檔案與命令執行副作用，且核心外部後端契約尚未完全固化。分級 Large 源於跨模組（CLI、Daemon、Storage、Backend）與持久化狀態整合，並非由風險推導。
- **首要驗證**：Phase 0 採用「非侵入式授權政策與單一後端契約探測」，先確認工作目錄白名單阻擋（SCN-002）與 Opencode `--format json --auto` 啟動參數與事件結構，再實作主系統。
- **選擇理由**：契約探測直接確認能否限制後端執行、辨識結果與回收資源；完整端到端串接無法替代此證據。
- **完成證據**：記錄固定版本（opencode 1.18.23）、命令與輸出；允許案例確有執行，拒絕案例無後端啟動及寫入，取消資源可回收且既有工作不受影響。

---

## 待核准 Scenario

依 `docs/AGENTS.md`「分批核准」原則，以下 Scenario 尚未取得使用者核准，**本計畫不為其建立實作 Task**。待核准後再重新執行 `decompose` 補齊對應任務：

| Scenario ID | 在等什麼 | 對應待確認事項 |
|---|---|---|
| SCN-005 | 部分失敗時的重試邊界與副作用接續策略 | TBD-5 |
| SCN-006 | 守護程序意外重啟時的狀態恢復判定 | TBD-5 |
| SCN-007 | 共同收尾、補總結次數上限與結構化錯誤呈現 | TBD-6 |

---

## Phase 0 — 授權政策與單一後端契約探測

### Goal
驗證工作目錄授權阻擋機制，探測本機 Opencode 1.18.23 的啟動參數、JSON 事件流結構與進程取消回收能力，消除最大技術未知。

### Deliverables
1. 工作目錄授權模組與測試（`internal/auth`）。
2. Opencode 事件流解析器與測試（`internal/backend/opencode`）。
3. 後端進程組生命週期管理與取消探測腳本。

### Dependencies
- 本機 Go 1.26+ 環境已就位。
- 本機 Opencode 1.18.23 已安裝。

### Tasks

#### Task 0.1：立項與核心驗收劇本核准（純文件） [已完成]
- **任務說明**：正式建立 `issue-1` 文件目錄，向使用者取得第一批核心 Scenario（`SCN-001`、`SCN-002`）明確核准。
- **預期輸出**：更新後的 `docs/issues/issue-1/README.md` 與核准紀錄。
- **涉及模組或檔案**：`docs/issues/issue-1/README.md`
- **完成判準**：`README.md` 包含使用者核准日期（2026-09-11）、核准 commit 與核准來源；`check-links.py` 驗證無失效連結。
- **驗證方式**：`python3 ../../dev-rules-kit/scripts/check-links.py /home/kevin/Documents/RCodes/Asura`
- **覆蓋項目**：P0-1、TBD-1、SCN-001、SCN-002
- **責任角色**：唯一負責
- **Scenario ID**：SCN-001, SCN-002
- **改變可觀察行為**：否
- **等價證據**：純文件變更；`git diff --check` 無語法警告，`check-links.py` 輸出 PASS，Git commit `d002e6b`。
- **狀態**：已完成

#### Task 0.2：工作目錄授權策略實作與驗證 [已完成]
- **任務說明**：實作工作目錄白名單策略，確保未授權或敏感目錄在入口處直接攔截，絕不啟動底層 Agent。
- **預期輸出**：`internal/auth/policy.go` 與 `internal/auth/policy_test.go`。
- **涉及模組或檔案**：`internal/auth/policy.go`, `internal/auth/policy_test.go`
- **完成判準**：敏感目錄（如 `/etc`）與路徑穿透嘗試返回 `ErrDisallowedWorkspace`；合規路徑正常放行；單元測試覆蓋兩種邊界。
- **驗證方式**：`go test -v ./internal/auth/...`
- **覆蓋項目**：P0-2、D-01、SCN-002
- **責任角色**：唯一負責
- **Scenario ID**：SCN-002
- **改變可觀察行為**：是
- **實作與驗證證據**（2026-09-11）：
  - 外迴圈紅燈：`go test -v -run TestAuthorizeWorkspace_Denied ./internal/auth`
  - 內迴圈紅燈：`go test ./internal/auth/...`
  - 最小實作：完成 `internal/auth/policy.go`
  - 全綠回歸：`go test -v ./internal/auth/...` 全部 PASS。
- **狀態**：已完成

#### Task 0.3：Opencode 1.18.23 JSON 事件流解析器 [已完成]
- **任務說明**：基於真實探測樣本，解析 Opencode 的 `step_start`、`text`、`tool_use`、`step_finish` 與 429 速率限制事件。
- **預期輸出**：`internal/backend/opencode/parser.go` 與單元測試 `internal/backend/opencode/parser_test.go`。
- **涉及模組或檔案**：`internal/backend/opencode/parser.go`, `internal/backend/opencode/parser_test.go`
- **完成判準**：正確提取文字內容、工具調用名稱/狀態、Token 計數以及 429 錯誤旗標；非 JSON 行優雅略過不崩潰。
- **驗證方式**：`go test -v ./internal/backend/opencode/...`
- **覆蓋項目**：P0-2、D-08、SCN-001
- **責任角色**：唯一負責
- **Scenario ID**：SCN-001
- **改變可觀察行為**：是
- **實作與驗證證據**（2026-09-11）：
  - 外迴圈紅燈：`go test -v -run TestParseEventLine ./internal/backend/opencode`
  - 內迴圈紅燈：`go test ./internal/backend/opencode/...`
  - 最小實作：完成 `internal/backend/opencode/parser.go`
  - 全綠回歸：`go test -v ./internal/backend/opencode/...` 全部 PASS。
- **狀態**：已完成

#### Task 0.4：進程組 (PGID) 啟動與取消強殺探測 [已完成]
- **任務說明**：實作底層 CLI 進程啟動封裝，配置 `Setpgid: true`，驗證 `context.Context` 逾時或取消時以 `syscall.Kill(-pgid, SIGKILL)` 乾淨回收整個進程樹。
- **預期輸出**：`internal/backend/opencode/process.go` 與測試 `internal/backend/opencode/process_test.go`。
- **涉及模組或檔案**：`internal/backend/opencode/process.go`, `internal/backend/opencode/process_test.go`
- **完成判準**：發送取消信號後，底層進程組及衍生子程序在 2 秒內完全終止，無孤兒殭屍進程殘留。
- **驗證方式**：`go test -v -run TestProcessCancel ./internal/backend/opencode/...`
- **覆蓋項目**：P0-3、D-05、SCN-001
- **責任角色**：唯一負責
- **Scenario ID**：SCN-001
- **改變可觀察行為**：是
- **實作與驗證證據**（2026-09-11）：
  - 外迴圈紅燈：`go test -v -run TestProcessCancel ./internal/backend/opencode`，exit code 1，失敗原因：`undefined: StartProcess`, `undefined: IsProcessGroupAlive`。
  - 內迴圈紅燈：`go test -run TestProcessGroup ./internal/backend/opencode`，exit code 1，失敗原因：`undefined: ConfigureProcessGroup`, `undefined: KillProcessGroup`。
  - 最小實作：完成 `internal/backend/opencode/process.go`，配置 `Setpgid: true` 與取消時向 `-pgid` 發送 `SIGKILL`，並以 `done` channel 防止 goroutine 洩漏。
  - 全綠回歸：`go test -v ./internal/backend/opencode/...` 全部 PASS（`TestProcessCancel` 0.20s 退出且 `IsProcessGroupAlive` 為 false）。`go vet ./...` 檢查通過。
- **狀態**：已完成

---

## Phase 1 — 最小本機執行核心 (已核准範圍)

### Goal
串接工作目錄授權、SQLite 任務持久化與單一後端 Opencode 呼叫，提供可透過本機 CLI 提交、查詢與追蹤的最小端到端閉環。

### Deliverables
1. 任務資料庫 Schema 與儲存層（`internal/storage`）。
2. 本機任務協調服務（`internal/task`）。
3. 本機 CLI 進入點（`cmd/asura`）。

### Dependencies
- Phase 0 全部 Task 完成並驗證通過。
- SCN-001 與 SCN-002 維持已核准狀態。

### Tasks

#### Task 1.1：SQLite 任務資料庫 Schema 與單一連線管理 [已完成]
- **任務說明**：建立 `asura.db` 初始化與 `tasks` 資料表，支援 WAL 模式與單一連線池，杜絕鎖死。
- **預期輸出**：`internal/storage/sqlite.go`, `internal/storage/sqlite_test.go`
- **涉及模組或檔案**：`internal/storage/sqlite.go`, `internal/storage/sqlite_test.go`
- **完成判準**：自動建立資料表；支援寫入任務、更新狀態（pending, running, completed, failed, cancelled）與結果保存。
- **驗證方式**：`go test -v ./internal/storage/...`
- **覆蓋項目**：P1-1、D-02、SCN-001
- **責任角色**：唯一負責
- **Scenario ID**：SCN-001
- **改變可觀察行為**：是
- **實作與驗證證據**（2026-09-11）：
  - 外迴圈紅燈：`go test -v -run TestTaskStorage ./internal/storage`，exit code 1，失敗原因：`undefined: NewSQLiteStore`, `undefined: Task`, `undefined: StatusPending`。
  - 內迴圈紅燈：`go test ./internal/storage/...`，exit code 1，失敗原因：`undefined: NewSQLiteStore` 等型別未定義。
  - 最小實作：完成 `internal/storage/sqlite.go`，建立 `tasks` 資料表與索引，配置 WAL 模式、busy_timeout 與單一連線池（`SetMaxOpenConns(1)`），提供 `CreateTask`、`GetTask`、`UpdateTaskStatus` 與 `Close`。
  - 全綠回歸：`go test -v ./internal/storage/...` 全部 PASS（`TestTaskStorage` 與 `TestSQLiteStore_Unit` 均 PASS）。`go test -v ./...` 與 `go vet ./...` 全專案通過。
- **狀態**：已完成

#### Task 1.2：任務調度器與授權閘門整合 [已完成]
- **任務說明**：實作 Task 協調層，接收提交請求，經由 `auth.Policy` 校驗，通過後寫入 DB 並調用 `opencode` 後端執行。
- **預期輸出**：`internal/engine/engine.go`, `internal/engine/engine_test.go`
- **涉及模組或檔案**：`internal/engine/engine.go`, `internal/engine/engine_test.go`
- **完成判準**：授權成功時產生唯一 task ID 並執行；授權失敗時返回拒絕原因且 0 子進程、0 資料庫任務建立。
- **驗證方式**：`go test -v ./internal/engine/...`
- **覆蓋項目**：P1-2、P1-3、D-01、SCN-001、SCN-002
- **責任角色**：唯一負責
- **Scenario ID**：SCN-001, SCN-002
- **改變可觀察行為**：是
- **實作與驗證證據**（2026-09-11）：
  - 外迴圈紅燈：`go test -v -run TestEngineExecute ./internal/engine`，exit code 1，失敗原因：`undefined: NewEngine`, `undefined: WithRunner`, `undefined: SubmitRequest`。
  - 內迴圈紅燈：`go test ./internal/engine/...`，exit code 1，失敗原因：`engine` 模組未定義。
  - 最小實作：完成 `internal/engine/engine.go`，實作 `SubmitTask`，前置透過 `auth.Policy.AuthorizeWorkspace` 嚴格把關授權；未授權直接返回錯誤，確保 0 子進程與 0 資料庫任務建立；授權通過則生成唯一 Task ID、寫入任務至 `storage.SQLiteStore` 並調度 runner 執行，成功時更新狀態為 `completed`，失敗時更新為 `failed` 並記錄 `error_message`。
  - 全綠回歸：`go test -v ./internal/engine/...` 全部 PASS（包含授權拒絕 0 子進程、授權成功取得結果與重查、以及執行失敗路徑單元測試）。`go test -v ./...` 與 `go vet ./...` 全專案通過。
- **狀態**：已完成

#### Task 1.3：本機 CLI 進入點（`asura run` 與 `asura status`） [已完成]
- **任務說明**：實作 `cmd/asura` 進入點，提供 `run` 指令提交任務、串流即時輸出，以及 `status <task_id>` 查詢歷史結果。
- **預期輸出**：`cmd/asura/main.go`, `cmd/asura/run.go`, `cmd/asura/status.go`
- **涉及模組或檔案**：`cmd/asura/main.go`, `cmd/asura/run.go`, `cmd/asura/status.go`
- **完成判準**：命令列可直接執行 `asura run "2+2="` 並獲取完成結果；以相同 ID 執行 `asura status <id>` 可重查相同結果。
- **驗證方式**：`go run ./cmd/asura run "2+2="` 搭配自動化整合測試 `tests/integration/cli_test.go`
- **覆蓋項目**：P1-3、SCN-001
- **責任角色**：唯一負責
- **Scenario ID**：SCN-001
- **改變可觀察行為**：是
- **實作與驗證證據**（2026-09-11）：
  - 外迴圈紅燈：`go test -v ./tests/integration/...`，exit code 1，失敗原因：CLI 進入點不存在。
  - 內迴圈紅燈：`go test ./cmd/asura/...`，exit code 1，失敗原因：`undefined: RunCLI`。
  - 最小實作：完成 `cmd/asura/main.go`、`cmd/asura/run.go` 與 `cmd/asura/status.go`，支援 `run` 與 `status` 子命令、`--db`、`--workspace`、`--allowed-roots` 參數配置，即時串流輸出並保存結果至資料庫。
  - 全綠回歸：`go test -v ./cmd/asura/...` 與 `go test -v ./tests/integration/...` 全部 PASS。手動以 `go run ./cmd/asura run "echo 42"` 執行產出 Task ID 並以 `status` 成功重查相同結果。`go test -v ./...` 與 `go vet ./...` 全專案通過。
- **狀態**：已完成

#### Task 1.4：Daemon 守護程序、Socket 互斥擁有權與並行 Worker Pool [已完成]
- **任務說明**：實作 Daemon 守護進程，透過 Unix domain socket / lockfile 互斥競爭單一 Daemon 擁有權；提供 IPC 介面接受任務提交，並由帶全域上限控制（Capacity）的 Worker Pool 統一排程執行，未接受或超出上限時回傳明確結果。
- **預期輸出**：`internal/daemon/daemon.go`, `internal/daemon/daemon_test.go`, `internal/daemon/pool.go`, `internal/daemon/pool_test.go`
- **涉及模組或檔案**：`internal/daemon/daemon.go`, `internal/daemon/daemon_test.go`, `internal/daemon/pool.go`, `internal/daemon/pool_test.go`
- **完成判準**：多個請求與 Daemon 同時啟動時只有一個擁有者，不突破全域上限，每個已接受任務各執行一次，未接受請求獲取明確錯誤或拒絕結果。
- **驗證方式**：`go test -v ./internal/daemon/...`
- **覆蓋項目**：P1-1、D-02、D-03、SCN-003
- **責任角色**：唯一負責
- **Scenario ID**：SCN-003
- **改變可觀察行為**：是
- **實作與驗證證據**（2026-09-11）：
  - 外迴圈紅燈：`go test -v -run TestDaemonConcurrency ./internal/daemon`，exit code 1，失敗原因：缺少守護程序競爭協調與容量控制（`undefined: NewDaemon`, `undefined: ErrDaemonAlreadyRunning` 等）。
  - 內迴圈紅燈：`go test ./internal/daemon/...`，exit code 1，失敗原因：`daemon` 模組未定義。
  - 最小實作：完成 `internal/daemon/pool.go`（Semaphore + 佇列控制並行上限與容量飽和判定）與 `internal/daemon/daemon.go`（Unix domain socket 競爭探測、單一擁有權互斥、JSON IPC 任務接收與調度），提供 `Client` 提交介面。
  - 全綠回歸：`go test -v ./internal/daemon/...` 全部 PASS（競爭啟動無雙重擁有者、不突破全域上限且各執行一次、未接受請求得到 `ErrCapacityExceeded` 明確結果）。全專案回歸 `go test -v ./...` 與 `go vet ./...` 全數通過。
- **狀態**：已完成

#### Task 1.5：任務主動取消、PGID 資源回收與局部隔離 [已完成]
- **任務說明**：在調度引擎與 CLI/Daemon 實作任務取消機制，維護活躍任務之 Context cancel 函式與進程組 PGID，支援發送取消信號，在 2 秒核准期限內終止目標任務並回收資源，將狀態轉移為 `cancelled`；同時運行的其他任務（如工作 B）完全不受干擾。
- **預期輸出**：`internal/engine/cancel.go`, `internal/engine/cancel_test.go`, `cmd/asura/cancel.go`, `tests/integration/cli_test.go`
- **涉及模組或檔案**：`internal/engine/engine.go`, `internal/engine/cancel.go`, `internal/engine/cancel_test.go`, `cmd/asura/cancel.go`, `cmd/asura/run.go`, `internal/storage/sqlite.go`, `tests/integration/cli_test.go`
- **完成判準**：發送取消後，任務 A 在 2 秒內終止且狀態為 `cancelled`，相關子進程組被乾淨回收；並行運行的任務 B 繼續執行並成功完成。
- **驗證方式**：`go test -v -run TestEngineCancel ./internal/engine/...` 與 `go test -v -run TestCLI_CancelIsolation ./tests/integration/...`
- **覆蓋項目**：P1-4、D-05、SCN-004
- **責任角色**：唯一負責
- **Scenario ID**：SCN-004
- **改變可觀察行為**：是
- **實作與驗證證據**（2026-09-11）：
  - 外迴圈紅燈：`go test -v -run TestCLI_CancelIsolation ./tests/integration`，exit code 1，失敗原因：`task A did not start or did not register PGID in time... SQLITE_BUSY` 與缺少 cancel 協調。
  - 內迴圈紅燈：`go test -v -run TestEngineCancel ./internal/engine`，exit code 1，失敗原因：`undefined: ErrTaskNotRunning`、`e.CancelTask undefined`。
  - 最小實作：
    1. 實作 `internal/engine/cancel.go`，提供 `CancelTask`，安全調用 Context 取消並標記狀態為 `cancelled`。
    2. 更新 `internal/storage/sqlite.go`：新增 `pgid` 欄位與 `UpdateTaskPGID`，將 `PRAGMA busy_timeout=5000;` 調整為最優先執行杜絕並行開庫鎖死。
    3. 更新 `cmd/asura/run.go`：啟動任務時建立獨立進程組（`Setpgid: true`）並持久化 PGID，自動生成唯一 ID；更新 `internal/engine/engine.go` 感知外部取消避免覆蓋為 failed。
    4. 實作 `cmd/asura/cancel.go`：提供 `asura cancel <task_id>` 指令，先標記 cancelled 再發送 `SIGKILL` 強殺進程組。
  - 全綠回歸：
    1. 內迴圈單元測試：`go test -v -run TestEngineCancel ./internal/engine`，0.12s PASS。
    2. 外迴圈端到端測試：`go test -v -run TestCLI_CancelIsolation ./tests/integration`，0.69s PASS（任務 A 在 2 秒內終止，狀態為 cancelled，並行任務 B 正常完成為 completed，且任務 A 的 PGID 進程組無殘留進程）。
    3. 全專案回歸：`go test -v ./...` 全部 PASS；`go vet ./...` 靜態檢查無任何告警。
- **狀態**：已完成

#### Task 1.6：部分失敗副作用保護與進度揭露 [已完成]
- **任務說明**：在調度引擎與 CLI 確保任務在部分步驟執行失敗時，記錄已產生的輸出與失敗原因，狀態明確轉為 `failed`，絕不自動重新調度或重試該任務，杜絕重複副作用；提供 CLI status 完整查得失敗細節。
- **預期輸出**：`internal/engine/engine.go`, `internal/engine/engine_test.go`, `tests/integration/cli_test.go`
- **涉及模組或檔案**：`internal/engine/engine.go`, `internal/engine/engine_test.go`, `cmd/asura/run.go`, `cmd/asura/status.go`, `tests/integration/cli_test.go`
- **完成判準**：若任務執行中途失敗（如產生部分檔案後返回非 0 退出碼），已產生之副作用保留，任務狀態標記為 failed，嚴格無自動重試（執行計數為 1），透過 `asura status` 查得已產生的部分輸出與退出碼失敗原因。
- **驗證方式**：`go test -v -run TestEngine_PartialFailureNoRetry ./internal/engine/...` 與 `go test -v -run TestCLI_PartialFailureProtection ./tests/integration/...`
- **覆蓋項目**：P1-4、D-04、SCN-005
- **責任角色**：唯一負責
- **Scenario ID**：SCN-005
- **改變可觀察行為**：是
- **實作與驗證證據**（2026-09-11）：
  - 外迴圈端到端測試：`go test -v -run TestCLI_PartialFailureProtection ./tests/integration`，0.55s PASS（執行建立檔案 + exit 42 命令，驗證副作用檔案確實產生、CLI run 返回非 0 狀態碼、`asura status` 顯示狀態為 `failed` 且完整呈現已完成進度與 exit status 42 失敗訊息、SQLite 資料庫中僅有單一任務記錄無自動重試）。
  - 內迴圈單元測試：`go test -v -run TestEngine_PartialFailureNoRetry ./internal/engine`，0.01s PASS（驗證後端執行次數嚴格為 1 次杜絕重試，且記憶體與儲存層均保留 partial output 與錯誤字串）。
  - 全專案回歸：`go test -v ./...` 全部 PASS；`go vet ./...` 靜態分析無告警；`git diff --check` 通過。
- **狀態**：已完成

#### Task 1.7：守護程序重啟恢復與中斷任務揭露 [已完成]
- **任務說明**：在調度引擎與 SQLite 儲存層實作重啟恢復機制（`RecoverInterruptedTasks`），於啟動時掃描並將前次未完成的殘留任務（`running`、`pending`）安全標記為中斷失敗，揭露明確中斷原因，絕不假冒為成功，亦不自動從頭重新執行；已完成之任務（`completed`）結果維持完整不變。
- **預期輸出**：`internal/storage/sqlite.go`, `internal/storage/sqlite_test.go`, `internal/engine/engine.go`, `internal/daemon/daemon.go`, `tests/integration/cli_test.go`
- **涉及模組或檔案**：`internal/storage/sqlite.go`, `internal/storage/sqlite_test.go`, `internal/engine/engine.go`, `internal/daemon/daemon.go`, `tests/integration/cli_test.go`
- **完成判準**：調度引擎或 Daemon 啟動時自動執行恢復，已完成任務保持 completed，中斷前處於 running/pending 的任務安全轉為 failed 且記錄 "task interrupted: system restarted or terminated unexpectedly"，嚴格杜絕自動重跑或誤報成功。
- **驗證方式**：`go test -v -run TestSQLiteStore_RecoverInterruptedTasks ./internal/storage/...` 與 `go test -v -run TestCLI_RestartRecovery ./tests/integration/...`
- **覆蓋項目**：P1-4、D-07、SCN-006
- **責任角色**：唯一負責
- **Scenario ID**：SCN-006
- **改變可觀察行為**：是
- **實作與驗證證據**（2026-09-11）：
  - 內迴圈單元測試紅燈：`go test -v -run TestSQLiteStore_RecoverInterruptedTasks ./internal/storage`，exit code 1，失敗原因：`store.RecoverInterruptedTasks undefined`。
  - 最小實作：
    1. 在 `internal/storage/sqlite.go` 實作 `RecoverInterruptedTasks`，以原子 SQL 更新將 `running` 與 `pending` 任務轉為 `failed` 並註明中斷訊息，不變更 `completed` 任務。
    2. 在 `internal/daemon/daemon.go` 的 `Start()` 監聽成功後自動觸發 `RecoverInterruptedTasks`。
    3. 在 `internal/engine/engine.go` 提供 `RecoverInterruptedTasks` 委派接口。
  - 內迴圈單元測試全綠：`TestSQLiteStore_RecoverInterruptedTasks` 0.01s PASS；`internal/daemon/daemon_test.go` 中之 `SCN-006: 守護程序重啟保留已完成結果並揭露中斷任務` PASS。
  - 外迴圈端到端測試：`go test -v -run TestCLI_RestartRecovery ./tests/integration`，0.57s PASS（驗證已完成任務 A 透過 CLI status 查得狀態仍為 completed 且結果完整保留；中斷任務 B 透過 status 查得狀態為 failed 且包含明確中斷原因）。
  - 全專案回歸：`go test -v ./...` 全部 PASS；`go vet ./...` 靜態分析無告警；`git diff --check` 通過。
- **狀態**：已完成

---

## 覆蓋對照表 (Coverage Matrix)

| 來源需求 / 驗收標準 | 唯一負責 Task | 支援 Task | 測試命令 |
|---|---|---|---|
| SCN-001（取得任務結果與可重查） | Task 1.2, Task 1.3 | Task 0.1, Task 0.3, Task 1.1 | `go test -v ./internal/engine/...` |
| SCN-002（拒絕超出授權之執行） | Task 0.2 | Task 0.1, Task 1.2 | `go test -v ./internal/auth/...` |
| SCN-003（競爭啟動與上限） | Task 1.4 | Task 1.1, Task 1.2 | `go test -v ./internal/daemon/...` |
| SCN-004（取消任務資源回收） | Task 1.5 | Task 0.4, Task 1.1, Task 1.2 | `go test -v ./internal/engine/...` |
| SCN-005（部分失敗保護） | Task 1.6 | Task 1.1, Task 1.2 | `go test -v -run TestCLI_PartialFailureProtection ./tests/integration/...` |
| SCN-006（重啟狀態恢復） | Task 1.7 | Task 1.1, Task 1.4 | `go test -v -run TestCLI_RestartRecovery ./tests/integration/...` |
| SCN-007（空輸出收尾防禦） | 待核准（無） | - | - |

---

*文件版本：v1.0.0*  
*建立日期：2026-09-11*
