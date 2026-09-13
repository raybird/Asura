# 程式碼審查報告

建立日期：2026-09-13。

## 變更摘要

本報告整理使用者提供的 reviewer model 審查結果，對應 Issue #2 的提交 `9d263de`（同步取消契約、權限邊界、Daemon CLI 與佇列排水修復）。其中 Request ID 與 Task ID 分離、SQLite 欄位遷移及 OpenCode 事件錯誤傳遞仍有三項缺失。

## 審查版本

- Reviewed HEAD SHA：`9d263de5e60dad8fcbfb2a08fd4f06579c3c63fd`
- 獨立 reviewer：使用者提供的 reviewer model（原始輸出未提供模型名稱或執行識別碼）。
- Review artifact：`docs/issues/issue-2/review-9d263de.md`
- Reviewed patch-id：`6b2e97d61ac7a93aa1cdbff0c14f1848301ba8e0`（依專案規範，以 `git diff main...HEAD | git patch-id --stable` 計算分支變更）。
- 單一提交 patch-id：`109e0d6d141f1249f1f675d424073493b3f05696`（`git show HEAD --pretty=format: | git patch-id --stable`）。
- 範圍與來源：原始審查針對最新提交；2026-09-13 整理時以提交內容與問題位置確認對應上述 SHA。本次只持久化既有審查結果，未重新執行程式碼審查或測試。

## 整體評估

提交存在既有資料庫升級阻斷、並行重送重複執行及事件錯誤處理未完整串接的問題。原始 reviewer 已重現資料庫升級錯誤；全套測試因沙箱限制而未通過，不能作為完整回歸證據。依下列缺失，流程判定為 **RETURN TO execute-task**。

## 需求符合度

- 需求描述：Issue #2 本機 AI 執行路徑整合 Phase 1.5。
- 是否符合需求：尚未完整符合。
- 缺漏：既有資料庫無法升級啟動；INT-004-REDELIVERY 的重送去重缺少原子保障；INT-006 的後端失敗與部分輸出可能被轉成成功。

## 問題與風險

以下保留原始優先級；三項均為需修正的行為缺失，標記為 🔴 **MUST FIX**。

### 1. [P1] 先遷移既有欄位，再建立 Request ID 索引

- 位置：[internal/storage/sqlite.go](../../../internal/storage/sqlite.go)，第 104–110 行。
- 問題：使用上一版建立的資料庫啟動時，`tasks` 尚無 `request_id`，此處建立索引會立即報錯並返回，後面的 `ALTER TABLE` 永遠不會執行。
- 證據：原始 reviewer 已用前一版 schema 重現 `no such column: request_id`。
- 影響：既有使用者無法開啟資料庫或啟動 daemon。
- 修正建議：先完成欄位遷移，再建立索引，並檢查遷移錯誤。

### 2. [P1] 保留 Request ID 的原子唯一性保障

- 位置：[internal/daemon/daemon.go](../../../internal/daemon/daemon.go)，第 304–309 行。
- 問題：兩個連線同時提交相同 Request ID 時，各自的 `handleConn` 都可能先查不到紀錄，再以不同的隨機 task ID 成功寫入並執行。原本由主鍵提供的重複執行防護已被移除，而新 `request_id` 索引並不唯一。
- 影響：重送可能造成專案修改等副作用執行兩次，違反 INT-004-REDELIVERY。
- 修正建議：以非空 Request ID 的唯一約束及衝突後查回既有任務，或等效的原子操作保障去重。

### 3. [P2] 同步更新 Run，避免吞掉新增的事件錯誤

- 位置：[internal/backend/opencode/adapter.go](../../../internal/backend/opencode/adapter.go)，第 140–143 行。
- 問題：後端以退出碼 0 輸出部分文字後再輸出 error 事件時，新增分支會回傳部分結果與錯誤，但 `Run` 的 `extractErr != nil` 分支仍將非空 `rawStdout` 回傳為成功。
- 影響：任務仍可能被 Engine 標記 completed，且輸出由抽取文字退化成整段 JSON 事件。
- 修正建議：同步修改呼叫端，保留 `extracted` 與 `extractErr`，不要把已辨識的後端錯誤當作格式解析失敗而降級成功。

## 驗收標準與測試證據

- 已有失敗證據：原始 reviewer 回報以前一版 schema 重現 `no such column: request_id`；提供的審查文字未附完整命令、退出碼及日誌。
- 並行去重與事件錯誤：原始審查提供失敗路徑分析，未附可重複執行的測試輸出。
- 全套回歸：原始 reviewer 回報因沙箱限制而未通過；不得據此宣稱完整回歸通過。
- 規格修訂、紅綠重構及 Gate 豁免：提供的審查結果未包含完整查核證據，本報告不補作通過判定。
- 2026-09-13 文件整理屬純文件工作，不適用程式行為紅燈要求；以章節、問題數量、相對連結及空白檢查驗證文件。

## 破壞性邊界條件

| 情境 | 預期結果 | 審查指出的失敗結果 |
|---|---|---|
| 開啟缺少 `request_id` 的既有資料庫 | 成功遷移並可啟動 daemon；遷移失敗須回報錯誤 | 建立索引提前失敗，遷移無法執行 |
| 兩個連線並行提交相同非空 Request ID | 對應同一任務，後端只執行一次 | 產生不同 task ID 並重複執行 |
| 後端退出碼 0，先輸出文字再輸出 error 事件 | 保留部分文字及錯誤，任務不得標記 completed | 回傳原始 JSON 串流並降級為成功 |

## 架構符合度

缺失集中於 Storage 遷移、Daemon 去重及 Adapter → Engine 錯誤傳遞。原始審查未提供完整分層與依賴查核結論，不能推定整體架構已通過審查。

## 安全性檢查

並行重送可能讓專案修改等副作用重複執行，必須由原子去重保障阻止。其餘授權、輸入驗證與資源回收的完整安全性查核未包含在提供的結果中。

## 程式品質與效能

主要問題為遷移執行順序、查詢後寫入的競爭條件，以及錯誤處理呼叫端未同步更新。原始結果未包含效能量測，不作效能通過判定。

## 改進建議

1. 補上既有 schema 的升級回歸測試，確認欄位遷移與索引建立順序及錯誤回報。
2. 補上相同 Request ID 並行提交測試，核對回傳任務識別與獨立的後端執行次數。
3. 補上 `Run` 層級的部分文字加 error 事件測試，確認錯誤與抽取文字皆保留，並驗證 Engine 不標記 completed。

以上為後續修復的驗證建議，2026-09-13 本次文件整理未執行這些測試。

## 流程判定

- **`RETURN TO execute-task`**
- 判定理由：兩項 P1 與一項 P2 行為缺失尚待修正；既有資料庫升級已有失敗回報，完整回歸證據不足。應修復三項問題並補齊驗證後再進行審查。
