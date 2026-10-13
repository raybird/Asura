# Issue 48 - Flaky test：TestRuntimeSelectionLocalCancel 在 CI 偶發失敗 (Small)

## 概述

來源：[GitHub #48](https://github.com/raybird/Asura/issues/48)。`cmd/asura/runtime_selection_test.go` 的 `TestRuntimeSelectionLocalCancel` 在 GitHub Actions 偶發失敗，重跑後通過；本機連跑 30 次全過（2026-10-09，issue 記錄）。目標是找出真正原因並修正，不以加長等待或重試掩蓋。

已觀察到的失敗（2026-10-13 由 CI log 核對）：

| 日期 | CI run | 失敗位置 | 訊息 | 耗時 |
|---|---|---|---|---|
| 2026-10-04 | [37198088965](https://github.com/raybird/Asura/actions/runs/37198088965) | `runtime_selection_test.go:112` | `runtime did not start` | 5.01s |
| 2026-10-09 | [37950457101](https://github.com/raybird/Asura/actions/runs/37950457101) attempt 1 | `runtime_selection_test.go:118` | `cancel: ... task is not running: task status is failed` | 0.67s |
| 2026-10-10 | [38005313683](https://github.com/raybird/Asura/actions/runs/38005313683) attempt 1 | `runtime_selection_test.go:118` | 同上 | 0.78s |

症狀 2 的 0.7 秒代表 runtime 已啟動並登記 PGID（測試迴圈才會離開），之後在取消前就轉為 `failed`；症狀 1 則是 5 秒內 task 都沒有登記 PGID。這個測試走 `handleRun --backend auto` 的 local 路徑：admission 探測 codex 版本 → codex adapter 再驗一次版本 → `BeforeStart` 取 flock → `cmd.Start` → `recordRuntimeProcessStart` 寫鎖檔與 PGID。

### 症狀 2 原因（2026-10-13 查明）

這是 production 的取消競態，不只是測試時序問題。`recordRuntimeProcessStart` 先寫入 PGID，再檢查取消意圖檔；外部 `asura cancel` 則是先寫意圖檔，再把狀態改成 `cancelling`。當取消剛好落在兩者之間：

1. runner 寫入 PGID，測試（或使用者）看到 PGID 後執行 cancel，寫入意圖檔
2. runner 看到意圖檔，直接殺掉行程組，但沒有把狀態改成 `cancelling`
3. codex adapter 回報 `empty_result`，engine 以 `UpdateTaskStatusIfRunning` 把仍在 `running` 的任務記為 `failed`
4. cancel 端讀到 `failed`，回報 `task is not running: task status is failed`

證據：在 adapter 暫時記錄行程結束狀態，失敗的每一輪都是 `signal: killed` 且 `ctx.Err()=nil`；在 `recordRuntimeProcessStart` 各 kill 分支記錄觸發點，14 次失敗全部來自意圖檔檢查分支（診斷碼已移除，未進入交付）。shell runner 在 `run.go` 遇到同樣情況會先 `UpdateTaskStatusIfRunning(..., StatusCancelling, ...)`，auto 路徑缺了這一步。

### 症狀 1 調查（2026-10-13，未重現）

本機在 2 顆或 1 顆 CPU 加 6～12 個背景負載的條件下累計跑了 340 次，只出現症狀 2，症狀 1 一次都沒有出現。已排除的假設：

- 測試與 `handleRun` 同時初始化同一個全新 DB，造成 migration 失敗：兩個 goroutine 並行 `NewSQLiteStore` 300 輪，0 錯誤
- `GetProcessStartTime` 讀 `/proc/<pid>/stat` 失敗，導致登記 PGID 前就被殺：行程在被回收前都讀得到，這個 kill 分支在所有壓力輪次中都沒有觸發
- 2026-10-04（`30cc401`）之後相關程式只有 config 接線與 `ProbeExecutable` 重構，沒有語意變化

尚未排除：CI 整體負載下 admission 版本探測超過 `RuntimeProbeTimeout`（3 秒），任務在登記 PGID 前就失敗。測試現在失敗時會印出 runner 端的任務狀態、錯誤訊息與輸出，CI 再發生時可以直接判斷。

## 涉及檔案

- `cmd/asura/runtime_process.go` — 登記 PGID 後看到外部取消意圖時，先把 `running` 改成 `cancelling` 再回收行程
- `cmd/asura/runtime_selection_test.go` — 新增 `TestRuntimeSelectionStartSeesCancelIntent`（不依賴時序的紅燈）；`TestRuntimeSelectionLocalCancel` 失敗時印出 runner 端任務狀態與輸出

修正只動一個 production 檔案，維持 Small。

## 驗收條件

1. **AC-1**：查出症狀 2（`task status is failed`）的失敗原因，並用可重複的手段（注入延遲、故障或 CI 等效的負載條件）在修正前穩定取得紅燈；紅燈須保存 runner 端的任務終態、錯誤訊息與 stub 輸出，證明紅燈與 CI 症狀相同。症狀 1（`runtime did not start`）不在本 issue 修正：測試失敗時須印出 runner 端的任務終態、錯誤訊息與輸出，並另開 issue 追蹤 — 檢查方式：在修正前的程式上執行重現命令，紀錄失敗次數與診斷輸出；確認追蹤 issue 存在
   - 失敗路徑：無法重現或紅燈訊息與 CI 症狀不符時，不進行修正；回報已嘗試的手段與結果，交由使用者決定下一步
2. **AC-2**：修正測試或程式後，AC-1 的同一重現手段轉為綠燈，且本機最小回歸（`go vet ./...`、`go test -count=1 -race -v ./...`、`go build ./cmd/asura`）全數通過 — 檢查方式：重跑 AC-1 命令與回歸命令
   - 失敗路徑：修正只是加長等待期限或加上重試，或原因是時序假設卻沒有改成不依賴時序的判準，都判定未通過
3. **AC-3**：在 CI 等效條件（限制 CPU、加 `-race`）下以 `-count=200` 執行本測試，修正前至少失敗 1 次作為對照組、修正後 0 失敗，前後條件相同；PR CI 一次全綠作為補充 — 檢查方式：記錄壓力命令原文、CPU 限制方式與修正前後的失敗次數
   - 失敗路徑：修正後仍有任何失敗即未通過；修正前同條件 0 失敗時，判準沒有鑑別力，不得當作通過，回報後重新決定條件

- **核准日期**: 2026-10-13（AC-1 於同日依 Q2 修訂並重新核准）
- **核准來源**: AC-1、AC-2 出自使用者於 2026-10-09 撰寫的 GitHub #48「完成判準」：「找出失敗原因，並以能重現的方式（例如注入延遲，或在 CI 等效條件下）先取得紅燈」、「修正測試或程式後，在 CI 連續穩定通過；或明確記錄這個時序假設，並調整為不依賴時序的判準」、「不以加長等待時間或重試掩蓋真正的失敗」；2026-10-13 使用者以 `/dev-cycle issue:48` 要求推進。AC-3 出自 2026-10-13 Q1（TBD-1）回答「壓力對照為主 (Recommended)」，接受建議：CI 等效條件跑 `-count=200`，修正前有失敗、修正後 0 失敗，另加 PR CI 一次全綠。AC-1 修訂出自 2026-10-13 Q2（TBD-2）回答「先交付，症狀 1 另開 issue (Recommended)」，接受建議：#48 以症狀 2 的修正交付，症狀 1 保留失敗診斷並另開 issue 追蹤
- **核准 commit**: e6528d7（AC-1 修訂；原核准 1ed06e3）

## 風險與首要驗證

- **最大風險**：失敗原因未知，可能是 production 的取消或啟動路徑有真實缺陷（runtime 被誤殺或誤判失敗），而不只是測試時序問題；若誤判為測試問題，修測試會掩蓋產品缺陷
- **風險等級與理由**：Medium — 有一項重要未知（根因），誤判時會造成返工與殘留缺陷；修正範圍可回復，不涉及資料或權限
- **首要驗證**：先讓失敗路徑留下診斷（任務終態、ErrorMessage、stub 輸出），再以注入延遲、故障或 CI 等效負載重現兩種症狀
- **選擇理由**：本機 30 次全過，代表正常條件下抓不到問題；沒有可重複的紅燈，就無法區分測試時序問題與產品缺陷，修正也無從驗證
- **完成證據**：AC-1 的重現命令在修正前穩定失敗，且失敗訊息與 CI 症狀一致

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|------|------|------|------|
| TBD-1 | issue 寫「在 CI 連續穩定通過」，需決定可判定的穩定性判準：CI 執行次數，或以修正前、後同條件的壓力對照為主 | 已解決 | 2026-10-13 使用者選擇壓力對照為主：失敗率只有幾個百分點，CI 連跑數次沒修好也可能全綠，只有修正前的紅燈基線能證明判準有效；寫入 AC-3 |
| TBD-2 | 症狀 1（`runtime did not start`）本機 340 次壓力測試未重現，依 AC-1 失敗路徑不做修正；需決定 #48 是否以症狀 2 的修正交付，症狀 1 另行追蹤 | 已解決 | 2026-10-13 使用者選擇先交付、症狀 1 另開 issue：症狀 2 是三次失敗中的兩次，且是真實產品缺陷；症狀 1 需等 CI 再發生，靠新的診斷輸出定位。已修訂 AC-1，追蹤於 [#51](https://github.com/raybird/Asura/issues/51) |

## 實作與驗證步驟

1. ✅ **AC-1：重現與定位原因** — 驗證方式：在測試失敗路徑補診斷輸出，以注入延遲、故障或 CI 等效負載（例如限制 CPU 後並行跑 `-race -count=N`）重現兩種症狀，紀錄修正前的失敗次數與訊息
   - 產出：可重複的重現命令或測試、原因說明（寫回本節）
   - 相依：無
   - 完成判準：修正前的紅燈與 CI 症狀一致；查不出原因時依 AC-1 失敗路徑停止並回報
   - 完成證據（2026-10-13，go1.27.1 linux/amd64，12 核）：
     - 症狀 2 ✅：壓力條件下（見步驟 3 的命令）修正前分別失敗 7/100、12/100、14/100、16/200，訊息全部是 `runtime_selection_test.go:131: cancel: error: failed to cancel task: task is not running: task status is failed`；runner 端為 `status=failed err="backend codex failed (empty_result): codex produced empty result"`，與 CI 一致。原因見「症狀 2 原因」
     - 症狀 2 不依賴時序的紅燈：`go test -count=1 -race -run 'TestRuntimeSelectionStartSeesCancelIntent$' -v ./cmd/asura` 修正前為 `runtime_selection_test.go:279: status=running, want cancelling`（行程確實被回收，只差狀態）
     - 症狀 1：未重現，依失敗路徑不修正；`TestRuntimeSelectionLocalCancel` 失敗時會印出 runner 端任務狀態與輸出（`1a8ef0f`），另開 [#51](https://github.com/raybird/Asura/issues/51) 追蹤
2. ✅ **AC-2：最小修正** — 驗證方式：重跑步驟 1 的重現命令轉綠，並跑本機最小回歸
   - 產出：測試或程式的最小修正；若改到 production 路徑，在「涉及檔案」補列並重新評估分級
   - 相依：步驟 1
   - 完成判準：同條件綠燈；diff 不含單純加長等待或重試
   - 完成證據（2026-10-13）✅ 症狀 2：
     - `TestRuntimeSelectionStartSeesCancelIntent` 修正後 PASS；`go test -count=1 -race -run 'TestRuntimeSelection' -v ./cmd/asura` 6 項全部 PASS
     - `go vet ./...` 通過；`go test -count=1 -race -v ./...` 23 個 package 全部 `ok`；`go build ./cmd/asura` 成功
     - 被 SKIP 的 14 項不計為通過，都是需要固定版本 runtime 或登入憑證的 live／contract 測試，本次沒有改到 `internal/backend`：TestLiveAdapter（2 處）、TestCodexProbe_BinaryVersion、TestCodexProbe_NonInteractive_JSONL、TestCodexProbe_ReadOnlySandbox、TestCodexProbe_WorkspaceWriteSandbox、TestCodexProbe_ContextCancellation、TestAdapter_Run_Live、TestCodexProbe_Live、TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly、TestUtilityLiveProbe、TestUtilityCapacityProbe、TestINT001、TestINT007
     - 修正是補上缺少的狀態轉移，沒有加長等待或重試
3. ✅ **AC-3：壓力對照** — 驗證方式：同一 CPU 限制與 `-race` 條件下跑 `-count=200`，修正前的數據可在步驟 1 取得；修正後 0 失敗，PR CI 一次全綠
   - 產出：壓力命令原文、條件與修正前後的失敗次數（寫回本節）
   - 相依：步驟 2
   - 完成判準：修正前至少失敗 1 次，修正後 0 失敗
   - 完成證據（2026-10-13）：
     - 執行檔：修正前、後各以 `go test -race -c ./cmd/asura` 建立（修正前版本只還原 `runtime_process.go`）
     - 條件：背景 6 個 `taskset -c 0,1 yes > /dev/null`，測試以 `taskset -c 0,1 ./asura.test -test.run '^TestRuntimeSelectionLocalCancel$' -test.count=200 -test.v` 執行
     - 結果：修正前 PASS 184／FAIL 16（全為症狀 2），修正後 PASS 200／FAIL 0
     - PR CI 一次全綠：PR #52 在 `01aaf4e` 的 [run 38366193444](https://github.com/raybird/Asura/actions/runs/38366193444) 為 success，`TestRuntimeSelectionLocalCancel`（0.84s）與 `TestRuntimeSelectionStartSeesCancelIntent` 均 PASS。同一 HEAD 的 push run 38366164679 被 workflow `concurrency.cancel-in-progress` 取消（conclusion=cancelled），不是測試失敗

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-10-13 | 建立；AC-1、AC-2 依 GitHub #48 完成判準核准；Q1 回答後核准 AC-3 | Raybird |
| 2026-10-13 | 規格修訂：症狀 1 本機未重現，依 Q2 回答將 AC-1 收斂為症狀 2，症狀 1 移至 #51 追蹤 | Raybird |
| 2026-10-13 | PR #52 獨立審查 PASS（審查 `f830ee6..35d334d`，[報告](https://github.com/raybird/Asura/pull/52#issuecomment-6141269578)），無 MUST FIX | Raybird |
| 2026-10-13 | PR #52 squash 合併至 main（merge commit `69900e9`，合併內容即審查的 HEAD `35d334d`）；GitHub #48 由 `Closes #48` 自動關閉，issue 結案 | Raybird |

---
**建立日期**: 2026-10-13  
**分級**: Small — 預期修正集中在單一測試或單一啟動／取消路徑；根因若跨多個 production 檔案，改評為 Medium  
**風險**: Medium — 根因未知，可能是產品缺陷而非測試時序問題\
**狀態**: 已完成 — PR #52 於 2026-10-13 合併（merge commit `69900e9`）；症狀 1 由 #51 追蹤
