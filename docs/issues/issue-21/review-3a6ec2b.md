# Issue #21 PR #22 獨立審查報告

- 日期：2026-09-24
- 範圍：完整 PR #22；TARGET `main`
- Reviewed BASE SHA：`9d5c2a3ecae3a59341ec63b69fb9e573ef1b5c79`
- Reviewed HEAD SHA：`3a6ec2b25f518613b1600be292ce049dfeee8bb0`
- Reviewed patch-id：`930d9da5385458d4bddf82e79f4c14a2f7cf1aa4`
- 獨立 reviewer：Codex 隔離 reviewer `/root/issue21_independent_review`（非原實作者）
- Review artifact：`docs/issues/issue-21/review-3a6ec2b.md`
- 版本核對：本機 HEAD 與 PR source HEAD 相同；建立本報告前工作區乾淨。固定 BASE..HEAD 包含完整 PR；相對前次產品 HEAD `0cd9e29`，新增先前審查報告、Task 4.3 證據及既有 PGID 取消測試的故障注入修正，**沒有產品程式變更**。

## 問題與風險

- **MUST FIX：無。** [取消測試](../../../internal/engine/cancel_test.go#L682)原以偵測進程組死亡後關閉 SQLite store 的競爭時序注入失敗，CI 曾得到 `got nil`。新測試以 SQLite `BEFORE UPDATE OF pgid` trigger 在 `NEW.pgid = 0` 時明確 `RAISE(ABORT, 'blocked pgid clear')`，並要求 `CancelTask` 錯誤同時含 `persist cleared process group` 與 trigger 的字面錯誤。相較舊版只斷言非 nil，這使失敗確定發生在 PGID 清除寫入，沒有弱化 fail-closed 判準，也沒有改產品取消邏輯。本次 `go test -count=30 -race ./internal/engine -run '^TestEngineCancel_ClearPGIDErrorFailsClosed$'` exit 0。
- **SHOULD FIX：Daemon 記憶查詢取消傳遞仍待改善。** [daemon.go](../../../internal/daemon/daemon.go#L1000) 的 `memory_page`／`memory_detail_bounded` 仍使用 `context.Background()`。Web 的 10 秒 IPC deadline 限制等待，客戶端中止不直接取消 SQLite 查詢。現有 keyset 50+1、欄位與回應上限降低正常查詢負擔；此項已在 PR 與前次報告揭露，沒有證據顯示本次 WEBMEM 驗收因此失敗。建議後續將連線生命週期傳入查詢 context，並驗證鎖等待／取消。
- **NICE TO HAVE：無。**

## 已查核維度

### 需求、驗收與證據

[WEBMEM-001～010](README.md#gherkin-驗收劇本)於 2026-09-23 由使用者「$dev-cycle 核准」；[G1～G5](technical-analysis.md)由「核准 G-TBD-1～5 現行提案」核准。核准基線 `21aafa8` 到本 HEAD 的 Gherkin 可觀察語意未變。PR Proof of Test 恰有十項未豁免 Scenario；任務證據保存在 [implementation-plan.md](implementation-plan.md)。先前 [同 workspace 競態審查](review-0cd9e29.md)不直接沿用至新 HEAD，以下再核對完整路徑。

| Scenario | 實作與證據核對 | 結論 |
|---|---|---|
| WEBMEM-001 | Web session／Host／變更方法 Origin 入口；`TestWEBMEMHTTPBoundary` 對未登入與跨站變更核對拒絕、零寫入／backend，合法 GET 按 G1 可讀。 | PASS |
| WEBMEM-002 | Daemon canonical 授權、workspace＋ID 精確 scope；真實 IPC fixture 覆蓋 A/B、父子、symlink、越界與撤權，缺失／跨 scope ID 同結果。 | PASS |
| WEBMEM-003 | SQLite keyset 50+1、摘要／detail sentinel 及索引；Daemon 編碼前、Web 編碼後檢查 512 KiB；10k、同 timestamp、超大內容與 metadata 測試。 | PASS |
| WEBMEM-004 | HTTP 手動新增、確認刪除、失敗碼與重啟持久化；既有測試驗證刪除不改已密封 Task 快照。 | PASS |
| WEBMEM-005 | Web DTO allowlist，不接受 client 密封 context；Daemon 解析 explicit IDs，跨 scope／缺失／已刪 ID 在 backend 前拒絕。 | PASS |
| WEBMEM-006 | 預設 off，明確 bounded 走既有 selector、額度、provenance 與 stage admission；真實提交測試核對保存 context。 | PASS |
| WEBMEM-007 | Request ID 在送出前顯示；同 ID 原 payload／改順序回同 Task，改模式 409；`retry`／`retry503` UI probe 核對按鈕可見與 JSON 不變。 | PASS |
| WEBMEM-008 | 稽核取已保存 Task context／trace；queued bounded 的 `first_stage` 與尚未執行 trace 分開呈現；legacy／off／0 auto／unknown／estimated 有 probe。 | PASS；G5 既有 status 大小限制已揭露 |
| WEBMEM-009 | 純文字顯示；workspace／session 及 Task 顯示世代阻止 A 遲到覆寫 B，涵蓋長結果分頁與輪詢；UI `race`／`taskRace` 探測。 | PASS |
| WEBMEM-010 | 本次 `go vet ./...`、`go test -count=1 -race ./...`、`go build -o /tmp/asura_issue21_review_3a6ec2b ./cmd/asura`、WEBMEM integration、完整／audit／taskRace／retry503 UI probe 與固定 diff check 均 exit 0；CI 已納入四條 UI probe。 | PASS；新 HEAD 的遠端 CI 在寫報告時尚在執行 |

### 相關失敗面

| 輸入／狀態 | 預期與覆蓋判定 |
|---|---|
| 無 session、跨 Origin 變更、帶不同 Origin 的 GET | 變更拒絕無副作用；GET 按 G1 不誤作服務端 Origin 拒絕。HTTP 整合與 cookie／入口程式符合。 |
| 父子、symlink 別名／撤權、跨 scope／缺失 ID | 每次 canonical 授權、精確 scope、不洩漏 ID 存在性；IPC／HTTP fixture 符合。 |
| 10k、同 timestamp、超大 detail／metadata／body、JSON 逸出 | 有界 SQL 投影、IPC／HTTP bytes；storage／Daemon／HTTP fixture 與 query plan 符合。SQL 投影不證明 SQLite 引擎內部 BLOB I/O，文件已揭露。 |
| 回應遺失／503、同 ID 重送、表單改動 | UI 可重試原 JSON；Daemon 指紋與 backend 計數保障同 Task／衝突；probe 與 integration 符合。 |
| 同 workspace A 查詢、長結果或輪詢晚於 B；跨 workspace／session 遲到 | Task token 與 workspace／session generation 排除舊回應；可控順序 UI probe 斷言 B status／result／audit 不被覆寫。 |
| queued bounded 尚無 pipeline trace | 顯示保存的接受時 `first_stage` 預估與「Stage trace：未提供」；UI audit probe 符合。 |
| PGID 清除 SQLite 寫入失敗 | 取消不得宣告成功；trigger 只阻擋 `UPDATE OF pgid` 且 `NEW.pgid=0`，測試要求特定錯誤。race 單測 30 次及完整 race 通過，沒有改產品程式。 |
| Web 中止但 Daemon SQLite 查詢仍在執行 | Web IPC deadline 存在，Daemon context 未傳遞取消；維持已揭露 SHOULD FIX。 |

架構與安全：Web 不直接存取 SQLite，CLI 全量 `memory_list/get` 契約保留；Task 密封快照與 G5 status 路徑未變。測試真偽：新故障注入使用真實 SQLite trigger，而非 mock `CancelTask`；斷言與被測程式的輸入及錯誤傳播可不一致，因此不是同義反覆。UI probe 是已揭露的最小 DOM 模擬，真實 HTTP→IPC→SQLite 由 integration 測試涵蓋。

## 豁免、待確認與限制

- 無未核准 Scenario 或 gate 豁免。
- G5 首版 Task status 可能傳完整 `resolved_context`／result，新 memory page/detail 的 512 KiB 上限不涵蓋該既有路徑；使用者已核准。
- 遠端 CI：同一 HEAD 的較早 push run 因 concurrency 設定取消，`gh pr checks` 顯示的該筆「fail」不是測試斷言失敗；PR run `35929438440` 在本報告撰寫時為 `in_progress`。本機上述全套命令已通過，遠端 CI 最終狀態仍需交付流程確認。
- Daemon 記憶查詢取消傳遞列為 SHOULD FIX；本次沒有修改 IPC 協定或以 Web deadline 冒充 SQLite 取消。

## 流程判定

**PASS**。此結論僅綁定上述 Reviewed BASE／HEAD／patch-id；若新增唯一一個只含本報告的非 merge 提交，須依 `review-evidence.md` 的本機 artifact 例外檢查後才可沿用。任何後續產品、測試或其他文件變更均需重審。
