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
| SCN-003 | 守護程序競爭啟動協調與全域容量計數策略 | TBD-3 |
| SCN-004 | 進程與瀏覽器資源歸屬及取消期限確認 | TBD-4 |
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

#### Task 1.1：SQLite 任務資料庫 Schema 與單一連線管理
- **任務說明**：建立 `asura.db` 初始化與 `tasks` 資料表，支援 WAL 模式與單一連線池，杜絕鎖死。
- **預期輸出**：`internal/storage/sqlite.go`, `internal/storage/sqlite_test.go`
- **涉及模組或檔案**：`internal/storage/sqlite.go`, `internal/storage/sqlite_test.go`
- **完成判準**：自動建立資料表；支援寫入任務、更新狀態（pending, running, completed, failed, cancelled）與結果保存。
- **驗證方式**：`go test -v ./internal/storage/...`
- **覆蓋項目**：P1-1、D-02、SCN-001
- **責任角色**：唯一負責
- **Scenario ID**：SCN-001
- **改變可觀察行為**：是
- **BDD 外迴圈**：標註 SCN-001 的儲存整合測試；執行命令：`go test -v -run TestTaskStorage ./internal/storage`；預期失敗原因：缺少 SQLite 初始化與任務儲存能力。
- **TDD 內迴圈**：單元測試路徑 `internal/storage/sqlite_test.go`；執行命令：`go test ./internal/storage/...`；預期失敗原因：`storage` 模組未定義。
- **最小實作與回歸**：實作 `internal/storage/sqlite.go`；全綠回歸命令：`go test -v ./internal/storage/...`。

#### Task 1.2：任務調度器與授權閘門整合
- **任務說明**：實作 Task 協調層，接收提交請求，經由 `auth.Policy` 校驗，通過後寫入 DB 並調用 `opencode` 後端執行。
- **預期輸出**：`internal/engine/engine.go`, `internal/engine/engine_test.go`
- **涉及模組或檔案**：`internal/engine/engine.go`, `internal/engine/engine_test.go`
- **完成判準**：授權成功時產生唯一 task ID 並執行；授權失敗時返回拒絕原因且 0 子進程、0 資料庫任務建立。
- **驗證方式**：`go test -v ./internal/engine/...`
- **覆蓋項目**：P1-2、P1-3、D-01、SCN-001、SCN-002
- **責任角色**：唯一負責
- **Scenario ID**：SCN-001, SCN-002
- **改變可觀察行為**：是
- **BDD 外迴圈**：標註 SCN-001 與 SCN-002 的引擎整合測試；執行命令：`go test -v -run TestEngineExecute ./internal/engine`；預期失敗原因：缺少整合調度引擎。
- **TDD 內迴圈**：單元測試路徑 `internal/engine/engine_test.go`；執行命令：`go test ./internal/engine/...`；預期失敗原因：`engine` 模組未定義。
- **最小實作與回歸**：實作 `internal/engine/engine.go`；全綠回歸命令：`go test -v ./internal/engine/...`。

#### Task 1.3：本機 CLI 進入點（`asura run` 與 `asura status`）
- **任務說明**：實作 `cmd/asura` 進入點，提供 `run` 指令提交任務、串流即時輸出，以及 `status <task_id>` 查詢歷史結果。
- **預期輸出**：`cmd/asura/main.go`, `cmd/asura/run.go`, `cmd/asura/status.go`
- **涉及模組或檔案**：`cmd/asura/main.go`, `cmd/asura/run.go`, `cmd/asura/status.go`
- **完成判準**：命令列可直接執行 `asura run "2+2="` 並獲取完成結果；以相同 ID 執行 `asura status <id>` 可重查相同結果。
- **驗證方式**：`go run ./cmd/asura run "2+2="` 搭配自動化整合測試 `tests/integration/cli_test.go`
- **覆蓋項目**：P1-3、SCN-001
- **責任角色**：唯一負責
- **Scenario ID**：SCN-001
- **改變可觀察行為**：是
- **BDD 外迴圈**：標註 SCN-001 的 CLI 端到端測試；執行命令：`go test -v ./tests/integration/...`；預期失敗原因：`cmd/asura` 進入點不存在。
- **TDD 內迴圈**：`cmd/asura/run_test.go`；執行命令：`go test ./cmd/asura/...`；預期失敗原因：CLI 命令處理函式未定義。
- **最小實作與回歸**：實作 `cmd/asura` 相關檔案；全綠回歸命令：`go test -v ./...`。

---

## 覆蓋對照表 (Coverage Matrix)

| 來源需求 / 驗收標準 | 唯一負責 Task | 支援 Task | 測試命令 |
|---|---|---|---|
| SCN-001（取得任務結果與可重查） | Task 1.2, Task 1.3 | Task 0.1, Task 0.3, Task 1.1 | `go test -v ./internal/engine/...` |
| SCN-002（拒絕超出授權之執行） | Task 0.2 | Task 0.1, Task 1.2 | `go test -v ./internal/auth/...` |
| SCN-003（競爭啟動與上限） | 待核准（無） | - | - |
| SCN-004（取消任務資源回收） | 待核准（無） | - | - |
| SCN-005（部分失敗保護） | 待核准（無） | - | - |
| SCN-006（重啟狀態恢復） | 待核准（無） | - | - |
| SCN-007（空輸出收尾防禦） | 待核准（無） | - | - |

---

*文件版本：v1.0.0*  
*建立日期：2026-09-11*
