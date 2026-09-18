# Issue 15 - 實作計畫：Phase 3D Multi-Backend Foundation

建立日期：2026-09-18  
核准基準：`37a2084`  
責任邊界：涵蓋 Gherkin `MBACK-001` ～ `MBACK-012` 全部已核准劇本

## 設計方案

### 1. 核心型別與介面定義 (`internal/backend`)

```go
package backend

import (
    "context"
    "time"
)

type Role string

const (
    RoleOracle  Role = "oracle"
    RoleStriker Role = "striker"
    RoleCritic  Role = "critic"
)

type Capability string

const (
    CapReadOnlySandbox    Capability = "read_only_sandbox"
    CapWorkspaceWrite      Capability = "workspace_write"
    CapStructuredEvents    Capability = "structured_events"
    CapProcessCancellation Capability = "process_cancellation"
)

type FailureClassification string

const (
    FailureNone            FailureClassification = ""
    FailureUnavailable     FailureClassification = "backend_unavailable"
    FailureUnsupported     FailureClassification = "unsupported_capability"
    FailureAuth            FailureClassification = "authentication_failed"
    FailureRateLimit       FailureClassification = "rate_limited"
    FailureExecution       FailureClassification = "execution_failed"
    FailureCancelled       FailureClassification = "cancelled"
    FailureEmptyResult     FailureClassification = "empty_result"
)

type EventType string

const (
    EventTypeText       EventType = "text"
    EventTypeTool       EventType = "tool"
    EventTypeToolStatus EventType = "tool_status"
    EventTypeError      EventType = "error"
    EventTypeRateLimit  EventType = "rate_limit"
)

type Event struct {
    Type      EventType         `json:"type"`
    Text      string            `json:"text,omitempty"`
    Tool      string            `json:"tool,omitempty"`
    Status    string            `json:"status,omitempty"`
    Error     string            `json:"error,omitempty"`
    Timestamp time.Time         `json:"timestamp"`
    Metadata  map[string]string `json:"metadata,omitempty"`
}

type ExecutionRequest struct {
    Role      Role              `json:"role"`
    Workspace string            `json:"workspace"`
    Prompt    string            `json:"prompt"`
    Metadata  map[string]string `json:"metadata,omitempty"`
}

type ExecutionResult struct {
    BackendID      string                `json:"backend_id"`
    BackendVersion string                `json:"backend_version"`
    Output         string                `json:"output"`
    PartialOutput  string                `json:"partial_output,omitempty"`
    Events         []Event               `json:"events,omitempty"`
    FailureClass   FailureClassification `json:"failure_class,omitempty"`
    StartedAt      time.Time             `json:"started_at"`
    CompletedAt    time.Time             `json:"completed_at"`
}

type Backend interface {
    ID() string
    Version(ctx context.Context) (string, error)
    Capabilities() []Capability
    HasCapability(cap Capability) bool
    Run(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error)
}

type Registry interface {
    Register(b Backend) error
    Get(id string) (Backend, error)
    List() []string
    Default() Backend
}
```

---

## 實作步驟與 Phase 規劃

本計畫依據 [`docs/AGENTS.md`](../../AGENTS.md) 與 [`README.md`](./README.md#風險與首要驗證) 風險優先原則規劃。風險評估為 **High**，首要驗證任務（Codex CLI 合約探測與外迴圈 Fail-Closed 防禦）安排在 Phase 1 與 Phase 2 優先實施。

---

### Phase 1：首要驗證與 Codex CLI 合約探測（High Risk / First Verification）

#### Task 1.1 — 執行 Codex CLI 0.155.0 Contract Probe
- **產出與範圍**：針對本機 `/home/kevin/.nvm/versions/node/v22.20.0/bin/codex` 進行契約探測腳本與整合測試，驗證 `codex exec` 的 non-interactive invocation、`-C` 工作目錄、`-s read-only` / `-s workspace-write` 沙箱行為、`--json` 事件結構與 context cancellation。
- **相依**：無
- **驗收編號**：`@MBACK-002`, `@MBACK-006`, `@MBACK-007`
- **完成判準**：Probe 獲得固定版本 `codex-cli 0.155.0` 在唯讀模式拒絕檔案寫入、在 workspace-write 允許寫入、終止時 process tree 回收之具體證據。
- **驗證**：執行 probe 測試並輸出結果至測試報告。
- **狀態**：✅ 已完成
- **證據**：`internal/backend/codex/adapter_probe_test.go` 通過 `TestCodexProbe_BinaryVersion`, `TestCodexProbe_NonInteractive_JSONL`, `TestCodexProbe_ReadOnlySandbox`, `TestCodexProbe_WorkspaceWriteSandbox`, `TestCodexProbe_ContextCancellation`（PASS，28.94s）。

#### Task 1.2 — 現有 Runner 與 OpenCode 邊界審計
- **產出與範圍**：盤點 `internal/engine/engine.go`、`internal/pipeline/stage_*.go` 與 `internal/backend/opencode/adapter.go`，標記解耦點，確保 Phase 3C 既有安全測試基底具備可重複執行能力。
- **相依**：無
- **驗收編號**：`@MBACK-001`
- **完成判準**：現有測試 `go test ./internal/pipeline/...` 23 項全綠；審計確認本機環境 opencode 為 `1.18.31`，解耦點為 StageRunnerFunc 與 SubmitRequest.Backend。
- **驗證**：`go test -v ./internal/pipeline/...`
- **狀態**：✅ 已完成
- **證據**：`internal/pipeline` 23 項單元測試全數 PASS；審計報告記錄於 `requirement-analysis.md` 與 `technical-analysis.md`。

---

### Phase 2：`internal/backend` 共同合約實作與 Registry

#### Task 2.1 — 共同 Backend 契約型別與介面實作
- **產出與範圍**：`internal/backend/backend.go`、`internal/backend/events.go`、`internal/backend/errors.go`。
- **相依**：Task 1.1
- **驗收編號**：`@MBACK-001`, `@MBACK-006`, `@MBACK-011`
- **完成判準**：定義 `Backend`、`Registry`、`Role`、`Capability`、`FailureClassification`、`Event`，並包含完整的單元測試與 JSON 序列化驗證。
- **驗證**：`go test -v ./internal/backend/...`
- **狀態**：✅ 已完成
- **證據**：`internal/backend/backend_test.go` 通過 `TestExecutionRequest_JSON`, `TestExecutionResult_JSON`, `TestExecutionError`（PASS）。

#### Task 2.2 — 記憶體 BackendRegistry 實作與未註冊後端 Fail-Closed
- **產出與範圍**：`internal/backend/registry.go`、`internal/backend/registry_test.go`。
- **相依**：Task 2.1
- **驗收編號**：`@MBACK-003`
- **完成判準**：實作 Thread-safe `BackendRegistry`，支援註冊、查詢與預設後端；查詢未知後端時明確返回 `ErrBackendNotFound`，禁止 fallback。
- **驗證**：單元測試驗證註冊成功、覆蓋拒絕、未知查詢錯誤且模型呼叫為 0。
- **狀態**：✅ 已完成
- **證據**：`internal/backend/registry_test.go` 通過 `TestRegistry_RegisterAndGet`（驗證 ErrBackendNotFound fail-closed）與 `TestRegistry_Concurrency`（並行安全）（PASS）。

---

### Phase 3：OpenCode Adapter 遷移

#### Task 3.1 — 封裝 OpenCode Adapter 實作共同 Backend 介面
- **產出與範圍**：`internal/backend/opencode/adapter.go` 擴充實作 `backend.Backend`。
- **相依**：Task 2.1, Task 2.2
- **驗收編號**：`@MBACK-001`, `@MBACK-008`
- **完成判準**：OpenCode Adapter 提供 `ID() = "opencode"`、宣告 `CapReadOnlySandbox`、`CapWorkspaceWrite`、`CapProcessCancellation`、`CapStructuredEvents`，並透過 `Run()` 傳回結構化 `ExecutionResult`。
- **驗證**：`go test -v ./internal/backend/opencode/...`
- **狀態**：✅ 已完成
- **證據**：`internal/backend/opencode/adapter_backend_test.go` 通過 `TestAdapter_BackendInterface`，靜態確認實作 `backend.Backend` 介面（PASS）。

#### Task 3.2 — 回歸驗證 Phase 3C asura-readonly 唯讀安全邊界
- **產出與範圍**：`internal/backend/opencode/adapter_test.go`。
- **相依**：Task 3.1
- **驗收編號**：`@MBACK-001`, `@MBACK-008`
- **完成判準**：Phase 3C 建立之 `asura-readonly` 攻擊性整合測試（寫入阻止、配置獨立性、環境變數消毒）全數保持 PASS。
- **驗證**：`go test -v -run "TestReadOnly|TestAdapter" ./internal/backend/opencode/...`
- **狀態**：✅ 已完成
- **證據**：`internal/backend/opencode` 25 項測試全數 PASS（含 16 項 asura-readonly / isolated config 安全邊界測試）。

---

### Phase 4：Codex CLI Adapter 實作

#### Task 4.1 — 實作 Codex CLI 執行器與進程回收
- **產出與範圍**：`internal/backend/codex/adapter.go`、`internal/backend/codex/adapter_test.go`。
- **相依**：Task 1.1, Task 2.1
- **驗收編號**：`@MBACK-002`, `@MBACK-007`
- **完成判準**：實作 `codex.Adapter`，依 Role 配置適當參數（Oracle/Critic 使用 `-s read-only`，Striker 使用 `-s workspace-write`）；Context Cancel 時透過獨立 Process Group 向進程樹發送 SIGKILL 回收。
- **驗證**：`go test -v ./internal/backend/codex/...`
- **狀態**：📝 待實作
- **證據**：-

#### Task 4.2 — 實作 Codex 事件解析與錯誤正規化
- **產出與範圍**：`internal/backend/codex/parser.go`、`internal/backend/codex/parser_test.go`。
- **相依**：Task 4.1
- **驗收編號**：`@MBACK-006`, `@MBACK-011`
- **完成判準**：解析 Codex `--json` 輸出流，將各類事件映射為標準 `backend.Event`；遇到 auth、rate-limit、non-zero exit、empty result 時精確分類為 `FailureClassification`，並保留 partial output。
- **驗證**：單元測試搭配真實 recorded fixture 驗證事件解析與錯誤映射。
- **狀態**：📝 待實作
- **證據**：-

---

### Phase 5：Pipeline Orchestrator 解耦與 Capability Preflight

#### Task 5.1 — PipelineOrchestrator 解耦 RunnerFunc
- **產出與範圍**：`internal/pipeline/orchestrator.go`、`internal/pipeline/stage_*.go`。
- **相依**：Task 2.1, Task 3.1, Task 4.1
- **驗收編號**：`@MBACK-001`, `@MBACK-002`
- **完成判準**：Pipeline 各 Stage 改為接收 `backend.Backend` 與 `Role`；解除對 `engine.RunnerFunc` 之依賴。
- **驗證**：`go test -v ./internal/pipeline/...`
- **狀態**：📝 待實作
- **證據**：-

#### Task 5.2 — 實作 Three-Face Capability Preflight
- **產出與範圍**：`internal/pipeline/orchestrator.go`、`internal/pipeline/orchestrator_test.go`。
- **相依**：Task 5.1
- **驗收編號**：`@MBACK-003`, `@MBACK-008`
- **完成判準**：Three-Face 管線在啟動 Oracle 前，檢查後端是否具備 `CapReadOnlySandbox`；若未具備或後端不存在，立即 fail-closed 終止，0 模型呼叫，絕不 fallback。
- **驗證**：單元測試驗證不支援唯讀之後端在 Oracle 前被攔截，呼叫次數為 0。
- **狀態**：📝 待實作
- **證據**：-

#### Task 5.3 — PipelineTrace 擴充 BackendID 與 Version
- **產出與範圍**：`internal/pipeline/types.go`、`internal/pipeline/orchestrator.go`。
- **相依**：Task 5.1
- **驗收編號**：`@MBACK-009`
- **完成判準**：`StageTrace` 與 `PipelineTrace` 完整記錄實際執行的 `BackendID` 與版本號。
- **驗證**：執行 Direct 與 Three-Face 任務，斷言 Trace JSON 包含正確的 backend 資訊。
- **狀態**：📝 待實作
- **證據**：-

#### Task 5.4 — 落實 No Automatic Failover 邊界防禦
- **產出與範圍**：`internal/pipeline/orchestrator.go`。
- **相依**：Task 5.1
- **驗收編號**：`@MBACK-010`
- **完成判準**：若指定後端（如 Codex）在 Striker 階段失敗，保存 partial output 並標記 Failed，嚴格禁止自動切換至 OpenCode 重試（OpenCode 呼叫次數為 0）。
- **驗證**：測試驗證部分執行失敗後無第二次執行嘗試。
- **狀態**：📝 待實作
- **證據**：-

---

### Phase 6：Engine 整合、SubmitRequest 與 Fingerprint

#### Task 6.1 — SubmitRequest 與 ExecutionContext 納入 BackendID
- **產出與範圍**：`internal/engine/engine.go`。
- **相依**：Task 2.2, Task 5.1
- **驗收編號**：`@MBACK-001`
- **完成判準**：`SubmitRequest` 增加 `Backend string` 欄位，未指定時預設 `"opencode"`；解耦對 `opencode` 套件的直接 import。
- **驗證**：`go test -v ./internal/engine/...`
- **狀態**：📝 待實作
- **證據**：-

#### Task 6.2 — BackendID 納入 Request Fingerprint 計算
- **產出與範圍**：`internal/engine/engine.go`、`internal/engine/engine_test.go`。
- **相依**：Task 6.1
- **驗收編號**：`@MBACK-004`
- **完成判準**：更新 Request Fingerprint 演算法，相同 Request ID 但不同 Backend 必須產出不同 Hash，系統判定為衝突並拒絕。
- **驗證**：單元測試驗證同 Request ID 不同 Backend 遭到拒絕。
- **狀態**：📝 待實作
- **證據**：-

#### Task 6.3 — Redelivery 保持同一 Backend 身份
- **產出與範圍**：`internal/engine/engine.go`。
- **相依**：Task 6.2
- **驗收編號**：`@MBACK-005`
- **完成判準**：相同 Request ID 與相同 Backend 重送時返回既有 Task，不重複調用後端，不重新選後端。
- **驗證**：單元測試驗證 Redelivery 命中既有 Task。
- **狀態**：📝 待實作
- **證據**：-

---

### Phase 7：CLI 與 Daemon 整合

#### Task 7.1 — CLI `asura ask` 與 `asura run` 支援 `--backend`
- **產出與範圍**：`cmd/asura/main.go`。
- **相依**：Task 6.1
- **驗收編號**：`@MBACK-001`, `@MBACK-002`, `@MBACK-003`
- **完成判準**：CLI 增加 `--backend` 參數（預設 `opencode`），支援指定 `codex`；輸入未知後端時在發送前報錯。
- **驗證**：`go build ./cmd/asura` 並執行 CLI 參數測試。
- **狀態**：📝 待實作
- **證據**：-

#### Task 7.2 — Daemon 排程佇列喚醒保護
- **產出與範圍**：`internal/daemon/workspace_queue.go`、`internal/daemon/daemon.go`。
- **相依**：Task 6.1
- **驗收編號**：`@MBACK-012`
- **完成判準**：Task 在 Workspace 佇列排隊後喚醒執行時，依 Task 內記錄之 BackendID 執行，不重新套用預設值。
- **驗證**：`go test -v ./internal/daemon/...`
- **狀態**：📝 待實作
- **證據**：-

---

### Phase 8：外迴圈測試、MBACK 驗收與回歸驗證

#### Task 8.1 — 實作 Gherkin MBACK-001 ～ MBACK-012 外迴圈驗收測試
- **產出與範圍**：`tests/e2e/mback_test.go`。
- **相依**：Phase 1 ~ Phase 7
- **驗收編號**：`@MBACK-001` ～ `@MBACK-012`
- **完成判準**：12 項 Gherkin 驗收劇本外迴圈測試全數 PASS。
- **驗證**：`go test -v ./tests/e2e/...`
- **狀態**：📝 待實作
- **證據**：-

#### Task 8.2 — 系統完整回歸測試與靜態檢查
- **產出與範圍**：全專案。
- **相依**：Task 8.1
- **驗收編號**：所有驗收項目
- **完成判準**：全專案編譯無警告、race detector 檢查通過、無語法與邏輯回歸。
- **驗證**：`go test -race -count=1 ./...`、`go vet ./...`、`go build ./cmd/asura`
- **狀態**：📝 待實作
- **證據**：-

---

## 測試策略

| 驗收編號 | 責任 Task | 測試層級 | 測試策略 |
|---|---|---|---|
| **MBACK-001** | Task 3.1 / 6.1 | E2E / Integration | 驗證未帶 Backend 時自動選用 OpenCode，維持 Direct/Three-Face 完整相容 |
| **MBACK-002** | Task 4.1 | Integration | 指定 `--backend codex`，驗證僅調用 Codex adapter，OpenCode 呼叫次數為 0 |
| **MBACK-003** | Task 2.2 / 5.2 | Unit / Contract | 提交未知 BackendID，驗證在啟動任何進程前立即報錯拒絕 |
| **MBACK-004** | Task 6.2 | Unit (Engine) | 相同 Request ID 搭配不同 Backend，驗證返回 Conflict 錯誤 |
| **MBACK-005** | Task 6.3 | Unit (Engine) | 相同 Request ID 搭配相同 Backend 重送，驗證返回同一 Task 且後端不重呼叫 |
| **MBACK-006** | Task 2.1 / 4.2 | Unit (Adapters) | 注入錯誤輸出 fixture，驗證能正確轉換為標準 FailureClassification |
| **MBACK-007** | Task 1.1 / 4.1 | Integration / Process | 取消 Context，驗證進程組確實收到信號並回收，且狀態為 Cancelled |
| **MBACK-008** | Task 3.1 / 5.2 | Unit (Pipeline) | 模擬不支援唯讀能力的 Backend，驗證在 Oracle 前即被拒絕（0 stage calls） |
| **MBACK-009** | Task 5.3 | Integration | 驗證產出之 PipelineTrace 中各 Stage 包含 BackendID 與 Version |
| **MBACK-010** | Task 5.4 | Integration (Pipeline) | 模擬 Codex 在 Striker 失敗，驗證任務 Failed 且絕不切換 OpenCode 重試 |
| **MBACK-011** | Task 2.1 / 4.2 | Unit (Parser) | 將 OpenCode 與 Codex 的事件日誌映射為共同 BackendEvent |
| **MBACK-012** | Task 7.2 | Integration (Daemon) | 佇列排隊喚醒時，驗證維持原本設定之 BackendID 執行 |

---

## 風險與首要驗證

> 引用自 [README.md](./README.md#風險與首要驗證)。
