# 程式碼審查報告 (Issue #9: 專案級基礎記憶與取消契約健全化 - 終態競態防禦)

## 變更摘要
本次審查涵蓋 Issue #9（專案級基礎記憶）之整體實作，以及針對 PR #10 第五輪審查回饋中提出之 1 項 MUST FIX（終態覆寫線性化競態）與 1 項 SHOULD FIX（Done Callback 失敗防禦）之完整修復。主要變更包含：
1. **防禦已提交終態之線性化競態 (PR #10 第五輪 MUST FIX)**：
   - 修復缺陷：`SubmitTask()` 在執行 `UpdateTaskStatusIfRunning(..., StatusCompleted, ...)` 持久化終態後，其 `e.cancels` 與 `e.doneChs` 註冊表是待函式返回時由 defer 清理。若 `CancelTask()` 於此極窄窗口內並發調用，原先檢查條件 `!exists && task.PGID <= 0` 會因 `exists == true` 漏攔，並以無條件 `UpdateTaskStatus` 將已完成任務倒轉覆寫為 `cancelling` -> `cancelled`。
   - 解決方案：
     - 在 `internal/engine/cancel.go` 入口處加入保護：當 `task.PGID <= 0` 且資料庫狀態已為 `StatusCompleted` 或 `StatusFailed` 時，即使 registration 尚未清除，亦嚴格拒絕取消並回傳 `ErrTaskNotRunning`。
     - 狀態轉移統一採用原子條件式 CAS (`UpdateTaskStatusIfRunning`)：`running` 透過 CAS 分流至 `completed`/`failed`（SubmitTask 贏）或 `cancelling`（CancelTask 贏）。CAS 失敗時回讀資料庫最新狀態，若已為終態且 `PGID <= 0` 則立即返回 `ErrTaskNotRunning`，徹底消滅競態覆寫。
   - 測試覆蓋：`internal/engine/cancel_test.go` 新增 `TestEngineCancel_DoesNotOverwriteCommittedTerminalState`（table-driven 測試 `completed` 與 `failed` 終態）。在模擬 active registration 未清理的並發情境下，修復前精準紅燈（逾時失敗），修復後 0.01s 綠燈通過，確保狀態絕不被覆寫。
2. **Daemon Done Callback Fail-Closed 保護 (PR #10 第五輪 SHOULD FIX)**：
   - 修復缺陷：`internal/daemon/daemon.go` 中的 `dispatchTask` 與 `dispatchQueuedTask` 在 worker 結束時呼叫 `store.GetTask(context.Background(), taskID)`，原先僅在 `err == nil && task.Status == storage.StatusCancelling` 時返回，若查詢資料庫發生錯誤或 task 為 nil，會錯誤地放行至 `releaseWorkspaceAndWakeNext` 提前釋放鎖。
   - 解決方案：將防禦條件改為 Fail-Closed 策略：`if err != nil || task == nil || task.Status == storage.StatusCancelling { return }`，在無法確認任務安全狀態時，絕不提前釋放專案互斥鎖。

## 審查版本
- Reviewed BASE SHA：`202f1b30e0c56aa087fd1c8f877c09bec1475019`
- Reviewed HEAD SHA：`9f8c9a6a764c77e0198fc5f7582d688160a99fed`
- Reviewed patch-id：`553011e133c220e4da88bc6ea9936613a25c1a44`
- 獨立 reviewer：Independent Reviewer (獨立審查員)
- Review artifact：`docs/issues/issue-9/review-9f8c9a6.md`
- 審查日期：2026-09-16

## 整體評估
本次審查給予 **PASS**。
PR #10 第五輪審查回饋提出之「防止 CancelTask 覆寫已提交之終態」MUST FIX 與「Done callback fail-closed」SHOULD FIX 已全面修復完成：
1. 取消狀態轉移與執行完成狀態轉移於 SQLite 資料庫層達到嚴格的互斥原子性，解決了記憶體註冊表清理延遲造成的線性化競態。
2. Daemon 層在非明確成功時堅持 fail-closed，專案互斥鎖生命週期健全性更上層樓。
3. 全專案 7 項 Gherkin 驗收劇本、單元測試、全套端到端整合測試與 `-race` 並發檢測全數綠燈。

## 需求與契約符合度
- 需求描述：Issue #9 專案級基礎記憶契約健全化及 PR #10 第五輪審查修復。
- 是否符合需求：**是**。
- 契約查核：
  - **MEM-001 ~ MEM-007 全驗收劇本**：記憶 CLI IPC、正規化專案路徑隔離、越界拒絕、非記憶管理零 AI 調用等行為皆保持完整相容。
  - **狀態轉移契約**：
    - `running -> completed / failed`（SubmitTask 提交）與 `running -> cancelling -> cancelled`（CancelTask 流程）互斥排他。
    - 已提交之終態（`completed` / `failed`）在無殘留進程組（`PGID <= 0`）時，不可被取消，必返回 `ErrTaskNotRunning`。
  - **並發安全**：無資料競爭，無孤兒鎖提前釋放。

## 測試真實性與驗證結果
- **測試命令與結果**：
  1. `go test -v -count=1 ./internal/engine -run '^TestEngineCancel_DoesNotOverwriteCommittedTerminalState$'`：PASS (0.01s)
  2. `go test -v -count=1 ./internal/engine`：PASS (2.31s)
  3. `go test -v -count=1 ./internal/daemon`：PASS (6.51s)
  4. `go test -v -count=1 ./internal/storage`：PASS (1.92s)
  5. `go test -race -count=1 ./internal/engine ./internal/storage ./internal/cron ./internal/daemon ./cmd/asura`：PASS（無任何 data race）
  6. `go test -v -count=1 ./tests/integration`：PASS（全套端到端整合測試通過，82.05s）
- **真實性查核**：
  - `TestEngineCancel_DoesNotOverwriteCommittedTerminalState` 具備真實失敗重現性（修復前 red，修復後 green）。
  - 測試無任何 `t.Skip` 或偽造條件。

## 結論
PR #10 第五輪審查要求之各項問題均已徹底解決，已具備完整合併條件。本獨立審查報告判定結果為 **PASS**。
