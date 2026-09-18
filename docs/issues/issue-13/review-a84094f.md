# 審查報告 (PR #14 / Issue #13)

- 審查日期：2026-09-17
- 範圍：完整 PR (`799625c77ffb38e6e0c2302d407f20e38f22ea5c..a84094f53e7e45278619fbc44642a237abc837a5`)
- Reviewed BASE SHA：`799625c77ffb38e6e0c2302d407f20e38f22ea5c`
- Reviewed HEAD SHA：`a84094f53e7e45278619fbc44642a237abc837a5`
- Reviewed patch-id：`b9e8fb8cf29455b54a5d63e268668e21334c3fbe`
- 獨立 reviewer：Independent Code Reviewer (Subagent)
- Review artifact：`docs/issues/issue-13/review-a84094f.md`

---

## 審查結論與流程判定

### 流程判定：`PASS`

**總體評價**：
本次 PR 嚴格遵循既定架構規格與實作計畫，在維持 Phase 3B 既有 Direct 模式 100% 向後相容的前提下，成功落地固定式 Three-Face Pipeline（Oracle → Striker → Critic）。
- 完整實現 10 項 Gherkin 驗收劇本（@TFACE-001 ~ @TFACE-010），所有測試皆於真實 Daemon/IPC 與 SQLite 儲存層環境下執行，無假綠燈或空斷言。
- 具備高度嚴密的 Fail-Closed 失敗防禦，各 Stage 失敗即刻中斷並持久化現場，無靜默回退（silent fallback）。
- 透過 `engine.PipelineRunner` 介面徹底解耦 `engine` 與 `pipeline` 套件，無循環依賴。
- 保持極佳的並發安全（Stateless Orchestrator），不可變 ExecutionContext 密封保證與 Redelivery 冪等防禦皆正確生效。
- 嚴格恪守 YAGNI 原則，未過早引入動態路由或自動修復迴圈。
- 本次審查無任何阻塞性問題（MUST FIX = 0）。

---

## 問題與風險分類

### MUST FIX（阻塞項目）
- **無**。

---

### SHOULD FIX（建議改善項目，不阻礙本次合併）

1. **`DefaultCriticStage` 之 Prompt 組裝可考慮納入專案記憶（Project Context）**
   - **位置**：`internal/pipeline/stage_critic.go:36`
   - **現況**：在 `stage_critic.go` 中，Critic 組合提示詞時使用的是原始 Prompt `execCtx.OriginalPrompt`，而未如 `stage_oracle.go` 與 `stage_striker.go` 一致調用 `engine.ComposePrompt(execCtx.OriginalPrompt, execCtx)`。
   - **影響**：雖然 Oracle 在生成 Plan 時已完整參考專案記憶，且 Oracle Plan 會完整傳入 Critic，但若 Critic 需要直接比對原始專案記憶約束，目前 Critic Prompt 區塊未直接展開 Project Context 列表。
   - **建議**：未來可將 Critic 的 prompt 生成調整為 `engine.ComposePrompt(execCtx.OriginalPrompt, execCtx)`，使三階段在提示詞上具備一致的上下文視野。

2. **Daemon IPC Ingress 建議補齊 `ExecutionMode` 合法性防禦檢驗**
   - **位置**：`internal/daemon/daemon.go:476-480`
   - **現況**：CLI 端（`run.go`、`ask.go`）已透過 `pipeline.IsValidExecutionMode` 嚴格阻擋非法 mode；但若外部第三方客戶端直接透過 Unix Socket 發送未定義的 mode（如 `"foo"`），Daemon 目前會將其接受並寫入資料庫 `tasks.execution_mode`，隨後 Engine 在非 `"three-face"` 情況下會走入 `else`（Direct 模式）。
   - **影響**：可能導致資料庫記錄之 `execution_mode` 為未知字串，而實際管線卻以 Direct 模式執行且 `pipeline_trace` 記錄為 direct。
   - **建議**：在 `daemon.go` 之 IPC Request 進入點加上防禦檢核：
     ```go
     if !pipeline.IsValidExecutionMode(pipeline.ExecutionMode(reqMode)) {
         _ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("invalid execution mode: %q", reqMode)})
         continue
     }
     ```

---

### NICE TO HAVE（改進亮點與後續演進建議）

1. **使用者主動取消（Cancelled）時的 Partial Trace 持久化**
   - **位置**：`internal/engine/engine.go:415, 428`
   - **現況**：任務被取消時，Engine 會持久化 partial output 至 `tasks.result`，但跳過了 `UpdateTaskPipelineTrace`。
   - **建議**：若在取消收尾時一併將當前收集到的 `traceJSON` 寫入，將便於使用者於 `asura status` 查核任務究竟是在 Oracle、Striker 還是 Critic 階段被取消。

---

## 已查核維度詳情

### 1. 完整驗收劇本查核（Proof of Test 核實）

| Scenario ID | 劇本描述 | 實作覆蓋與證據位置 | 查核結論 |
|---|---|---|---|
| **@TFACE-001** | Three-Face 任務依固定順序完成 | `tests/integration/three_face_pipeline_test.go:206` (`TestTFACE001_FixedOrderExecution`)<br>`internal/pipeline/orchestrator_test.go:65` | **通過**。驗證嚴格順序 `[oracle, striker, critic]` 各執行 1 次，無重複無跳步，trace 完整保存。 |
| **@TFACE-002** | Oracle Plan 為結構化且可追溯 | `tests/integration/three_face_pipeline_test.go:253` (`TestTFACE002_OraclePlanStructuredAndTraceable`)<br>`internal/pipeline/types_test.go:27` | **通過**。Oracle 產出結構化 `OraclePlan`（Goal, Constraints, Steps, ExpectedOutcome），Striker 正確接收並注入 Prompt，DB trace 完整保留。 |
| **@TFACE-003** | 所有角色使用同一 ExecutionContext | `tests/integration/three_face_pipeline_test.go:315` (`TestTFACE003_SharedSealedExecutionContext`) | **通過**。任務接受時密封 context。測試驗證在任務接受後立即刪除資料庫記憶，Oracle、Striker、Critic 依然取得原密封記憶，杜絕執行中資料漂移。 |
| **@TFACE-004** | Oracle 失敗時 Fail-Closed | `tests/integration/three_face_pipeline_test.go:387` (`TestTFACE004_OracleFailureFailClosed`)<br>`internal/pipeline/orchestrator_test.go:136` | **通過**。Oracle 故障時，Striker 與 Critic 呼叫計數為 0，Task 標為 `failed`，錯誤訊息完整保留，嚴禁 silent fallback。 |
| **@TFACE-005** | Striker 失敗不進行 Critic 或自動重試 | `tests/integration/three_face_pipeline_test.go:437` (`TestTFACE005_StrikerFailureNoCriticNoRetry`)<br>`internal/pipeline/orchestrator_test.go:175` | **通過**。Striker 故障時，Critic 不啟動，無未經授權之自動 retry，保存 partial output 與 Oracle Plan，Task 標為 `failed`。 |
| **@TFACE-006** | Critic 成功 Review Striker Output | `tests/integration/three_face_pipeline_test.go:489` (`TestTFACE006_CriticReviewSuccess`)<br>`internal/pipeline/stage_test.go:82` | **通過**。Critic 正確審查 Striker 產出並產出 `CriticReview`（Verdict, Findings, Summary），trace 完整持久化。 |
| **@TFACE-007** | Critic 失敗不得假裝完整成功 | `tests/integration/three_face_pipeline_test.go:542` (`TestTFACE007_CriticFailureMustNotClaimSuccess`)<br>`internal/pipeline/orchestrator_test.go:208` | **通過**。覆蓋雙子路徑：（1）Critic 執行拋錯；（2）Critic Verdict 為 `failed`。兩者皆正確保存 Striker Output 並將 Task 標為 `failed`。 |
| **@TFACE-008** | Direct mode 維持既有任務語意 | `tests/integration/three_face_pipeline_test.go:618` (`TestTFACE008_DirectModeSemanticsPreserved`)<br>`internal/engine/engine_test.go:1173` | **通過**。預設或顯式指定 direct 時，Oracle 與 Critic 呼叫次數為 0，僅調用 Striker 1 次，Prompt 組裝維持 Phase 3B 格式。 |
| **@TFACE-009** | Three-Face Redelivery 不造成重複執行 | `tests/integration/three_face_pipeline_test.go:669` (`TestTFACE009_ThreeFaceRedeliveryIdempotency`)<br>`internal/daemon/daemon_test.go` | **通過**。Request Fingerprint 納入 Mode 計算。重送相同 Request ID + 相同 Payload 時，直接回傳既有 Task，無任何二次模型調用。 |
| **@TFACE-010** | Pipeline Metrics 可識別額外模型成本 | `tests/integration/three_face_pipeline_test.go:725` (`TestTFACE010_PipelineMetricsAndCostDistinction`)<br>`internal/daemon/metrics_test.go:18` | **通過**。Direct 模式回報 1 stage, 1 call；Three-Face 回報 3 stages, 3 calls，總耗時 `duration_ms > 0` 且可查。 |

---

### 2. 關鍵失敗面查核

1. **並發與 Data Race（Concurrency Safety）**：
   - `PipelineOrchestrator` 設計為完全無狀態（Stateless），其內部欄位僅有唯讀介面，執行狀態 `PipelineTrace` 於 `Execute()` 內部區域配置，支援高並發 worker 多協程同時調用，無共用記憶體寫入競爭。
   - CLI 端使用執行緒安全的 `safeBuffer` 封裝輸出緩衝，消除 MultiWriter 併發寫入時的潛在 Data Race。
   - `SQLiteStore` 維持單一連線池與 WAL 模式，杜絕多線程死鎖。
   - 所有測試皆通過 `-race` 檢驗。

2. **未配置 PipelineRunner 之 Fail-Closed 防禦**：
   - 查核 `internal/engine/engine.go:369-376`，當請求 `three-face` 模式但未注入 `PipelineRunner` 時，系統主動以獨立 context 將任務更新為 `StatusFailed` 並回傳錯誤，絕不偷偷降級執行 Direct。

3. **重送指紋防禦（Fingerprint Calculation）**：
   - 查核 `internal/daemon/daemon.go:1389-1402`，指紋計算 `calculateTaskFingerprint` 包含 `canonicalWs + ":" + prompt + ":mode=" + mode + ":" + sortedMemoryIDs`。
   - 若相同 Request ID 嘗試篡改 mode（如先發 direct 後發 three-face），指紋比對失敗，立即回傳 `"request ID reused with different payload"`，防禦跨模式重送污染。

4. **資料庫結構向後相容性與遷移**：
   - 查核 `internal/storage/sqlite.go:148-212`，`initSchema` 使用 `getTableColumns` 動態檢測既有欄位，對 `execution_mode`（預設 `'direct'`）與 `pipeline_trace`（預設 `''`）執行非破壞性 `ALTER TABLE`，對舊版資料庫平滑升級，歷史任務無損。

---

### 3. 架構邊界與設計規範查核

1. **低耦合介面隔離**：
   - `internal/engine` 定義了輕量級抽象介面 `PipelineRunner`，由外部注入；`internal/pipeline` 不反向依賴 `engine` 核心調度邏輯，成功防範 Go 套件循環依賴（import cycle）。
2. **遵守 YAGNI 與範圍控制**：
   - 嚴格限定於 Direct 與 Three-Face 兩種固定模式。
   - 未偷跑動態複雜度路由（Dynamic Routing）、未偷跑多模型自動故障轉移（Failover）、未偷跑 Critic Recursive Repair Loop，完全符合 `requirement-analysis.md` 的邊界劃分。
3. **Commit 訊息規範遵循**：
   - 檢核範圍內全部 Commit，無任何 `Co-Authored-By: Claude` 或 AI 標記，格式符合 Conventional Commits 規範。
