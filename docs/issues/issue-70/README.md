# Issue 70 - Flaky test：TestCLI_LatestReview_H01_PathEscapedTaskIDHandshake 偶發 side-effect marker 出現 (Small)

## 概述

來源：[GitHub #70](https://github.com/raybird/Asura/issues/70)。`tests/integration/cli_test.go` 的 `TestCLI_LatestReview_H01_PathEscapedTaskIDHandshake` 在 CI 偶發失敗，重跑後通過。目標是分別查出兩種失敗訊息的原因並取得可重現的紅燈，不以加長等待或重試掩蓋。

| 日期 | 來源 | 被測提交 | 失敗位置 | 訊息 |
|---|---|---|---|---|
| 2026-09-24 前後 | issue-21 審查本機完整 race 測試（[review-ddd8c1e.md](../issue-21/review-ddd8c1e.md)） | `ddd8c1e`（2026-09-23，早於 `a84dc3a`） | — | side-effect marker 斷言失敗 |
| 2026-09-28 | PR #24 [run 36428409448](https://github.com/raybird/Asura/actions/runs/36428409448) attempt 1 | `caaa09c`（不在 main） | `cli_test.go:962` | `side-effect file exists! cmd.Start() was executed despite external cancel`（2.72s） |
| 2026-09-28 | 同一 run attempt 2 | `caaa09c`（不在 main） | `cli_test.go:953` | `cancel command failed: ... unable to verify process identity: process group 9469 is alive with orphan members, but identity cannot be verified (leader exited, runner unavailable, no task identity in orphans)`（1.75s） |
| 2026-10-21 | main push [run 39537742081](https://github.com/raybird/Asura/actions/runs/39537742081) | `c82e8e4`（main） | `cli_test.go:962` | 同 2026-09-28 attempt 1（4.99s） |

測試主程先持有平坦鎖檔，啟動 `asura run`（指令為 `touch <marker>; sleep 5`），等任務轉為 running 後啟動 `asura cancel` 子程序，固定等 50ms 就放開鎖，最後斷言 cancel 成功、marker 不存在、任務為 cancelled。

### 症狀 1 原因（2026-10-21 查明）：測試的時序假設

外部 `asura cancel` 會先寫取消意圖 sidecar（`.task-<雜湊>.cancel`），才開 SQLite、以 CAS 登記 `cancelling`，再排隊等鎖。runner 取得鎖後、`cmd.Start` 前檢查 sidecar 與 DB 狀態，兩者都沒有就啟動。產品保證的是：**cancel 寫出意圖之後**才拿到鎖的 runner 不會啟動。

測試固定等 50ms 就放鎖，等於假設 `-race` 建置的 cancel 子程序 50ms 內一定寫得出 sidecar。cancel 較晚寫出時，runner 先拿到鎖並執行 `touch`，cancel 之後才殺掉行程：cancel 成功，副作用卻已經發生。取消落在啟動之後，產品本來就該這樣處理；問題出在測試的前提。

證據：在 cancel 子程序前注入啟動延遲（[delay-injection.diff](evidence/delay-injection.diff)，只改測試，預設不啟用），main 上延遲 ≤40ms 全部通過，≥42ms 全部以 `side-effect file exists!` 失敗，41ms 時兩種結果都有，門檻正好落在 50ms 的固定等待內（[delay-sweep-main.log](evidence/delay-sweep-main.log)）。

### 症狀 2 原因（2026-10-21 查明）：`caaa09c` 的取消驗證競態，已由 `a84dc3a` 修正

`caaa09c` 是 PR #24（#23）開發中的中間提交，不在 main 上（`git merge-base --is-ancestor caaa09c origin/main` 回 1，見地雷 L-1）。那個版本的 cancel 沒有意圖 sidecar：它在鎖握手**之前**就以 `engine.CancelTask` 做 CAS，並以空的身分 metadata 驗證、回收行程組。如果 CAS 落在 runner 登記 PGID 之後、自我檢查之前，runner 會自己殺掉行程組並回收 leader。此時 cancel 驗證會看到 leader 已不存在，只剩尚未回收的 `touch` 成員；metadata 是空的，runner 身分也無從比對，結果回報「無法驗證身分」。

同一 PR 的下一個提交 `a84dc3a`（2026-09-29，「強化遷移與取消握手安全性」）加入意圖 sidecar，改成鎖握手完成、取得 runner 身分 metadata 之後才驗證與回收，`requestExternalCancellation` 的註解寫明是為了「避免在 runner 身分資料尚未可用時誤判 orphan process group」。PR #24 於 2026-09-29 squash 合併為 `19cb3d4`，main 從未包含 `caaa09c` 的取消流程。

證據：同一份延遲注入，在各版本的失敗窗口附近掃描（未加 CPU 限制或背景負載）：

| 版本 | 執行次數 | 症狀 1 | 症狀 2 型 | 紀錄 |
|---|---|---|---|---|
| `caaa09c` | 430 | 199 | 4（延遲 23～25ms：3 次與 CI 相同的 orphan 訊息，1 次 `read /proc/<pid>/stat: no such process`，同屬驗證時行程組正被 runner 回收） | [delay-sweep-caaa09c.log](evidence/delay-sweep-caaa09c.log) |
| `a84dc3a` | 150 | 31 | 0 | [delay-sweep-a84dc3a.log](evidence/delay-sweep-a84dc3a.log) |
| main `23af953` | 410 | 246 | 0 | [delay-sweep-main.log](evidence/delay-sweep-main.log) |

`caaa09c` 的失敗門檻在 17～26ms，比 main 早，因為它沒有 sidecar，CAS 要等 SQLite 初始化完才發生。main 上所有 CI 紀錄也都只有症狀 1。

### 未注入延遲時本機無法重現

main `23af953` 無負載 `-count=20` 與 `taskset -c 0,1` 加 6 個背景 `yes` 負載的 `-count=40` 全部通過（[no-injection-main.log](evidence/no-injection-main.log)）。因此修正前後的穩定性對照改用延遲注入（AC-3）。

## 涉及檔案

- `tests/integration/cli_test.go` — `TestCLI_LatestReview_H01_PathEscapedTaskIDHandshake` 改成等 cancel 寫出意圖 sidecar 後才放鎖
- `docs/issues/issue-70/` — 本 README 與 `evidence/` 的重現、對照紀錄

不改產品程式，維持 Small。延遲注入只套在 worktree，不進交付。

## 驗收條件

1. **AC-1**：兩種訊息分別判定原因。症狀 1 以延遲注入在 main 上穩定重現 `cli_test.go` 的 side-effect marker 失敗，證明原因是測試固定等待 50ms 的時序假設；症狀 2 在 `caaa09c` 以同一手段重現與 CI 相同的訊息，而 `a84dc3a` 與 main 同條件下不出現，判定已由 `a84dc3a` 在合併前修正，本 issue 不為它改程式 — 檢查方式：在各版本 worktree 套用 [delay-injection.diff](evidence/delay-injection.diff)，以 `ASURA_I70_CANCEL_DELAY=<秒>` 執行本測試，記錄各延遲的結果與訊息
   - 失敗路徑：無法重現或紅燈訊息與 CI 不符時，不進行修正；回報已嘗試的手段與結果，交由使用者決定
2. **AC-2**：測試改成等 cancel 的取消意圖 sidecar 出現後才放鎖，最多等 10 秒；逾時或 cancel 在寫出意圖前就結束時判定失敗並印出原因。不改產品程式。本機最小回歸（`go vet ./...`、`go test -count=1 -race -v ./...`、`go build ./cmd/asura`）全數通過 — 檢查方式：檢視 diff，執行回歸命令；另在 worktree 讓 runner 看不到 sidecar（只在驗證時暫改 `cmd/asura/run.go`），修正後的測試必須失敗，證明它仍抓得到產品端的退化
   - 失敗路徑：修正只是加長固定等待或加上重試，或反向檢查下測試仍通過，都判定未通過
3. **AC-3**：同一機器、同一延遲注入下，延遲 41ms、45ms、300ms 各 `-count=20`：修正前每個延遲至少失敗 1 次，修正後 0 失敗；修正後不注入延遲，在 `taskset -c 0,1` 加 6 個背景 `yes` 負載下 `-count=100` 0 失敗；PR CI 一次全綠作為補充 — 檢查方式：記錄命令原文、條件與修正前後的 PASS／FAIL 次數
   - 失敗路徑：修正後仍有任何失敗即未通過；修正前某延遲 0 失敗時，該延遲沒有鑑別力，不得當作通過，回報後重新決定條件

- **核准日期**: 2026-10-21
- **核准來源**: GitHub #70「完成判準」：「沿用 #48：先找出原因並取得可重現的紅燈，不以加長等待或重試掩蓋。兩種訊息分別判定，不混為同一原因」。2026-10-21 使用者以 `/dev-cycle issue:70` 要求推進；同日逐題確認：Q1「症狀 2 的處理」回答「判定已由 a84dc3a 修正 (Recommended)」，接受建議：比照 #51 記錄 `caaa09c` 與 main 的對照證據，本 issue 不為症狀 2 改程式（AC-1）；Q2「症狀 1 的修正」回答「等意圖 sidecar (Recommended)」，接受建議：測試等 sidecar 出現後才放鎖，最多 10 秒，逾時或 cancel 提前結束即失敗，不改產品程式（AC-2）；Q3「穩定性判準」回答「注入延遲對照 (Recommended)」，接受建議：41／45／300ms 修正前有失敗、修正後 0 失敗，加上修正後不注入延遲的負載壓測 `-count=100` 全過與 PR CI 一次全綠（AC-3）；Q4「同檔另一個握手測試要一起改嗎」回答「不改，記入待確認事項 (Recommended)」（TBD-1）
- **核准 commit**: d610587

## 風險與首要驗證

- **最大風險**：把產品缺陷誤判成測試時序問題；若 runner 在 cancel 公告意圖後仍會啟動，改測試會掩蓋 G-01／H-01 的取消保證失效
- **風險等級與理由**：Medium — 開始時根因未知，誤判會留下產品缺陷；修正範圍只在測試，可回復，不涉及資料或權限
- **首要驗證**：以延遲注入取得兩種症狀的紅燈，並比對 PR #24 合併前後的版本，確認症狀 1 只在 cancel 晚於放鎖時出現、症狀 2 只存在於 `caaa09c`
- **選擇理由**：本機未注入延遲時抓不到紅燈；延遲注入能直接控制 cancel 與 runner 的先後順序，區分「取消晚到」與「取消先到仍啟動」
- **完成證據**：AC-1 的掃描結果，加上 AC-2 的反向檢查（runner 看不到 sidecar 時修正後的測試失敗）

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|------|------|------|------|
| TBD-1 | 同檔 `TestCLI_LatestReview_G01_HandshakeCancelBeforeStart`（`cli_test.go` 約 797 行）放鎖前輪詢 1 秒等 `cancelled`，持鎖期間狀態不會變成 `cancelled`，實際上就是固定等 1 秒，屬同類時序假設 | 不影響本次交付 | 2026-10-21 Q4 回答「不改，記入待確認事項」：未注入延遲時 cancel 約 9ms 內就寫出 sidecar（main 上要額外注入約 41ms 才吃光 50ms 的餘裕），1 秒的餘裕大上百倍，也沒有失敗紀錄；在 PR 揭露，由 reviewer 評估。追蹤：無，該測試失敗時另開 issue，比照本 issue 的延遲注入判定 |

## 實作與驗證步驟

1. ✅ **AC-1：重現與判定兩種症狀** — 驗證方式：AC-1 的檢查方式
   - 產出：延遲注入補丁、三個版本的掃描紀錄與原因說明（寫回「概述」）
   - 相依：無
   - 完成判準：症狀 1 在 main 穩定重現且訊息與 CI 一致；症狀 2 在 `caaa09c` 重現、在 `a84dc3a` 與 main 不出現
   - 完成證據（2026-10-21，go1.27.2 linux/amd64，12 核，未加 CPU 限制或背景負載）：
     - 條件：`git worktree add --detach <dir> <commit>`（`caaa09c`、`a84dc3a`、main `23af953`），套用 [delay-injection.diff](evidence/delay-injection.diff)，各以 `go test -race -c ./tests/integration` 建立執行檔，在該 worktree 的 `tests/integration` 執行 `ASURA_I70_CANCEL_DELAY=<秒> <test binary> -test.run '^TestCLI_LatestReview_H01_PathEscapedTaskIDHandshake$' -test.count=<N> -test.v`。注入多 3 行，失敗行號 965／956 分別對應原本的 962／953
     - 症狀 1：main 延遲 0.3s 10/10 失敗；0.036～0.040s 各 20/20 通過，0.042～0.049s 各 20/20 失敗，訊息全部是 `side-effect file exists! cmd.Start() was executed despite external cancel`（[delay-sweep-main.log](evidence/delay-sweep-main.log)）
     - 症狀 2：`caaa09c` 4/430（延遲 0.023～0.025s），`a84dc3a` 0/150，main 0/410，見「症狀 2 原因」
2. ✅ **AC-2：測試改等意圖 sidecar** — 驗證方式：AC-2 的檢查方式
   - 產出：`tests/integration/cli_test.go` 的修正；反向檢查與回歸結果（寫回本節）
   - 相依：步驟 1
   - 完成判準：diff 不含固定等待或重試；反向檢查失敗；本機最小回歸通過，被 SKIP 的測試列名不計為通過
   - 完成證據（2026-10-21，修正提交 `d38a632`）：
     - diff：移除 `time.Sleep(50 * time.Millisecond)`，改成每 10ms 檢查一次 sidecar，出現就放鎖；10 秒內沒出現，或 cancel 在寫出前就結束，都以 `t.Fatalf` 失敗。沒有重試，也沒有加長固定等待。紅燈即 AC-1 的延遲注入失敗：這支測試本身就是整合層，沒有另一層單元保障可分開取得紅燈，因此合併為單迴圈
     - 反向檢查：在 `d38a632` worktree 套用 [reverse-check-run.diff](evidence/reverse-check-run.diff)，讓 runner 的 `cmd.Start` 前檢查找不到 sidecar，不注入延遲 `-count=10`：exit 1，10/10 為 `side-effect file exists!`（[reverse-check.log](evidence/reverse-check.log)），證明修正後的測試仍抓得到產品端守門失效
     - 精煉：code-simplify 檢查為 no-op，等待迴圈只用一次，沿用同檔的 flock 解鎖與 `t.Fatalf` 寫法
     - 本機最小回歸（`d38a632`，預設 TMPDIR）：`go vet ./...` exit 0；`go test -count=1 -race -v ./...` exit 0，23 個 package 全部 `ok`，本測試 PASS（2.17s）；`go build ./cmd/asura` exit 0
     - 被 SKIP 的 14 項不計為通過，都是需要固定版本 runtime 或登入憑證的 live／contract 測試，本次沒有改到 `internal/backend`：TestAdapter_Run_Live、TestCodexProbe_BinaryVersion、TestCodexProbe_ContextCancellation、TestCodexProbe_Live、TestCodexProbe_NonInteractive_JSONL、TestCodexProbe_ReadOnlySandbox、TestCodexProbe_WorkspaceWriteSandbox、TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly、TestINT001、TestINT007、TestLiveAdapter（2 處）、TestUtilityCapacityProbe、TestUtilityLiveProbe
     - 環境紀錄：當天 /tmp（tmpfs）一度配額已滿，`go build` 回報 `disk quota exceeded`，所以延遲掃描與壓測都把 TMPDIR 指到家目錄。用同一設定跑完整回歸時，Unix socket 路徑超過 107 bytes，`bind: invalid argument` 造成 7 個 package 失敗；改用 /tmp 底下指回家目錄的 symlink，又因 realpath 不符造成 13 個 package 失敗。這兩輪都是環境問題，不計入。/tmp 空間恢復後，以預設 TMPDIR 重跑才是上列結果
3. ✅ **AC-3：延遲注入對照與負載壓測** — 驗證方式：AC-3 的檢查方式
   - 產出：命令原文、條件與修正前後的 PASS／FAIL 次數（寫回本節，紀錄存 `evidence/`）
   - 相依：步驟 2
   - 完成判準：修正前三個延遲都至少失敗 1 次，修正後全部 0 失敗；負載壓測 0 失敗；PR CI 一次全綠
   - 完成證據（2026-10-21，go1.27.2 linux/amd64，12 核）：
     - 執行檔：修正前 `23af953`、修正後 `d38a632` 各自建 worktree，套用同一份 [delay-injection.diff](evidence/delay-injection.diff)，以 `go test -race -c ./tests/integration` 建立
     - 延遲對照：在 `tests/integration` 執行 `ASURA_I70_CANCEL_DELAY=<d> <test binary> -test.run '^TestCLI_LatestReview_H01_PathEscapedTaskIDHandshake$' -test.count=20 -test.v`，未加 CPU 限制

       | 延遲 | 修正前 `23af953` FAIL／20 | 修正後 `d38a632` FAIL／20 |
       |---|---|---|
       | 41ms | 7 | 0 |
       | 45ms | 20 | 0 |
       | 300ms | 20 | 0 |

       修正前的失敗全部是 `side-effect file exists!`（[ac3-delay-23af953.log](evidence/ac3-delay-23af953.log)、[ac3-delay-d38a632.log](evidence/ac3-delay-d38a632.log)）
     - 負載壓測：`d38a632` 不啟用注入，背景 6 個 `taskset -c 0,1 yes > /dev/null`，`taskset -c 0,1 <test binary> -test.run '^TestCLI_LatestReview_H01_PathEscapedTaskIDHandshake$' -test.count=100 -test.v`：exit 0，PASS 100／FAIL 0（[ac3-load-d38a632.log](evidence/ac3-load-d38a632.log)）
     - PR CI 一次全綠：PR #71 在 `b2de3f6` 的 [run 39550898789](https://github.com/raybird/Asura/actions/runs/39550898789)（pull_request）為 success，`TestCLI_LatestReview_H01_PathEscapedTaskIDHandshake` PASS（5.00s）。同一 HEAD 的 push run 39550865186 被 workflow `concurrency.cancel-in-progress` 取消（conclusion=cancelled），不是測試失敗

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-10-21 | 建立；以延遲注入判定症狀 1 為測試時序假設、症狀 2 為 `caaa09c` 已由 `a84dc3a` 修正的競態；Q1～Q4 回答後核准 AC-1～AC-3 | Raybird |
| 2026-10-21 | PR #71 獨立審查 PASS（審查 `23af953..ab3021f`，[報告](https://github.com/raybird/Asura/pull/71#issuecomment-6309265775)），無 MUST FIX；依 SHOULD FIX 更正 TBD-1 的時間描述：41ms 是注入延遲的門檻，不是 cancel 寫出 sidecar 實際所需的時間 | Raybird |
| 2026-10-21 | PR #71 第二輪獨立審查 PASS（審查完整範圍 `23af953..1638f5c`，[報告](https://github.com/raybird/Asura/pull/71#issuecomment-6309295790)），無 MUST FIX、SHOULD FIX；NICE TO HAVE 四項中，清除調查用 worktree 一項已在結案前完成，其餘三項（測試註解點名 `taskCancelIntentPath`、失敗路徑先殺子行程、症狀 2 的措辭以程式機制為主）未處理 | Raybird |
| 2026-10-21 | PR #71 squash 合併至 main（merge commit `6f4d4e9`，合併內容即審查的 HEAD `1638f5c`）；GitHub #70 由 `Closes #70` 自動關閉，issue 結案。合併後 main push CI [run 39555053821](https://github.com/raybird/Asura/actions/runs/39555053821) 為 success，本測試 PASS（3.76s） | Raybird |

---
**建立日期**: 2026-10-21  
**分級**: Small — 只改一個測試函式，不改產品程式  
**風險**: Medium — 開始時根因未知，可能是產品缺陷而非測試時序問題\
**狀態**: 已完成 — PR #71 於 2026-10-21 合併（merge commit `6f4d4e9`）
