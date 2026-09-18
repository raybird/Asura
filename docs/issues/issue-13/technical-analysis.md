# Issue 13 - 技術分析與架構決策：Phase 3C Three-Face Fixed Pipeline

建立日期：2026-09-17

## 1. 管線調度邊界方案取捨

### 方案 A：直接在 `internal/engine` 或 `opencode.Adapter` 中嵌入 Oracle / Critic 邏輯
- **做法**：在 `Engine.SubmitTask` 內部寫死 if-else 邏輯，或在 `opencode.Adapter` 內自行串接三次模型呼叫。
- **缺點**：
  - 破壞既有 Engine 的簡潔協調責任，使 Engine 程式碼急遽膨脹。
  - 將角色邏輯寫死在特定後端 adapter 內，未來 Phase 3D（Multi-Backend）或更換其他 LLM 提供商時必須全盤重構。
  - 各 Stage 難以獨立單元測試與 Mock。

### 方案 B：建立獨立 `internal/pipeline` 模組，將 Striker 抽象為 Stage 之一（推薦採納）
- **做法**：
  - 新增 `internal/pipeline` 套件，定義通用的 `Stage` 介面與 `PipelineOrchestrator`。
  - 定義 `OracleStage`、`StrikerStage`、`CriticStage` 三個具體實作。
  - `StrikerStage` 負責接收 `ExecutionContext` 與 `OraclePlan`，將其組裝後委託既有 `RunnerFunc` 執行。
  - `Engine` 依據 `ExecutionMode` 決定直接呼叫 `RunnerFunc`（Direct mode）或委託 `PipelineOrchestrator`（Three-Face mode）。
- **優勢**：
  - 職責高度單一，邊界清晰。
  - 每個 Stage 與 Orchestrator 均可透過 Mock 獨立驗證狀態機與 Fail-Closed 行為。
  - 為後續 Phase 3C-2（Dynamic Routing）與 Phase 3D（Multi-Backend）提供乾淨的擴充入口。

**決策結論**：採納 **方案 B**。

## 2. 資料契約設計

### 2.1 執行模式（ExecutionMode）
```go
type ExecutionMode string

const (
    ModeDirect    ExecutionMode = "direct"
    ModeThreeFace ExecutionMode = "three-face"
)
```

### 2.2 Oracle Plan 契約
```go
type OraclePlan struct {
    Goal            string            `json:"goal"`
    Constraints     []string          `json:"constraints"`
    Steps           []string          `json:"steps"`
    ExpectedOutcome string            `json:"expected_outcome"`
    Metadata        map[string]string `json:"metadata,omitempty"`
}
```

### 2.3 Critic Review 契約
```go
type ReviewVerdict string

const (
    VerdictPass           ReviewVerdict = "pass"
    VerdictNeedsAttention ReviewVerdict = "needs_attention"
    VerdictFailed         ReviewVerdict = "failed"
)

type CriticReview struct {
    Verdict  ReviewVerdict     `json:"verdict"`
    Findings []string          `json:"findings"`
    Summary  string            `json:"summary"`
    Metadata map[string]string `json:"metadata,omitempty"`
}
```

### 2.4 Pipeline Trace 審計契約
```go
type StageStatus string

const (
    StagePending   StageStatus = "pending"
    StageRunning   StageStatus = "running"
    StageCompleted StageStatus = "completed"
    StageFailed    StageStatus = "failed"
    StageSkipped   StageStatus = "skipped"
)

type StageTrace struct {
    Name       string        `json:"name"`
    Status     StageStatus   `json:"status"`
    DurationMs int64         `json:"duration_ms"`
    Error      string        `json:"error,omitempty"`
    Output     string        `json:"output,omitempty"`
}

type PipelineTrace struct {
    Mode        ExecutionMode `json:"mode"`
    OraclePlan  *OraclePlan   `json:"oracle_plan,omitempty"`
    CriticReview *CriticReview `json:"critic_review,omitempty"`
    Stages      []StageTrace  `json:"stages"`
    ModelCalls  int           `json:"model_calls"`
    DurationMs  int64         `json:"duration_ms"`
    FinalResult string        `json:"final_result,omitempty"`
}
```

## 3. 儲存層 Trace 持久化方案取捨

### 方案 1：新增 `pipeline_traces` 獨立關聯資料表
- **做法**：建立一對一關聯表，以 `task_id` 作為外鍵儲存各 stage 的 trace。
- **缺點**：查詢單一任務狀態時需跨表 JOIN，且增加了資料庫交易複雜度與遷移成本。

### 方案 2：在 `tasks` 表擴充 `execution_mode` 與 `pipeline_trace` 欄位（推薦採納）
- **做法**：
  - `ALTER TABLE tasks ADD COLUMN execution_mode TEXT DEFAULT 'direct'`
  - `ALTER TABLE tasks ADD COLUMN pipeline_trace TEXT DEFAULT ''`
- **優勢**：
  - 與 Phase 3B 的 `resolved_context`、`memory_refs` 架構完全一致，保持資料表扁平與讀寫原子性。
  - 任務讀取時可直接完整取得 Trace，避免維護多表一致性問題。

**決策結論**：採納 **方案 2**。

## 4. Orchestrator 狀態機與 Fail-Closed 流程

Three-Face 模式固定狀態機：
1. **Init**：初始化 `PipelineTrace`（Mode = `three-face`）。
2. **Stage 1: Oracle**：
   - 傳入 Sealed `ExecutionContext`。
   - 呼叫 Oracle 模型解析，產出 `OraclePlan`。
   - 若失敗：標記 Oracle stage `failed`，Striker 與 Critic 標記為 `skipped`，管線立即回傳錯誤，Task 標記為 `failed`。
3. **Stage 2: Striker**：
   - 傳入 `ExecutionContext` 與 `OraclePlan`。
   - 組裝執行提示，調用 `RunnerFunc` 產生工作成果。
   - 若失敗：標記 Striker stage `failed`，Critic 標記為 `skipped`，管線回傳錯誤，Task 標記為 `failed`。
4. **Stage 3: Critic**：
   - 傳入 `ExecutionContext`、`OraclePlan` 與 Striker Output。
   - 調用 Critic 模型產出 `CriticReview`。
   - 若 Critic 呼叫失敗：標記 Critic stage `failed`，Task 不得標記為 completed，應標記為 `failed`（或帶有明確審查失敗錯誤）。
   - 若審查成功：記錄 `CriticReview`，FinalResult 取 Striker Output（若 Verdict 為 needs_attention 亦保存並標示）。
5. **Finalization**：
   - 計算總 ModelCalls（成功執行時 Three-Face 為 3，Direct 為 1）。
   - 寫入 `pipeline_trace` 至儲存層。

## 5. 向後相容性與冪等 Redelivery

1. **Direct Mode 相容**：
   - 當 `ExecutionMode` 為空或 `direct` 時，直接跳過 Oracle 與 Critic，僅調用 Striker。
   - ModelCalls 記為 1。
   - Phase 3B 的 Prompt 組裝、記憶解析與單一 Runner 調度保持 100% 行為不變。
2. **Redelivery 冪等**：
   - 請求指紋（Fingerprint）擴充納入 `ExecutionMode`，確保相同 Request ID 但不同 mode 時被安全阻擋。
   - 相同 Request ID 且相同 payload 重送時，直接由 SQLite 讀取既有 Task（包含 `pipeline_trace`），絕不重複觸發任何模型調用。
