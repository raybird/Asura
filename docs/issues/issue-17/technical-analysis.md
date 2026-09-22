# Issue 17 - Phase 3E：Context Window Management Foundation 技術分析

> NOTE（2026-09-21）：下列 CLI 欄位及控制能力敘述是待 Task 1.1 驗證的假設清單，不是已完成的能力探測結論；不以此直接判定 exact 或 unsupported。固定版本的 live artifact 與離線 fixture 必須分開保存。

## 技術可行性分析

### 1. OpenCode (1.18.23 / 1.18.31) Context 與 Token 觀測能力

- **命令結構與模式**：
  - Asura 透過 `opencode run --format json --agent asura-readonly|asura-striker ...` 啟動非互動式獨立進程。
  - 每次呼叫為獨立進程，Asura 目前並未啟用 `--continue` 或 `--session` 跨 Stage 延續對話歷史（每個 Stage 為全新對話）。
- **事件串流與 Token 回報**：
  - OpenCode JSONL 的事件中包含 `part.tokens` 物件，欄位包含：
    - `total`: 總 token 數
    - `input`: 輸入 prompt token 數
    - `output`: 模型生成 token 數
    - `reasoning`: 思考過程 token 數（可選）
  - 這些數據散佈於各回合或 step 的事件中，在進程結束前可彙總為單一 Stage 執行的總 TokenUsage。
- **不可用控制能力（Unsupported Capabilities）**：
  - **工具輸出攔截**：OpenCode CLI 在執行中無外部攔截 hook 機制，無法在工具輸出送入模型 context 前進行即時動態裁剪。
  - **模型內部會話編輯**：無法透過外部進程直接修改 OpenCode 內部維護的上下文記憶體。
  - **模型窗口動態查詢**：CLI 未提供查詢目前使用模型之精確 Context Window 上限的獨立指令。

### 2. Codex CLI (0.155.0 / 0.155.1) Context 與 Token 觀測能力

- **命令結構與模式**：
  - Asura 透過 `codex exec --json --ephemeral -C <workspace> ...` 執行任務。
  - `--ephemeral` 保證進程不將 session 持久化至硬碟，各 Stage 間完全隔離。
- **事件串流與 Token 回報**：
  - Codex JSONL 在 `turn.completed` 事件或 `item.completed` 事件中附帶 `usage` 物件：
    - `input_tokens` / `prompt_tokens`: 輸入 token 數
    - `output_tokens` / `completion_tokens`: 輸出 token 數
    - `cached_tokens` / `cache_read_input_tokens`: 快取命中 token 數（若有）
    - `total_tokens`: 總計 token 數
  - 可從 stdout 中可靠解析並對應至標準 `TokenUsage`。
- **不可用控制能力（Unsupported Capabilities）**：
  - **原生對話歷史替換**：在單次 `codex exec` 運行中，無法動態替換先前的對話歷史。
  - **工具輸出前置過濾**：沙箱與指令執行由 Codex 內部執行器控制，外部進程無法在模型看見輸出前插手中介。

---

## 方案選型與取捨

### 方案一：Token 估算機制選型（Token Estimation Method）

| 方案 | 說明 | 優點 | 缺點 | 結論 |
|---|---|---|---|---|
| **A. 外部 CGO / Python 分詞庫** | 引入 `tiktoken-go` 或 Python tokenizer sidecar | 精度高，接近模型實際 token 數 | 破壞 Asura 輕量化、純 Go、無 CGO 與獨立部署目標；多模型（OpenCode 各 provider / Claude / GPT）需相依多種分詞器 | ❌ 否決 |
| **B. 啟發式安全字元換算 + 來源標記 (Provenance)** | 依文字長度（例如英文 ~4 chars/token，中文/代碼 ~1.5~2 chars/token）結合安全保留係數，明確標註 `provenance=estimated` | 純 Go 零依賴、速度快、邏輯透明；審計層明確區分實測與估算，不偽造精度 | 為近似上限預估，非 100% 精確 | ✅ **採納** |
| **C. 遠端 API Dry-Run / Counting API** | 在執行前向模型 provider API 發送 count tokens 請求 | 絕對精確 | 需要額外網路請求與 API 金鑰授權，違反 CLI 黑盒子封裝原則，且增加延遲 | ❌ 否決 |

**取捨決策**：採納 **方案 B**。Asura 核心保持純 Go 零額外依賴。估算結果嚴格標示為 `ProvenanceEstimated`，而由後端 CLI 回傳之真實數據標示為 `ProvenanceExact`。數值缺失時標示為 `ProvenanceUnknown`，絕不以 0 假冒。

### 方案二：模型 Context Window 限制來源優先序

在進行准入判斷時，系統需要知道後端模型的最大 Context Limit（例如 128k, 200k, 1M）。

| 優先序 | 來源 | 說明 |
|---|---|---|
| **Priority 1** | **程序級顯式配置** | `ASURA_CONTEXT_LIMIT`；啟動時驗證並注入，首版不新增逐任務 Metadata |
| **Priority 2** | **靜態模型目錄 (Static Model Catalog)** | 僅對 Task 1.1 證實可在執行前可靠識別的實際模型查表；目錄項目附來源，不依 BackendID 猜測模型 |
| **Priority 3** | **未知降級政策 (Unknown / Unbounded Fallback)** | 若模型未列於目錄且未配置，標記為 `LimitUnknown`。在此狀態下，僅記錄估算數值，不進行強制阻擋，除非管理者配置了全域安全上限 |

詳細設定、role 模型來源、unknown、policy 及入口串接契約以 [實作計畫第 2 節](implementation-plan.md) 為準；預設 policy 與保留比例仍待 TBD-2 核准。

### 方案三：Context 超量處理政策（Overflow Policy）

當某個 Stage 的估算 Context 需求超過模型限制（或安全門檻）時的系統反應：

```diff
- 傳統推測性方案 (Anti-pattern)：
- 1. 自動刪除或壓縮 Project Memory 快照
- 2. 自動截斷原始 Prompt 或 StrikerOutput
- 3. 自動切換到其他窗口較大的後端 (Silent Failover)

+ Asura 契約防禦方案 (Selected Pattern)：
+ 1. 嚴格維持 Sealed Context 不變性（絕不私自刪除記憶或截斷 Prompt）
+ 2. 於後端調用前執行 Stage Preflight 拒絕（Fail-Closed，調用次數為 0）
+ 3. 若為 Striker/Critic 超量，保留已完成 Stage 之 Trace 與產物，終止後續 Stage
+ 4. 嚴格禁止切換至另一後端（No Automatic Failover）
```

**取捨決策**：採納 **Fail-Closed 審計中斷機制**。超量時回報明確的 `ErrContextLimitExceeded`，絕不擅自竄改使用者密封上下文。

---

## 架構影響評估

### 1. `internal/backend` 契約擴充
```go
// TokenUsage 代表標準化之 Token 用量觀測
type TokenUsage struct {
    InputTokens     *int64 `json:"input_tokens,omitempty"`
    OutputTokens    *int64 `json:"output_tokens,omitempty"`
    TotalTokens     *int64 `json:"total_tokens,omitempty"`
    CachedTokens    *int64 `json:"cached_tokens,omitempty"`
    ReasoningTokens *int64 `json:"reasoning_tokens,omitempty"`
}

// DataProvenance 標示數據來源與可信度
type DataProvenance string

const (
    ProvenanceExact     DataProvenance = "exact"     // 後端實測回報
    ProvenanceEstimated DataProvenance = "estimated" // 啟發式估算
    ProvenanceUnknown   DataProvenance = "unknown"   // 未知或缺失
)

// ExecutionResult 擴充 TokenUsage
type ExecutionResult struct {
    // ... 既有欄位保持不變 ...
    Usage          *TokenUsage    `json:"usage,omitempty"`
    UsageSource    DataProvenance `json:"usage_source,omitempty"`
}
```

nullable 欄位各自保存 presence：nil 為 unknown，指向 0 為 exact zero；整筆來源只描述已知欄位。首版不衍生 total；按 probe 證實的事件語意聚合，缺回合欄位不冒充完整總量。雙後端 adapter 將 parser 結果寫入 ExecutionResult，即使執行失敗仍保留。

### 2. `internal/pipeline` Stage Admission 與 Orchestrator 流程
- 新增 `ContextBudget` 與 `StageAdmission` 資料結構。
- Orchestrator 在調用每個 Stage（Oracle, Striker, Critic）之前：
  1. 呼叫 Stage.Prepare 組裝唯一的 request；估算與 ExecutePrepared 共用同一 request.Prompt，不在 contextwin 複製 Stage 組裝邏輯。
  2. 呼叫 `contextwin.Estimator` 計算該 Stage 的上下文預算與預估 Token 需求。
  3. 驗證是否在容許窗口與 Headroom 內。
  4. 若超量且啟用了阻擋政策，立即記錄該 Stage 為 `StageFailed`（原因為 context limit exceeded），後續調用次數為 0，返回 partial output 與已完成 trace。

Stage 回傳業務產物、ExecutionResult 與 error；Orchestrator 保存用量後處理成功／失敗。Oracle／Critic 非法 JSON 與 backend error 均不可丟棄已消耗用量。runner 無用量時為 unknown。

### 3. `internal/storage` 資料持久化
- `StageTrace` 擴充 `Usage` 與 `Admission` 欄位。
- SQLite JSON 序列化天然向前相容，歷史任務無此欄位時解析為 nil，不影響既有讀取。

---

## 實作細節與待釐清項目

1. **OpenCode 與 Codex 的本機 Live Probe 實測**：
   - 需要撰寫專屬測試，透過真實 CLI 執行驗證其輸出的 JSONL 確實包含預期的 usage 結構。
2. **啟發式估算係數**：
   - 預設估算比例：英文 1 token ≈ 4 字元；中文及多字節字元 1 token ≈ 1.5 字元；代碼符號按 1 token ≈ 2.5 字元估算，並提供保險 Headroom（如預留 20% 給輸出與系統格式）。
3. **預設超量阻擋啟用開關**：
   - 第一版預設採取「**觀測與審計模式（Observation/Audit Mode）**」或「**可配置門檻阻擋**」，避免在未取得使用者對門檻之明確核准前，破壞現有 Direct / Three-Face 正常任務之執行。
