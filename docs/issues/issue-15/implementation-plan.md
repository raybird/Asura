# Issue 15 - 實作計畫 (Implementation Plan)

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

## 實作步驟

### Phase 1: 現況審計與 Codex CLI 探測 (Probe)
- [ ] Task 1.1: 針對本機 `codex-cli 0.155.0` 執行 contract probe，記錄版本、參數、事件格式與 sandboxing 特性。 (MBACK-002, MBACK-006, MBACK-007)
- [ ] Task 1.2: 盤點現有 Pipeline Runner 與 OpenCode Adapter 邊界，確定遷移路徑。 (MBACK-001)

### Phase 2: `internal/backend` 共同契約實作
- [ ] Task 2.1: 建立 `internal/backend/backend.go`，定義 `Backend`、`Registry`、`Role`、`Capability`、`FailureClassification`、`Event`。 (MBACK-001, MBACK-003, MBACK-006, MBACK-011)
- [ ] Task 2.2: 實作記憶體預設 `BackendRegistry` 與相關單元測試。 (MBACK-003)

### Phase 3: OpenCode Adapter 遷移
- [ ] Task 3.1: 讓 `opencode.Adapter` 實作共同 `backend.Backend` 介面。 (MBACK-001, MBACK-008)
- [ ] Task 3.2: 確保 Phase 3C 的 `asura-readonly` 配置、安全測試全數綠燈通過。 (MBACK-001, MBACK-008)

### Phase 4: Codex CLI Adapter 實作
- [ ] Task 4.1: 實作 `internal/backend/codex/adapter.go`，串接 `codex exec`。 (MBACK-002, MBACK-007)
- [ ] Task 4.2: 實作 `internal/backend/codex/parser.go`，將 Codex jsonl 事件解析並正規化為 `backend.Event` 與 `FailureClassification`。 (MBACK-006, MBACK-011)
- [ ] Task 4.3: 實作進程組隔離與 Context 取消回收。 (MBACK-007)

### Phase 5: Pipeline Orchestrator 解耦與 Capability Preflight
- [ ] Task 5.1: 重構 `internal/pipeline/orchestrator.go`，依賴 `backend.Backend` 取代 `RunnerFunc`。 (MBACK-001, MBACK-002, MBACK-008)
- [ ] Task 5.2: 實作 Three-Face 的 Capability Preflight 邏輯，在 Oracle 啟動前進行能力驗證，若不支援立即 Fail-Closed（0 calls）。 (MBACK-008)
- [ ] Task 5.3: 更新 `StageTrace`，記錄各 Stage 的實際 `BackendID` 與 `BackendVersion`。 (MBACK-009)
- [ ] Task 5.4: 驗證 No Failover 原則：部分執行失敗保存 partial output，不進行二次重試。 (MBACK-010)

### Phase 6: Engine 整合、SubmitRequest 與 Fingerprint
- [ ] Task 6.1: `engine.SubmitRequest` 增加 `Backend` 欄位（未指定時 default 為 `opencode`）。 (MBACK-001)
- [ ] Task 6.2: 更新 Request Fingerprint 計算邏輯，將 `Backend` 納入雜湊。 (MBACK-004)
- [ ] Task 6.3: 更新 Redelivery 邏輯：相同 Request ID 若指定不同 Backend 必須拒絕；相同則返回既有 Task。 (MBACK-004, MBACK-005)
- [ ] Task 6.4: 移除 `engine.go` 對 `internal/backend/opencode` 的直接相依，改由 Registry 注入。 (MBACK-001, MBACK-003)

### Phase 7: CLI 與 Daemon 整合
- [ ] Task 7.1: CLI `asura ask` 與 `asura run` 增加 `--backend` 參數（預設 `opencode`）。 (MBACK-001, MBACK-002)
- [ ] Task 7.2: Daemon 狀態查詢與工作區排隊（Queue Wake）維持原 BackendID。 (MBACK-012)

### Phase 8: 綜合驗證與回歸
- [ ] Task 8.1: 撰寫 Gherkin MBACK-001 ～ MBACK-012 外迴圈測試並確認全數綠燈。
- [ ] Task 8.2: 執行完整回歸驗證：`go test -race -count=1 ./...`、`go vet ./...`、`go build ./cmd/asura`。

---

## 使用方式對照

### CLI 變更
- 變更前：
  ```bash
  asura ask "優化資料庫查詢"
  ```
- 變更後：
  ```bash
  # 預設仍為 opencode，100% 向後相容
  asura ask "優化資料庫查詢"
  
  # 明確指定使用 codex
  asura ask --backend codex "優化資料庫查詢"
  
  # 指定不存在的後端，執行前拒絕 (fail-closed)
  asura ask --backend unknown "優化資料庫查詢"
  # 輸出：error: backend 'unknown' not found (no processes spawned)
  ```

### Trace 格式變更
```json
{
  "mode": "three-face",
  "stages": [
    {
      "name": "oracle",
      "status": "completed",
      "backend_id": "codex",
      "backend_version": "0.155.0",
      "duration_ms": 1240
    }
  ]
}
```

---

## 測試策略

| 驗收編號 | 測試層級 | 測試策略 |
|---|---|---|
| **MBACK-001** | E2E / Integration | 驗證未帶 Backend 時自動調用 OpenCode，且 Direct/Three-Face 正常運作 |
| **MBACK-002** | Integration | 指定 `--backend codex`，驗證僅調用 Codex adapter，OpenCode 呼叫次數為 0 |
| **MBACK-003** | Unit / Contract | 提交未知 BackendID，驗證在啟動任何進程前立即報錯拒絕 |
| **MBACK-004** | Unit (Engine) | 相同 Request ID 搭配不同 Backend，驗證返回 Conflict 錯誤 |
| **MBACK-005** | Unit (Engine) | 相同 Request ID 搭配相同 Backend 重送，驗證返回同一 Task 且後端不重呼叫 |
| **MBACK-006** | Unit (Adapters) | 注入錯誤輸出 fixture，驗證能正確轉換為標準 FailureClassification |
| **MBACK-007** | Integration / Process | 取消 Context，驗證進程組確實收到信號並回收，且狀態為 Cancelled |
| **MBACK-008** | Unit (Pipeline) | 模擬不支援唯讀能力的 Backend，驗證在 Oracle 前即被拒絕（0 stage calls） |
| **MBACK-009** | Integration | 驗證產出之 PipelineTrace 中各 Stage 包含 BackendID 與 Version |
| **MBACK-010** | Integration (Pipeline) | 模擬 Codex 在 Striker 失敗，驗證任務 Failed 且絕不切換 OpenCode 重試 |
| **MBACK-011** | Unit (Parser) | 將 OpenCode 與 Codex 的事件日誌映射為共同 BackendEvent |
| **MBACK-012** | Integration (Daemon) | 佇列排隊喚醒時，驗證維持原本設定之 BackendID 執行 |

---

## 風險與首要驗證

> 引用自 [README.md](./README.md#風險與首要驗證)。

---

## 檢查清單

- [ ] `internal/backend` 套件建立並提供完整介面與 Registry
- [ ] OpenCode Adapter 遷移完成且 Phase 3C 安全性不打折
- [ ] Codex Adapter 實作完成（支援 probe 證明之 sandbox 與 jsonl）
- [ ] Pipeline Orchestrator 解耦並實作 Capability Preflight
- [ ] Engine SubmitRequest / Fingerprint / Redelivery 支援 BackendID
- [ ] CLI 支援 `--backend`
- [ ] MBACK-001 ～ MBACK-012 測試全數通過
- [ ] 全套回歸測試通過（`go test -race -count=1 ./...`）
