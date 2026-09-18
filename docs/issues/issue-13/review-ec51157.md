# 程式碼審查報告 (PR #14 / Issue #13)

## 變更摘要
針對 Phase 3C Three-Face Fixed Pipeline（Oracle → Striker → Critic）於 HEAD `ec51157` 之實作進行第三輪增量與全量獨立嚴格審查。本次審查特別覆核相對於 `3d49816` 之 5 項增量修復項目，涵蓋 ReadOnly Adapter 顯式覆寫能力、唯讀分析白名單補齊、權限隔離與 deep merge 防禦測試、PipelineTrace 持久化錯誤處理（`errors.Join`）、以及工作區鎖無副作用輪詢機制（`IsLocked`）：
- `internal/daemon/daemon.go`：`DefaultReadOnlyPermissionPolicy` 顯式將 mutating / escape capability（`*`, `edit`, `write`, `patch`, `bash`, `task`, `skill`, `external_directory`, `webfetch`, `websearch`）設為 `deny`，並開放唯讀分析白名單（`read`, `glob`, `grep`, `list`, `lsp`）。
- `internal/daemon/workspace_queue.go`：新增 `IsLocked` 純狀態查詢方法，消除排隊輪詢之副作用。
- `internal/daemon/daemon_test.go`：強化 `TestDaemon_ThreeFace_CapabilityIsolation` 測試，驗證 JSON 合法性、分析白名單、副作用黑名單及 OpenCode deep merge 覆寫行為；改寫 `TestDaemon_ReleaseWorkspace_SymlinkTargetChanged_DoesNotLeak` 採用 `IsLocked` 輪詢。
- `internal/engine/engine.go`：`SubmitTask` 改用 `errors.Join(runErr, fmt.Errorf("persist pipeline trace failed: %w", traceErr))`，防範雙重失敗時持久化錯誤被丟棄。
- `internal/engine/engine_test.go`：新增 `TestEngine_PipelineTracePersistenceError_ErrorsJoin` 測試。
- `tests/integration/`：完整覆核 TFACE-001 ~ TFACE-010 端到端整合測試。

## 審查版本
- Reviewed BASE SHA：`799625c77ffb38e6e0c2302d407f20e38f22ea5c` (origin/main)
- Reviewed HEAD SHA：`ec51157a0fe1fa14e0607aa06675b74d3c5a1fee` (issue-13-three-face-pipeline HEAD)
- 短 SHA：`ec51157`
- 獨立 reviewer：Independent Code Reviewer (Subagent)
- Review artifact：`docs/issues/issue-13/review-ec51157.md`
- Reviewed patch-id：`bac5cdfc39c7ff4d7c3636d0c9f19d699b3f03a1`
- 審查日期：2026-09-17

## 整體評估
本次審查針對上一輪提出的 1 項 MUST FIX 與 4 項 SHOULD FIX 增量修復進行嚴格檢驗，並對全專案進行全套測試驗證（`go test -race -count=1 ./...`，含 10 項 TFACE 外迴圈端到端測試）。
所有修復項目均已精準落實：
1. `DefaultReadOnlyPermissionPolicy` 完整阻斷了 OpenCode deep merge 下外層 allow 逃逸的風險，同時補齊了 Oracle / Critic 分析所需的代碼探查與 LSP 能力。
2. `PipelineTrace` 持久化失敗透過 `errors.Join` 與管線執行錯誤完整合併，消除了審計失敗被靜默吞掉的漏洞。
3. `WorkspaceQueue.IsLocked` 實現了純讀取查詢，徹底消除了測試與外部輪詢對工作區隊列累積的副作用。
全套測試 100% 通過（全綠），無資料競爭（data race），權限邊界健全，Fail-Closed 語意嚴謹。
本次審查判定為 **PASS**，建議核准合併。

## 增量修復項目查核結果

| 編號 | 項目 | 查核位置 | 查核結論 |
|---|---|---|---|
| **MUST FIX** | ReadOnly Adapter 顯式覆寫 mutating / escape capabilities | `internal/daemon/daemon.go:39-41` | **通過**。`DefaultReadOnlyPermissionPolicy` 顯式將 `*`, `edit`, `write`, `patch`, `bash`, `task`, `skill`, `external_directory`, `webfetch`, `websearch` 全部設為 `deny`，並開放唯讀分析白名單（`read`, `glob`, `grep`, `list`, `lsp`）。 |
| **SHOULD FIX 1** | 補齊 Oracle / Critic 唯讀分析能力 | `internal/daemon/daemon.go:41` | **通過**。白名單納入 `read`, `glob`, `grep`, `list`, `lsp`，完全支援結構探索、grep 搜尋與語意分析，且無寫入或逃逸權限。 |
| **SHOULD FIX 2** | `TestDaemon_ThreeFace_CapabilityIsolation` 強化測試 | `internal/daemon/daemon_test.go:2122-2184` | **通過**。測試驗證了 JSON 解析合法性、白名單為 allow、黑名單為 deny，並模擬了 OpenCode deep merge，確認外層即使有 `allow` 也會被覆寫為 `deny`。 |
| **SHOULD FIX 3** | PipelineTrace 持久化錯誤處理 | `internal/engine/engine.go:463`<br>`internal/engine/engine_test.go:1278-1327` | **通過**。`engine.go` 改用 `errors.Join(runErr, fmt.Errorf("persist pipeline trace failed: %w", traceErr))`，`engine_test.go` 新增對應測試驗證底層錯誤與持久化錯誤皆被保留。 |
| **SHOULD FIX 4** | `TestDaemon_ReleaseWorkspace_SymlinkTargetChanged_DoesNotLeak` 移除副作用 polling | `internal/daemon/workspace_queue.go:97-104`<br>`internal/daemon/daemon_test.go:1698-1716` | **通過**。`WorkspaceQueue` 新增無副作用之 `IsLocked` 方法，測試改用 `IsLocked` 輪詢，釋放後單次執行 `TryAcquire`，避免隊列重複入隊。 |

## 需求符合度
- 需求描述：Phase 3C Three-Face Fixed Pipeline（Oracle → Striker → Critic）架構實作、安全權限邊界、Fail-Closed 與審計持久化。
- 是否符合需求：**是（完全符合）**。
- 缺漏或過度實作說明：無。嚴格遵循 YAGNI 原則，未加入未被要求的複雜路由機制；Direct 模式維持 100% 向後相容。

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
   - **影響與建議**：不影響安全性與正確性。後續若需於 CLI 查核取消發生在哪一個 stage，可考慮在取消路徑中補寫當前的 traceJSON。

## 驗收標準與測試證據
- **核准狀態覆核**：`docs/issues/issue-13/README.md` 核准表逐項查核，10 項 Scenario（@TFACE-001 ~ @TFACE-010）狀態均為 `已核准`（核准日期 2026-09-17），無任何殘留之「待核准」或「待重新核准」。
- **測試驗證全綠**：
  - `go test -race -count=1 ./...` 全數通過（含 `asura/tests/integration` 真實 OpenCode 端到端執行）。
  - 外迴圈劇本測試 `go test -v -race -run "TestTFACE" ./tests/integration`（10/10 通過）。
  - 增量修復測試：
    - `TestDaemon_ThreeFace_CapabilityIsolation`（通過）
    - `TestDaemon_ReleaseWorkspace_SymlinkTargetChanged_DoesNotLeak`（通過）
    - `TestEngine_PipelineTracePersistenceError_ErrorsJoin`（通過）
- **假綠燈與同義反覆防範檢查**：
  - 所有測試斷言皆以獨立預期值檢驗（如比對呼叫順序陣列 `[oracle, striker, critic]`、真實調用次數計數器、資料庫中的實際狀態字串與 JSON 結構），未透過被測程式碼重算期望值，無假綠燈。
- **Gate 豁免查核**：**無**（未申請亦無任何豁免紀錄，所有驗收標準均為實質測試通過）。

## 破壞性邊界條件

### Edge Case 1：使用者環境配置 `* : allow` 嘗試誘導 Oracle / Critic 寫入或外洩（Deep Merge 逃逸）
- **輸入／狀態**：使用者環境中的全域配置包含了 `{"*": "allow", "bash": "allow", "edit": "allow", "webfetch": "allow"}`。惡意 Prompt 嘗試誘導 Oracle 執行 `edit` 修改代碼或執行 `bash` 發送外網請求。
- **預期結果**：OpenCode 進行 deep merge 時，Daemon 注入的 `DefaultReadOnlyPermissionPolicy` 顯式將所有 mutating / escape capability（`*`, `edit`, `write`, `patch`, `bash`, `task`, `skill`, `external_directory`, `webfetch`, `websearch`）覆寫為 `deny`，因此即使外層為 `allow` 亦被強制阻擋；且 `DisableAuto: true` 禁止自動提權。Oracle 僅能使用 `read`, `glob`, `grep`, `list`, `lsp` 進行唯讀分析。
- **現有覆蓋**：`internal/daemon/daemon.go:39-41`；`internal/daemon/daemon_test.go:TestDaemon_ThreeFace_CapabilityIsolation`。
- **判定**：**已覆蓋且安全（通過）**。

### Edge Case 2：底層管線執行失敗且同時資料庫發生 I/O 故障（雙重失敗）
- **輸入／狀態**：Striker 執行發生嚴重錯誤（`runErr != nil`），Engine 嘗試持久化帶有失敗資訊的 `PipelineTrace` 至資料庫，但資料庫此時遭遇磁碟空間耗盡或已斷開連線（`traceErr != nil`）。
- **預期結果**：系統不丟棄任何一方的錯誤資訊；透過 `errors.Join(runErr, fmt.Errorf("persist pipeline trace failed: %w", traceErr))` 將兩者完整合併並向外傳播，Task 狀態標記為 Failed，呼叫端能透過 `errors.Is` 取得底層原因，同時審計系統能查核持久化失敗。
- **現有覆蓋**：`internal/engine/engine.go:463`；`internal/engine/engine_test.go:TestEngine_PipelineTracePersistenceError_ErrorsJoin`。
- **判定**：**已覆蓋且安全（通過）**。

### Edge Case 3：並發任務頻繁查詢與爭奪工作區鎖時的隊列污染
- **輸入／狀態**：多個任務在短時間內密集輪詢或等待同一個工作區的鎖定釋放。
- **預期結果**：輪詢端使用純狀態查詢 `WorkspaceQueue.IsLocked(workspace)`，不呼叫 `TryAcquire`，避免重複入隊污染等待計數與次序；鎖釋放時依互斥鎖安全移交給隊列頭部的任務，保障 FIFO 順序與無死鎖。
- **現有覆蓋**：`internal/daemon/workspace_queue.go:97-104`；`internal/daemon/daemon_test.go:TestDaemon_ReleaseWorkspace_SymlinkTargetChanged_DoesNotLeak`。
- **判定**：**已覆蓋且安全（通過）**。

### Edge Case 4：Critic LLM 輸出破損 JSON 或非法 Verdict
- **輸入／狀態**：LLM 審查回傳非 JSON 字串、缺少 verdict 欄位、或回傳非法字串（如 `"unknown"`, `"warning"`）。
- **預期結果**：拒絕自動降級或假裝成功；Fail-Closed 拋出 `critic output invalid json` 或 `critic review invalid verdict`，管線標示為失敗，Striker 輸出保留但 Task 終態不得為 Success。
- **現有覆蓋**：`internal/pipeline/stage_critic.go:50-62`；`internal/pipeline/stage_test.go:TestCriticStage_InvalidJSON_FailClosed`、`TestCriticStage_InvalidVerdict_FailClosed`。
- **判定**：**已覆蓋且安全（通過）**。

## 架構符合度
- **分層邊界與依賴方向**：
  - `internal/engine` 定義抽象介面 `PipelineRunner`，`internal/pipeline` 提供 Orchestrator 實作並注入 Engine。保持清晰單向依賴，無循環依賴。
  - `internal/daemon` 負責組裝 Adapter、Stage、Orchestrator 與 Engine。
- **並發安全性**：
  - `WorkspaceQueue.IsLocked` 與 `TryAcquire` 皆受 `q.mu` 互斥鎖完整保護。
  - `PipelineOrchestrator` 無狀態（Stateless），Trace 於每次調度於局部棧上建立，支援高並發。

## 安全性檢查
- **存取控制與授權**：Oracle 與 Critic 嚴格套用唯讀權限策略，明確阻斷所有寫入、建置、測試、外網與子任務生成能力，並防禦 deep merge 權限逃逸。
- **輸入驗證與防禦**：Daemon IPC Ingress 與 Engine 雙層防禦 `ExecutionMode`；結構化輸出嚴格校驗非空與有效列舉。
- **敏感資料保護**：ExecutionContext 與專案記憶採密封快照機制，保持任務執行上下文不變。

## 程式品質與效能
- 程式碼結構清晰，註解詳盡，命名符合 Go 慣例。
- 全量測試皆開啟 `-race` 檢驗，未偵測到任何 Data Race。
- Direct 模式與 Three-Face 模式指標均可量測與審計，無不必要的效能開銷。

## 改進建議
- 本次增量修復已徹底解決前輪所有安全與健壯性隱患。
- 建議在未來的 Phase 4 多後端支援時，維持目前 `PipelineRunner` 與 Stage 介面的正交性設計。

## 流程判定
### **`PASS`**
- 獨立 reviewer 已完成當前 HEAD（`ec51157`）之全量審查。
- 0 個 MUST FIX、0 個 SHOULD FIX。
- 所有 10 項 Gherkin 驗收標準均具備真實通過證據且無假綠燈。
- 所有破壞性邊界條件均通過驗證。
- 報告已依規範持久化於 `docs/issues/issue-13/review-ec51157.md`。
