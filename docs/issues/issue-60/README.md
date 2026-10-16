# Issue 60 - Flaky test：TestExecutionAuditConcurrentWriter 在 CI 反覆出現 audit_persistence_failed (Small)

## 概述

來源：[GitHub #60](https://github.com/raybird/Asura/issues/60)。`internal/storage/sqlite_execution_audit_test.go` 的 `TestExecutionAuditConcurrentWriter` 自 2026-10-15 起在 CI 反覆失敗，main 連續兩次 push 紅燈。目標是證實原因並修正，不以加長 timeout 或重試掩蓋。

| 日期 | CI run | 事件 | 結果 |
|---|---|---|---|
| 2026-10-15 | [38698781930](https://github.com/raybird/Asura/actions/runs/38698781930)（`0025307`） | pull_request | attempt 1 失敗，attempt 2 通過 |
| 2026-10-15 | [38701415915](https://github.com/raybird/Asura/actions/runs/38701415915)（`98bd866`） | pull_request | attempt 1 失敗，attempt 2 通過 |
| 2026-10-15 | [38704096080](https://github.com/raybird/Asura/actions/runs/38704096080)（main `402a407`） | push | 失敗（12.29s） |
| 2026-10-15 | [38706497426](https://github.com/raybird/Asura/actions/runs/38706497426)（main `96c302e`） | push | 失敗（13.19s） |

每次訊息都是 `sqlite_execution_audit_test.go:501: observation failed under concurrent metadata writer: audit_persistence_failed`。

### 現況分析（2026-10-16，探索期證據，見下方步驟 1）

**這個測試的來由**：#41 為了抓「deferred transaction 先讀、再升級寫鎖時 snapshot 已過期而立即失敗」的競態而加入。修正是 `beginAuditWrite`（`internal/storage/sqlite_execution_audit.go:390`）先執行 `UPDATE task_execution_audits SET digest=digest WHERE 0` 取得 writer lock，讓 busy_timeout 在 snapshot 建立前等待。#41 當時就遇過一次「writer 不停搶鎖、等超過 10 秒 busy_timeout」的失敗，改成每次更新間隔 1 ms 緩解，並明說測試不要求無限壓力下突破既有 timeout（`docs/issues/issue-41/evidence/verification.md:96`）。

**目前的 fixture**：第二條連線在整個 observation 期間不停執行 `UPDATE tasks SET updated_at=...`，每次間隔 1 ms，沒有上限。SQLite 的鎖沒有排隊機制，主連線的 busy handler 每次重試都可能又被搶走，等鎖時間呈長尾分布，沒有上限。

**CI 證據**（各 run 的 `--- PASS/FAIL` 耗時）：
- 2026-10-13 以前，本測試約 3～4.6 秒，storage 套件約 88～115 秒。
- 2026-10-15 起，**通過的 run 也要 9.5～20 秒**，storage 套件約 171～181 秒。失敗的兩次是 12～13 秒，符合「單次等鎖超過 10 秒 busy_timeout」。
- `0713d0e`（PR #55，2026-10-13，套件 88 秒）與 `dd5266c`（main，2026-10-15，套件 179 秒）內容相同，所以變慢來自 CI 環境，不是程式變更。GitHub #60 內文「#47 的 installer 測試增加負載」的推測不成立。

**產品行為判斷**：busy_timeout 10 秒是 #41 審查接受的既有契約（`docs/issues/issue-41/review-2769542.md:85`），等鎖逾時回報 `audit_persistence_failed` 是 fail-closed 的正確行為。daemon 只用單一連線（`SetMaxOpenConns(1)`），不會在行程內出現這種無上限的搶鎖。問題在於測試用無上限的寫入壓力，卻要求每一次 observation 都在 10 秒內成功。

## 涉及檔案

- `internal/storage/sqlite_execution_audit_test.go` — 競爭 writer 的寫入壓力改為每次 observation 有上限

## 驗收條件

1. **AC-1**：用可重複的手段證實原因：失敗發生在 `beginAuditWrite` 等 writer lock 逾時（底層錯誤為 `SQLITE_BUSY`，等滿 busy_timeout 才失敗），而不是 #41 的 snapshot 競態（拿掉 `UPDATE ... WHERE 0` 的舊程式在 append 階段 1 秒內失敗）。本機無法讓等鎖超過 10 秒時，以只用於診斷的較短 busy_timeout 取得同一機制的紅燈；診斷碼不進交付 — 檢查方式：記錄診斷方式、條件、失敗次數與底層錯誤
   - 失敗路徑：底層錯誤不是 begin 階段的 `SQLITE_BUSY`，或失敗沒有等滿 busy_timeout，則不依本分析修正；回報已嘗試的手段與結果，交由使用者決定
2. **AC-2**：只修改測試：競爭 writer 改為在每次 observation 期間並行執行固定次數的實際 `updated_at` 更新，寫完即停，使主連線的等鎖時間有上限；assertion 仍要求 40 次 observation 全部成功，production 程式（含 busy_timeout 與 `beginAuditWrite`）不變。新 fixture 仍須抓得到 #41 的競態：在拿掉 `UPDATE ... WHERE 0` 的舊程式上執行必須失敗。本機最小回歸（`go vet ./...`、`go test -count=1 -race -v ./...`、`go build ./cmd/asura`）全數通過 — 檢查方式：舊程式對照、回歸命令
   - 失敗路徑：修正是加長 timeout、加重試、略過 iteration 或放寬 assertion，或改動 production 程式，或新 fixture 在舊程式上不再失敗，都判定未通過
3. **AC-3**：壓力對照：相同 CPU 限制、負載、`-race` 與診斷用 busy_timeout 下，以 `-count=30` 執行，修正前至少失敗 1 次、修正後 0 失敗；另以正式 10 秒 busy_timeout 在 4 CPU 加負載下記錄修正後的單次等鎖最大值。PR CI 一次全綠作為補充，合併後 main push CI 結果於結案時記錄 — 檢查方式：記錄命令原文、條件與修正前後的失敗次數
   - 失敗路徑：修正後仍有任何失敗即未通過；修正前同條件 0 失敗時判準沒有鑑別力，不得當作通過，回報後重新決定條件

- **核准日期**: 2026-10-16
- **核准來源**: 2026-10-16 Q1（TBD-1）回答「核准，只修測試 (Recommended)」，接受建議：AC-1～AC-3 照草稿核准，只修改 `sqlite_execution_audit_test.go` 的 writer fixture，production 程式與 busy_timeout 不變；重現結果推翻分析時依 AC-1 失敗路徑停止回報。GitHub #60 由使用者於 2026-10-16 要求開立（「先開這個 issue」），同日以 `/dev-cycle issue:60` 要求推進
- **核准 commit**: 2f01b3b

## 風險與首要驗證

- **最大風險**：CI 上「單次等鎖超過 10 秒」本機重現不了（本機最長 5.5 秒），根因靠較短 busy_timeout 的等價重現與 CI 耗時推得；另一風險是新 fixture 削弱 #41 的回歸保護
- **風險等級與理由**：Medium — 有重要未知（CI 的實際錯誤無法直接取得）；修正範圍只在測試、容易回復
- **首要驗證**：取得失敗時的底層錯誤與等鎖時間，並以舊程式對照確認新 fixture 仍有鑑別力
- **選擇理由**：底層錯誤區分「等鎖逾時」與「snapshot 競態」兩種機制，舊程式對照直接回答修正是否掩蓋 #41 的問題
- **完成證據**：修正前的紅燈為 begin 階段 `SQLITE_BUSY`；新 fixture 在舊程式上仍失敗，在現行程式上 0 失敗

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|------|------|------|------|
| TBD-1 | AC-1～AC-3 是否核准；特別是 AC-2「只修測試、production 程式與 busy_timeout 不變」的範圍 | 已解決 | 2026-10-16 使用者核准，只修測試：10 秒 busy_timeout 與 fail-closed 是 #41 接受的既有契約，失敗來自測試無上限的寫入壓力 |

## 實作與驗證步驟

1. ✅ **AC-1：證實原因**— 驗證方式：暫時在 `AppendTaskAudit` 記錄底層錯誤與等鎖時間（不進交付），在負載下重跑；本機等不到 10 秒時改用較短的診斷用 busy_timeout
   - 產出：診斷方式、條件、失敗次數與底層錯誤（寫回本節）
   - 相依：無
   - 完成判準：失敗為 begin 階段 `SQLITE_BUSY` 且等滿 busy_timeout；舊程式對照為 append 階段快速失敗；不符時依 AC-1 失敗路徑停止並回報
   - 完成證據（2026-10-16，main `96c302e`，go1.27.1 linux/amd64，12 核；診斷碼只在 scratch worktree，未進入交付）：
     - 診斷：`AppendTaskAudit` 暫時記錄 `beginAuditWrite` 的底層錯誤與等待時間，各條件以 `go test -race -c` 建立的執行檔加 `-test.run '^TestExecutionAuditConcurrentWriter$' -test.v` 執行
     - 正式 10 秒 busy_timeout：無負載 `-count=10`、`taskset -c 0,1` 加 6 個 `yes` 的 `-count=10`、`taskset -c 0` 加 8 個 `yes` 的 `-count=20`、`taskset -c 0-3` 無負載與加 8 個 `yes` 各 `-count=30`，共 100 次全部 PASS，**本機重現不了 10 秒以上的等鎖**。單次等鎖中位數約 0.3 ms，但有長尾：各條件最大值為 2.73 秒、5.51 秒、3.04 秒、3.43 秒、3.14 秒；在 1 CPU 加負載下測試耗時 10.6～16.9 秒，與 CI 通過 run 的 9.5～20 秒相近
     - 等價重現：診斷版再把 busy_timeout 暫時縮成 1 秒，`taskset -c 4,5` 加 6 個 `yes`、`-count=30`：FAIL 9/30，9 次全為 begin 階段 `database is locked (5) (SQLITE_BUSY)`，等待 1.020～1.042 秒，也就是等滿 busy_timeout 才失敗；append、commit 階段 0 次錯誤
     - #41 競態對照：同一診斷版拿掉 `beginAuditWrite` 的 `UPDATE ... WHERE 0`，無負載 `-count=5`：FAIL 5/5，全在 append 階段，0.79～0.89 秒失敗，begin 階段 0 次錯誤
     - 結論：CI 的失敗是等 writer lock 逾時，與 #41 的 snapshot 競態是不同機制，符合「現況分析」
2. ✅ **AC-2：修正測試**— 驗證方式：舊程式對照與本機最小回歸
   - 產出：測試 fixture 的最小修正
   - 相依：步驟 1
   - 完成判準：diff 只在測試，assertion 未放寬；舊程式上失敗、現行程式上通過
   - 完成證據（2026-10-16）：
     - 修正：拿掉整段期間不停更新、間隔 1 ms 的 writer goroutine，改為每次 observation 前啟動 writer，並行做 3 次 `updated_at` 更新後結束，`AppendTaskAudit` 回傳後等 writer 結束再判斷結果；40 次 observation 全部成功的 assertion 未改，production 程式未修改
     - 舊程式對照：以 `go test -overlay` 只替換 `sqlite_execution_audit.go` 為拿掉 `UPDATE ... WHERE 0` 的版本，測試用交付版本，`taskset -c 4-11` 無負載 `-count=20`：FAIL 20/20，全為 `observation failed under concurrent metadata writer: audit_persistence_failed`，1.07～1.35 秒失敗。新 fixture 仍抓得到 #41 的競態
     - 本機最小回歸（交付版本）：`go vet ./...` 通過；`go test -count=1 -race -v ./...` 23 個 package 全部 `ok`，本測試 PASS（4.83s）；`go build ./cmd/asura` 成功
     - 被 SKIP 的 14 項不計為通過，都是需要固定版本 runtime 或登入憑證的 live／contract 測試，與本次修改無關：TestLiveAdapter（2 處）、TestCodexProbe_BinaryVersion、TestCodexProbe_NonInteractive_JSONL、TestCodexProbe_ReadOnlySandbox、TestCodexProbe_WorkspaceWriteSandbox、TestCodexProbe_ContextCancellation、TestAdapter_Run_Live、TestCodexProbe_Live、TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly、TestUtilityLiveProbe、TestUtilityCapacityProbe、TestINT001、TestINT007
     - 重構 no-op：修正只把無上限的 writer 改為每次 observation 固定次數，沒有可精煉的內容；沿用上述綠燈
3. ✅ **AC-3：壓力對照**— 驗證方式：同條件 `-count=30` 修正前後各一次；正式 busy_timeout 下記錄等鎖最大值；PR CI 一次全綠
   - 產出：壓力命令原文、條件與修正前後的失敗次數（寫回本節）
   - 相依：步驟 2
   - 完成判準：修正前至少失敗 1 次，修正後 0 失敗
   - 完成證據（2026-10-16）：
     - 條件：`go test -race -overlay` 把 `sqlite.go` 的 busy_timeout 暫時換成 1 秒（只用於壓力對照），修正前的測試檔取自 main `96c302e`，修正後為交付版本；各自 `go test -race -c` 建立執行檔，背景 6 個 `taskset -c 4,5 yes > /dev/null`，以 `taskset -c 4,5 ./<bin> -test.run '^TestExecutionAuditConcurrentWriter$' -test.count=30 -test.v` 執行
     - 結果：修正前 PASS 15／FAIL 15，修正後 PASS 30／FAIL 0
     - 正式 10 秒 busy_timeout、背景 8 個 `taskset -c 0-3 yes`、`taskset -c 0-3`、`-count=30`（加上步驟 1 的等待時間記錄，只用於量測）：修正後 PASS 30／FAIL 0，單次等鎖 1200 筆，中位數 0.44 ms、p99 0.186 秒、最大 0.244 秒。修正前同條件（步驟 1）最大 3.14 秒
     - PR CI 一次全綠：PR #61 在 `a5f723a` 的 [run 38841328343](https://github.com/raybird/Asura/actions/runs/38841328343) attempt 1 為 success，本測試 PASS（13.41s），storage 套件 138.3 秒
     - CI 耗時說明：13.41 秒是準備資料加上 40 次 observation 依序執行的總和，不是單次等鎖。本機以 `go test -overlay` 在交付版本測試上加分段計時（只用於量測）：無負載時準備 0.72～0.75 秒、40 次 append 合計 2.35～2.38 秒、單次最長 65 ms；`taskset -c 0-3` 加 8 個 `yes` 時準備 1.47～1.70 秒、append 合計 4.48～4.79 秒、單次最長 144 ms。writer 每次 observation 只做 3 次更新就停，單次等鎖不會隨壓力時間無限拉長；CI 的單次等鎖值沒有直接量測

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-10-16 | 建立；Q1 回答後核准 AC-1～AC-3（只修測試） | Raybird |
| 2026-10-16 | PR #61 獨立審查 PASS（審查 `96c302e..18f78fa`，[報告](https://github.com/raybird/Asura/pull/61#issuecomment-6207030303)），無 MUST FIX、SHOULD FIX；NICE TO HAVE 兩項（writer 成功次數斷言、診斷片段存檔）未處理 | Raybird |
| 2026-10-16 | PR #61 squash 合併至 main（merge commit `e07327a`，合併內容即審查的 HEAD `18f78fa`）；GitHub #60 由 `Closes #60` 自動關閉。合併後 main push CI [run 38843697285](https://github.com/raybird/Asura/actions/runs/38843697285) attempt 1 為 success，本測試 PASS（15.14s），main 恢復綠燈；issue 結案 | Raybird |

---
**建立日期**: 2026-10-16  
**分級**: Small — 只修改單一測試檔  
**風險**: Medium — CI 的 10 秒以上等鎖本機重現不了，根因由等價重現與 CI 耗時推得\
**狀態**: 已完成 — PR #61 於 2026-10-16 合併（merge commit `e07327a`）
