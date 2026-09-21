# Issue 17 - Phase 3E：Context Window Management Foundation 實作計畫

## 設計方案

### 1. 資料結構與契約設計

#### (1) `internal/backend` Token Usage 與 Provenance
```go
package backend

// DataProvenance 標示數據來源與可信度
type DataProvenance string

const (
	ProvenanceExact     DataProvenance = "exact"     // 後端進程回報之實測值
	ProvenanceEstimated DataProvenance = "estimated" // 啟發式安全估算值
	ProvenanceUnknown   DataProvenance = "unknown"   // 未知或未回報
)

// TokenUsage 代表標準化之 Token 用量觀測
type TokenUsage struct {
	InputTokens     int64 `json:"input_tokens,omitempty"`
	OutputTokens    int64 `json:"output_tokens,omitempty"`
	TotalTokens     int64 `json:"total_tokens,omitempty"`
	CachedTokens    int64 `json:"cached_tokens,omitempty"`
	ReasoningTokens int64 `json:"reasoning_tokens,omitempty"`
}

// ExecutionResult 擴充 TokenUsage 與資料來源
// 保持既有欄位向前相容
```

#### (2) `internal/contextwin` 核心介面與型別
```go
package contextwin

// LimitSource 標示模型 Context 上限之來源
type LimitSource string

const (
	LimitSourceConfig  LimitSource = "config"  // 顯式配置覆蓋
	LimitSourceCatalog LimitSource = "catalog" // 靜態模型目錄
	LimitSourceUnknown LimitSource = "unknown" // 未知上限
)

// ModelWindowInfo 描述模型窗口與限制
type ModelWindowInfo struct {
	Model       string      `json:"model"`
	MaxTokens   int64       `json:"max_tokens"`
	LimitSource LimitSource `json:"limit_source"`
}

// StageEstimate 描述單一 Stage 在執行前估算之 Context 需求
type StageEstimate struct {
	Stage             string                 `json:"stage"`
	EstimatedPromptTokens int64              `json:"estimated_prompt_tokens"`
	ReservedOutputTokens  int64              `json:"reserved_output_tokens"`
	TotalEstimatedTokens  int64              `json:"total_estimated_tokens"`
	Provenance        backend.DataProvenance `json:"provenance"`
	EstimationMethod  string                 `json:"estimation_method"`
}

// AdmissionVerdict 准入檢查結論
type AdmissionVerdict string

const (
	VerdictAdmitted AdmissionVerdict = "admitted"
	VerdictRejected AdmissionVerdict = "rejected"
	VerdictWarning  AdmissionVerdict = "warning"
	VerdictBypassed AdmissionVerdict = "bypassed" // 未配置限制或處於未知模式
)

// StageAdmission 記錄執行前之准入審計結果
type StageAdmission struct {
	Stage       string           `json:"stage"`
	Verdict     AdmissionVerdict `json:"verdict"`
	Model       string           `json:"model,omitempty"`
	MaxTokens   int64            `json:"max_tokens,omitempty"`
	LimitSource LimitSource      `json:"limit_source"`
	Estimate    StageEstimate    `json:"estimate"`
	Reason      string           `json:"reason,omitempty"`
}
```

### 2. Orchestrator Stage-Aware Admission 流程設計

```text
[PipelineOrchestrator]
       │
       ├──► 1. 準備 Stage 原始/組裝後之真實 Prompt
       │        - Oracle: System Instruction + Context Prompt
       │        - Striker: Base Prompt + OraclePlan (若存在)
       │        - Critic: System Instruction + Original Prompt + OraclePlan + StrikerOutput
       │
       ├──► 2. 呼叫 contextwin.AdmissionEngine.EvaluateStage(stage, fullPrompt, model, budgetConfig)
       │        ├── 計算 StageEstimate (標註 ProvenanceEstimated)
       │        └── 判定 StageAdmission (Admitted / Rejected / Warning / Bypassed)
       │
       ├──► 3. 准入檢查判定
       │        ├── Rejected:
       │        │     - 記錄 StageTrace (Status: StageFailed, Error: "context limit exceeded")
       │        │     - 調用後端次數 = 0 (Fail-Closed)
       │        │     - 絕不自動更換 Backend / 不重跑已完成 Stage
       │        │     - 中斷管線並回傳已產生之 Partial Result 與 Trace
       │        └── Admitted / Bypassed:
       │              - 呼叫後端 Backend.Run(ctx, req)
       │
       └──► 4. 取得 ExecutionResult
                ├── 萃取 TokenUsage (標註 ProvenanceExact)
                └── 記入 StageTrace.UsageObservation
```

---

## 實作步驟

### Phase 1：雙後端 Context 能力探測與 Token 正規化
- 📝 **Task 1.1**（對應 CTXWIN-001）：編寫 OpenCode 1.18.23 / 1.18.31 與 Codex CLI 0.155.0 / 0.155.1 的 Context Contract Probe 測試與樣本解析，產出 unsupported 能力矩陣。
  - 產出：`internal/backend/opencode/probe_context_test.go`、`internal/backend/codex/probe_context_test.go`
  - 完成判準：Probe 測試清晰斷言 stdout/jsonl 之 usage 結構，未支援之能力（工具攔截、歷史替換）明確記錄為 unsupported，區分 live 與 hermetic。
- 📝 **Task 1.2**（對應 CTXWIN-001, CTXWIN-002）：擴充 `opencode/parser.go` 與 `codex/parser.go`，將事件流中的 token 數據正規化為標準 `backend.TokenUsage`。
  - 產出：`internal/backend/opencode/parser.go`、`internal/backend/codex/parser.go`
  - 完成判準：解析出之 Usage 包含 input、output、total、cached，若無數值則為 nil，不偽造 0。

### Phase 2：Context 預算、估算器與模型目錄核心套件
- 📝 **Task 2.1**（對應 CTXWIN-002, CTXWIN-005）：定義 `internal/backend` 與 `internal/contextwin` 核心型別（`TokenUsage`, `DataProvenance`, `ModelWindowInfo`, `StageEstimate`, `StageAdmission`）。
  - 產出：`internal/backend/usage.go`、`internal/contextwin/types.go`
  - 完成判準：資料結構明確區分 Exact / Estimated / Unknown，嚴格維持 Sealed Context 不變性（不提供默改 prompt 或 memory 之介面）。
- 📝 **Task 2.2**（對應 CTXWIN-002）：實作靜態模型目錄 `Catalog` 與已知模型 Context 上限查詢，支援配置優先與未知降級。
  - 產出：`internal/contextwin/catalog.go`、`internal/contextwin/catalog_test.go`
  - 完成判準：可正確識別常見模型限制，未列模型標記為 `LimitSourceUnknown`。
- 📝 **Task 2.3**（對應 CTXWIN-002, CTXWIN-003）：實作輕量啟發式 Token 估算器 `Estimator`，提供字元換算與安全保留計算，標記 `ProvenanceEstimated`。
  - 產出：`internal/contextwin/estimator.go`、`internal/contextwin/estimator_test.go`
  - 完成判準：對不同語言字元精確估算，不依賴外部 CGO 分詞庫，計算結果附帶 provenance。

### Phase 3：Stage-Aware Prompt 組裝與准入引擎
- 📝 **Task 3.1**（對應 CTXWIN-003）：建立 Stage Prompt 組裝器與估算整合，分別對 Direct、Oracle、Striker、Critic 產生對應的估算。
  - 產出：`internal/contextwin/stage_estimator.go`、`internal/contextwin/stage_estimator_test.go`
  - 完成判準：Striker 與 Critic 在具備 OraclePlan 與 StrikerOutput 時能正確評估膨脹後之真實上下文。
- 📝 **Task 3.2**（對應 CTXWIN-004）：實作准入檢查引擎 `AdmissionEngine`，支援配置超量政策（拒絕、警示、忽略未知）。
  - 產出：`internal/contextwin/admission.go`、`internal/contextwin/admission_test.go`
  - 完成判準：超量時產出 `VerdictRejected`，未設定限制時產出 `VerdictBypassed`。

### Phase 4：Orchestrator 整合與 Fail-Closed 防禦
- 📝 **Task 4.1**（對應 CTXWIN-003, CTXWIN-004, CTXWIN-006）：擴充 `pipeline.StageTrace` 與 `pipeline.PipelineTrace`，新增 `Admission` 與 `Usage` 欄位。
  - 產出：`internal/pipeline/types.go`、`internal/pipeline/types_test.go`
  - 完成判準：Trace 正確承載估算與實測數據，JSON 序列化與反序列化測試通過。
- 📝 **Task 4.2**（對應 CTXWIN-004, CTXWIN-007）：Orchestrator 在各 Stage 執行前整合 Preflight 准入檢查，超量時 fail-closed 拒絕，模型呼叫次數為 0，保留已完成 stage 之結果且不切換 backend。
  - 產出：`internal/pipeline/orchestrator.go`、`internal/pipeline/orchestrator_test.go`
  - 完成判準：Striker 或 Critic 超量拒絕時，前面 Stage 的產出完整保留在 Trace，絕無二次呼叫或跨後端重跑。

### Phase 5：持久化相容性與 Engine 審計串接
- 📝 **Task 5.1**（對應 CTXWIN-006, CTXWIN-007）：`internal/storage` 驗證 SQLite 對擴充後之 PipelineTrace 儲存與讀取向前向後相容。
  - 產出：`internal/storage/sqlite_test.go`
  - 完成判準：歷史無 Usage 任務讀取時不報錯且 Usage 為 nil，新任務之 Trace 與 Usage 完整持久化。
- 📝 **Task 5.2**（對應 CTXWIN-005, CTXWIN-007）：驗證 Engine 接收 SubmitRequest、相同 Request ID 重送時，Context 不變性與 Admission 結果一致性。
  - 產出：`internal/engine/engine_test.go`
  - 完成判準：重送請求不重跑後端執行，不覆蓋既有任務結果。

### Phase 6：外迴圈驗收與全系統回歸
- 📝 **Task 6.1**（對應 CTXWIN-001 ～ CTXWIN-008）：建立外迴圈 BDD 測試套件，完整覆蓋 CTXWIN-001 ～ CTXWIN-008 所有 Scenario。
  - 產出：`tests/integration/context_window_test.go`
  - 完成判準：全數 CTXWIN 劇本在 hermetic / mock 環境下驗收通過。
- 📝 **Task 6.2**（對應 CTXWIN-008）：執行既有回歸測試網（MBACK-001 ～ MBACK-012, TFACE-001 ～ TFACE-009, CTXMEM-001 ～ CTXMEM-008）。
  - 產出：測試執行日誌與審查報告
  - 完成判準：所有既有測試套件 100% 通過（全綠）。

---

## 使用方式對照

### 變更前
```go
// 呼叫端無從得知各 Stage 的 Prompt 膨脹與 Token 使用量
res, err := backend.Run(ctx, req)
// res 僅包含 Output 與 Events，缺乏標準 Usage
trace.Stages = append(trace.Stages, StageTrace{
    Name: "striker",
    DurationMs: 120,
})
```

### 變更後
```go
// 1. 執行前：Orchestrator 根據實際 Stage Prompt 執行准入檢查
admission := admissionEngine.Check(stage, fullPrompt, model)
if admission.Verdict == VerdictRejected {
    // Fail-closed：不呼叫後端，記錄拒絕原因
    return failClosed(stage, admission)
}

// 2. 執行後：後端回傳精確 UsageObservation
res, err := backend.Run(ctx, req)
trace.Stages = append(trace.Stages, StageTrace{
    Name:       "striker",
    Admission:  admission, // 包含估算值與資料來源 (Estimated)
    Usage:      res.Usage, // 包含後端實測值 (Exact)
    DurationMs: 120,
})
```

---

## 測試策略

1. **單元測試（Unit Tests）**：
   - `internal/backend/opencode` 與 `codex` 的 parser 測試，驗證從 JSONL 原始行中精確抽取 TokenUsage。
   - `internal/contextwin` 的估算邏輯與模型目錄測試，測試中英混雜字串、代碼字串之估算合理性。
   - `internal/pipeline` 的 Orchestrator 測試，透過 Mock Backend 模擬各種窗口大小與超量情境。
2. **能力探測（Contract Probe）**：
   - 區分 Hermetic（使用真實 CLI 輸出錄製之樣本 fixture 進行解析）與 Live Probe（需本機環境存在特定 CLI 時執行），確保在無外部 CLI 的 CI 環境中測試仍可執行。
3. **整合測試（Integration Tests）**：
   - 驗證 Direct 模式 1 次模型呼叫、Three-Face 模式 3 次模型呼叫下的完整 PipelineTrace 與各 Stage 之 Usage 記錄。
   - 驗證相同 Request ID 重送之冪等性。
4. **回歸測試網（Regression Suite）**：
   - 執行 `go test ./...`，驗證 MBACK、TFACE、CTXMEM 既有測試無任何回歸破壞。

---

## 驗收編號對應的測試層級與策略

| 驗收編號 | 核心驗證標的 | 測試層級 | 測試策略 |
|---|---|---|---|
| **CTXWIN-001** | 固定版本 OpenCode / Codex 能力探測與 unsupported 矩陣 | Contract Probe / Unit | 撰寫 probe 測試與 fixture 解析，驗證可觀測之 token usage 及未支援能力斷言 |
| **CTXWIN-002** | Exact、Estimated、Unknown 數據來源明確區分且不偽造 0 | Unit / Type Test | 驗證 `TokenUsage` 與 `StageEstimate` 之 provenance 標籤與序列化行為 |
| **CTXWIN-003** | Three-Face 各 Stage 依實際 Prompt 獨立進行動態估算 | Unit / Pipeline Test | 模擬 OraclePlan 與 StrikerOutput，斷言 Striker 與 Critic 之估算值反映動態內容 |
| **CTXWIN-004** | 超量時 Preflight Fail-Closed 拒絕，0 後端呼叫，保留已完成 Trace | Integration / Orchestrator | 設定超低預算上限，驗證 Striker 被拒絕時 Oracle 結果依然保留且不換後端 |
| **CTXWIN-005** | Sealed Context 與 Memory 快照不可變性 | Unit / Engine Test | 斷言預算評估前後 `ExecutionContext` 之記憶快照與原始 Prompt 完全一致 |
| **CTXWIN-006** | Task / PipelineTrace 具有 stage 分別的 estimate 與 observation | Storage / Integration | 驗證 Trace 存入 SQLite 後重讀，能正確讀出各 Stage 之估算與實測用量 |
| **CTXWIN-007** | 相同 Request ID 重送冪等性與決策一致性 | Engine Integration | 重送相同 Request ID，驗證返回相同任務與 Trace，無二次後端調用 |
| **CTXWIN-008** | 未啟用新限制時既有功能與回歸測試 100% 通過 | Regression Test Suite | 執行 `go test ./...` 既有全部測試，保持全綠 |

---

## 風險與首要驗證

引用 [README.md](./README.md) 的 `## 風險與首要驗證` 節點。

---

## 檢查清單

- [ ] 探測 OpenCode 與 Codex CLI 的真實 usage 輸出與 session 行為
- [ ] 定義標準 `TokenUsage` 與 `DataProvenance`
- [ ] 實作 stage-aware 的 prompt 估算器（支援 OraclePlan 與 StrikerOutput 動態計算）
- [ ] 整合 Orchestrator 前置檢查，落實超量 fail-closed 且不擅改 sealed context
- [ ] 保持向前向後相容，既有 SQLite 資料無縫遷移
- [ ] 通過 CTXWIN-001 ～ CTXWIN-008 驗收劇本
- [ ] 全套既有測試回歸通過
