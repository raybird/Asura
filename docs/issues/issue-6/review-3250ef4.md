# 審查報告

審查日期：2026-09-14

- 範圍：完整 PR（Issue #6 分支 `main..issue-6-cron-capacity`），重點覆核 `367ba10` 對 `review-f82dd57.md` 的修復
- Reviewed BASE SHA：`2530375f641a0aa8a3503bee65a93a65599dc58c`
- Reviewed HEAD SHA：`3250ef4c7862dd6479ea497851c1fae572e9f778`
- Reviewed patch-id：`a3ad1431be2fdcb73e3bfa72eac5e585fa99db62`
- 獨立 reviewer：Claude Code 主審（非原實作者），加上 3 位獨立 reviewer subagent，分工如下：
  - (A) Daemon 併發與容量調度
  - (B) Cron 引擎、儲存與 CLI
  - (C) 測試證據與驗收稽核
- Review artifact：`docs/issues/issue-6/review-3250ef4.md`
- 風險：High（定時自主觸發本機 AI、同專案檔案寫入互斥）

## 審查版本與契約說明

- **程式與測試實際審查的版本是 `367ba10`**。審查期間 HEAD 前進到 `3250ef4`（docs：同步 dev-rules-kit v4.0.0）。`git diff 367ba10 3250ef4 --name-status` 只有 10 個流程規範檔：`AGENTS.md`、`docs/AGENTS.md`、`docs/.dev-rules-kit.json`、`docs/agents/*`。程式、測試與 `docs/issues/issue-6/` 都沒有變動，所以本報告的程式結論完全適用於 `3250ef4`。
- **流程契約改依 2.0**。`3250ef4` 把 `docs/AGENTS.md` 升到流程契約 2.0，與目前 review skill 的需求一致，因此本報告依 2.0 判定，依據為 `docs/agents/acceptance.md`、`verification.md`、`review-evidence.md`。上一份報告依 1.22；兩版在紅綠重構證據、假綠燈、證據持久力的語意上一致。2.0 另外明訂「語法錯誤、缺依賴不是目標行為的紅燈」。
- **`3250ef4` 不屬於 Issue #6 的交付範圍**，本報告只確認它對程式沒有影響，不審查它的內容。PR 推送後會一併帶入這個提交，是否拆成獨立 PR 由使用者決定。
- **本地 HEAD 與遠端 PR 不同**。遠端 PR #7 的 source HEAD 仍是 `f82dd57`，本地領先 3 個未推送提交。本報告審的是本地 `3250ef4`，不代表遠端 PR 目前的內容。

## 整體評估

**判定：`RETURN TO execute-task`。**

`367ba10` 的修復大部分真的有效：

- 派工失敗雪崩、重啟後 `queued` 殘留、`cron list` 顯示 UTC、喚醒時沿用錯誤類別，都已修好。
- 常見時區的 DST 無窮迴圈已消除。
- CRON-004 走真實的 `cronLoop` 觸發，CRON-003 走真正的 CLI binary，CRON-008 真的重啟 Daemon。這三項的突變測試都能被殺掉，驗收覆蓋比上一輪明顯紮實。

但仍有 7 項 MUST FIX：

- **3 項是併發邏輯缺陷**，都已實跑重現，其中 1 項是本輪修復才引入的。
- **1 項是 README「待確認事項」寫的行為與程式碼相反**，commit 與 Timeline 也宣稱了實際沒完成的修復。
- **3 項是驗收證據缺口**：本輪完全沒有紅燈紀錄；3 項修復沒有能抓到錯的測試；刪除的測試有兩條覆蓋真的消失。

## 上一輪發現覆核

| review-f82dd57 項目 | 判定 | 證據 |
|---|---|---|
| MUST FIX 1：DST 無窮迴圈 | 部分修復 | (B) 在 18 組時區與年份上呼叫 `Next` 共 5,727,869 次，沒有卡死，也沒有回傳錯誤欄位；上一輪卡死的案例都正常回傳。殘留：午夜切換時區會漏跑觸發（SHOULD FIX 1）；24 小時以上的跳躍在歷史時區資料上仍會卡死（NICE） |
| MUST FIX 2：喚醒沿用前一個任務的類別 | 行為已修復，證據不足 | 類別已持久化，喚醒時讀取（`internal/daemon/daemon.go:737-741`）；(A) 重現情境 A、B 都轉綠。但官方測試抓不到回歸，見 MUST FIX 6 |
| MUST FIX 3：喚醒時前一個任務仍佔 pool 槽位 | 部分修復 | 原情境已不再 failed，`TestDaemon_WakeQueuedTask_PoolSlotFreed` 有鑑別力。但喚醒仍受 pool MaxQueue 限制，見 MUST FIX 2 |
| MUST FIX 4：workspace key 沒有正規化 | 部分修復 | 尾斜線、`//`、`/.`、`sub/..`、symlink、相對路徑都已判定為同一專案。但子目錄 `A/sub` 仍與 `A` 同時執行（SHOULD FIX 3）；釋放時重新解析 key 會造成鎖外洩（MUST FIX 3） |
| MUST FIX 5：重啟後的 `queued` 殘留 | 已修復 | `internal/storage/sqlite.go:520`；(B) 用舊資料庫實跑遷移後，`queued` 被正確標成 failed |
| MUST FIX 6：派工失敗雪崩 | 已修復 | (B) 重跑原情境 7 秒，只產生 1 筆 failed 任務與 1 筆 failed run，NextRunAt 有推進 |
| MUST FIX 7：list／runs 顯示 UTC | 行為已修復，證據不足 | (B) 以 binary 在 Taipei 實跑，顯示 03:00。測試抓不到回歸，見 MUST FIX 6 |
| MUST FIX 8：派工失敗時沒處理 `Release` 回傳值 | 行為已修復，沒有測試 | 所有路徑都改用 `releaseWorkspaceAndWakeNext`（`internal/daemon/daemon.go:215`、`:539`、`:718`、`:727`、`:733`、`:748`）；突變 Mi 存活 |
| MUST FIX 9：Scenario ID 錯位與覆蓋缺口 | 大幅改善，仍有殘留 | 整合測試名稱已對齊核准 Gherkin；CRON-004 的 When 子句（M1）、CRON-006 的 When 子句（M2）、CRON-005「不產生新任務」（M3）、CRON-007 拒絕原因（M4）的突變都被殺掉。殘留問題見 MUST FIX 7、SHOULD FIX 9 與 11 |
| MUST FIX 10：紅燈證據不完整 | 未修復 | 見 MUST FIX 5 |
| SHOULD FIX 1：IsReachable 只搜 5 年 | 已修復 | 改為 50 年；`0 0 29 2 1` 得到 2044-02-29；最壞情況 `0 0 31 2 *` 約 99µs |
| SHOULD FIX 2：秋季重複、春季漏跑 | 未修復 | New York `30 1 * * *` 在 2026-11-01 仍觸發兩次；`README.md` 沒有補上說明 |
| SHOULD FIX 3：排隊任務佔用全域容量 | 未修復 | (A) 重現 R3 仍失敗 |
| SHOULD FIX 4：取消 queued 任務時重複釋放容量 | 已修復，沒有測試 | `internal/daemon/daemon.go:414-419`；並行取消 50 輪 inFlight 都守恆；突變 M13 存活 |
| SHOULD FIX 5：先取鎖、後寫入 queued 的競態 | 未修復，而且實跑重現 | 升級為 MUST FIX 1 |
| SHOULD FIX 6：MaxCronWorkers 設定邊界 | 未修復 | (A) 重現 R10 仍失敗 |
| SHOULD FIX 7：非終態任務讓排程永久被略過 | 未修復 | (B) 探針重現 |
| SHOULD FIX 8：`RecordCronRun` 失敗時失去重疊保護 | 已修復 | (B) 探針 B：只派工一次 |
| SHOULD FIX 9：斷言鑑別力不足 | 部分修復 | M9 已被殺；M5 仍存活 |
| SHOULD FIX 10：DOM／DOW 採 AND 語意 | 已列入待確認事項，但內容錯誤 | 見 MUST FIX 4 |
| SHOULD FIX 11：測試失敗時卡死 | 未修復 | 新測試仍會卡死，見 SHOULD FIX 10 |
| SHOULD FIX 12：靠時序的斷言 | 部分修復 | CRON-002 仍然睡 30ms 後才檢查 |
| SHOULD FIX 13：修改既有測試沒記錄理由 | 未修復 | 本輪又刪除了測試，見 MUST FIX 7 |

## 問題與風險

### 🔴 MUST FIX

**1. 排隊任務可能永遠不會被喚醒**
- **位置**：先取鎖、後寫入 queued 的順序在 `internal/daemon/daemon.go:204-208`（cron）與 `:527-531`（submit）；喚醒端在 `:731-735` 讀到非 queued 狀態就直接跳過。
- **原因**：SQLite 只有單一連線（`internal/storage/sqlite.go:109`），負載下寫入 queued 會排隊等連線，時間窗是毫秒級。
- **重現**：(A) 壓力測試（5 個專案、12 個 goroutine、混合 cron 與互動任務、加上路徑變體）：
  - 非 race 兩輪分別殘留 12 筆與 4 筆（其中一輪完全沒有取消操作）。
  - `-race` 四輪各殘留 1～3 筆。
  - 殘留任務都卡在 `queued`，但 inFlight 已歸零、專案佇列是空的。
  - 在副本中插樁確認，每筆殘留在喚醒當下讀到的都是 `pending`。
- **影響**：
  - 違反 CRON-001「任務 1 完成後任務 2 自動啟動」。
  - 若殘留的是 cron 任務，`internal/cron/scheduler.go:109` 會把它當成仍在執行，之後每次觸發都被略過，排程在重啟前等於失效。
  - 在這個時間窗內被取消的任務，會被 queued 寫入覆蓋，之後仍被喚醒執行。
- **建議**：先以條件式更新把狀態從 pending 改成 queued，再放進專案佇列；或讓喚醒端把 `pending` 也視為可執行，並處理寫入錯誤。

**2. 被喚醒的 cron 任務會被 pool 的 MaxQueue 拒絕，標成 failed**
- **位置**：`internal/daemon/pool.go:145-156`、`internal/daemon/daemon.go:743-746`
- **情境**：`MaxWorkers≥3`、`MaxCronWorkers=1`：
  1. 互動任務 i1 在專案 A 執行，cron-B 佔住 cron 配額。
  2. pool 的 cron 佇列已滿（0、1、10 筆都會發生）。
  3. 專案 A 的 cron-A2 在專案佇列排隊。
  4. i1 完成後，A2 變成 `failed: capacity exceeded: worker pool saturated`，而此時有閒置 worker。
- **影響**：違反 CRON-001。`--workers 3` 搭配預設佇列 10 就能固定重現；IPC 自帶 `class=cron` 的任務也會中。
- **確認**：(A) 三個子案例都實跑失敗。
- **建議**：已經拿到專案鎖的排隊任務，被喚醒時不應再算成新提交，不受 pool 佇列上限檢查；或在排隊當下就預留 pool 名額。

**3. 釋放專案鎖時重新解析 key，可能讓鎖永久外洩（本輪修復引入）**
- **位置**：`internal/daemon/daemon.go:699`（對已經正規化的 key 再解析一次）、`:707`（喚醒時從原始路徑重算 key）、`:718`；`internal/auth/policy.go:61-67` 解析失敗時直接回傳原始字串。
- **原因**：取鎖時用當下解析出的路徑，釋放時卻重新解析；symlink 目標在這段期間改變，就會釋放到另一個 key。
- **情境**：
  1. `root/link` 指向 `real`，`real` 有任務在執行，此時 cron 以 `link` 觸發而排隊。
  2. `link` 改指向 `root/other`（仍在授權根目錄內）。
  3. 前一個任務完成後，原本的 key 永遠被已完成的任務佔住，之後提交到 `real` 的任務一直停在 `queued`，各自佔住 inFlight，要重啟 daemon 才能恢復。
- **確認**：(B) 與 (A) 各自實跑重現。同一探針在 `f82dd57` 上不會外洩（但那一版兩條路徑會並行，也就是原本的 MUST FIX 4），確認這是本輪才出現的回歸。
- **建議**：key 只在取鎖時算一次，之後存下來一路傳遞，釋放與喚醒都用同一個值。

**4. README「待確認事項」寫的行為與程式碼相反，提交與 Timeline 宣稱了沒完成的修復**
- **TBD-1（`docs/issues/issue-6/README.md:173-175`）**：
  - README 聲稱 parser「嚴格遵循 POSIX/Vixie cron 規範……採 OR 語意」。
  - 程式碼 `internal/cron/parser.go:167` 是 `if !dayMatch || !weekdayMatch`，也就是 AND；`README.md:133` 寫的也是 AND。
  - 實測：`0 0 13 * 5` 從 2026-09-17 起算得到 2026-11-13（13 日且週五）。若是 OR 語意，應為 2026-09-18。(B)、(C) 各自實跑確認。
- **TBD-2（`:176-178`）**：
  - README 聲稱 DST 時「會保證步進遞增至當天該時區有效之時間點」。
  - 實際是整天跳過：New York `30 2 * * *` 從 3/14 直接跳到 3/15。`internal/cron/parser_test.go:166-168` 自己的期望值也是整天跳過。
- **宣稱超過實際**：
  - commit `367ba10` 宣稱「解決 10 項 MUST FIX」與 SHOULD FIX 5，但 MUST FIX 10 沒處理、SHOULD FIX 5 沒修，MUST FIX 3、4 只修好一部分。
  - Timeline 寫「全套整合回歸通過 (0 DATA RACE)」，但沒有全套日誌（見 MUST FIX 5）。附帶一提，(C) 實跑全套確實通過。
- **影響**：待確認事項是交給使用者拍板、並由 PR 原文揭露的內容，前提寫反會直接誤導決策。依 `docs/agents/review-evidence.md`，這屬於不實回報。
- **建議**：
  - TBD-1 改為據實描述目前的 AND 語意，並請使用者決定要不要改成 OR。
  - TBD-2 據實記錄三件事：春季整天跳過、秋季重複觸發、午夜切換時區漏跑。
  - 格式改為 `docs/AGENTS.md`「待確認事項」規定的表格（TBD-N 編號、狀態）。
  - 修正 Timeline 與 `**狀態**` 的敘述。

**5. 本輪修復完全沒有紅燈紀錄，「全套回歸」其實不是全套**
- **位置**：`docs/issues/issue-6/evidence/remediation-2/` 只有 `outer-green.log`、`unit-tests.log`、`final-regression.log` 三份綠燈日誌；commit、README 與其他目錄都找不到紅燈。
- **格式問題**：三份日誌都只是 `go test -v` 的原始輸出，沒有驗收編號、實際命令、exit code 與日期。
- **不是全套**：
  - `final-regression.log` 只涵蓋 `tests/integration` 一個套件，缺 TestINT001～007 共 8 個頂層測試。
  - 看不出有跑 `-race`：日誌耗時 18.8 秒，含 race 的實跑是 67 秒。
  - `unit-tests.log` 缺 engine、web、backend/opencode 三個套件。
- **替代重現**：(C) 拿 `f82dd57` 的 production code 加上新測試跑，大部分新測試確實會紅，證明它們有鑑別力。但這不能取代紀錄義務；`verification.md` 要求紅綠重構證據四段都要記錄。
- **判定依據**：`docs/agents/verification.md`「紅綠重構證據」。上一輪 MUST FIX 10 等於沒有處理。

**6. 三項修復沒有能抓到錯的回歸測試**
- **MUST FIX 2（類別持久化）**：`TestDaemon_QueuedTaskClassPreservedOnWake`（`internal/daemon/daemon_test.go:1153`）在 `f82dd57` 上照樣 PASS；把喚醒類別改回舊行為、固定成 cron、固定成 interactive 三種突變也都 PASS。
  - 原因：測試用 MaxQueue=2，任務即使被當成 cron 喚醒也能排進 pool 佇列並跑完；而且它手動重演取鎖與狀態更新，沒有經過 submit 路徑。
- **MUST FIX 7（本地時區顯示）**：`TestCRON003_CLICronManagement` 在 `f82dd57`（UTC 顯示）上照樣 PASS；拿掉 `.Local()` 的突變 (e) 與下次觸發時刻加 2 天的突變 M8b 都存活。斷言只比對當月前綴，見 `tests/integration/cron_test.go:379-382`。
- **MUST FIX 8（派工失敗時的佇列交接）**：沒有任何測試，突變 Mi 存活。
- **判定依據**：bug 修復要先有會失敗的測試。這三個綠燈都無法證明本次修復的行為正確，不是證據持久力層級的問題。

**7. 刪除既有測試，其中兩條覆蓋真的消失，而且沒有記錄理由**
- **`TestCRON001_CronAndInteractiveWorkspaceMutex`**（`f82dd57` 版 `tests/integration/cron_test.go:789`）：
  - 原本在整合層斷言：互動任務執行中觸發同專案 cron 時，cron 任務為 `queued`、run 紀錄帶有關聯 TaskID、任務最後完成。
  - 這正是前前次 MUST FIX「排程繞過專案鎖」的回歸測試。
  - 新 HEAD 在整合層沒有對應測試；突變 M6 只被單元層的 `internal/daemon/daemon_test.go:1233` 附帶殺掉。
- **`TestCRON007_CLICronHistoryQuery`**：
  - 原本斷言 `runs` 的排序（skipped 在前、triggered 在後）。
  - 現在沒有任何測試驗排序，`asura cron runs` CLI 也完全沒有測試。
- **其餘被刪除的測試**：(C) 逐一確認都已搬移或有等價覆蓋，其中 `TestCRON003_UnreachableDateRejection` 改走 CLI，屬強化。
- **判定依據**：`docs/agents/verification.md`「假綠燈」——刪除既有測試必須記錄真實理由。

### 🟡 SHOULD FIX

1. **DST 修復在午夜切換的時區會漏跑觸發**
   - **位置**：`internal/cron/parser.go:155-158`、`:169-173`。
   - **原因**：後備路徑用 `AddDate`，保留了原本的時分，所以會跳過整天或整月。
   - **實測**：
     - Santiago `0 3 * * 0` 從 2026-09-05 起算得到 09-13，正確是 09-06。
     - Santiago `0 3 6 9 *` 得到 2027-09-06，整整跳過一年。
     - Azores、Havana 也受影響，每年都會發生。
   - **測試抓不到**：Santiago 子測試（`internal/cron/parser_test.go:191`）只驗回傳值非零；只拿掉月份或日期防護的突變 (f1)、(f2) 都存活，而 (f2) 拿掉後在 Santiago 會卡死。
   - **建議**：後備路徑改成先前進、再截斷到當天或當月的起點；並用暴力參考實作比對午夜切換時區。
2. **秋季重複觸發與春季整天跳過沒有處理或說明**
   - 見覆核表的 SHOULD FIX 2。
   - 列入待確認事項，請使用者決定政策，並寫進 `README.md`。
3. **專案粒度沒有決策紀錄**
   - 子目錄 `A/sub` 的任務與 `A` 同時執行（(A) 實跑）。
   - `tests/integration/web_test.go` 的 WEB008 刻意把任務放在不同子目錄，看起來子目錄被當成不同專案。
   - 但 `internal/daemon/daemon_test.go:1077` 的註解卻寫「子路徑均被正規化」。
   - 建議列入待確認事項，請使用者決定粒度是授權根目錄、git 根目錄還是完整路徑，並修正測試註解。
4. **排隊任務佔用全域容量**
   - 位置：`internal/daemon/daemon.go:69-78`。
   - `MaxQueue=0` 時，同專案有 cron 在排隊，其他專案的互動任務就會被拒，而保留的 worker 是閒的。
5. **MaxCronWorkers 設定邊界沒有驗證**
   - 位置：`internal/daemon/pool.go:77-79`。
   - `MaxCronWorkers≥MaxWorkers` 或 `--workers 1` 時，保留互動通道不存在，也沒有任何警告。
6. **非終態任務讓排程永久被略過**
   - 位置：`internal/cron/scheduler.go:105-109`，搭配 `internal/engine/engine.go:145-147`。
   - engine 在執行時授權失敗會直接返回，不更新狀態，任務停在 `queued`；排程修好並 resume 之後，每次觸發仍被略過。
7. **專案鎖交給跑不了的任務，造成隊頭阻塞**
   - 位置：`internal/daemon/daemon.go:743`。
   - 被喚醒的 cron 卡在 pool 的 cron 佇列時仍持有專案鎖；即使取消它，同專案的互動任務仍被擋住，而此時有閒置 worker。
   - DB 狀態無法分辨任務是在專案佇列還是 pool 佇列。
   - 需要設計決策。
8. **CRON-003 的時刻斷言在每月最後一天會誤判失敗**
   - 位置：`tests/integration/cron_test.go:379-382`。
   - 斷言只比對當月前綴。每月最後一天 03:00 之後，下次觸發已在下個月，測試就會失敗；例如 2026-09-30 04:00 起算得到 2026-10-01。
   - 建議改成比對完整時刻的字面值，並固定測試的時區與起點。
9. **CRON-006 的整合層斷言形同空檢查**
   - `tests/integration/cron_test.go:694` 在 `cronLoop` 第一次 tick 之前就檢查「沒有補跑」。
   - `:703` 只檢查「晚於現在」。
   - M5（改成 now+7 分鐘）在所有層都存活；M10（改成補跑）在整合層存活，只被 `internal/cron/scheduler_test.go:61` 殺掉。
10. **測試失敗時會卡死，而不是乾淨地 FAIL**
    - 位置：`tests/integration/cron_test.go:127`、`internal/daemon/daemon_test.go:1146`。
    - `t.Fatalf` 之後，defer 的 `Close` 等待被阻塞的 runner，最後要到 5 分鐘逾時才 panic。
11. **文件追溯仍然錯位**
    - `docs/issues/issue-6/implementation-plan-decomposition.md`：
      - Phase 3、4 任務段落的 Scenario ID 仍是錯位版本（`:154`、`:165`、`:177`、`:188`、`:200`、`:237`、`:239`、`:259`、`:261`），並引用已刪除的測試。
      - 核對表 `:325` 列的 `internal/daemon/scheduler_test.go` 不存在；`:329` 列的 `sqlite_test.go` 裡沒有 cron 測試。
    - `docs/issues/issue-6/implementation-plan.md:79-82` 仍引用不存在的測試。
    - README 步驟概要（`:146-147`）與 Timeline（`:165-166`）仍是錯位的 ID。
12. **CRON-008 的還原斷言不完整**
    - 位置：`tests/integration/cron_test.go:899`。
    - 只驗 ID、Name、Status，沒驗 workspace、schedule、prompt 與 paused 狀態。
    - 「多次歷史紀錄」實際只寫入 1 筆，而且是直接寫 store，不是經 daemon 產生。

### 🟢 NICE TO HAVE

- **解析器**
  - `1/5` 只解析成分鐘 1。
  - 超大 step 在 `internal/cron/parser.go:130` 溢位，靜默得到錯誤結果。
  - `+5` 會被接受。
  - 歷史時區資料上 24 小時以上的時差跳躍仍會卡死（例如 Pacific/Apia 2011）；建議比照小時路徑，補上「至少前進 1 分鐘」的保底。
- **排程觸發**
  - `resume` 不重新檢查授權（`internal/daemon/daemon.go:622-649`）。
  - `Trigger` 不重新檢查排程狀態，已 paused 的排程仍會派工一次。
  - `ScheduledTime` 記錄的是 now，不是原本的預定時刻。
  - 容量滿時該期排程直接作廢：沒有文件說明，run 紀錄沒有 TaskID，而且仍寫入 `last_run_at`。
  - `RecordCronRun` 失敗時 `runningRuns` 項目外洩。
- **Daemon**
  - IPC 請求可以自帶 `Class`，而且不做驗證；現在還會寫入 DB，喚醒時沿用（`internal/daemon/daemon.go:480-483`）。
  - `internal/engine/engine.go:171` 接受 `queued` 狀態，`asura run --id <queued-id>` 可以繞過專案鎖；MUST FIX 1 留下的殘留任務會讓這個問題更容易被利用。
  - `internal/daemon/pool.go:56-59` 是無效分支。
- **CLI**
  - CLI 與 daemon 時區不同時，顯示的時刻沒有時區標示。
- **文件**
  - README 刪掉了 4 列既有的 Timeline 紀錄。2.0 規範只要求 Timeline 記錄語意事件，所以不列為缺失，但 README 開頭仍連到 PR #7，建議保留發送 PR 的那一列。
  - README 新增的 `**修訂日期**` 欄位不在 issue 範本中。

## 已查核維度

### 驗收與證據

- **規格修訂**：比對 `git diff df5b48a:docs/issues/issue-5/README.md 3250ef4:docs/issues/issue-6/README.md`。Gherkin 區塊 CRON-001～008 逐字不變；核准表為逐項表格，8 項都是 `已核准`；沒有規格修訂。
- **驗收覆蓋**（依核准 Gherkin 編號；除非另外註明，行號都指 `tests/integration/cron_test.go`）：

  | Scenario | 判定 | 重點 |
  |---|---|---|
  | CRON-001 | 部分 | 使用者提交路徑 ✅（`:118-149`）；排程提交路徑只有單元層（`internal/daemon/daemon_test.go:1233`），整合層覆蓋被刪除；MUST FIX 1、2 推翻了「自動接續」 |
  | CRON-002 | 部分 | 保留 worker ✅（`:283-288`、`internal/daemon/pool_test.go:162`）；以 IPC 模擬 CLI／Web，而且睡 30ms 後才檢查 |
  | CRON-003 | 部分 | 走 CLI binary 的 add、pause、resume、remove ✅（`:329-426`）；list 時刻斷言太弱（MUST FIX 6、SHOULD FIX 8） |
  | CRON-004 | ✅ | 走真實 `cronLoop`（`:494-516`，M1 被殺）；狀態經 IPC `GetTask` 查詢，與 `asura status` 走同一條路徑 |
  | CRON-005 | ✅ | 「不產生新任務」（`:612-613`，M3 被殺）；skipped 紀錄（`:617-633`）；前次任務以假資料模擬 |
  | CRON-006 | 部分 | 真實呼叫 `Start()`（M2 被殺）；「不補跑」與「下一個觸發點」的斷言太弱（SHOULD FIX 9） |
  | CRON-007 | ✅ | unauthorized、paused、拒絕原因（`:772-801`，M4 被殺）；以 scheduler 搭配假 dispatcher 觸發 |
  | CRON-008 | 部分 | 真的重啟 Daemon（`:855-880`）；下次觸發時間 ✅（M12 被殺）；定義欄位只驗一部分（SHOULD FIX 12） |

- **紅綠重構證據**：
  - 本輪沒有任何紅燈紀錄（MUST FIX 5）。
  - 3 項修復沒有具鑑別力的測試（MUST FIX 6）。
  - 既有測試被刪除，覆蓋消失（MUST FIX 7）。
  - 除此之外沒有發現同義反覆或 mock 掉核心行為的測試。`internal/storage/sqlite_test.go` 只改了錯誤訊息的文字，斷言沒有弱化。
- **實跑結果**（2026-09-14，程式版本 `367ba10`）：

  | 命令 | 結果 |
  |---|---|
  | `go vet ./...` | exit 0 |
  | `go test -race -count=1 -timeout 10m ./...` | exit 0，69 秒，9 個套件都 ok，0 DATA RACE（這次 TestINT001／INT007 也通過）(C) |
  | `go test -race -count=3 ./internal/cron ./internal/daemon ./internal/storage ./cmd/asura` | exit 0，沒有 flaky (C)(A) |
  | `go test -race -count=3 -run 'TestCRON\|TestWEB008\|TestCLI_LatestReview_G01' ./tests/integration` | exit 0，3 次都通過 (C) |

- **突變測試**：
  - 被殺：M1、M2、M3、M4、M8a、M9、M12、Mg、(f)（整組 DST 防護全拿掉）。
  - 存活：M5、M8b、(a)、(e)、(f1)、(f2)、Mi、M13。
  - 只被單元層殺掉：M6、M10、(c)、(d)。
  - 用卡死的方式「殺掉」：(b)。

### 相關失敗面

| 輸入／狀態 | 預期 | 現有覆蓋 | 判定 |
|---|---|---|---|
| 高併發下同專案連續提交（寫入 queued 前任務 1 剛好完成） | 任務 2 最終被執行 | 無 | MUST FIX 1 |
| `--workers 3`，cron 配額被佔住、pool 的 cron 佇列已滿，同專案 cron 被喚醒 | 被喚醒的任務排隊等待後執行 | 無 | MUST FIX 2 |
| 排隊期間 symlink 改指向 | 鎖正確釋放，後續任務可執行 | 無 | MUST FIX 3 |
| America/New_York、`0 9 * * *`，DST 開始前一天 | 算出 09:00 EDT，不卡死 | `internal/cron/parser_test.go:147` | 已修復 |
| America/Santiago、`0 3 * * 0`，午夜切換那週 | 09-06 03:00 | 只驗回傳值非零 | SHOULD FIX 1 |
| 容量飽和時排程到期 | 不雪崩，記錄 failed 並推進 | `internal/cron/scheduler_test.go:263` | 已修復 |
| daemon 崩潰時有 queued 任務，接著重啟 | 標成 failed | `internal/storage/sqlite_test.go:290` | 已修復 |
| 路徑寫成 `/root/A/`、`//A`、symlink | 判定為同專案、回傳 queued | `internal/daemon/daemon_test.go:1146`（只測尾斜線） | 已修復，覆蓋偏窄 |
| 路徑是子目錄 `/root/A/sub` | 待使用者決定 | 無 | SHOULD FIX 3 |
| 每月最後一天 03:00 之後跑整合測試 | 測試穩定 | — | SHOULD FIX 8（誤判失敗） |

### 需求、架構、安全、品質

- **需求**：核准的 8 個 Scenario 都有對應實作；CRON-001、CRON-002 在必要邊界下仍被推翻。`asura cron runs` 屬合理的支撐性實作。
- **架構**：
  - 依賴方向不變：`internal/cron` 只依賴 `internal/auth` 與 `internal/storage`，沒有循環依賴。
  - 本輪把專案鎖的交接收斂到 `releaseWorkspaceAndWakeNext`，方向正確；但 key 在釋放時重新計算（MUST FIX 3），而「取鎖、寫入狀態、pool 派工」三步不是原子操作（MUST FIX 1、2），交接模型仍需重新設計。
  - 查核依據有限：`docs/agents/project.md` 仍是空白範本，專案也沒有架構文件。
- **安全**：
  - 授權在新增、觸發、執行三個時點都走 `AuthorizeWorkspace`，會解析 symlink，擋得住 `..` 逃逸。
  - 同專案寫入互斥仍可因競態與子目錄被繞過（MUST FIX 1、SHOULD FIX 3），這正是本 issue 列出的最大風險。
  - 查詢全部參數化；沒有發現 secret 外洩。
- **品質**：`wakeQueuedTask` 仍以遞迴處理連續失敗，深度上限是專案佇列長度。(A) 的壓力測試沒有發現死結，inFlight 計數守恆。

### 豁免、待確認與限制

- **Gate 豁免**：無，README 沒有 `## Gate 豁免紀錄`。
- **待確認事項**：README 新增了 2 項，但格式不符（沒有 TBD-N 編號與狀態欄），而且內容與程式碼矛盾（MUST FIX 4）。依規範維持原狀態不代為修改；兩項實質上都是 `待確認`，而且都影響本次交付的可觀察行為（CRON-003、CRON-004 的觸發時刻）。
- **限制**：
  - 程式結論是在 `367ba10` 上取得，`3250ef4` 與它只差流程規範文件。
  - 本報告不代表遠端 PR #7，遠端 source HEAD 仍是 `f82dd57`。
  - 並行負載下 TestINT001、TestINT007 偶爾會因本機 opencode 環境失敗，單獨執行時通過，屬環境因素。

## 流程判定

**`RETURN TO execute-task`**

判定依據：
- 3 項併發邏輯缺陷已實跑重現，其中 MUST FIX 3 是本輪修復引入的回歸。
- README「待確認事項」寫的行為與程式碼相反，提交宣稱了未完成的修復。
- 本輪沒有紅燈紀錄，3 項修復沒有具鑑別力的測試，刪除的測試讓兩條覆蓋消失。

`review-143b03a.md` 的 `PASS` 已被 `review-f82dd57.md` 推翻；本報告同樣不支持以目前 HEAD 合併 PR #7。
