# Issue 17 - Phase 3E：Context Window Management Foundation 需求分析

## 需求描述

承接 Phase 3D [Issue #15](https://github.com/raybird/Asura/issues/15) / [PR #16](https://github.com/raybird/Asura/pull/16) 已建立的 OpenCode + Codex CLI 雙後端共同契約，以及 Phase 3B 的 Sealed `ExecutionContext`、Phase 3C 的 Direct / Three-Face 固定管線。

Asura 目前的調度邏輯尚未具備 Context Window 的預先感知與後驗審計能力：
1. **執行前未知**：系統不知道當前任務送至特定 backend、特定 stage 時，其實際 prompt context 大小以及是否會超過該後端或底層模型的窗口上限。
2. **Three-Face 階段膨脹未知**：Oracle 階段會附加系統架構指引；Striker 階段會動態注入由 Oracle 產出的 `OraclePlan`；Critic 階段更會同時注入 `OraclePlan` 與 `StrikerOutput`。若僅在任務進入點以原始 prompt 估算，將嚴重低估 Striker 與 Critic 的上下文負載。
3. **執行後觀測缺失**：`backend.ExecutionResult` 與 `pipeline.StageTrace` 尚未統一記錄後端回報的真實 token usage（例如 input, output, cached tokens），無法進行精確的成本審計與容量監控。
4. **能力邊界未驗證**：原架構草圖中的五層防禦（如工具輸出截斷、對話歷史動態改寫）屬於遠期提案，在尚未對固定版本 OpenCode 與 Codex CLI 完成真實能力探測前，不能假設 CLI 具備模型內部 context 編輯權，嚴禁以推測性假設實作偽功能。

本期核心目標：建立**能力證據優先、最小可行 Context Window 管理基礎（Context Window Management Foundation）**，包含：
- 固定版本 **OpenCode 1.18.23 / 1.18.31** 與 **Codex CLI 0.155.0 / 0.155.1** 的能力探測報告與 unsupported 能力清冊。
- Direct 與 Three-Face 任務級、stage-aware 的 Context 預算估算與准入檢查（Admission）。
- token 數據來源可信度（Provenance）、估算值與實測值分離的資料表示。
- 審計資訊完整記錄於 Task / PipelineTrace，並保持密封上下文（Sealed Context）、工作區隔離、不自動切換後端（No Automatic Failover）等既有契約。

## 現況分析（現有程式碼邏輯）

### 1. `internal/engine/engine.go` - 上下文封裝與 Prompt 組合
- `ExecutionContext` 密封了 `OriginalPrompt`、`Workspace`、`Backend`、`ProjectMemories`、`MemoryRefs` 與 `CreatedAt`。
- `ComposePrompt` 函式（`engine.go#L62-L75`）單純將 `ProjectMemories` 格式化為 Markdown 區塊並與 `OriginalPrompt` 拼接。此處未計算字元數或 token 數，亦未考量各後端模型上限。
- `SubmitRequest` 接收使用者請求後，建立 `ExecutionContext` 與計算 Request Fingerprint（納入 backend ID、workspace、prompt 等），保證相同 Request ID 之冪等重送。

### 2. `internal/pipeline` - Stage 組裝與 Orchestrator
- **OracleStage**（`stage_oracle.go#L49-L51`）：組合 Planner system instruction 與 base prompt，呼叫後端產生 JSON plan。
- **StrikerStage**（`stage_striker.go#L48-L73`）：在 base prompt 前方附加 `### Oracle Execution Plan`（包含 Goal、Constraints、Steps、Expected Outcome）。
- **CriticStage**（`stage_critic.go#L48-L52`）：組合 Critic 審查 instruction、Original Prompt、Oracle Plan JSON 與 `StrikerOutput`。
- **PipelineOrchestrator**（`orchestrator.go`）：
  - 依序調度 Stage，並產出 `PipelineTrace`。
  - `StageTrace` 目前僅包含 `Name`、`Status`、`DurationMs`、`BackendID`、`BackendVersion`、`Error`、`Output`。
  - 缺乏 token usage 數據與執行前的容量准入判定。

### 3. `internal/backend` - 共同後端契約與事件解析
- `backend.ExecutionResult`（`backend.go#L54-L63`）包含 `Output`、`PartialOutput`、`Events`、`FailureClass`、耗時等，但沒有 Token Usage 或 Context 限制相關欄位。
- `internal/backend/opencode/parser.go`：在解析 OpenCode 的 JSONL 事件時，結構體中已有 `TokenUsage`（`Total`, `Input`, `Output`, `Reasoning`），但未統一提升至 `backend.ExecutionResult`。
- `internal/backend/codex/parser.go`：Codex JSONL 事件具有 `RawEvent.Usage`，目前僅抽取文字與工具事件，未正規化 token usage。
- 雙後端均以單次 CLI 進程執行（非長期共享記憶體），進程退出後狀態獨立。

### 4. `internal/storage` - SQLite 資料持久化
- `SQLiteStore` 儲存 Task 與 PipelineTrace（JSON 字串）。既有資料庫 schema 須維持向前相容，擴充 Trace 欄位不得破壞既有 Task 的讀取。

## 問題點總結

1. **Prompt 膨脹不可見**：Three-Face 的後續 Stage 提示詞顯著大於原始 Prompt，目前 Orchestrator 無法預測各階段是否會超出模型限制，可能在執行到 Striker 或 Critic 時才發生非預期的溢出失敗。
2. **缺乏標準 Usage 審計契約**：OpenCode 與 Codex 各自的 token 回報格式不同，且既有 `ExecutionResult` 與 `StageTrace` 遺漏此資訊。
3. **推測性功能風險**：若在缺乏能力探測的情況下實作工具輸出攔截或動態對話裁剪，將破壞 CLI 後端黑盒子的一致性。
4. **估算值與實測值混淆**：若將字元數換算或啟發式估算冒充為 backend 實測 token，會破壞審計的可信度。若缺少真實 token 數據，亦不可捏造為 0。

## 目標

1. **Probe First**：完成 OpenCode 與 Codex CLI 的 Context 能力探測，產出具備可重現性之 probe 報告與 unsupported 清冊。
2. **Stage-Aware Admission**：為 Direct、Oracle、Striker、Critic 定義明確的實際 prompt 估算機制與前置檢查（Admission），支援動態產物（OraclePlan、StrikerOutput）生成後的階段性准入。
3. **Usage Observation & Provenance**：定義標準化的 `UsageObservation` 與 `ContextBudget`，明確區分 `Exact`（後端實測）、`Estimated`（估算）與 `Unknown`（未知），嚴禁虛報 0。
4. **Trace 審計與向前相容**：將預算估算與實測用量納入 `StageTrace`，確保既有資料庫與查詢相容。
5. **安全與不變性保障**：維持 Sealed Context 不變性，超量時不擅自竄改 prompt 或丟棄 memory；維持 No Automatic Failover 與重送冪等性。

## 審查後補充（2026-09-21）

- 用量可能只回報部分欄位，資料契約需逐欄位區分 unknown 與真實零值，不能只以整筆 UsageSource 表示。
- 實際 prompt 組裝與 Backend.Run 位於三個 Stage，ExecutionResult 建立於雙後端 adapter；計畫須涵蓋這些邊界才能將原生用量送到 Trace，並保留失敗後已取得的用量。
- 現有 SubmitRequest／ExecutionContext 沒有模型與預算設定欄位，BackendID 也不等於模型。首版提案以程序級環境設定注入本機 run 與 daemon，模型識別依 probe 證據，無法辨識時保留 unknown，不猜測模型。
- 修訂僅補齊規劃；固定版本能力結論尚待 live probe，預設政策與驗收仍待核准。
