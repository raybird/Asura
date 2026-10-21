# Issue 51 - Flaky test：TestRuntimeSelectionLocalCancel 偶發 runtime did not start（#48 症狀 1） (Small)

## 概述

來源：[GitHub #51](https://github.com/raybird/Asura/issues/51)。`cmd/asura/runtime_selection_test.go` 的 `TestRuntimeSelectionLocalCancel` 在 CI 等了 5 秒都沒看到 task 登記 PGID。#48 處理了另一種症狀（`task status is failed`），這一種本機 340 次壓力測試都沒重現，於是另開本 issue 追蹤。

| 日期 | CI run | 被測提交 | 訊息 |
|---|---|---|---|
| 2026-10-04 | [37198088965](https://github.com/raybird/Asura/actions/runs/37198088965) | `30cc401`（`issue-41-execution-audit`） | `runtime_selection_test.go:112: runtime did not start`（5.01s） |

2026-10-13～2026-10-21 的 CI 失敗紀錄（含重跑前的 attempt）中，本測試沒有再出現這個症狀；同期的失敗只有 #54、#60 的測試。#60 雖然也回報 `audit_persistence_failed`，但機制是等 writer lock 超過 busy_timeout，與本 issue 的 snapshot 升級競態不同。

### 原因（2026-10-21 查明）

`30cc401` 不在 main 上：它是 PR #42（#41）開發中的中間提交。#41 當天就查到這次失敗，記錄在 [issue-41 驗證紀錄「CI WAL 並行 writer 修正」](../issue-41/evidence/verification.md)：

- audit append／recovery 用 deferred transaction 先讀後寫，另一個連線寫入後 read snapshot 已過期，升級成寫鎖時立即失敗，回報 `audit_persistence_failed`
- runner 的 guard 寫 audit 失敗，任務在 Run 開始前就轉為 `failed`，不會登記 PGID，測試因此等滿 5 秒
- #41 本機修正前 `-count=40` 失敗 3 次，訊息相同；修正提交 `2769542` 在首次讀取前以 `UPDATE task_execution_audits SET digest=digest WHERE 0` 取得 writer lock，之後 `-count=50` 全過
- PR #42 於 2026-10-05 squash 合併為 `db7967c`，`2769542` 的修正內容隨之進入 main；`30cc401` 與 `2769542` 兩個中間提交本身都不在 main 上，main 從未包含有問題的版本

#48 的調查以 `30cc401` 為比較基準，判定「之後相關程式沒有語意變化」，沒注意到它不是合併後的版本，所以把 #41 已修好的 regression 當成未知原因。

## 涉及檔案

- `docs/issues/issue-51/README.md` — 原因、重現證據與結案紀錄
- `docs/issues/issue-48/README.md` — Timeline 補一行更正，指向本 issue
- `docs/agents/project.md` — 「本地 gate 與案例」登記地雷 L-1：把 PR 中間提交當成 main 基準
- `docs/issues/issue-51/evidence/` — 重跑紀錄與診斷補丁

本 issue 不改程式。重現用的診斷輸出只套在 scratchpad 的 worktree，不進交付。

## 驗收條件

1. **AC-1**：在 `30cc401` 上以 #41 的同一命令重現 `runtime did not start`，runner 端診斷顯示任務沒有登記 PGID 就已 `failed`，且錯誤與 audit 寫入失敗相關，證明與 #41 查到的 WAL 競態是同一機制 — 檢查方式：在 `30cc401` 的 worktree 套用 #48 的失敗診斷（只改測試），執行 `go test -count=40 -race ./cmd/asura -run '^TestRuntimeSelectionLocalCancel$' -v`，記錄失敗次數與診斷輸出
2. **AC-2**：同一命令、同一機器、同樣的診斷補丁下，修正提交 `2769542` 與目前 main 都沒有出現 `runtime did not start` — 檢查方式：在兩個版本執行 AC-1 的命令，記錄各自的 PASS／FAIL 次數與失敗訊息
3. **AC-3**：結案紀錄一致：本 README 狀態記為已完成，結案依據寫明由 #41 的 `2769542` 修正、本 issue 無程式變更；issue-48 Timeline 補一行更正；project.md 登記「把 PR 中間提交當成 main 基準」的地雷，證據連到 issue-48 與本 issue；GitHub #51 由本 issue 的 PR 關閉 — 檢查方式：人工核對兩份 README、project.md 地雷條目與 GitHub #51 狀態

- **核准日期**: 2026-10-21
- **核准來源**: GitHub #51「完成判準」：「沿用 #48：先找出原因並取得可重現的紅燈，不以加長等待或重試掩蓋」。2026-10-21 使用者以 `/dev-cycle issue:51` 要求推進；同日 Q1「#51 要怎麼結案？」回答「補證據後已完成 (Recommended)」，接受建議：建 README 記錄根因與 #41 證據，在 worktree 以 `30cc401` 重跑取得本 issue 自己的紅燈、修正後版本同條件取得綠燈，以「已完成——由 #41 的 2769542 修正，本 issue 無程式變更」結案，文件 PR 經獨立審查後關閉 GitHub #51（AC-1～AC-3）。Q2「要不要在 issue-48 補一筆更正？」回答「補 Timeline 一行 (Recommended)」（AC-3）。同日 PR #68 審查後，Q3「要怎麼處理〔地雷登記〕？」回答「本 PR 補上 (Recommended)」，AC-3 增列 project.md 地雷；Q4「要再加 NOTE 嗎？」回答「維持只補 Timeline」，AC-3 不變

## 風險與首要驗證

**風險**: Low — 不改程式，原因與修正已有 #41 的紅綠證據；唯一未知是 `30cc401` 能否在本機再次重現，重現不了時改為引用 #41 證據並回報

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|------|------|------|------|
| TBD-1 | PR #68 審查 SHOULD FIX 2（`需決策`）：「把 PR 中間提交當成 main 基準」是否登記為 project.md 地雷 | 已解決 | 2026-10-21 Q3 回答「本 PR 補上 (Recommended)」：#48 實際踩過，之後比對歷史 CI 失敗時還可能再踩；寫入 project.md 地雷 L-1，AC-3 增列 |
| TBD-2 | PR #68 審查 NICE TO HAVE 3（`需決策`）：issue-48「症狀 1 調查」被推翻的段落旁是否加帶日期的 NOTE | 已解決 | 2026-10-21 Q4 回答「維持只補 Timeline」：沿用 Q2 的決定，issue-48 只有 Timeline 一行更正，內文不動 |

## 實作與驗證步驟

1. ✅ **AC-1：在失敗提交重現** — 驗證方式：AC-1 的檢查方式
   - 產出：命令、條件、失敗次數與 runner 端診斷（寫回本節）
   - 相依：無
   - 完成判準：至少失敗 1 次，且診斷顯示 PGID 為 0、任務 `failed`、錯誤與 audit 寫入相關；0 次失敗時停止，回報並交由使用者決定是否改為只引用 #41 證據
   - 完成證據（2026-10-21，go1.27.2 linux/amd64，12 核，未加 CPU 限制或背景負載）：
     - 條件：`git worktree add --detach <dir> 30cc401`，套用 [diagnostic-patch.diff](evidence/diagnostic-patch.diff)（即 #48 加入的失敗診斷，只改測試），在該 worktree 執行 `go test -count=40 -race ./cmd/asura -run '^TestRuntimeSelectionLocalCancel$' -v`
     - 結果：exit 1，PASS 34／FAIL 6（[rerun-30cc401.log](evidence/rerun-30cc401.log)）
     - 其中 4 次是症狀 1（約 5.03s）：`runtime_selection_test.go:124: runtime did not start`，runner 端皆為 `status=failed pgid=0 err="audit_persistence_failed\naudit_persistence_failed"`，stderr `error: audit_persistence_failed`。任務沒有登記 PGID 就因 audit 寫入失敗而 failed，與 #41 查到的機制一致
     - 另外 2 次是 #48 的症狀 2（約 0.27s，`task is not running: task status is failed`，runner 端 `empty_result`），#48 已在 main 修正，不計入本 issue
2. ✅ **AC-2：修正後版本對照** — 驗證方式：AC-2 的檢查方式
   - 產出：兩個版本的 PASS／FAIL 次數（寫回本節）
   - 相依：步驟 1
   - 完成判準：兩個版本都沒有 `runtime did not start`
   - 完成證據（2026-10-21，與步驟 1 同一機器、同一命令，依序執行）：
     - `2769542`（套用同一份診斷補丁）：exit 0，PASS 40／FAIL 0（[rerun-2769542.log](evidence/rerun-2769542.log)）
     - main `2c3dde0`（測試本身已含診斷）：exit 0，PASS 40／FAIL 0（[rerun-main-2c3dde0.log](evidence/rerun-main-2c3dde0.log)）
     - `2769542` 的父提交就是 `30cc401`，同條件下症狀 1 由 4/40 變 0/40。該提交在 `cmd/asura` 與 daemon 的其他改動只涉及 admission 被拒時的輸出，這次失敗的任務已通過 admission，不經過那段路徑
3. ✅ **AC-3：結案紀錄** — 驗證方式：AC-3 的檢查方式
   - 產出：本 README、issue-48 Timeline 與 project.md 地雷 L-1；PR 以 `Closes #51` 關閉 GitHub issue
   - 相依：步驟 2
   - 完成判準：兩份 README 與 project.md 地雷 L-1 的敘述一致，GitHub #51 在 PR 合併後為 closed
   - 完成證據（2026-10-21，人工核對）：issue-48 Timeline 的更正、project.md 地雷 L-1 與本 README「原因」一致，經 PR #68 兩輪獨立審查查核；PR #68 合併後 GitHub #51 為 closed（closedAt 2026-10-21T03:17:17Z）

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-10-21 | 建立；查出 `30cc401` 是 #41 的中間提交，原因已由 `2769542` 修正；Q1、Q2 回答後核准 AC-1～AC-3 | Raybird |
| 2026-10-21 | PR #68 首輪獨立審查 PASS（審查 `2c3dde0..d5ad671`，[報告](https://github.com/raybird/Asura/pull/68#issuecomment-6306939209)），無 MUST FIX；依 Q3 修訂 AC-3 增列 project.md 地雷，並修正 squash 合併的措辭 | Raybird |
| 2026-10-21 | PR #68 第二輪獨立審查 PASS（審查完整範圍 `2c3dde0..e9980aa`，[報告](https://github.com/raybird/Asura/pull/68#issuecomment-6307018990)），無 MUST FIX、SHOULD FIX；NICE TO HAVE 四項中，步驟 3 完成判準與分級說明兩項於結案時補上，地雷證據行號與 log 檔頭兩項未處理 | Raybird |
| 2026-10-21 | PR #68 squash 合併至 main（merge commit `c82e8e4`，合併內容即審查的 HEAD `e9980aa`）；GitHub #51 由 `Closes #51` 自動關閉，issue 結案。合併後 main push CI [run 39537742081](https://github.com/raybird/Asura/actions/runs/39537742081) attempt 1 為 failure，失敗的是與本 issue 無關的 `TestCLI_LatestReview_H01_PathEscapedTaskIDHandshake`（`cli_test.go:962` side-effect marker）；本測試 `TestRuntimeSelectionLocalCancel` PASS（0.66s） | Raybird |

---
**建立日期**: 2026-10-21  
**分級**: Small — 只更新文件，不改程式  
**風險**: Low — 原因與修正已有 #41 的紅綠證據\
**狀態**: 已完成 — PR #68 於 2026-10-21 合併（merge commit `c82e8e4`）
