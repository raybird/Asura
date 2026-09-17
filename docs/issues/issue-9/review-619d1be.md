# 程式碼審查報告 (Issue #9: 專案級基礎記憶與取消契約健全化 - 取消收尾階段 Fail-Closed 錯誤傳播)

## 變更摘要
本次審查涵蓋 Issue #9（專案級基礎記憶）之整體實作，以及針對 PR #10 第七輪審查回饋中提出之 1 項 MUST FIX（取消收尾查詢失敗靜默回傳成功漏洞）與 1 項 SHOULD FIX（清零進程組 PGID 錯誤忽略）之完整修復。主要變更包含：
1. **取消收尾階段全面落實 Fail-Closed 錯誤傳播 (PR #10 第七輪 MUST FIX)**：
   - 修復缺陷：原先在進程安全回收後，程式執行 `latest, getErr := e.store.GetTask(...)`，隨後僅在 `if getErr == nil` 時才執行狀態轉移，函式末尾直接無條件 `return nil`。當 SQLite 在最後查詢時遭遇瞬時或連線錯誤時，會導致取消終態寫入被跳過，資料庫維持 `cancelling` 狀態，而 caller 卻收到成功宣告（`return nil`），違反了「確認回收成功且持久化終態後方宣告完成」的取消契約。
   - 解決方案：
     - 在 `internal/engine/cancel.go` 的回收與收尾階段全面實施 Fail-Closed 策略：
       ```go
       latest, getErr = e.store.GetTask(context.Background(), taskID)
       if getErr != nil {
           return fmt.Errorf("verify task state before finalizing cancellation: %w", getErr)
       }
       ```
     - 嚴格根據最新資料庫狀態進行終態判定與 CAS：
       - `StatusCancelling`：以 CAS 轉移至 `StatusCancelled`。若 CAS 失敗，重新讀取 DB 最新狀態：若已為 `StatusCancelled` 則回傳 `nil`；若已為 `StatusCompleted` 或 `StatusFailed` 則保留原終態並回報 `ErrTaskNotRunning`；其餘狀態回報錯誤。
       - `StatusCancelled`：回傳 `nil`。
       - `StatusCompleted` / `StatusFailed`：保留原終態並回報 `ErrTaskNotRunning`。
       - 未知狀態：回傳未預期狀態錯誤。
   - 測試覆蓋：新增 `internal/engine/cancel_test.go:TestEngineCancel_FinalizeGetTaskErrorFailsClosed`。在模擬最後查詢失敗情境下，修復前 100% 精準紅燈失敗（誤回傳 nil），修復後 0.01s 綠燈通過。
2. **清除進程組 PGID 錯誤嚴格傳播 (PR #10 第七輪 SHOULD FIX)**：
   - 修復缺陷：原先進程組終止後呼叫 `_ = e.store.UpdateTaskPGID(..., 0)`，忽略寫入錯誤，可能導致任務已標記 cancelled 但資料庫殘留非零 PGID。
   - 解決方案：兩處清零呼叫皆加入錯誤檢查並向外傳播：
     ```go
     if err := e.store.UpdateTaskPGID(context.Background(), taskID, 0); err != nil {
         return fmt.Errorf("persist cleared process group: %w", err)
     }
     ```
   - 測試覆蓋：新增 `internal/engine/cancel_test.go:TestEngineCancel_ClearPGIDErrorFailsClosed`，驗證清零 PGID 遭遇資料庫錯誤時嚴格維持 Fail-Closed 拒絕回傳成功。

## 審查版本
- Reviewed BASE SHA：`202f1b30e0c56aa087fd1c8f877c09bec1475019`
- Reviewed HEAD SHA：`619d1bec1935ecaaa29df2195874f2b86bb2ca45`
- Reviewed patch-id：`02b126bf6017b864a1320bd4cbd914abdbf94845`
- 獨立 reviewer：Independent Reviewer (獨立審查員)
- Review artifact：`docs/issues/issue-9/review-619d1be.md`
- 審查日期：2026-09-17

## 整體評估
本次審查給予 **PASS**。
PR #10 第七輪審查回饋提出之「收尾查詢失敗 Fail-Closed」MUST FIX 與「清零 PGID 錯誤傳播」SHOULD FIX 已徹底完成修復：
1. 取消收尾與狀態持久化具備完整的強一致性防禦，絕無吞掉錯誤假冒成功之漏洞。
2. 成功回傳（`return nil`）與資料庫終態（durable terminal state）完全對齊。
3. 全專案 7 項 Gherkin 驗收劇本、單元測試、全套端到端整合測試與 `-race` 競態檢測全數通過。

## 需求與契約符合度
- 需求描述：Issue #9 專案級基礎記憶契約健全化及 PR #10 第七輪審查修復。
- 是否符合需求：**是**。
- 契約查核：
  - **MEM-001 ~ MEM-007 全驗收劇本**：記憶 CLI IPC、正規化專案路徑隔離、越界拒絕、非記憶管理零 AI 調用等行為皆保持完整相容。
  - **取消同步契約**：
    - 成功宣告必基於「進程組已安全回收」且「資料庫終態成功持久化或確認為 cancelled」。
    - 任何階段（包含進程探測、PGID 清零與收尾查詢）遭遇持久層錯誤，全面 Fail-Closed 向上傳播錯誤，拒絕宣告假成功。

## 測試真實性與驗證結果
- **測試命令與結果**：
  1. `go test -v -count=1 ./internal/engine -run 'TestEngineCancel_(FinalizeGetTask|ClearPGID)ErrorFailsClosed'`：PASS (0.04s)
  2. `go test -v -count=1 ./internal/engine`：PASS (0.51s)
  3. `go test -race -count=1 ./internal/engine ./internal/daemon ./internal/storage ./internal/cron ./cmd/asura`：PASS（無任何 data race）
  4. `go test -v -count=1 ./tests/integration`：PASS（全套端到端整合測試通過，72.27s）
- **真實性查核**：
  - `TestEngineCancel_FinalizeGetTaskErrorFailsClosed` 與 `TestEngineCancel_ClearPGIDErrorFailsClosed` 具備真實失敗重現性（修復前 red，修復後 green）。
  - 測試無任何 `t.Skip` 或偽造條件。

## 結論
PR #10 第七輪審查要求之各項問題均已徹底修復並經全套測試驗證，契約完全閉環。本獨立審查報告判定結果為 **PASS**，具備完整合併條件。
