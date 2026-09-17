# 程式碼審查報告 (PR #14 / Issue #13)

## 變更摘要
針對 Phase 3C Three-Face Fixed Pipeline（Oracle → Striker → Critic）於 HEAD `26f72d5` 之實作進行第二輪獨立嚴格審查。本次異動範圍涵蓋管線調度核心、Stage 抽象與語意校驗、Daemon/Engine 雙層模式防禦、唯讀權限隔離、工作區鎖釋放協調、以及端到端外迴圈整合測試：
- `internal/pipeline/`：Orchestrator 固定順序排程；移除 Oracle/Critic 偽 fallback，落實 Fail-Closed 與 JSON 語意校驗；Critic 提示詞注入密封專案記憶。
- `internal/daemon/`：為 Oracle/Critic 注入獨立之 `readOnlyAdapter`（`DefaultReadOnlyPermissionPolicy`，`DisableAuto: true`），修改能力收斂至 Striker；修復 IPC Ingress 模式校驗防禦；修復 done callback 中的專案鎖外洩缺陷。
- `internal/engine/`：SubmitTask 與 switch default 雙層防禦未定義 ExecutionMode；檢查並傳播 `PipelineTrace` 持久化錯誤。
- `cmd/asura/`：CLI 在啟動前與進程組執行中防禦 `StatusCancelling`，避免誤啟動被取消之任務。
- `tests/integration/`：完整驗收 TFACE-001 ~ TFACE-010 端到端整合測試。

## 審查版本
- Reviewed BASE SHA：`799625c77ffb38e6e0c2302d407f20e38f22ea5c` (origin/main)
- Reviewed HEAD SHA：`26f72d50c1ed9ad778cc356dff7ee4839dd62b02` (issue-13-three-face-pipeline HEAD)
- 獨立 reviewer：Independent Code Reviewer (Subagent)
- Review artifact：`docs/issues/issue-13/review-26f72d5.md`
- Reviewed patch-id：`7bb5e641fa4d4ab4c59dd0c5f4b5c4dbce8e203e`
- 審查日期：2026-09-17

## 整體評估
本次審查針對前一輪提出的 4 項 MUST FIX 與 2 項 SHOULD FIX 進行重點覆核，並對全量程式碼變更與回歸測試執行全面檢視。
所有 MUST FIX 與 SHOULD FIX 均已澈底修復且具備嚴謹的單元/整合測試保護；全套測試（含 race detector 與全套外迴圈整合測試）100% 通過（全綠）。
各角色權限邊界劃分清晰，結構化契約真正落實 Fail-Closed，並發鎖釋放機制已排除洩漏風險。
本次審查判定為 **PASS**，無殘留 MUST FIX 項目，建議核准合併。

## 前輪覆核重點查核結果

| 編號 | 項目 | 查核位置 | 查核結論 |
|---|---|---|---|
| **MUST FIX 1** | Oracle / Critic 並沒有真正唯讀 | `internal/daemon/daemon.go:155-197` | **通過**。已建立獨立之 `readOnlyAdapter`，配置 `DefaultReadOnlyPermissionPolicy`（`{"*":"deny","read":"allow"}`）與 `DisableAuto: true`；工作區修改、建置與 auto 權限嚴格收斂至 Striker。 |
| **MUST FIX 2** | 號稱 Fail-Closed 但結構化輸出其實是 Fail-Open | `internal/pipeline/stage_oracle.go:49-64`<br>`internal/pipeline/stage_critic.go:50-62` | **通過**。已徹底移除假 fallback 物件與假 verdict；Oracle Plan 嚴格校驗 `Goal` 非空、`Steps` 非空且無空白步驟；Critic Review 嚴格校驗合法三種 Verdict（`pass`、`needs_attention`、`failed`），非法一律回報 error 中斷管線。 |
| **MUST FIX 3** | 非法 ExecutionMode 會 silent fallback 為 Direct | `internal/daemon/daemon.go:497-503`<br>`internal/engine/engine.go:207-212, 409-417` | **通過**。IPC Ingress 於進入點檢驗 `!pipeline.IsValidExecutionMode` 即刻拒絕；Engine.SubmitTask 與執行期 switch default 進行第二層防禦，未知 mode 一律 Fail-Closed 標記為 Failed 並回傳錯誤，杜絕任何 silent fallback。 |
| **MUST FIX 4** | CI 為紅（TestDaemon_ReleaseWorkspace_SymlinkTargetChanged_DoesNotLeak 失敗） | `internal/daemon/daemon.go:942-947`<br>`internal/daemon/daemon_test.go:1645-1715` | **通過**。修正 done callback 判斷，唯有 `err == nil && task != nil && task.Status == storage.StatusCancelling` 才保留給 CancelTask 解鎖，其餘情況一律釋放工作區鎖；測試引入原子狀態同步，杜絕競爭條件，全套測試全綠。 |
| **SHOULD FIX 1** | Critic 缺少 sealed Project Memories | `internal/pipeline/stage_critic.go:35` | **通過**。Critic 提示詞組裝已改用 `engine.ComposePrompt(execCtx.OriginalPrompt, execCtx)`，使審查階段具備與前兩階段完全一致的密封專案記憶視野。 |
| **SHOULD FIX 2** | PipelineTrace persistence error 被忽略 | `internal/engine/engine.go:460-466` | **通過**。已檢查 `store.UpdateTaskPipelineTrace` 之回傳錯誤，並在 `runErr == nil` 時提升為任務執行錯誤，確保審計持久化完整性。 |

## 需求符合度
- 需求描述：Phase 3C Three-Face Fixed Pipeline（Oracle → Striker → Critic）架構實作與邊界防禦。
- 是否符合需求：**是（完全符合）**。
- 缺漏或過度實作說明：無。嚴格恪守 YAGNI 原則，未過早引入動態複雜度路由、多模型自動容錯切換或 Critic 遞迴修復迴圈。Direct 模式保持 100% 向後相容。

## 問題與風險

嚴重度標籤說明：
- 🔴 **MUST FIX** — 邏輯錯誤、安全漏洞，必須在 merge 前修正
- 🟡 **SHOULD FIX** — 潛在問題、邊界未處理，建議修正
- 🟢 **NICE TO HAVE** — 可讀性、最佳實踐，可列入後續 backlog

### 🔴 MUST FIX
- **無**。

### 🟡 SHOULD FIX
- **無**。

### 🟢 NICE TO HAVE
1. **取消狀態下的 Partial PipelineTrace 補齊**
   - **位置**：`internal/engine/engine.go:432`
   - **現況**：任務被外部取消（Cancelled）時，Engine 會持久化 partial output 至 `tasks.result`，但未持久化當前已完成階段之 `PipelineTrace`。
   - **影響與建議**：不影響安全性與正確性。後續若需於 CLI 查核取消發生在哪一個 stage（Oracle 或 Striker），可考慮在取消路徑中補寫當前的 traceJSON。

## 驗收標準與測試證據
- **核准狀態覆核**：`docs/issues/issue-13/README.md` 核准表逐項查核，10 項 Scenario（@TFACE-001 ~ @TFACE-010）狀態均為 `已核准`（核准日期 2026-09-17），無任何殘留之「待核准」或「待重新核准」。
- **測試驗證全綠**：
  - `go test -race -count=1 ./...` 全數通過（含 `asura/tests/integration` 117.5s 真實 OpenCode 端到端執行）。
  - 外迴圈劇本測試 `go test -v -race -run "TestTFACE" ./tests/integration`（10/10 通過）。
  - 修復回歸測試 `go test -v -race -run "TestDaemon_ReleaseWorkspace_SymlinkTargetChanged_DoesNotLeak|TestDaemon_IPC_InvalidExecutionMode_Rejected|TestDaemon_ThreeFace_CapabilityIsolation" ./internal/daemon` 全部通過。
  - Engine 模式防禦測試 `go test -v -race -run "TestEngine_InvalidExecutionMode_FailClosed" ./internal/engine` 通過。
  - Pipeline 語意與 JSON 邊界測試 `go test -v -race -run "TestOracleStage|TestCriticStage" ./internal/pipeline` 全部通過。
- **假綠燈與同義反覆防範檢查**：
  - 所有測試斷言皆以獨立預期值檢驗（如比對呼叫順序陣列 `[oracle, striker, critic]`、真實調用次數計數器、資料庫中的實際狀態字串與 JSON 結構），未透過被測程式碼重算期望值，無假綠燈。
- **Gate 豁免查核**：**無**（未申請亦無任何豁免紀錄，所有驗收標準均為實質測試通過）。

## 破壞性邊界條件

### Edge Case 1：Oracle 輸出畸形 JSON 或空白 Steps / 缺失 Goal
- **輸入／狀態**：LLM 模型回傳純文字無 JSON、或回傳 `{"goal": "", "steps": ["step 1"]}`、或 `{"goal": "ok", "steps": []}`、或 `{"goal": "ok", "steps": ["  "]}`。
- **預期結果**：拒絕任何猜測性 fallback；立即 Fail-Closed 中斷管線，回傳明確之語意錯誤（如 `oracle plan missing goal`、`oracle plan steps cannot be empty`、`oracle plan step 1 is empty`），Striker 與 Critic 呼叫計數為 0，Task 標記為 Failed。
- **現有覆蓋**：`internal/pipeline/stage_oracle.go:49-64`；`internal/pipeline/stage_test.go:TestOracleStage_InvalidJSON_FailClosed`、`TestOracleStage_SemanticValidation_FailClosed`。
- **判定**：**已覆蓋且安全（通過）**。

### Edge Case 2：Critic 輸出未定義之 Verdict 或破損格式
- **輸入／狀態**：LLM 審查回傳非 JSON 字串、缺少 verdict 欄位、或傳回未定義字串（如 `"unknown"`, `"warning"`）。
- **預期結果**：拒絕自動降級為 needs_attention；Fail-Closed 拋出 `critic output invalid json` 或 `critic review invalid verdict`，管線標示為失敗，Striker 輸出保留但 Task 終態不得為 Success。
- **現有覆蓋**：`internal/pipeline/stage_critic.go:50-62`；`internal/pipeline/stage_test.go:TestCriticStage_InvalidJSON_FailClosed`、`TestCriticStage_InvalidVerdict_FailClosed`。
- **判定**：**已覆蓋且安全（通過）**。

### Edge Case 3：外部 Client 繞過 CLI 發送非法 ExecutionMode 注入
- **輸入／狀態**：外部客戶端直接透過 Unix Domain Socket 發送 `{"mode": "malicious_mode"}`，或資料庫任務欄位被異常寫入未定義字串。
- **預期結果**：
  1. Daemon IPC Ingress 在入口處透過 `!pipeline.IsValidExecutionMode` 檢驗拒絕，不予排程並返回錯誤。
  2. Engine SubmitTask 在第一層驗證模式，且執行期 switch default 觸發 Fail-Closed 將任務置為 Failed，杜絕 silent fallback 到 Direct 模式。
- **現有覆蓋**：`internal/daemon/daemon.go:497-503`、`internal/engine/engine.go:207-212, 409-417`；`internal/daemon/daemon_test.go:TestDaemon_IPC_InvalidExecutionMode_Rejected`、`internal/engine/engine_test.go:TestEngine_InvalidExecutionMode_FailClosed`。
- **判定**：**已覆蓋且安全（通過）**。

### Edge Case 4：Oracle 與 Critic 嘗試越權修改工作區或執行 Shell 指令
- **輸入／狀態**：惡意 Prompt 誘導 Oracle 或 Critic 呼叫工具進行檔案覆寫、刪除或執行 bash。
- **預期結果**：Daemon 配置之獨立 `readOnlyAdapter` 生效，`DefaultReadOnlyPermissionPolicy`（`{"*":"deny","read":"allow"}`）與 `DisableAuto: true` 嚴格阻擋任何寫入與自動執行能力，唯有 Striker 階段具備受管之工作區變更能力。
- **現有覆蓋**：`internal/daemon/daemon.go:155-197`；`internal/daemon/daemon_test.go:TestDaemon_ThreeFace_CapabilityIsolation`。
- **判定**：**已覆蓋且安全（通過）**。

### Edge Case 5：Striker 執行中任務被取消（CancelTask）時的專案鎖與進程防護
- **輸入／狀態**：Striker 階段被使用者發送 CancelTask 請求，任務狀態變更為 `StatusCancelling`，底層進程組收到中斷信號。
- **預期結果**：Orchestrator 不啟動 Critic；Daemon 的 done callback 偵測到 `StatusCancelling` 狀態時，絕不提前釋放 workspace lock，完整委託給 CancelTask 協調器進行進程組回收與安全解鎖，避免後續任務在舊進程尚未退場時發生競爭衝突。
- **現有覆蓋**：`internal/daemon/daemon.go:942-947`、`cmd/asura/run.go:192, 226, 250`；`tests/integration/three_face_pipeline_test.go:TestTFACE005_StrikerFailureNoCriticNoRetry`。
- **判定**：**已覆蓋且安全（通過）**。

## 架構符合度
- **分層邊界與依賴方向**：
  - `internal/engine` 定義了 `PipelineRunner` 抽象介面，`internal/pipeline` 提供 Orchestrator 實作並注入 Engine。兩者之間保持單向介面依賴，無循環依賴（import cycle）。
  - `internal/daemon` 作為裝配層，統一組裝 Adapter、Stages、Orchestrator 與 Engine，責任切分分明。
- **並發安全性**：
  - `PipelineOrchestrator` 本身為無狀態（Stateless），`PipelineTrace` 於每次執行時在局部棧上建立，支援多 Worker 同時並發調度，無共享狀態競爭。

## 安全性檢查
- **存取控制與授權**：Oracle 與 Critic 嚴格套用唯讀權限策略（`*":"deny","read":"allow"`），能力邊界完全收斂。
- **輸入驗證與防禦**：Daemon IPC Ingress 與 Engine 雙層檢驗 `ExecutionMode`；結構化輸出驗證嚴格拒絕非 JSON 與空白欄位，無 Fail-Open 漏洞。
- **敏感資料保護**：ExecutionContext 與專案記憶採密封快照機制，不隨外部異動而漂移，且不於 trace 中明文洩漏未授權資訊。

## 程式品質與效能
- 程式碼結構清晰，命名遵循 Go 語言標準慣例。
- 全量測試皆開啟 `-race` 檢驗，未偵測到任何 Data Race。
- Direct 模式與 Three-Face 模式指標（Stage 數量、呼叫次數、耗時）均可被量測與審計，無額外不必要的效能開銷。

## 改進建議
- 本次實作已相當完整，針對未來 Phase 4 多後端支援，可保持目前 `PipelineRunner` 與 Stages 介面抽象，使 Multi-Backend 替換更加平滑。

## 流程判定
### **`PASS`**
- 獨立 reviewer 已完成當前 HEAD（`26f72d5`）之全面審查。
- 0 個 MUST FIX、0 個 SHOULD FIX。
- 所有 10 項 Gherkin 驗收標準均具備真實通過證據且無假綠燈。
- 所有破壞性邊界條件均通過驗證。
- 報告已依規範持久化於 `docs/issues/issue-13/review-26f72d5.md`。
