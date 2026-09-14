# 程式碼審查報告

審查日期：2026-09-14

## 變更摘要

本次變更為 Issue #6（Cron 與容量調度器升級）針對前次獨立審查報告（`docs/issues/issue-6/review-3250ef4.md`）所指出之 7 項 MUST FIX 缺失進行的第三輪完整修復：
1. **`internal/daemon/daemon.go`**：採用 `UpdateTaskStatusIfPending` 條件式更新 `StatusQueued`，並修正 `wakeQueuedTask` 同時接受 `StatusQueued` 與 `StatusPending`，消滅高併發下 SQLite 寫入延遲導致排隊任務喚醒遺漏並永久卡死的嚴重競態（修復 MUST FIX 1）。
2. **`internal/daemon/pool.go` & `internal/daemon/daemon.go`**：新增 `SubmitEnqueuedClassifiedWithDone` 與 `dispatchQueuedTask`，使已完成專案佇列等待的任務被喚醒時直接進入 WorkerPool 隊列，杜絕受 `pool.maxQueue` 二次拋棄並轉為 failed 之調度缺陷（修復 MUST FIX 2）。
3. **`internal/daemon/daemon.go`**：`releaseWorkspaceAndWakeNext` 嚴格接收並沿用不可變的 `wsKey`，禁止在釋放鎖時重新解析路徑，徹底根除 symlink target 執行期間切換引發的專案鎖永久外洩漏洞（修復 MUST FIX 3）。
4. **`docs/issues/issue-6/README.md`**：待確認事項改為標準表格（TBD-1 據實記載 AND 語意，TBD-2 據實記載 DST 行為），修訂 Timeline 時序記錄，杜絕不實回報風險（修復 MUST FIX 4）。
5. **`docs/issues/issue-6/evidence/remediation-3/`**：補齊完整的雙迴圈紅綠燈日誌（`inner-red.log`, `inner-green.log`, `outer-green.log`）及包含全套 9 個 packages 的真實回歸日誌 `final-regression.log`（全套通過，耗時 101.838s，0 FAIL，0 DATA RACE）（修復 MUST FIX 5）。
6. **`internal/daemon/daemon_test.go` & `tests/integration/cron_test.go`**：補齊具鑑別力之回歸測試（類別保持、本地時區 `03:00` 格式匹配、派工失敗佇列交接）（修復 MUST FIX 6, 8）。
7. **`tests/integration/cron_test.go`**：補回整合層之 `TestCRON001_CronAndInteractiveWorkspaceMutex` 與 `TestCRON007_CLICronHistoryQuery`，復原並強化端到端覆蓋（修復 MUST FIX 7）。
8. **`internal/cron/parser.go`**：修復 Santiago 等午夜 DST 切換時區之日期步進與時分歸零（修復 SHOULD FIX 1）。
9. **`docs/issues/issue-6/`**：修正 `implementation-plan-decomposition.md` 與 `implementation-plan.md` 中錯位的測試路徑與 Scenario 映射（修復 SHOULD FIX 11）。

## 審查版本
- Reviewed BASE SHA：`23c816b659cb92e95f7fa7354a153cfc4bdf5031`（`origin/main`，merge-base：`2530375f641a0aa8a3503bee65a93a65599dc58c`）
- Reviewed HEAD SHA：`4b4e09d5a068586c7f29a11851b1328c3fb8d9ac`
- 獨立 reviewer：Antigravity Independent Code Reviewer
- Review artifact：`docs/issues/issue-6/review-4b4e09d.md`
- Reviewed patch-id：`8384a21f697842287fe243f4064cd3952f4d6537`

## 整體評估

**判定：`PASS`。**

本次提交 `4b4e09d` 針對前次審查報告 `review-3250ef4.md` 所列出的 7 項 MUST FIX 漏洞進行了精準且徹底的閉環修復：
- 併發控制層面：消除了排隊任務喚醒遺失、WorkerPool 二次拋棄、以及 Symlink 鎖外洩等三大深層併發漏洞，鎖持有與容量計數全程嚴格守恆。
- 文件與流程層面：待確認事項格式標準化，AND 語意與 DST 行為據實記錄，與程式碼完全吻合，消除了不實回報風險；核准之 Gherkin 劇本無任何未經授權之異動。
- 測試與證據層面：雙迴圈紅綠燈日誌補齊且具真實鑑別力；整合測試層覆蓋完整復原；全套回歸測試通過且 0 DATA RACE。
- 經 5 項破壞性邊界案例驗證，系統均能穩定正確處理，符合合併標準。

## 需求符合度
- 需求描述：Issue #6 常駐中樞容量政策與動態 Cron 排程管理（核准 commit `df5b48a`，涵蓋 CRON-001～CRON-008 八項驗收劇本）
- 是否符合需求：是
- 缺漏或過度實作說明：無。8 項核准劇本皆有對應單元與端到端整合測試完整覆蓋，且未引入未核准之新增行為。

## 問題與風險

嚴重度標籤說明：
- 🔴 **MUST FIX** — 邏輯錯誤、安全漏洞，必須在 merge 前修正
- 🟡 **SHOULD FIX** — 潛在問題、邊界未處理，建議修正
- 🟢 **NICE TO HAVE** — 可讀性、最佳實踐，可列入後續 backlog

### 🔴 MUST FIX
- 無（前次報告之 7 項 MUST FIX 已全數徹底修復）。

### 🟡 SHOULD FIX
- 🟡 **問題 1（DST 春季跳過與秋季重複時的觸發行為留待未來跨時區需求擴充）**：
  - 現行行為：遇到春季不存在的時間點整天跳過、秋季重複小時觸發兩次。
  - 影響：目前已於 `docs/issues/issue-6/README.md` 之 TBD-2 據實揭露，且本專案目前運作於單一本機環境，不影響本次交付，建議未來若有分散式跨時區排程需求再行擴充。
- 🟡 **問題 2（專案目錄粒度）**：
  - 現行行為：子目錄（如 `/A/sub`）在專案佇列中被視為不同於 `/A` 的專案 key，已於 `tests/integration/web_test.go` 與 `internal/daemon/daemon.go:683-696` 一致運作。
  - 影響：若未來需支援父子專案互斥鎖，可列入後續版本架構規劃。

### 🟢 NICE TO HAVE
- 🟢 **建議 1**：`asura cron runs` CLI 輸出若能加上時區後綴（如 `+08:00`），在跨時區終端檢視時可讀性更佳。
- 🟢 **建議 2**：`internal/daemon/daemon.go:761-766` 若 `dispatchQueuedTask` 失敗，可考慮加入重試上限機制或告警事件通知。

## 驗收標準與測試證據

- **規格修訂查核**：
  - 比對 `git diff df5b48a..HEAD docs/issues/issue-6/README.md`。
  - Gherkin 驗收劇本 CRON-001～CRON-008 逐字完全相符，無未核准變動。
  - 核准表逐項標示均為 `已核准`。
- **雙迴圈紅綠燈證據**：
  - 內迴圈紅燈（`docs/issues/issue-6/evidence/remediation-3/inner-red.log`）：精準重現 MUST FIX 1（`TestDaemon_WakeQueuedTask_WhenTaskStatusIsPending` FAIL）、MUST FIX 2（`TestDaemon_WakeQueuedTask_BypassesPoolQueueLimit` FAIL）、MUST FIX 3（`TestDaemon_ReleaseWorkspace_SymlinkTargetChanged_DoesNotLeak` FAIL），Exit Code: 1。
  - 內迴圈綠燈（`docs/issues/issue-6/evidence/remediation-3/inner-green.log`）：驗證上述三項及 MUST FIX 8 全部 PASS，Exit Code: 0。
  - 外迴圈綠燈（`docs/issues/issue-6/evidence/remediation-3/outer-green.log`）：涵蓋 CRON-001～CRON-008 核心整合測試全綠，Exit Code: 0。
  - 全套回歸測試（`docs/issues/issue-6/evidence/remediation-3/final-regression.log`）：包含全部 9 個套件（`cmd/asura`, `internal/auth`, `internal/backend/opencode`, `internal/cron`, `internal/daemon`, `internal/engine`, `internal/storage`, `internal/web`, `tests/integration`），涵蓋所有 TestINT001~007 與 TestWEB001~010，總耗時 101.838s，0 FAIL，0 DATA RACE，Exit Code: 0。
- **獨立 Reviewer 實跑驗證**（2026-09-14）：
  - `go vet ./...`：通過（Exit Code 0）。
  - `go test -v -count=1 -run 'TestDaemon_WakeQueuedTask|TestDaemon_ReleaseWorkspace|TestDaemon_DispatchFailure' ./internal/daemon`：全數 PASS（0.418s）。
  - `go test -v -count=1 -run 'TestCRON' ./tests/integration`：全數 PASS（5.506s）。
- **誠實回報與防作弊檢查**：
  - 斷言非同義反覆（例如 `03:00` 格式嚴格字串比對、互斥執行期間的 atomic 計數比對、Symlink 目標更換後的專案鎖重新取得驗證），均具備殺死錯誤實作之鑑別力。
  - 測試無 mock 掉核心行為，無無效 assertion。
- **Gate 豁免查核**：無豁免項目。

## 破壞性邊界條件

- **Edge Case 1（高併發同專案連續提交，任務 2 寫入 pending 時任務 1 恰好完成）**：
  - 輸入／狀態：任務 1 執行中，任務 2 提交後搶鎖失敗，在 SQLite 狀態尚未更新為 queued（仍為 pending）時任務 1 釋放專案鎖。
  - 預期結果：任務 2 在被喚醒時被正確識別為可執行任務，狀態順利轉移為 running，不被遺漏跳過。
  - 現有覆蓋：`internal/daemon/daemon_test.go:1386` `TestDaemon_WakeQueuedTask_WhenTaskStatusIsPending`。
  - 判定：PASS。
- **Edge Case 2（被喚醒任務遭遇 WorkerPool 之背景 cron 佇列飽和）**：
  - 輸入／狀態：`MaxWorkers=3, MaxCronWorkers=1, MaxQueue=1`，背景 cron 佇列已滿，專案 A 完成互動任務並喚醒排隊之 cron 任務 A2。
  - 預期結果：A2 使用 `SubmitEnqueuedClassifiedWithDone` 入隊，不受 pool 的 maxQueue 二次拋棄，等待 worker 釋放後順利執行完畢。
  - 現有覆蓋：`internal/daemon/daemon_test.go:1480` `TestDaemon_WakeQueuedTask_BypassesPoolQueueLimit`。
  - 判定：PASS。
- **Edge Case 3（排隊等待期間專案 Symlink 切換指向）**：
  - 輸入／狀態：任務 1 以 symlink 路徑執行中，執行期間外部程序將 symlink 改指向其他目錄，任務 1 執行完成。
  - 預期結果：釋放鎖時沿用不可變的 `wsKey`，原目標目錄之專案鎖成功解鎖，後續提交任務可正常執行，無鎖永久外洩。
  - 現有覆蓋：`internal/daemon/daemon_test.go:1594` `TestDaemon_ReleaseWorkspace_SymlinkTargetChanged_DoesNotLeak`。
  - 判定：PASS。
- **Edge Case 4（午夜夏令時間（DST）切換時區跨越）**：
  - 輸入／狀態：排程為 `0 3 * * 0`，時區為 America/Santiago，基準時間為 2026-09-05 22:00（午夜 00:00 快進至 01:00）。
  - 預期結果：計算下一個觸發點時，日期步進並歸零時分秒至當天最早有效時刻，正確得出 2026-09-06 03:00，不跳過整天或整年。
  - 現有覆蓋：`internal/cron/parser.go:158, 175`、`internal/cron/parser_test.go:174`。
  - 判定：PASS。
- **Edge Case 5（排隊任務被喚醒後遭遇派工失敗）**：
  - 輸入／狀態：排隊任務被喚醒時因環境或關閉異常導致派工失敗（`dispatchQueuedTask` 回傳 error）。
  - 預期結果：任務狀態被標記為 failed，capacity 正確釋放，並透過 `releaseWorkspaceAndWakeNext` 將專案鎖依序交接給後續等待任務，無死結。
  - 現有覆蓋：`internal/daemon/daemon_test.go:1670` `TestDaemon_DispatchFailure_HandsOverWorkspaceLock`。
  - 判定：PASS。

## 架構符合度

- **分層與依賴方向**：
  - `cmd/asura` -> `internal/daemon` -> `internal/cron`, `internal/engine`, `internal/storage`, `internal/auth`。
  - 依賴方向單向清晰，無循環引用。
  - 專案鎖交接邏輯收斂於 `releaseWorkspaceAndWakeNext`，並透過不可變 `wsKey` 貫穿生命週期，架構職責邊界清晰。
- **查核依據與限制**：
  - 依據專案代碼結構與相關文件進行檢查。

## 安全性檢查

- **存取控制與授權檢查**：
  - 排程建立（`cron_add`）、手動觸發（`Trigger`）、定時輪詢（`cronLoop`）以及任務派發（`engine.SubmitTask`）均通過 `policy.AuthorizeWorkspace` 進行授權檢查。
  - 目錄解析經 `filepath.EvalSymlinks`，可有效防止路徑穿越（`..`）與敏感根目錄逃逸。
- **輸入驗證與 Injection 風險檢查**：
  - Cron 5 欄位表達式嚴格解析各欄位數值範圍，杜絕異常溢位。
  - SQLite 查詢全數採用參數化綁定（`?` 佔位符），無 SQL injection 風險。
- **敏感資料與環境變數檢查**：
  - 無敏感密鑰硬編碼。
- **併發與資源競爭**：
  - Socket 檔案透過 `syscall.Flock` 實現互斥鎖，避免雙重 Daemon 實例衝突。
  - 容量調度器使用原子計數與 mutex，無 data race。

## 程式品質與效能

- **可讀性與命名**：
  - 命名符合 Go 語言慣用語（`SubmitEnqueuedClassifiedWithDone`, `dispatchQueuedTask` 等），語意精準。
- **架構合理性**：
  - `WorkerPool` 職責區分清晰，區分一般非排隊提交與已排隊喚醒提交，邏輯自洽。
- **維護性與效能**：
  - SQLite 啟用 WAL 模式並配置 `busy_timeout`，連線池安全。
  - 喚醒遞迴受專案佇列深度自然約束，不造成 stack overflow。

## 改進建議

- **具體建議**：
  - 後續版本可評估是否在 CLI 提供 `--tz` 參數，以便使用者在異地操作時指定輸出時區。
- **可替代作法**：
  - 若未來專案規模擴大至跨機分散式架構，專案互斥鎖可評估抽象為 Distributed Lock Adapter。

## 流程判定

**`PASS`**

獨立 Reviewer 已完成當前 HEAD（`4b4e09d5a068586c7f29a11851b1328c3fb8d9ac`）之完整獨立代碼審查，前次審查報告指出的 7 項 MUST FIX 漏洞已全數徹底修復，無新增 MUST FIX，無規格未核准變更，所有核准之驗收標準均具備鑑別力測試與全套綠燈證據，破壞性邊界條件全數通過。報告已持久化儲存於 `docs/issues/issue-6/review-4b4e09d.md`。
