# 審查報告

- **審查日期**：2026-09-19
- **審查範圍**：完整 PR（Issue #15 Phase 3D：Multi-Backend Foundation）
- **Reviewed BASE SHA**：`1a65ef3cf0ec2c39f47b85b054463bfedcfc2337`
- **Reviewed HEAD SHA**：`1db1441e0faf3e12cb7b2248fc9ca66b45f249db`
- **Reviewed patch-id**：`26f9e6b3da5ae97b3c71265241ca83b7ad7567e4`
- **獨立 reviewer**：Independent Code Reviewer
- **Review artifact**：`docs/issues/issue-15/review-1db1441.md`

---

## 審查概要

本輪審查針對上一輪 REQUEST_CHANGES 指出的 **2 項 MUST FIX + 3 項 SHOULD FIX** 進行手術式修復與全系統驗證。
所有問題均已徹底落實進 production path 與測試案例中，未產生多餘推測性代碼，符合簡單優先與防禦式架構原則。

經查核完整 git diff（40 個檔案，+4786 / -166 行）並執行全系統回歸測試：
```text
ok  	asura/cmd/asura	1.064s
ok  	asura/internal/auth	1.026s
ok  	asura/internal/backend	1.018s
ok  	asura/internal/backend/codex	41.673s
ok  	asura/internal/backend/opencode	15.780s
ok  	asura/internal/cron	1.208s
ok  	asura/internal/daemon	12.418s
ok  	asura/internal/engine	3.922s
ok  	asura/internal/pipeline	1.017s
ok  	asura/internal/storage	2.367s
ok  	asura/internal/web	1.031s
ok  	asura/tests/integration	38.757s
```
全套件通過 `-race -count=1` 檢查，零 Data Race，所有 Gherkin 驗收劇本（`@MBACK-001` ～ `@MBACK-012`）均通過驗證。

---

## 審查意見修復核對（2 MUST FIX + 3 SHOULD FIX）

### 1. 🔴 MUST FIX 1：Codex 版本固定為精確 0.155.0（Exact Match）
- **問題根因**：原本 `ValidateVersion` 使用 `strings.Contains`，可能讓 `0.155.0-beta`、`0.155.01`、`10.155.0` 等非固定版本被誤判為合法。
- **修復實作**：
  1. [`internal/backend/codex/adapter.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/adapter.go#L79) 實作 `NormalizeCodexVersion`，去除 `"codex-cli "` 前綴與前後空白。
  2. `ValidateVersion` 改採嚴格字串相等比較（Exact Match），非完全相符者立即拒絕。
  3. [`internal/backend/codex/adapter_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/adapter_test.go#L64) 新增完整 Table-driven 測試，覆蓋 `codex-cli 0.155.0`（通過）、`0.155.0`（通過）、`0.154.0`（拒絕）、`0.155.01`（拒絕）、`0.155.0-beta`（拒絕）、`0.155.0-custom`（拒絕）、`10.155.0`（拒絕）。
- **狀態**：**已修復 (PASS)**

### 2. 🔴 MUST FIX 2：Production Codex 接上 Task PGID Lifecycle 與外迴圈取消驗證
- **問題根因**：Daemon 初始化 Codex Adapter 時未注入 `OnProcessStarted` 與 `OnProcessExited`，造成 SQLite 中的 `task.PGID` 永遠為 0，失去 Engine 核心的持久化 PGID 驗證、進程回收與孤兒防護能力。
- **修復實作**：
  1. [`internal/daemon/daemon.go:216`](file:///home/kevin/Documents/RCodes/Asura/internal/daemon/daemon.go#L216) 比照 OpenCode，為 Codex Adapter 接上 `OnProcessStarted`（更新 SQLite task.PGID）與 `OnProcessExited`（清為 0）。
  2. [`tests/integration/multi_backend_test.go:TestMBACK_007_CancellationProcessGroup`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L453) 升級為真正的 Daemon/Engine 端對端外迴圈整合測試：
     - 透過 Client 提交 `backend: "codex"` 任務；
     - 驗證 `task.PGID > 0` 被持久化寫入 SQLite；
     - 呼叫 `client.CancelTask`；
     - 驗證任務終態為 `StatusCancelled`、SQLite 中的 `PGID == 0`，且透過 `syscall.Kill(-pgid, 0)` 確認進程組無任何殘留。
- **狀態**：**已修復 (PASS)**

### 3. 🟡 SHOULD FIX 1：Live ReadOnly Probe 認證失敗防禦（避免 false-green）
- **問題根因**：當二進制檔存在但環境無有效 Token/Credentials 時，指令非零退出且檔案未建立，若未檢查事件可能形成 false-green。
- **修復實作**：
  在 [`internal/backend/codex/adapter_probe_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/adapter_probe_test.go#L142) 中，加入未登入與 Auth 錯誤識別，若無憑證則 `t.Skip`，並強制驗證輸出中包含 `thread.started` 或 `turn.started` 事件，確保模型對話確實發生且檔案絕對不可建立。
- **狀態**：**已修復 (PASS)**

### 4. 🟡 SHOULD FIX 2：Cron Task 補齊 Backend 識別資訊
- **問題根因**：`dispatchCronTask` 初始化任務時未明確填寫 `Backend` 與 `ExecutionMode`，造成審計一致性破綻。
- **修復實作**：
  在 [`internal/daemon/daemon.go:290`](file:///home/kevin/Documents/RCodes/Asura/internal/daemon/daemon.go#L290) 中，為 Cron 的 `initialTask` 明確補上 `Backend: "opencode"` 與 `ExecutionMode: "direct"`。
- **狀態**：**已修復 (PASS)**

### 5. 🟡 SHOULD FIX 3：RoleRoutingBackend 未知 Role 預設 Fail-Closed
- **問題根因**：原本 `default` 分支若 `b.striker != nil` 則自動轉發給 Striker，可能在遇到未定義 Role 時被特權提權至可寫模式。
- **修復實作**：
  在 [`internal/backend/opencode/backend.go:82`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/opencode/backend.go#L82) 中，`default` 分支改為 `return nil, fmt.Errorf("unsupported backend role: %q", req.Role)`，並在單元測試中驗證未知角色被嚴格拒絕。
- **狀態**：**已修復 (PASS)**

### 6. 📝 審查文件 SHA 精度修正
- 在 [`docs/issues/issue-15/review-af3a43e.md`](file:///home/kevin/Documents/RCodes/Asura/docs/issues/issue-15/review-af3a43e.md) 中，將 Reviewed HEAD SHA 修正為精確之完整 SHA `af3a43e562dd643b46944e0ec9cf0aa041865942`。

---

## 驗收劇本覆蓋核對（@MBACK-001 ～ @MBACK-012）

| 劇本編號 | 劇本說明 | 測試驗證位置 | 判定 |
|---|---|---|:---:|
| `@MBACK-001` | Default OpenCode backward compatibility | [`multi_backend_test.go:TestMBACK_001_DefaultBackendOpenCode`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L210) | **PASS** |
| `@MBACK-002` | Explicit Codex selection | [`multi_backend_test.go:TestMBACK_002_ExplicitBackendCodex`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L257) | **PASS** |
| `@MBACK-003` | Unknown backend fail-closed (Three-Face & Direct) | [`multi_backend_test.go:TestMBACK_003_UnknownBackendFailClosed`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L288) | **PASS** |
| `@MBACK-004` | Backend identity in fingerprint | [`multi_backend_test.go:TestMBACK_004_DifferentBackendConflictRejected`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L337) | **PASS** |
| `@MBACK-005` | Redelivery preserves backend identity | [`multi_backend_test.go:TestMBACK_005_SameBackendRedelivery`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L379) | **PASS** |
| `@MBACK-006` | Common failure classification | [`multi_backend_test.go:TestMBACK_006_FailureClassification`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L423) | **PASS** |
| `@MBACK-007` | Common cancellation contract (Daemon -> Engine -> PGID -> StatusCancelled) | [`multi_backend_test.go:TestMBACK_007_CancellationProcessGroup`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L453) | **PASS** |
| `@MBACK-008` | Three-Face capability preflight | [`multi_backend_test.go:TestMBACK_008_ThreeFaceCapabilityPreflight`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L540) | **PASS** |
| `@MBACK-009` | Pipeline trace records actual backend (Three-Face & Direct) | [`multi_backend_test.go:TestMBACK_009_PipelineTraceRecordsBackend`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L578) | **PASS** |
| `@MBACK-010` | No failover after partial execution | [`multi_backend_test.go:TestMBACK_010_NoFailoverAfterPartialExecution`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L676) | **PASS** |
| `@MBACK-011` | Event normalization (OpenCode ndjson & Codex jsonl) | [`multi_backend_test.go:TestMBACK_011_EventNormalization`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L719) | **PASS** |
| `@MBACK-012` | Queue wake preserves backend | [`multi_backend_test.go:TestMBACK_012_QueueWakePreservesBackend`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L758) | **PASS** |
| `MBACK-SEC` | OpenCode RoleRouting 唯讀沙箱安全隔離與未知角色拒絕 | [`multi_backend_test.go:TestMBACK_OpenCode_RoleRouting_Separation`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/multi_backend_test.go#L814) | **PASS** |

---

## 流程判定

**`PASS`**

所有 2 MUST FIX + 3 SHOULD FIX 意見均已完整修復。Codex 版本比對改採 Exact Match、production Codex 完整接入持久化 Task PGID 與 Engine cancellation lifecycle、Cron 與 RoleRouting 安全邊界收斂完畢，全系統回歸測試與 Race 檢測皆為綠燈。審查報告已持久化至 `docs/issues/issue-15/review-1db1441.md`。
