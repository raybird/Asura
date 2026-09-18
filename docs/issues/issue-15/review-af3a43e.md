# 審查報告

- **審查日期**：2026-09-18
- **審查範圍**：完整 PR（Issue #15 Phase 3D：Multi-Backend Foundation）
- **Reviewed BASE SHA**：`1a65ef3cf0ec2c39f47b85b054463bfedcfc2337`
- **Reviewed HEAD SHA**：`af3a43ea416eb74a694e82845a76e7a7f45fe44d`
- **Reviewed patch-id**：`5e8af3ea9c2c07226c1c53f503dfd274069041cd`
- **獨立 reviewer**：Independent Code Reviewer
- **Review artifact**：`docs/issues/issue-15/review-af3a43e.md`

---

## 審查概要

本次審查針對上一輪審查指出的 **4 項 MUST FIX + 2 項 SHOULD FIX** 進行嚴格修復查核。
在修正後的最新 HEAD `af3a43e` 中，所有架構安全邊界回退（OpenCode 唯讀權限退化、Direct 模式未走 Preflight 產生的 silent fallback、Codex 版本未鎖定、Probe false-green 等）均已完成修復並補齊對應自動化測試。

經查核完整 git diff（40 個檔案，+4701 / -164 行）並執行全系統回歸測試：
```text
ok  	asura/cmd/asura	1.076s
ok  	asura/internal/auth	1.016s
ok  	asura/internal/backend	1.025s
ok  	asura/internal/backend/codex	32.937s
ok  	asura/internal/backend/opencode	14.485s
ok  	asura/internal/cron	1.224s
ok  	asura/internal/daemon	12.409s
ok  	asura/internal/engine	3.841s
ok  	asura/internal/pipeline	1.017s
ok  	asura/internal/storage	2.535s
ok  	asura/internal/web	1.031s
ok  	asura/tests/integration	39.446s
```
全套件通過 `-race -count=1` 檢查，零 Data Race，所有 MBACK-001 ～ MBACK-012 驗收條件與邊界測試均 100% 通過。

---

## 審查意見修復核對（4 MUST FIX + 2 SHOULD FIX）

### 1. 🔴 MUST FIX 1：OpenCode 角色路由隔離（修復 Phase 3C 唯讀權限回退）
- **問題根因**：Daemon 原本直接將 `strikerAdapter` 註冊至 Registry，導致 Three-Face 模式下 Oracle 與 Critic 遺失了 Phase 3C 專屬 `asura-readonly` agent、isolated config 與唯讀權限控制。
- **修復實作**：
  1. 實作 [`internal/backend/opencode/backend.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/opencode/backend.go) 中的 `RoleRoutingBackend`，在單一 `"opencode"` 識別項下，將 `RoleOracle` 與 `RoleCritic` 嚴格路由至 `readOnlyAdapter`，`RoleStriker` 路由至 `strikerAdapter`。
  2. 在 [`internal/daemon/daemon.go`](file:///home/kevin/Documents/RCodes/Asura/internal/daemon/daemon.go#L210) 中註冊 `RoleRoutingBackend` 至 Registry。
- **驗證證據**：
  - 單元測試 [`internal/backend/opencode/backend_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/opencode/backend_test.go)：驗證角色路由與能力委派。
  - 整合測試 [`tests/integration/multi_backend_test.go:TestMBACK_OpenCode_RoleRouting_Separation`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L776)：斷言 Oracle 與 Critic 呼叫次數剛好為 2 次且全數進 `readOnlyAdapter`，Striker 剛好 1 次進 `strikerAdapter`。
- **狀態**：**已修復 (PASS)**

### 2. 🔴 MUST FIX 2 & 3：Direct 模式統一走 PipelineOrchestrator 與 Trace 保留
- **問題根因**：Direct 模式原本在 Engine 繞過 Orchestrator，造成未知後端 silent fallback 至 opencode（違反 MBACK-003），且無法產生記錄實際 `BackendID` 與 `Version` 的 `PipelineTrace`（違反 MBACK-009）。
- **修復實作**：
  1. [`internal/engine/engine.go:428`](file:///home/kevin/Documents/RCodes/Asura/internal/engine/engine.go#L428) 改為優先調用 `e.pipelineRunner.ExecutePipeline(taskCtx, "direct", execCtx)`。
  2. Direct 模式享有共同 Registry 解析、Preflight 能力檢查，未知後端 fail-closed（0 次模型呼叫）。
  3. Orchestrator 在 Direct 模式下產出包含 1 個 stage 的標準 `PipelineTrace`，並記錄實際 `BackendID` 與 `BackendVersion`。
- **驗證證據**：
  - 整合測試 [`tests/integration/multi_backend_test.go:TestMBACK_003_UnknownBackendFailClosed`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L288)：覆蓋 Direct 模式未知後端 fail-closed，0 模型呼叫，無 silent fallback。
  - 整合測試 [`tests/integration/multi_backend_test.go:TestMBACK_009_PipelineTraceRecordsBackend`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L532)：驗證 Direct 模式下 Trace 正確保存 BackendID 與 Version。
- **狀態**：**已修復 (PASS)**

### 3. 🔴 MUST FIX 4：Codex 0.155.0 Pinning 與版本相容性檢查
- **問題根因**：原本 Adapter 未強制驗證 CLI 版本，若外部環境版本漂移或 binary 不存在可能在未預期狀態下執行。
- **修復實作**：
  1. [`internal/backend/codex/adapter.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/adapter.go#L79) 實作 `ValidateVersion`，若版本不符 `ExpectedVersion`（預設 `0.155.0`）或找不到二進制檔，回傳 `ErrVersionMismatch`。
  2. `Run` 方法在啟動任何進程前執行 `ValidateVersion`，不符時立即返回 `FailureUnavailable`，0 執行進程調用。
- **驗證證據**：
  - 單元測試 [`internal/backend/codex/adapter_test.go:TestAdapter_VersionMismatch_FailClosed`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/adapter_test.go#L49)：驗證非 `0.155.0` 版本時 fail-closed 拒絕，0 執行調用。
- **狀態**：**已修復 (PASS)**

### 4. 🔴 MUST FIX 5：CI 與 Probe Gate 的 False-Green / CI 環境相容性問題
- **問題根因**：Probe 測試忽略 exit error 造成 false-green；且 live 測試若在未安裝 codex 的 CI 環境執行會中斷流程。
- **修復實作**：
  1. [`internal/backend/codex/adapter_probe_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/adapter_probe_test.go#L20) 實作 `requireLiveCodex(t, "0.155.0")`，所有 Probe 測試與 live 測試皆透過此 helper 進行守門，無合格 binary 時跳過，避免 CI false-red。
  2. 修復 `TestCodexProbe_ReadOnlySandbox`：斷言進程確實啟動並輸出日誌，嚴格檢查 forbidden.txt 未被建立，消除吞掉 error 的 false-green。
  3. 單元測試 [`internal/backend/codex/adapter_test.go:TestAdapter_Run_Cancellation`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/adapter_test.go#L92) 改用 hermetic fake script，不依賴 live codex 即可完整驗證 Context Cancel 與 PGID SIGKILL 回收。
- **狀態**：**已修復 (PASS)**

### 5. 🟡 SHOULD FIX 1：Capability Preflight 補齊 CapWorkspaceWrite 檢查
- **修復實作**：在 [`internal/pipeline/orchestrator.go:81`](file:///home/kevin/Documents/RCodes/Asura/internal/pipeline/orchestrator.go#L81) 中，無論 Direct 或 Three-Face 模式，皆強制要求後端具備 `CapWorkspaceWrite`；Three-Face 模式額外強制要求 `CapReadOnlySandbox`。
- **驗證證據**：全套件整合測試通過。
- **狀態**：**已修復 (PASS)**

### 6. 🟡 SHOULD FIX 2：Codex JSONL Parser 緩衝上限擴充與錯誤檢查
- **修復實作**：在 [`internal/backend/codex/parser.go:37`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/parser.go#L37) 中，將 `bufio.Scanner` 之緩衝上限擴充為 4MB（`maxTokenSize = 4 * 1024 * 1024`），並在迴圈結束後明確檢查 `scanner.Err()`，杜絕長文字截斷與靜默錯誤。
- **驗證證據**：單元測試與整合測試通過。
- **狀態**：**已修復 (PASS)**

---

## 驗收劇本覆蓋清單（@MBACK-001 ～ @MBACK-012）

| 劇本編號 | 劇本說明 | 測試驗證位置 | 判定 |
|---|---|---|:---:|
| `@MBACK-001` | Default OpenCode backward compatibility | [`tests/integration/multi_backend_test.go:TestMBACK_001_DefaultBackendOpenCode`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L210) | **PASS** |
| `@MBACK-002` | Explicit Codex selection | [`tests/integration/multi_backend_test.go:TestMBACK_002_ExplicitBackendCodex`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L257) | **PASS** |
| `@MBACK-003` | Unknown backend fail-closed (Three-Face & Direct) | [`tests/integration/multi_backend_test.go:TestMBACK_003_UnknownBackendFailClosed`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L288) | **PASS** |
| `@MBACK-004` | Backend identity in fingerprint | [`tests/integration/multi_backend_test.go:TestMBACK_004_DifferentBackendConflictRejected`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L337) | **PASS** |
| `@MBACK-005` | Redelivery preserves backend identity | [`tests/integration/multi_backend_test.go:TestMBACK_005_SameBackendRedelivery`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L379) | **PASS** |
| `@MBACK-006` | Common failure classification | [`tests/integration/multi_backend_test.go:TestMBACK_006_FailureClassification`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L423) | **PASS** |
| `@MBACK-007` | Common cancellation contract (PGID SIGKILL) | [`tests/integration/multi_backend_test.go:TestMBACK_007_CancellationProcessGroup`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L461) | **PASS** |
| `@MBACK-008` | Three-Face capability preflight | [`tests/integration/multi_backend_test.go:TestMBACK_008_ThreeFaceCapabilityPreflight`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L502) | **PASS** |
| `@MBACK-009` | Pipeline trace records actual backend (Three-Face & Direct) | [`tests/integration/multi_backend_test.go:TestMBACK_009_PipelineTraceRecordsBackend`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L540) | **PASS** |
| `@MBACK-010` | No failover after partial execution | [`tests/integration/multi_backend_test.go:TestMBACK_010_NoFailoverAfterPartialExecution`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L638) | **PASS** |
| `@MBACK-011` | Event normalization (OpenCode ndjson & Codex jsonl) | [`tests/integration/multi_backend_test.go:TestMBACK_011_EventNormalization`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L681) | **PASS** |
| `@MBACK-012` | Queue wake preserves backend | [`tests/integration/multi_backend_test.go:TestMBACK_012_QueueWakePreservesBackend`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L720) | **PASS** |
| `MBACK-SEC` | OpenCode RoleRouting 唯讀沙箱安全隔離 | [`tests/integration/multi_backend_test.go:TestMBACK_OpenCode_RoleRouting_Separation`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L776) | **PASS** |

---

## 流程判定

**`PASS`**

所有 4 MUST FIX + 2 SHOULD FIX 審查意見均已徹底修復，具備完備的單元與整合測試佐證，且無破壞既有 Phase 3C 唯讀沙箱邊界，全系統回歸測試通過。報告已持久化至 `docs/issues/issue-15/review-af3a43e.md`。
