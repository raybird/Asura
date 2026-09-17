# Issue 13 - 實作計畫：Phase 3C Three-Face Fixed Pipeline

建立日期：2026-09-17

## 實作步驟與 Phase 規劃

本計畫依據 [`docs/AGENTS.md`](../../AGENTS.md) 風險優先原則規劃。因風險評估為 **High**，首要驗證任務（固定順序與 Fail-Closed 語意）安排在 Phase 1 優先落實。

---

### Phase 1：契約定義與首要風險驗證（High Risk / First Verification）

- [x] ✅ **Task 1.1: 定義資料契約與序列化實體**
  - **對應驗收**：`@TFACE-002`, `@TFACE-006`, `@TFACE-010`
  - **前置相依**：無
  - **產出檔案**：`internal/pipeline/types.go`, `internal/pipeline/types_test.go`
  - **實作內容**：
    1. 定義 `ExecutionMode`（`direct`, `three-face`）。
    2. 定義 `OraclePlan` 結構體與 JSON 序列化。
    3. 定義 `CriticReview` 結構體（含 `Verdict` 與 `Findings`）與序列化。
    4. 定義 `PipelineTrace`、`StageTrace`、`StageStatus`。
  - **完成判準**：單元測試驗證所有結構體 JSON 序列化與反序列化 100% 正確，無資料遺失。
  - **驗證命令**：`go test -v ./internal/pipeline/...`
  - **完成證據**：`internal/pipeline/types_test.go` 通過 `TestExecutionMode`, `TestOraclePlanSerialization`, `TestCriticReviewSerialization`, `TestPipelineTraceSerialization`。

- [x] ✅ **Task 1.2: 建立外迴圈調度與 Fail-Closed 行為測試（首要驗證）**
  - **對應驗收**：`@TFACE-001`, `@TFACE-004`, `@TFACE-005`, `@TFACE-007`
  - **前置相依**：Task 1.1
  - **產出檔案**：`internal/pipeline/orchestrator_test.go`
  - **實作內容**：
    1. 撰寫固定執行順序測試（Oracle → Striker → Critic），驗證各 Stage 僅被調用一次。
    2. 撰寫 Oracle 失敗測試：驗證 Striker 與 Critic 均不啟動，整體回傳 Failed。
    3. 撰寫 Striker 失敗測試：驗證 Critic 不啟動，不自動重試，保存 partial trace。
    4. 撰寫 Critic 失敗測試：驗證不偽裝成成功，保存 Striker output 並明確標註 Review 失敗。
  - **完成判準**：紅燈測試先行（TDD Red），明確刻畫各 stage fail-closed 邊界條件。
  - **驗證命令**：`go test -v -run TestOrchestrator ./internal/pipeline/...`
  - **完成證據**：`internal/pipeline/orchestrator_test.go` 建立完成，紅燈如期攔截未實作之 `NewOrchestrator`，定義完整外迴圈驗收標準。

---

### Phase 2：Pipeline 調度器與 Stage 核心實作

- [x] ✅ **Task 2.1: 實作 PipelineOrchestrator 與 Stage 介面**
  - **對應驗收**：`@TFACE-001`, `@TFACE-004`, `@TFACE-005`, `@TFACE-007`
  - **前置相依**：Task 1.2
  - **產出檔案**：`internal/pipeline/orchestrator.go`
  - **實作內容**：
    1. 定義 `Stage` 介面（`OracleStage`, `StrikerStage`, `CriticStage`）。
    2. 實作 `PipelineOrchestrator`，協調 Stage 依序執行並組裝 `PipelineTrace`。
    3. 實作嚴格的 Fail-Closed 終止邏輯與耗時/呼叫次數記錄。
  - **完成判準**：Task 1.2 建立之外迴圈測試由紅燈轉為綠燈（TDD Green）。
  - **驗證命令**：`go test -v ./internal/pipeline/...`
  - **完成證據**：`internal/pipeline/orchestrator.go` 實作完成，外迴圈測試（順序控制、Oracle 失敗中斷、Striker 失敗中斷、Critic 失敗不假裝成功、Direct 模式）全數通過（PASS）。

- [x] ✅ **Task 2.2: 實作 Oracle Stage 與 Critic Stage**
  - **對應驗收**：`@TFACE-002`, `@TFACE-006`
  - **前置相依**：Task 2.1
  - **產出檔案**：`internal/pipeline/stage_oracle.go`, `internal/pipeline/stage_critic.go`
  - **實作內容**：
    1. 實作 `OracleStage`：接收 Sealed `ExecutionContext`，調用模型產生結構化 `OraclePlan`。
    2. 實作 `CriticStage`：接收 `ExecutionContext`、`OraclePlan` 與 Striker 產出，調用模型產生結構化 `CriticReview`。
    3. 提供可注入模型執行器函式介面（與測試 mock 介面相容）。
  - **完成判準**：單元測試驗證 Oracle 產生結構化 Plan 與 Critic 產出 Review 的正確性。
  - **驗證命令**：`go test -v -run "TestOracle|TestCritic" ./internal/pipeline/...`
  - **完成證據**：`internal/pipeline/stage_test.go` 通過 `TestOracleStage_Success`, `TestOracleStage_MarkdownJSON`, `TestOracleStage_RunnerError`, `TestCriticStage_Success`, `TestCriticStage_MarkdownJSON`, `TestCriticStage_RunnerError`。

- [x] ✅ **Task 2.3: 封裝 Striker Stage 與共用 Sealed ExecutionContext**
  - **對應驗收**：`@TFACE-003`, `@TFACE-008`
  - **前置相依**：Task 2.1
  - **產出檔案**：`internal/pipeline/stage_striker.go`
  - **實作內容**：
    1. 將既有 `RunnerFunc` 包裝為 `StrikerStage`。
    2. 將 `ExecutionContext` 與 `OraclePlan` 組裝為 Striker 提示詞。
    3. 確保三個 Stage 嚴格讀取同一個 Sealed Context，不重新解析 Memory。
  - **完成判準**：單元測試驗證 Striker 接收之 Prompt 正確融入 OraclePlan，且記憶參照保持不變。
  - **驗證命令**：`go test -v -run TestStriker ./internal/pipeline/...`
  - **完成證據**：`internal/pipeline/stage_striker_test.go` 通過 `TestStrikerStage_DirectMode_WithoutPlan`, `TestStrikerStage_ThreeFace_WithPlan`, `TestStrikerStage_RunnerError`。全專案 `go test ./internal/... ./cmd/...` 通過。

---

### Phase 3：儲存層持久化與 Engine 整合

- [x] ✅ **Task 3.1: 儲存層 tasks 表擴充 execution_mode 與 pipeline_trace**
  - **對應驗收**：`@TFACE-001`, `@TFACE-002`, `@TFACE-006`
  - **前置相依**：Task 1.1
  - **產出檔案**：`internal/storage/sqlite.go`, `internal/storage/sqlite_test.go`
  - **實作內容**：
    1. 在 `storage.Task` 結構體新增 `ExecutionMode` 與 `PipelineTrace` 欄位。
    2. 更新 `initSchema`，自動 migration 新增欄位。
    3. 擴充 `CreateTask`、`GetTask`、`GetTaskByRequestID` 支援新欄位之讀寫。
    4. 新增 `UpdateTaskPipelineTrace` 支援管線審計追蹤單獨更新。
  - **完成判準**：SQLite 遷移測試通過，新舊資料相容無損。
  - **驗證命令**：`go test -v ./internal/storage/...`
  - **完成證據**：`internal/storage/sqlite_test.go` 新增 `TestTaskPipelineTraceStorage` 通過。全套 storage 測試全綠（PASS）。

- [x] ✅ **Task 3.2: 整合 Engine 與 Pipeline 調度**
  - **對應驗收**：`@TFACE-001`, `@TFACE-008`
  - **前置相依**：Task 2.3, Task 3.1
  - **產出檔案**：`internal/engine/engine.go`, `internal/engine/engine_test.go`
  - **實作內容**：
    1. 定義 `PipelineRunner` 介面，避免 engine 與 pipeline 產生循環依賴。
    2. `SubmitRequest` 擴充 `Mode` 欄位。
    3. `Engine.SubmitTask` 依據 `ExecutionMode` 判斷模式：Direct 模式維持既有路徑並自動補齊 trace；Three-Face 模式調用 `PipelineRunner`。
    4. 將最終 `PipelineTrace` 與結果安全持久化至儲存層。
  - **完成判準**：Direct 模式與 Three-Face 模式均能正確由 Engine 執行並持久化終態。
  - **驗證命令**：`go test -v ./internal/engine/...`
  - **完成證據**：`internal/engine/engine_test.go` 新增 `TestEngine_ThreeFacePipeline`, `TestEngine_ThreeFacePipeline_Failure`, `TestEngine_DirectModeTrace` 全數通過。全套 engine 測試 PASS。

---

### Phase 4：Daemon 調度、Redelivery 防禦與 Metrics 收集

- [x] ✅ **Task 4.1: Daemon IPC 擴充與 Redelivery 冪等防禦**
  - **對應驗收**：`@TFACE-009`
  - **前置相依**：Task 3.2
  - **產出檔案**：`internal/daemon/daemon.go`, `internal/daemon/daemon_test.go`
  - **實作內容**：
    1. `IPCRequest` 擴充 `Mode` 欄位。
    2. 將 `Mode` 納入請求 Fingerprint 計算，防範不同 mode 碰撞與錯置。
    3. 同 Request ID 且同 payload 重送時直接回傳已持久化之 Task（含 trace），不重複執行任何 Stage。
    4. 實作 `dispatchTaskWithMode` 保留 `dispatchTask` 向後相容性。
    5. `NewDaemon` 自動配置預設之 `PipelineOrchestrator`。
  - **完成判準**：Redelivery 測試通過，確認無二次模型調用與副作用。
  - **驗證命令**：`go test -v -run TestThreeFaceRedelivery ./internal/daemon/...`
  - **完成證據**：`internal/daemon/daemon_test.go` 新增 `TestThreeFaceRedelivery` 通過，確認同 RequestID + Mode 重送不重跑、不同 Mode 重送安全拒絕。全套 daemon 測試 PASS。

- [x] ✅ **Task 4.2: Pipeline 耗時與模型呼叫計數指標**
  - **對應驗收**：`@TFACE-010`
  - **前置相依**：Task 2.1, Task 4.1
  - **產出檔案**：`internal/pipeline/orchestrator.go`, `internal/daemon/metrics_test.go`
  - **實作內容**：
    1. 精確計算各 stage 耗時與 pipeline 總耗時。
    2. 記錄實際調用之 stage call count（Direct 為 1，Three-Face 成功為 3）。
    3. 查詢 Task 時可於 Trace 中完整取得 Metrics。
  - **完成判準**：測試比對 Direct 與 Three-Face 之 Stage 數量與呼叫計數。
  - **驗證命令**：`go test -v -run TestPipelineMetrics ./internal/daemon/...`
  - **完成證據**：`internal/daemon/metrics_test.go` 建立完成並通過 `TestPipelineMetrics`，驗證 Direct（1 stage, 1 call）與 Three-Face（3 stages: oracle, striker, critic, 3 calls, duration_ms > 0）各指標完全正確。

---

### Phase 5：CLI 整合、端到端整合測試與回歸驗證

- [x] ✅ **Task 5.1: CLI 參數擴充與指令支援**
  - **對應驗收**：`@TFACE-001`, `@TFACE-008`
  - **前置相依**：Task 4.1
  - **產出檔案**：`cmd/asura/run.go`, `cmd/asura/ask.go`, `cmd/asura/status.go`, `cmd/asura/run_test.go`
  - **實作內容**：
    1. `asura run` 與 `asura ask` 支援 `--mode` 參數（預設 `direct`，可指定 `three-face`）。
    2. CLI status 查詢若有 `pipeline_trace` 則格式化輸出各 Stage 執行狀態。
  - **完成判準**：CLI 測試覆蓋參數解析與分派。
  - **驗證命令**：`go test -v ./cmd/asura/...`

- [ ] ⏳ **Task 5.2: 全套 Gherkin 端到端整合測試**
  - **對應驗收**：`@TFACE-001` ~ `@TFACE-010`
  - **前置相依**：Task 5.1
  - **產出檔案**：`tests/integration/three_face_pipeline_test.go`
  - **實作內容**：
    1. 端到端驗證全部 10 個 Gherkin Scenario。
    2. 驗證 Direct 模式向後相容性與 Three-Face 模式完整性。
  - **完成判準**：整合測試全數通過（All Green）。
  - **驗證命令**：`go test -v ./tests/integration/...`

- [ ] 📝 **Task 5.3: 全系統回歸測試與文件收尾**
  - **對應驗收**：全體驗收條件
  - **前置相依**：Task 5.2
  - **產出檔案**：根目錄 `README.md`
  - **實作內容**：
    1. 執行專案完整測試套件，確保無任何既有測試破壞。
    2. 更新根目錄 `README.md` 常青文件（新增 `--mode` 參數說明與 Three-Face 介紹）。
  - **完成判準**：`go test -race ./...` 全部通過。
  - **驗證命令**：`go test -race ./...`
