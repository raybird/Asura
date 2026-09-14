# 程式碼審查報告

審查日期：2026-09-14

## 變更摘要

審查範圍為 `main...f82dd57`（合併基底 `2530375`），涵蓋 Issue #6（Phase 2B 容量政策與動態 Cron 排程管理）整個分支。上一份審查 `review-143b03a.md` 審的是 `143b03a`，之後新增兩個純文件提交（`d5d9c3b`、`f82dd57`），patch-id 已不同，所以本報告是對目前 HEAD 的完整重審，不是只看增量。

主要異動模組：

| 模組 | 檔案 | 內容 |
|---|---|---|
| Cron 解析與排程 | `internal/cron/parser.go`、`internal/cron/scheduler.go` | 5 欄位解析、`Next`／`IsReachable`、`Trigger`（重疊略過、未授權自動暫停）、`AlignSchedules`（不補跑） |
| Daemon 容量調度 | `internal/daemon/daemon.go`、`internal/daemon/pool.go`、`internal/daemon/workspace_queue.go` | 同專案互斥佇列、互動／Cron 類別容量、`cronLoop`、`cron_*` IPC、`dispatchCronTask`、`wakeQueuedTask` |
| 儲存層 | `internal/storage/sqlite.go` | `cron_schedules`、`cron_runs` 資料表與 CRUD，新增 `queued` 任務狀態 |
| CLI | `cmd/asura/cron.go`、`cmd/asura/run.go` | `asura cron add/list/pause/resume/remove/runs` |
| 引擎 | `internal/engine/engine.go` | 接受 `queued` 狀態的任務 |
| 測試 | `tests/integration/cron_test.go` 與各套件 `*_test.go`；修改 `tests/integration/cli_test.go`、`tests/integration/web_test.go` | |
| 文件 | `README.md`、`docs/issues/issue-6/**` | |

## 審查版本

- Reviewed HEAD SHA：`f82dd570cf69686f1f0fbaffd3e2272372596c3d`
- 獨立 reviewer：Claude Code 主審（非原實作者），加上 3 位分工的獨立 reviewer subagent：(A) Daemon 併發與容量調度、(B) Cron 引擎／儲存／CLI／安全性、(C) 測試證據與驗收標準稽核
- Review artifact：`docs/issues/issue-6/review-f82dd57.md`
- Reviewed patch-id：`0e5ab6ca555f5f086fce86310ef4c9480f9aa0ba`（`git diff main...f82dd57 | git patch-id --stable`）

## 整體評估

**不建議合併，判定 `RETURN TO execute-task`。**

架構方向正確：`cron` 透過 `TaskDispatcher` 回呼交給 `daemon` 調度，依賴是單向的；授權檢查在新增、觸發、執行三個時點都走同一個 `AuthorizeWorkspace`。但本次重審實際找到 10 項 MUST FIX，多數都已用重現測試或突變測試實跑確認：

1. **排程時鐘在夏令時間時區會卡死整個 daemon**：`Next` 陷入無窮迴圈，主審也獨立重現。
2. **同專案佇列的喚醒路徑有多處缺陷**，直接違反 CRON-001／CRON-002，而且 issue 文件列的最大風險「同專案多任務並行造成原始碼毀損」仍然成立：
   - 喚醒時沿用錯誤的任務類別
   - 喚醒時前一個任務還沒釋放 pool 槽位
   - workspace 路徑沒有正規化
   - 重啟後殘留的 `queued` 任務不會被處理
   - 派工失敗時沒有處理佇列交接
3. **派工失敗時不推進 NextRunAt**，每秒產生一筆 failed 任務。
4. **`cron list` 以 UTC 顯示下次觸發時間**，違反「主機本地時區」的設計決策。
5. **驗收證據不完整**：
   - 實作期間把 Scenario ID 改成配合測試名稱，與核准 Gherkin 錯位。
   - CRON-004「預定時間點抵達」與 CRON-006「Daemon 重新啟動」兩個 When 子句零覆蓋，把對應程式碼拿掉，全套測試仍然綠燈。
   - 修復階段（remediation）的紅燈日誌無法從提交歷史重現。

前兩份審查（`review-a42c088.md`、`review-143b03a.md`）判定「CRON-001～008 完整覆蓋、紅綠證據真實完備」，本次查核推翻了這個結論。那是前次審查的判斷失準，本報告沒有發現實作者捏造證據：Phase 1～4 的日誌與各自提交逐行相符。

## 需求符合度

- 需求描述：未另外提供 `TASK_DESCRIPTION`，以 Issue #6 README 的核准 Gherkin（CRON-001～008）與 PR #7 為準。
- 是否符合需求：**部分符合**。
- 缺漏：
  - CRON-001、CRON-002：在「cron 先執行、互動任務排隊」這個順序，以及容量接近飽和時，行為違反劇本（見 MUST FIX 2、3、4）。
  - CRON-003：`list` 顯示的下次觸發時刻錯誤（MUST FIX 7）。
  - CRON-004、CRON-006：只在函式層級驗證，沒有經過真實觸發迴圈或 Daemon 重啟（MUST FIX 9）。
- 超出核准範圍：
  - `asura cron runs` 不在核准 Gherkin 裡。它是唯讀的支撐功能，也是 CLI 唯一能看到 `skipped`／`unauthorized` 紀錄的方式，判定為合理的支撐性實作。但它被標成 CRON-007，應在 issue 文件中改記為範圍補充。
  - 日（DOM）與週（DOW）同時指定時採交集（AND），偏離標準 cron 的聯集（OR）語意，找不到使用者的決策紀錄（SHOULD FIX 10）。

## 規格修訂查核

- 指令：`git diff df5b48a:docs/issues/issue-5/README.md HEAD:docs/issues/issue-6/README.md`。核准 commit 當時的路徑是 `issue-5/`，在 `b7cf0ab` 改名為 `issue-6/`。
- Gherkin 區塊（CRON-001～008）逐字不變；核准表 8 項都是「已核准」，採逐項表格形式；README 沒有其他區塊殘留 `待核准`／`待重新核准`。
- README 的「實作步驟概要」與 Timeline 對 Scenario ID 的標註與核准 Gherkin 不一致，例如步驟 4 標 CRON-006/007、Timeline 寫「CRON-005 未授權自動暫停」。這不構成規格修訂，但屬於追溯錯位，見 MUST FIX 9。

## 問題與風險

嚴重度標籤說明：
- 🔴 **MUST FIX**：邏輯錯誤、安全漏洞，必須在 merge 前修正
- 🟡 **SHOULD FIX**：潛在問題、邊界未處理，建議修正
- 🟢 **NICE TO HAVE**：可讀性、最佳實踐，可列入後續 backlog

### 🔴 MUST FIX

**🔴 1. 有夏令時間的時區下 `Schedule.Next` 無窮迴圈，daemon 整個卡死**
- 位置：`internal/cron/parser.go:155`、`internal/cron/parser.go:165`、`internal/cron/parser.go:172`
- 原因：快進到下個月、隔天、下一小時時用 `time.Date(...)` 重建時間。遇到不存在的本地時刻，負 UTC 偏移的時區會被正規化成較早的時刻，`t` 因此停住不前進；`maxTime` 上限擋不住沒有前進的迴圈。
- 重現：`America/New_York` 下 `0 9 * * *` 從 2027-03-13 10:00 起算，`Next` 3 秒內沒有回傳。主審在 HEAD 副本上實跑確認；reviewer B 另外確認 `30 2 * * *`、`0 3 * * *`、`America/Los_Angeles`、`America/Santiago` 都會卡死，Europe/Berlin、Asia/Taipei 正常。
- 影響：美國主機上的每日排程，每年 DST 開始前一天必定觸發。
  - `cronLoop` 卡在 `Trigger` 裡，所有排程停擺。
  - 重啟時 `Start()` 卡在 `AlignSchedules`（`internal/daemon/daemon.go:265`），`acceptLoop` 不會啟動。
  - `cron add`、`cron resume` 的 `IsReachable`（`cmd/asura/cron.go:49`）會卡住並吃滿 CPU。
  - `Close()` 無法正常結束。
- 建議：以 UTC 做欄位遞增，或每次快進後保證 `t` 嚴格前進；另外補 DST 時區的單元測試，期望值用人工推導的字面值。

**🔴 2. 喚醒排隊任務時沿用「剛完成那個任務」的類別**
- 位置：`internal/daemon/daemon.go:680-682`、`internal/daemon/daemon.go:716`
- 原因：`wakeQueuedTask(nextTaskID, workspace, class)` 傳入的是前一個任務的類別，而任務類別沒有持久化。
- 情境 A（違反 CRON-002）：`MaxWorkers=2`、`MaxCronWorkers=1`。
  1. 互動任務 i1 在專案 A 執行、cron-B 在專案 B 執行、cron-A 在專案 A 排隊。
  2. i1 完成後，cron-A 被當成互動任務派工，兩個 cron 同時佔滿兩個 worker。
  3. 新提交的互動任務無法立即啟動。
- 情境 B（違反 CRON-001）：cron 在專案 A 執行，同專案的互動任務排隊。cron 完成後，互動任務被當成 cron 派工，被 cron 上限擋下，結果被標成 `failed: capacity exceeded`。
- 確認：reviewer A 與 C 各自寫重現測試實跑失敗，主審讀碼確認。

**🔴 3. 喚醒時前一個任務還佔著 pool 槽位，排隊任務因此被標成 failed**
- 位置：喚醒發生在 worker 函式的 defer 中（`internal/daemon/daemon.go:678-683`），但 pool 要等函式回傳後才在 `internal/daemon/pool.go:199-203` 釋放 `running`／`runningCron`。派工被拒時，`internal/daemon/daemon.go:716-719` 直接把排隊任務標成 failed。
- 情境：`MaxWorkers=2`、`MaxCronWorkers=1`、`MaxQueue=1`。
  1. cron-A1 執行中、cron-B 在 pool 佇列、同專案 cron-A2 在專案佇列。
  2. A1 完成後，A2 變成 `failed: capacity exceeded: worker pool saturated`。
- 預設設定（2 workers／10 queue）下，大約 11 個排程同一分鐘觸發、其中一個專案有兩個排程時就會遇到。
- 確認：reviewer A 實跑重現。這個問題與 MUST FIX 2 無關，兩邊都是 cron 任務也會發生。

**🔴 4. 專案鎖的 key 沒有正規化，加一個尾斜線就能繞過同專案互斥**
- 位置：`internal/daemon/daemon.go:201`、`internal/daemon/daemon.go:515`、`internal/daemon/workspace_queue.go:26` 直接用原始字串當 key；授權檢查（`internal/auth/policy.go:40-57`）卻有做 `EvalSymlinks` 和 `Clean`。
- 情境：`/root/A` 正在執行時提交 `/root/A/`，回傳 `pending`，兩個任務同時執行（reviewer A 實跑確認）。依讀碼推理，`/root//A`、`/root/A/.`、指向同一目錄的 symlink、子目錄 `/root/A/sub` 也都會繞過。
- 影響：issue 文件列的最大風險「同專案多任務並行造成原始碼毀損」沒有被消除。
- 建議：用 policy 解析後的標準路徑當 key，並明確決定「專案」的粒度是授權根目錄還是 git 根目錄。

**🔴 5. 重啟後殘留的 `queued` 任務永遠不會被處理，取消它時還會釋放錯誤的容量**
- 位置：`internal/storage/sqlite.go:498-506`（`RecoverInterruptedTasks` 只處理 `running`、`pending`）；取消路徑在 `internal/daemon/daemon.go:411-414`。
- 情境：daemon 崩潰時有任務在排隊。重啟後它永遠是 `queued`，記憶體裡的專案佇列已經沒有它，也沒有人會喚醒。使用者取消它時會執行 `releaseCapacity()`，釋放到的其實是別的任務佔的名額（實測另一個任務還在執行，inFlight 就從 1 變成 0）。
- 影響：破壞既有 SCN-006「重啟揭露中斷工作」的保證。
- 確認：reviewer A 與 B 各自實跑確認，主審讀碼確認。

**🔴 6. 排程派工失敗時不推進 NextRunAt，每秒產生一筆 failed 任務**
- 位置：`internal/cron/scheduler.go:131-134`、`internal/daemon/daemon.go:207-214`、`internal/daemon/daemon.go:296-297`
- 原因：`Trigger` 在推進 NextRunAt 之前就回傳派工錯誤，下一個 tick 又重來一次。沒有寫入 `cron_runs`，`CronRunFailed` 從未被使用。
- 重現：`MaxQueue=0`，兩個不同專案的排程同時到期，約 7 秒內第二個專案累積 6 筆 failed 任務（reviewer B 實跑）；reviewer A 另一組設定下，3.5 秒內 3 筆（R9）。容量在更早的階段就不足時，雖然不會寫入任務，仍會每秒重試，等容量空出來再延遲補跑一次。
- 建議：無論派工成功與否都先推進 NextRunAt（至多一次語意），並把派工失敗記成一筆 `cron_runs`。

**🔴 7. `cron list`／`cron runs` 以 UTC 顯示時間，沒有標示時區**
- 位置：寫入時轉 `.UTC()`（`internal/storage/sqlite.go:537-540`），經過 IPC JSON 往返後仍是 UTC；顯示時直接 `Format`（`cmd/asura/cron.go:113`、`cmd/asura/cron.go:259-260`）。
- 重現：在 Asia/Taipei 新增 `0 3 * * *`，list 顯示 `2026-09-14 19:00:00`，實際時刻是 2026-09-15 03:00（reviewer B 實跑，主審讀碼確認）。
- 影響：CRON-003「可透過 list 查看下次觸發時刻」的輸出錯誤，違反主機本地時區的設計決策。
- 建議：顯示前轉 `.Local()`，或輸出時帶上時區。

**🔴 8. 派工失敗路徑沒有處理 `wsQueue.Release` 的回傳值，專案鎖可能永久卡死**
- 位置：`internal/daemon/daemon.go:212`（cron）、`internal/daemon/daemon.go:531`（submit）
- 情境：T1 已取得專案鎖但被 pool 拒絕，在它呼叫 `Release` 之前，同專案的 T2 已經排進佇列。`Release` 把持有權交給 T2，但沒有人喚醒 T2。
- 影響：T2 永遠停在 `queued`，該專案之後的所有任務都排在它後面，每筆還佔一格 inFlight，最後整個 daemon 拒絕新任務。
- 確認：程式缺陷已由讀碼確認。時間窗是微秒級，壓力測試沒有重現；但後果嚴重，而且修法很小（比照 `wakeQueuedTask` 處理回傳的下一個任務）。

**🔴 9. 驗收標準覆蓋缺口：Scenario ID 被重編，核准子句沒有測試**
- **重編來源**：原始拆解 `6eab1c6` 的對應是正確的。`f05dd94`（Phase 3）與 `02fdb92`（Phase 4）回頭改寫了 `docs/issues/issue-6/implementation-plan-decomposition.md`，讓 Scenario ID 配合實際寫出的測試名稱：解析被標成 CRON-003、重疊與不補跑成 CRON-004、未授權成 CRON-005、CLI 成 CRON-006/007。覆蓋核對表（`implementation-plan-decomposition.md:320-329`）還列著從沒寫過的測試，例如 `TestCRON004_PeriodicTriggerCreatesTask`、`TestCRON006_RestartNoCatchup`。README 的步驟概要（`docs/issues/issue-6/README.md:146-147`）與 Timeline（`:165-166`）沿用了錯位的編號。
- **零覆蓋**（突變後全套仍綠）：
  - CRON-004 When「預定時間點抵達」：`cronLoop`（`internal/daemon/daemon.go:280-301`）收到 ticker 後直接 `continue`，全套仍綠。所有測試都手動呼叫 `Scheduler.Trigger`。
  - CRON-006 When「Daemon 重新啟動」：把 `Start()` 裡的 `AlignSchedules`（`internal/daemon/daemon.go:265`）拿掉，全套仍綠。
- **缺斷言**（突變存活）：
  - CRON-003 CLI `list` 顯示的下次觸發時刻與狀態沒有測，而且 add/pause/resume/remove 只走 IPC client，沒有經過 `asura cron` CLI。
  - CRON-005「不產生新任務」：略過後照樣派工，突變仍存活。
  - CRON-007「記錄拒絕原因」：`PauseReason` 沒斷言，`internal/cron/scheduler.go:83-90` 的 `CronRun.Reason` 也沒填。
  - CRON-008 只重開 store，沒有重啟 Daemon；workspace、schedule、prompt 與 paused 狀態跨重開都沒驗。
- 建議：
  - 把測試名稱與註解改回核准 Gherkin 的編號。
  - 補上走真實 `cronLoop` 的測試（可注入時鐘或縮短 tick）和真實 Daemon 重啟測試。
  - 用建好的 binary 實跑 `asura cron add/list`，可沿用 `tests/integration/cli_test.go:149` 的做法。

**🔴 10. 紅綠重構證據不完整，修復階段的紅燈無法從提交重現**
- **修復階段紅燈與提交不符**：`docs/issues/issue-6/evidence/remediation/outer-red.log` 記錄 `cron_test.go:840:17 d.Scheduler undefined`，但 `143b03a` 的第 840 行是 `waitForSocket`，真正的呼叫在 876、1076、1079 行。以 `a42c088` 加上 `143b03a` 的測試實際重現，會得到 6 個錯誤，而日誌只有 3 個。可見紅燈是在 production code 已部分修改、測試檔也和提交版本不同的狀態下擷取的。
- **缺少行為紅燈**：前次兩項 MUST FIX 的修復（排程繞過專案鎖、不可達日期）都沒有行為紅燈；`TestCRON003_UnreachableDateRejection` 與 `TestCRON002_DaemonCronCapacityLimit` 連紅燈日誌都沒有。
- **紅燈都是編譯失敗**：Phase 1～4 的外迴圈與內迴圈紅燈全是 `undefined`／找不到 package。這只證明 API 還不存在，不證明斷言抓得到缺少的行為。reviewer C 實證，在 Phase 2 補上沒有行為的 stub 後，可以得到真正的行為紅燈（`cron_test.go:217 got "pending"`），能取得卻沒有紀錄。
- **對照核准編號**：CRON-004 沒有外迴圈紅燈；CRON-006 只有函式層級的紅燈。
- **日誌格式**：Phase 2 之後的日誌缺少 `docs/AGENTS.md`「紅綠重構證據」要求的驗收標準編號、實際命令、exit code 與日期。
- 判定依據：`docs/AGENTS.md`「紅綠重構證據」要求每段記錄「與目標行為相關的失敗原因」，而且證據必須可重複確認。

### 🟡 SHOULD FIX

1. **`IsReachable` 只搜 5 年，會誤判合法表達式**（`internal/cron/parser.go:148-149`）
   - `0 0 29 2 1` 新增時直接被拒（下一次實際在 2044 年）。
   - `0 0 29 2 2` 可以新增，但 2028-02-29 觸發後算不到下一次，就被自動 paused，原因寫 `unreachable schedule date`。
   - 2/29 搭配星期幾最長約 40 年一輪，搜尋窗口應至少 40～50 年。
2. **秋季回撥那天重複觸發**（`internal/cron/parser.go:177-178`）
   - New York 的 `30 1 * * *` 在 2026-11-01 觸發兩次。
   - 春季跳時那天的排程會整天被跳過，至少要在 `README.md` 說明。
3. **排隊任務一直佔著全域容量**（`internal/daemon/daemon.go:67-77`、`:204`、`:519`）
   - `MaxQueue=0` 時，同專案 cron 排隊會讓其他專案的互動任務被拒絕，而第二個 worker 是閒的（reviewer A 實跑）。
   - 小佇列設定下這也會違反 CRON-002，建議與 MUST FIX 2、3 一併重新設計容量計算。
4. **取消 `queued` 任務時容量可能被重複釋放**（`internal/daemon/daemon.go:411-414`）
   - 同一任務同時收到多個 cancel，inFlight 從 2 直接變 0（實跑確認）。
   - cancel 與喚醒同時發生時也會重複釋放（讀碼推理）。
5. **先取得專案鎖、後寫入 `queued` 狀態，中間有競態**（`internal/daemon/daemon.go:201-204`、`:515-519`，寫入錯誤被忽略）
   - 喚醒時可能讀到 `pending` 而把任務跳過，之後它永遠 `queued`。
   - 在這個時間窗內被取消的任務，會被無條件的 `queued` 寫入覆蓋，之後照樣被喚醒執行。
   - 以上是讀碼推理，時間窗很窄。
6. **設定邊界沒有驗證，保留的互動 worker 可能直接消失**
   - `internal/daemon/pool.go:76-78` 只把 `MaxCronWorkers` 壓到 `MaxWorkers`，而不是 `MaxWorkers-1`。
   - `--workers 1` 時沒有任何警告，保留通道實際上不存在。
7. **非終態任務會讓排程永久被略過**（`internal/cron/scheduler.go:105-108`，搭配 `internal/engine/engine.go:145-147`）
   - engine 在執行時授權失敗會直接 return，不更新狀態，任務停在 `queued`。
   - 使用者修好目錄並 resume 後，每次觸發仍被 `skipped`。
8. **`RecordCronRun` 失敗時失去重疊保護**（`internal/cron/scheduler.go:149-154`）
   - 任務已經派出，卻刪掉 `runningRuns` 又不推進 NextRunAt，下一個 tick 會重派一個重複任務。
   - `cron remove` 與 `Trigger` 同時發生時（外鍵違規）就會走到這條路徑。
9. **斷言鑑別力不足，屬於證據持久力問題，不是假綠燈**
   - `cmd/asura/run_test.go` 的「cron add 缺少必要參數」只檢查 exit code 非 0。把必填檢查改成 `if false` 仍然 PASS，因為後面的解析或連線也會回非 0。應比對錯誤訊息。
   - 突變 M5 存活：CRON-006 的下一個觸發點只檢查「晚於現在」，應以字面值比對，例如 `0 2 * * *` 應為隔天 02:00。
10. **日與週同時指定時採 AND，偏離標準 cron 的 OR 語意**
    - `README.md:133` 在前次審查後才補上說明，找不到使用者的決策紀錄，也沒有測試。
    - 建議列入 README 的 `## 待確認事項`，交給使用者決定。
11. **測試失敗時會卡住，而不是乾淨地 FAIL**
    - 位置：`tests/integration/cron_test.go:139-177`、`:809-838`、`:1001-1042`，以及 `:489` 沒有逾時的 `<-taskStarted`。
    - `defer d.Close()` 在阻塞的 channel 釋放前就執行，突變測試時都是先 Fatal、再卡到 timeout panic。
12. **靠時序的斷言**
    - `tests/integration/cron_test.go:886` 在 cron 不可能啟動的時間點檢查 `maxConcurrent`，永遠為真；實際抓到問題的是 `:906`。
    - `:1091` 睡 50ms 後才檢查並行數。
    - `TestCRON002_DaemonCronCapacityLimit` 沒有斷言 cron 2 最終有執行。
13. **修改既有測試沒有記錄理由**
    - `tests/integration/web_test.go` WEB008 把 A、B 移到不同子目錄：這是配合 CRON-001 的必要調整，斷言沒有弱化，但「同專案取消 A、排隊中的 B 要能接續執行」這條路徑因此失去覆蓋。
    - `tests/integration/cli_test.go` G01 把固定 sleep 改成輪詢，屬於強化。
    - 依 `docs/AGENTS.md`「假綠燈」，兩處都要記錄真實理由。

### 🟢 NICE TO HAVE

- **解析器**
  - `1/5` 被悄悄解析成只有分鐘 1，vixie cron 是 `1-59/5`。
  - 超大 step（`59/9223372036854775807`）在 `internal/cron/parser.go:130` 的 `i += step` 會整數溢位。
  - `+5` 會被接受。
- **排程觸發**
  - `resume` 不重新檢查授權（`internal/daemon/daemon.go:614-641`），而且 `:635` 的錯誤被忽略。觸發時仍會攔下，所以沒有安全漏洞。
  - skipped 路徑在 Next 為零值時會寫入零值 NextRunAt，排程停在 active 卻永遠不會觸發（`internal/cron/scheduler.go:122-125`）。
  - `ScheduledTime` 記錄的是 `now`，不是原本預定的時刻。
  - `cronLoop` 列出排程之後，`Trigger` 不再檢查狀態，暫停後可能再觸發一次。
  - 系統休眠喚醒或時鐘往前跳時會立即觸發一次，規格沒有決定要不要這樣。
- **儲存層與 CLI**
  - 外鍵 PRAGMA 只在開庫時設定一次，建議移到 DSN。
  - `tasks` 表沒有 `cron_id`，刪除排程後任務與排程的關聯就消失。
  - `cron list` 沒顯示 `pause_reason`。
  - 名稱含 tab 或換行會打亂表格。
  - ID 後面多出來的參數會被靜默忽略。
  - IPC client 沒有讀取逾時，daemon 卡住時 CLI 會跟著卡住。
- **Daemon**
  - `internal/daemon/pool.go:56-59` 的 if 分支兩邊都設 `maxCron=1`，是無效程式碼。
  - IPC 請求可以自行指定 `Class`（`internal/daemon/daemon.go:524-527`），而且不做驗證。
  - `internal/engine/engine.go:171` 現在接受 `queued`，所以 `asura run --id <queued-id>` 指向同一個 DB 時可以繞過 daemon 的專案鎖。
  - Shutdown 時，pool 佇列裡的任務會執行完，專案佇列裡的任務則被標成 failed，行為不一致。
- **測試與文件**
  - `internal/daemon/daemon_test.go:1068` 忽略 `afterRemove` 的錯誤。
  - `TestCRON003_UnreachableDateRejection` 只斷言 `err != nil`，沒有比對錯誤訊息。
  - README「涉及檔案」列的 `internal/daemon/scheduler.go` 與實際的 `internal/cron/` 不符。
  - `2c8855d` 覆寫了 README Timeline 原本的核准列，違反時序保留原則。

## 驗收標準與測試證據

- **驗收標準覆蓋**（依核准 Gherkin 編號，逐子句對照）：

  | Scenario | 判定 | 說明 |
  |---|---|---|
  | CRON-001 | 部分 | 「queued」與「依序執行」有斷言（`tests/integration/cron_test.go:219`、`:236`、`:905`、`:920`），但喚醒路徑的缺陷沒有被測試抓到 |
  | CRON-002 | 部分 | 初次派工有斷言（`:307`、`:1107`），排隊後被喚醒的路徑是壞的 |
  | CRON-003 | 部分 | 只走 IPC；CLI list 顯示沒有測 |
  | CRON-004 | ❌ | When 子句零覆蓋 |
  | CRON-005 | 部分 | 有 skipped 紀錄的斷言，「不產生新任務」沒有 |
  | CRON-006 | ❌ | When 子句零覆蓋，只有函式層級驗證 |
  | CRON-007 | 部分 | unauthorized 與 paused 有斷言，拒絕原因沒有 |
  | CRON-008 | 部分 | 只重開 store，沒有重啟 Daemon |

- **紅綠重構證據**：
  - Phase 1～4 日誌中的檔名、行號與錯誤訊息與各自提交逐行相符。reviewer C 以 worktree 重現 Phase 2、Phase 3 的紅燈，與日誌一致。
  - remediation 階段的紅燈與提交不符（MUST FIX 10）。
- **實跑結果**（2026-09-14，reviewer C）：

  | 命令 | 結果 |
  |---|---|
  | `go vet ./...` | exit 0 |
  | `go test -race -count=1 ./...` | FAIL，3 分 28 秒，0 DATA RACE；失敗只有 `internal/backend/opencode` TestINT001Unit、`tests/integration` TestINT001／TestINT007，錯誤為 `opencode execution failed: exit status 1` |
  | main 分支 `go test ./internal/backend/opencode -run TestINT001Unit` | 同樣 FAIL，屬本機 opencode CLI 環境因素，不是本分支造成的回歸 |
  | cron 相關子集 `-race -count=3`（含 WEB008、G01） | 全部 PASS 三次，0 DATA RACE |

- **誠實回報與防作弊**：
  - 沒有發現刪除、略過既有測試，或同義反覆的斷言。
  - 前次審查報告宣稱「完整覆蓋」「紅綠證據真實完備」與事實不符，本報告正式取代該結論。
  - remediation 紅燈日誌與提交不符，應重新擷取可重現的證據，不得沿用。
- **Gate 豁免查核**：無。README 沒有 `## Gate 豁免紀錄`。
- **待確認事項**：README 沒有 `## 待確認事項`。建議把 DOM/DOW 的 AND 語意（SHOULD FIX 10）與 DST 行為政策（SHOULD FIX 2）列入。

## 破壞性邊界條件

| # | 輸入／狀態 | 預期結果 | 現有覆蓋 | 判定 |
|---|---|---|---|---|
| 1 | 美國時區主機、每日 09:00 排程，時間來到 DST 開始前一天 | 算出隔天 09:00 EDT | 無 | **MUST FIX 1**：`Next` 無窮迴圈，daemon 卡死 |
| 2 | cron 在專案 A 執行中，使用者提交專案 A 的互動任務（排隊），`--queue 0` | cron 完成後互動任務以互動類別執行 | 無（現有測試只測「互動先、cron 後」） | **MUST FIX 2**：互動任務被標成 `failed: capacity exceeded` |
| 3 | 專案 A 任務執行中，提交 `/root/A/`（尾斜線） | 被判定為同專案並回傳 `queued` | 無 | **MUST FIX 4**：回傳 `pending`，兩個任務同時修改同一目錄 |
| 4 | daemon 崩潰時有任務處於 `queued`，接著重啟 | 標成 failed 並揭露中斷 | 無 | **MUST FIX 5**：永遠停在 `queued` |
| 5 | cron 容量飽和時另一排程到期 | 排隊，或略過一次並記錄 | 無 | **MUST FIX 6**：每秒產生一筆 failed 任務，之後延遲補跑 |
| 6 | 排程 `0 0 29 2 1` | 接受（下一次在 2044 年） | 無 | **SHOULD FIX 1**：被誤判為不可達 |

## 架構符合度

- 依賴方向：`internal/cron` 只依賴 `internal/auth`、`internal/storage`；`internal/daemon` 依賴 `internal/cron`、`internal/engine`、`internal/storage`、`internal/auth`；`internal/storage` 沒有依賴其他內部套件。沒有反向依賴或循環（以 `go list -f '{{.Imports}}'` 查核）。
- `cron.Scheduler` 以 `TaskDispatcher` 回呼交出調度責任，職責切分合理。
- 缺點：專案鎖的持有權交接散落在 `dispatchTask`、`wakeQueuedTask`、`dispatchCronTask`、submit、cancel 五處，各自手動處理 `releaseCapacity` 與 `Release` 的回傳值。MUST FIX 2、3、5、8 與 SHOULD FIX 4、5 都源自這裡。建議收斂成單一的「釋放並交接」函式。
- `internal/web`、`internal/backend`（標為不可觸及）確實沒有被改動。
- 依據與限制：專案沒有 `docs/architecture/` 文件，分層判定只依據 `docs/plans/post-core-roadmap.md` 的敘述與實際 import 關係，查核依據有限。

## 安全性檢查

- **存取控制與授權**：新增（`internal/daemon/daemon.go:556`）、觸發（`internal/cron/scheduler.go:81`）、執行（`internal/engine/engine.go:145`）三個時點都走 `AuthorizeWorkspace`，每次都做 `EvalSymlinks`，擋得住 symlink 與 `..` 逃逸。
- **互斥與完整性**：workspace key 沒有正規化，可以繞過同專案寫入互斥（MUST FIX 4）。這不是授權繞過，但正是本 issue 要防的檔案毀損風險。
- **能力擴張**：
  - IPC 請求可以自行指定 `Class`，`engine` 接受 `queued` 後，`asura run --id` 可以繞過專案鎖（NICE TO HAVE）。
  - `cron_add` 讓「能連上 socket」的影響從單次執行擴大為持久化的定時執行。socket 權限在本次 diff 中沒有變更，但這個擴大應在文件中揭露。
- **輸入驗證與 injection**：
  - SQL 全部參數化。
  - 解析器拒絕 `*/0`、反向範圍、空欄位、負數、超大數、全形數字，沒有會 panic 的輸入。
  - 提示詞含換行或引號時，JSON 往返後正確。
- **敏感資料**：沒有硬編碼的 secret 或環境變數外洩。
- **可用性**：DST 無窮迴圈（MUST FIX 1）與派工雪崩（MUST FIX 6）屬於本機阻斷服務類的缺陷。

## 程式品質與效能

- **可讀性**：命名與註解風格一致，但註解中的 Scenario ID 與核准 Gherkin 錯位，會誤導後續維護者。
- **維護性**：
  - 容量與專案鎖的釋放邏輯在多處重複（見架構符合度）。
  - `wakeQueuedTask` 以遞迴呼叫處理連續失敗，佇列很長時可能產生很深的呼叫堆疊。
- **效能**：`cronLoop` 每秒列出全部排程，目前規模可以接受；`Next` 採逐分鐘遞增，修正 DST 時需一併確認搜尋上限的成本。

## 改進建議

1. **修復 `Next`**：改用 UTC 或保證每步嚴格前進，補齊 DST 與 2/29 的字面值測試，並把 `IsReachable` 搜尋窗口放寬到至少 40～50 年（MUST FIX 1、SHOULD FIX 1、2）。
2. **重構專案佇列交接**，一次修掉 MUST FIX 2、3、5、8 與 SHOULD FIX 3～5：
   - 持久化任務類別。
   - 喚醒改在 pool 釋放槽位之後執行。
   - 統一由一個函式處理 `Release` 的回傳值與容量計數。
   - `RecoverInterruptedTasks` 納入 `queued`。
3. **workspace key** 改用 policy 解析後的標準路徑（MUST FIX 4）。
4. **`Trigger`** 先推進 NextRunAt 再派工，派工失敗記錄為 `cron_runs` 的 failed（MUST FIX 6、SHOULD FIX 8）。
5. **CLI 顯示時間**轉為本地時區（MUST FIX 7）。
6. **重新對齊證據**（MUST FIX 9、10）：
   - 把測試名稱、註解、decomposition 文件與 README 的 Scenario ID 改回核准 Gherkin。
   - 補上真實 `cronLoop` 觸發、真實 Daemon 重啟、CLI binary 的外迴圈測試。
   - 每項修正先取得行為紅燈（不是編譯紅燈），並完整記錄命令、exit code、Scenario ID 與日期。
7. **待確認事項**：DOM/DOW 語意與 DST 政策請使用者拍板並寫入 README。

## 流程判定

- **`RETURN TO execute-task`**
  - **判定依據**：
    - 目前 HEAD `f82dd570cf69686f1f0fbaffd3e2272372596c3d` 存在 10 項 MUST FIX，其中邏輯缺陷 8 項、驗收證據缺陷 2 項，多數已實跑重現。
    - CRON-004、CRON-006 的 When 子句沒有覆蓋；CRON-001、CRON-002 在必要邊界下被推翻。
    - remediation 階段的紅燈證據無法從提交重現。
    - 前次 `review-143b03a.md` 的 `PASS` 只代表 `143b03a`，且其覆蓋結論已被本報告推翻，不得作為 PR #7 的合併依據。
  - 本報告已持久化於 `docs/issues/issue-6/review-f82dd57.md`。
