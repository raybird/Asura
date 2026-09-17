# 程式碼審查報告 (Issue #9: 專案級基礎記憶與取消契約健全化 - 原子 CAS 狀態機重構)

## 變更摘要
本次審查涵蓋 Issue #9（專案級基礎記憶）之整體實作，以及針對 PR #10 第六輪審查回饋中提出之 1 項 MUST FIX（過期 pending/queued 快照繞過 CAS 覆寫終態之線性化競態）之完整修復。主要變更包含：
1. **取消狀態機全面 CAS 迴圈化 (PR #10 第六輪 MUST FIX)**：
   - 修復缺陷：原先 `CancelTask()` 於入口讀取任務狀態快照 `task, _ := e.store.GetTask(...)`，隨後依賴此初始快照判斷分支。若在 `CancelTask()` 讀取 pending 快照至獲取鎖繼續執行之時間窗口內，`SubmitTask()` 已搶先將任務推進為 `running` 甚至完成為 `completed`/`failed`，`CancelTask()` 會因持有舊快照進入無條件更新分支（`UpdateTaskStatus(..., StatusCancelled)` 或 `UpdateTaskStatus(..., StatusCancelling)`），導致已提交之終態被過期快照覆寫。
   - 解決方案：
     - 在 `internal/storage/sqlite.go` 新增原子 CAS 方法：
       - `UpdateTaskStatusIfPendingOrQueuedWithMsg`：僅在任務為 pending 或 queued 時轉移狀態並寫入訊息。
       - `UpdateTaskStatusIfCancelling`：僅在任務為 cancelling 時轉移狀態為 cancelled。
     - 將 `internal/engine/cancel.go` 中的 `CancelTask()` 重構為狀態轉移迴圈（State Machine Loop），將唯一 Linearization Point 鎖定在 SQLite：
       ```text
                        ┌→ completed
       running ─ CAS ───┼→ failed
                        └→ cancelling
       
       pending/queued ─ CAS → running
                     └ CAS → cancelled / cancelling
       ```
       - `pending/queued`：若無 runner 且 PGID <= 0，以 CAS 轉為 `cancelled`；若有 runner 或殘留 PGID，以 CAS 轉為 `cancelling`。任一 CAS 失敗立即 `continue` 重新取得最新快照重算。
       - `running`：以 CAS 轉為 `cancelling`，失敗立即 `continue` 重試。
       - `completed/failed`：PGID <= 0 立即返回 `ErrTaskNotRunning`；若有殘留 PGID 則進行清理並保留原終態。
       - 最終持久化：回收完成後，僅在任務仍為 `StatusCancelling` 時透過 `UpdateTaskStatusIfCancelling` 轉移為 `StatusCancelled`，絕不覆寫既有終態。
   - 測試覆蓋：新增 `internal/engine/cancel_test.go:TestEngineCancel_StalePendingSnapshotDoesNotOverwriteTerminalState`，包含 4 組場景（completed/failed x 無註冊/有註冊）。在模擬過期快照交錯下，修復前 100% 精準紅燈重現（誤判或逾時），修復後 0.15s 綠燈通過。

## 審查版本
- Reviewed BASE SHA：`202f1b30e0c56aa087fd1c8f877c09bec1475019`
- Reviewed HEAD SHA：`ec48ad34ae1c1ce3f043ef151af90f41d1a2aacc`
- Reviewed patch-id：`1287ab5b195bf48608ef87b0d55e242ced338824`
- 獨立 reviewer：Independent Reviewer (獨立審查員)
- Review artifact：`docs/issues/issue-9/review-ec48ad3.md`
- 審查日期：2026-09-16

## 整體評估
本次審查給予 **PASS**。
PR #10 第六輪審查回饋提出之「所有狀態轉移全面 CAS 化、消除過期快照覆寫終態」MUST FIX 已徹底完成修復：
1. 取消狀態機不再存在任何依賴過期快照執行無條件更新（unconditional UPDATE）之漏洞。
2. 狀態轉換迴圈邏輯嚴謹清晰，重試機制天然防禦所有並發交錯。
3. 全專案 7 項 Gherkin 驗收劇本、單元測試、全套端到端整合測試與 `-race` 競態檢測全數通過。

## 需求與契約符合度
- 需求描述：Issue #9 專案級基礎記憶契約健全化及 PR #10 第六輪審查修復。
- 是否符合需求：**是**。
- 契約查核：
  - **MEM-001 ~ MEM-007 全驗收劇本**：記憶 CLI IPC、正規化專案路徑隔離、越界拒絕、非記憶管理零 AI 調用等行為皆保持完整相容。
  - **狀態轉移契約**：
    - 任務所有狀態轉換均由 SQLite 條件式 CAS 保證互斥排他。
    - 任何已提交之終態（`completed` / `failed`）在無殘留進程組時，不可被取消，必返回 `ErrTaskNotRunning`。
    - 過期快照無法命中條件式 CAS，自動重新讀取最新狀態並正確回退。

## 測試真實性與驗證結果
- **測試命令與結果**：
  1. `go test -v -count=1 ./internal/engine -run '^TestEngineCancel_StalePendingSnapshotDoesNotOverwriteTerminalState$'`：PASS (0.15s)
  2. `go test -v -count=1 ./internal/engine`：PASS (0.47s)
  3. `go test -race -count=1 ./internal/engine ./internal/daemon ./internal/storage ./internal/cron ./cmd/asura`：PASS（無任何 data race）
  4. `go test -v -count=1 ./tests/integration`：PASS（全套端到端整合測試通過，70.06s）
- **真實性查核**：
  - `TestEngineCancel_StalePendingSnapshotDoesNotOverwriteTerminalState` 具備真實失敗重現性（修復前 red，修復後 green）。
  - 測試無任何 `t.Skip` 或偽造條件。

## 結論
PR #10 第六輪審查要求之過期快照 CAS 缺陷已徹底根治，所有狀態轉移均已完全線性化並經測試驗證。本獨立審查報告判定結果為 **PASS**，具備完整合併條件。
