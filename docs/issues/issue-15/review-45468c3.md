# 審查報告

- **審查日期**：2026-09-18
- **審查範圍**：完整 PR（Issue #15 Phase 3D：Multi-Backend Foundation）
- **Reviewed BASE SHA**：`1a65ef3cf0ec2c39f47b85b054463bfedcfc2337`
- **Reviewed HEAD SHA**：`45468c3552cd88adf0dfbd6e99a6f634a05ac871`
- **Reviewed patch-id**：`0a8a9063a5581d5deda9aba7de721dcf2520fbc8`
- **獨立 reviewer**：Independent Code Reviewer
- **Review artifact**：`docs/issues/issue-15/review-45468c3.md`

---

## 審查概要

本次提交完成了 Asura 的 Phase 3D「Multi-Backend Foundation（OpenCode + Codex CLI）」，在既有 Task、ExecutionContext、Pipeline Stage 架構之上，建立了共同 Backend 抽象契約（[`internal/backend`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/backend.go)），接入第二個真實後端 Codex CLI（固定版本 `codex-cli 0.155.0`），落實了 `--backend` 參數支援、Capability Preflight、No Automatic Failover、Request Fingerprint 與佇列喚醒保護。

經查核完整 git diff（35 個檔案，+4128 / -160 行）與執行全系統回歸測試（`go test -race -count=1 ./...`，全數 PASS），確認程式碼設計嚴謹、測試覆蓋完整，無任何資料競態（Data Race）或架構違背。

---

## 問題與風險

- **MUST FIX**：無。
- **SHOULD FIX**：無。
- **NICE TO HAVE**：
  1. **CLI 支援列表抽象化**：在 [`cmd/asura/ask.go`](file:///home/kevin/Documents/RCodes/Asura/cmd/asura/ask.go#L69-L74) 與 [`cmd/asura/run.go`](file:///home/kevin/Documents/RCodes/Asura/cmd/asura/run.go#L124-L129) 中，目前將支援後端寫死為 `opencode` 與 `codex`。未來若在 Phase 3E 新增其他後端，建議改由共同常數清單或向 `BackendRegistry` 動態驗證。
  2. **Codex 錯誤分類模式擴充**：[`internal/backend/codex/parser.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/parser.go#L140-L153) 的 `checkErrorLine` 目前採用常見關鍵字子字串匹配。未來若 Codex CLI 推出新版並提供特定結構化錯誤代碼，可進一步擴充匹配表。
  3. **二進制路徑查詢快取**：在 [`internal/backend/codex/adapter.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/adapter.go#L60-L67) 的 `Version` 方法中，未指定 `BinaryPath` 時會執行 `exec.LookPath("codex")`，未來可考慮快取解析結果以減少多餘的 PATH 搜尋。

---

## 已查核維度

### 1. 驗收與證據（Gherkin 劇本覆蓋核對）

| 劇本編號 | 劇本說明 | 測試驗證位置 | 判定 |
|---|---|---|:---:|
| `@MBACK-001` | Default OpenCode backward compatibility：未指定 backend 預設為 opencode，Direct 與 Three-Face 模式均相容，Task 與 Trace 記錄 BackendID 為 opencode，Codex 呼叫 0 次 | [`tests/integration/multi_backend_test.go:TestMBACK_001_DefaultBackendOpenCode`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L210)<br>[`cmd/asura/run_test.go:TestHandleAsk_BackendFlags`](file:///home/kevin/Documents/RCodes/Asura/cmd/asura/run_test.go#L359) | **PASS** |
| `@MBACK-002` | Explicit Codex selection：指定 `--backend codex` 僅呼叫 Codex adapter，OpenCode 呼叫 0 次，結果標準化為 `BackendResult` | [`tests/integration/multi_backend_test.go:TestMBACK_002_ExplicitBackendCodex`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L257)<br>[`internal/backend/codex/adapter_test.go:TestAdapter_Run_Live`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/adapter_test.go#L45) | **PASS** |
| `@MBACK-003` | Unknown backend fail-closed：指定未註冊後端在任何模型呼叫前 fail-closed 拒絕，0 進程與模型呼叫，絕不 fallback | [`tests/integration/multi_backend_test.go:TestMBACK_003_UnknownBackendFailClosed`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L288)<br>[`internal/pipeline/orchestrator_backend_test.go:TestOrchestrator_UnknownBackend_FailClosed`](file:///home/kevin/Documents/RCodes/Asura/internal/pipeline/orchestrator_backend_test.go#L47)<br>[`cmd/asura/run_test.go:TestRunCLI_UsageAndValidation`](file:///home/kevin/Documents/RCodes/Asura/cmd/asura/run_test.go#L126) | **PASS** |
| `@MBACK-004` | Backend identity in fingerprint：同 Request ID 不同 backend 判定為 payload 衝突拒絕，不執行新後端 | [`tests/integration/multi_backend_test.go:TestMBACK_004_DifferentBackendConflictRejected`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L319)<br>[`internal/daemon/daemon_test.go:TestDaemon_TaskFingerprint_MultiBackend`](file:///home/kevin/Documents/RCodes/Asura/internal/daemon/daemon_test.go#L2210) | **PASS** |
| `@MBACK-005` | Redelivery preserves backend identity：同 Request ID 相同 backend 重送返回既有 Task，0 重複調用 | [`tests/integration/multi_backend_test.go:TestMBACK_005_SameBackendRedelivery`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L360)<br>[`internal/daemon/daemon_test.go:TestDaemon_TaskFingerprint_MultiBackend`](file:///home/kevin/Documents/RCodes/Asura/internal/daemon/daemon_test.go#L2210) | **PASS** |
| `@MBACK-006` | Common failure classification：auth、rate limit、empty result 等錯誤正規化為 `FailureClassification`，保留 partial output | [`tests/integration/multi_backend_test.go:TestMBACK_006_FailureClassification`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L406)<br>[`internal/backend/codex/parser_test.go:TestCodexParser_Errors`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/parser_test.go#L43) | **PASS** |
| `@MBACK-007` | Common cancellation contract：Context 取消時發送 SIGKILL 至整組進程組（PGID），進程樹確實回收，Task 進入 StatusCancelled | [`tests/integration/multi_backend_test.go:TestMBACK_007_CancellationProcessGroup`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L437)<br>[`internal/backend/codex/adapter_test.go:TestAdapter_Run_Cancellation`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/adapter_test.go#L74)<br>[`internal/backend/codex/adapter_probe_test.go:TestCodexProbe_ContextCancellation`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/adapter_probe_test.go#L153) | **PASS** |
| `@MBACK-008` | Three-Face capability preflight：不具備 `CapReadOnlySandbox` 之弱後端在 Oracle 啟動前 fail-closed 終止，全 stage 0 模型調用，絕無 fallback | [`tests/integration/multi_backend_test.go:TestMBACK_008_ThreeFaceCapabilityPreflight`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L477)<br>[`internal/pipeline/orchestrator_backend_test.go:TestOrchestrator_ThreeFace_CapabilityPreflight_FailClosed`](file:///home/kevin/Documents/RCodes/Asura/internal/pipeline/orchestrator_backend_test.go#L83)<br>[`internal/backend/codex/adapter_probe_test.go:TestCodexProbe_ReadOnlySandbox`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/adapter_probe_test.go#L96) | **PASS** |
| `@MBACK-009` | Pipeline trace records actual backend：PipelineTrace 與各 stage trace 均完整記錄 actual BackendID 與 Version | [`tests/integration/multi_backend_test.go:TestMBACK_009_PipelineTraceRecordsBackend`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L516)<br>[`internal/pipeline/orchestrator_backend_test.go:TestOrchestrator_Trace_RecordsActualBackendAndVersion`](file:///home/kevin/Documents/RCodes/Asura/internal/pipeline/orchestrator_backend_test.go#L127) | **PASS** |
| `@MBACK-010` | No failover after partial execution：Striker 失敗時任務標記 Failed 並保存 partial output，OpenCode 呼叫次數嚴格為 0，絕不自動換後端重跑 | [`tests/integration/multi_backend_test.go:TestMBACK_010_NoFailoverAfterPartialExecution`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L562)<br>[`internal/pipeline/orchestrator_backend_test.go:TestOrchestrator_NoFailoverAfterPartialExecution`](file:///home/kevin/Documents/RCodes/Asura/internal/pipeline/orchestrator_backend_test.go#L173) | **PASS** |
| `@MBACK-011` | Event normalization：OpenCode ndjson 與 Codex jsonl 原生事件均正規化為標準 `BackendEvent` | [`tests/integration/multi_backend_test.go:TestMBACK_011_EventNormalization`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L605)<br>[`internal/backend/codex/parser_test.go:TestCodexParser_JSONL`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/parser_test.go#L12) | **PASS** |
| `@MBACK-012` | Queue wake preserves backend：Queued 任務喚醒時依 Task.Backend 調度執行，不重新套用預設值 opencode | [`tests/integration/multi_backend_test.go:TestMBACK_012_QueueWakePreservesBackend`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L644)<br>[`internal/daemon/daemon_test.go:TestDaemon_QueueWake_PreservesBackend`](file:///home/kevin/Documents/RCodes/Asura/internal/daemon/daemon_test.go#L2473) | **PASS** |

### 2. 相關失敗面（Negative & Boundary Cases 審查）

1. **未註冊或未知後端請求**：
   - *輸入／狀態*：呼叫端指定 `--backend unknown_backend`。
   - *預期行為*：CLI 參數檢查與 Orchestrator Capability Preflight 雙層 fail-closed 拒絕，不啟動任何進程，0 模型呼叫，絕不 fallback 至預設後端。
   - *現有覆蓋*：`TestRunCLI_UsageAndValidation`、`TestOrchestrator_UnknownBackend_FailClosed`、`TestMBACK_003_UnknownBackendFailClosed`。
   - *判定*：完全阻斷，符合安全預期。
2. **弱安全能力後端進入 Three-Face 模式**：
   - *輸入／狀態*：後端未宣告 `CapReadOnlySandbox` 卻要求執行 Three-Face 模式。
   - *預期行為*：Orchestrator 在進入 Oracle 前執行能力預檢，發現缺少唯讀沙箱立即返回 `ErrUnsupportedCapability` 終止，Oracle、Striker 與 Critic 調用數皆為 0。
   - *現有覆蓋*：`TestOrchestrator_ThreeFace_CapabilityPreflight_FailClosed`、`TestMBACK_008_ThreeFaceCapabilityPreflight`。
   - *判定*：完全阻斷，符合唯讀沙箱強制隔離防禦原則。
3. **部分階段執行失敗（Partial Execution Failure）**：
   - *輸入／狀態*：Striker 執行中崩潰或返回非零退出碼。
   - *預期行為*：任務終態標記為 Failed，保存已產生的 partial output，管線立即終止（不進入 Critic），且另一後端呼叫次數嚴格為 0（禁止 Silent Automatic Failover）。
   - *現有覆蓋*：`TestOrchestrator_NoFailoverAfterPartialExecution`、`TestMBACK_010_NoFailoverAfterPartialExecution`。
   - *判定*：完全阻斷，杜絕自動重跑可能帶來的非冪等副作用損害。
4. **同一 Request ID 惡意切換 Backend（Fingerprint 衝突）**：
   - *輸入／狀態*：請求端以相同 Request ID 提交任務，但意圖更換 BackendID。
   - *預期行為*：Fingerprint 因納入 BackendID 而不匹配，被判定為 Payload 衝突直接拒絕，既有任務不受影響。
   - *現有覆蓋*：`TestDaemon_TaskFingerprint_MultiBackend`、`TestMBACK_004_DifferentBackendConflictRejected`。
   - *判定*：完全符合冪等與防篡改設計。
5. **Context 取消時的進程組洩漏防禦**：
   - *輸入／狀態*：任務執行中 Client 主動 Cancel 或 Context 逾期。
   - *預期行為*：Adapter 啟動進程時配置獨立進程組（`Setpgid: true`），取消時向 `-pgid` 發送 `SIGKILL`，確保包含所有子進程（如 shell/python）被完全回收。
   - *現有覆蓋*：`TestCodexProbe_ContextCancellation`、`TestAdapter_Run_Cancellation`、`TestMBACK_007_CancellationProcessGroup`。
   - *判定*：進程樹確實清理回收，無孤兒進程殘留。

### 3. 需求、架構、安全、品質維度查核

- **架構與職責分離**：共同後端抽象定義於 [`internal/backend/backend.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/backend.go)，介面精簡聚焦（`ID`, `Version`, `Capabilities`, `HasCapability`, `Run`）。`InMemoryRegistry` 提供線程安全之後端實例註冊與查詢。Stage 與 Orchestrator 透過 Registry 解耦了對特定 Adapter 的直接依賴。
- **安全邊界保持**：OpenCode 原有唯讀沙箱防禦（Phase 3C 的 `CreateIsolatedReadOnlyConfigDir` 與環境變數隔離）在重構至共同契約後被完整保留；Codex CLI 亦嚴格透過 `-s read-only` 與 `--approve-for-me` 進行角色邊界隔離。
- **資料庫遷移與相容性**：[`internal/storage/sqlite.go`](file:///home/kevin/Documents/RCodes/Asura/internal/storage/sqlite.go#L209-L213) 針對既有 `tasks` 表透過 `ALTER TABLE` 新增 `backend TEXT NOT NULL DEFAULT 'opencode'` 欄位，讀取時具備向後相容預設值，升級平滑。
- **併發與品質**：全套件通過 `go test -race -count=1 ./...`，無 Data Race。

### 4. 豁免、待確認與限制

- **TBD-2（Codex CLI 憑證傳遞與模型配置）**：目前 Codex CLI 繼承 Daemon 執行環境之憑證與配置檔，已在 Phase 1 探測與外迴圈測試中驗證可行，不影響核心架構。
- **無任何豁免項目**。

---

## 流程判定

**`PASS`**

所有核准之驗收劇本（`@MBACK-001` ～ `@MBACK-012`）均具備嚴謹自動化測試與外迴圈整合測試，所有安全失敗面均通過驗證，無 MUST FIX 問題，報告已持久化。
