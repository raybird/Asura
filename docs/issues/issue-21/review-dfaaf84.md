# Issue #21 PR #22 獨立審查報告

- 日期：2026-09-24
- 範圍：完整 PR #22，TARGET `main`
- Reviewed BASE SHA：`9d5c2a3ecae3a59341ec63b69fb9e573ef1b5c79`
- Reviewed HEAD SHA：`dfaaf84d5a556b4c3c3c15b0146c8831ef988bd6`
- Reviewed patch-id：`3afda32c5c9eafe5e266166ab798f241f74f6216`
- 獨立 reviewer：Codex 隔離 reviewer `/root/issue21_independent_review`，非原實作者
- Review artifact：`docs/issues/issue-21/review-dfaaf84.md`
- 版本核對：本機 HEAD 與 PR #22 source HEAD 相同，建立報告前工作樹乾淨。固定 BASE..HEAD 為完整 PR；相對上一候選 `0d9f479`，只新增兩項 Daemon 回歸測試並更新兩處任務文件，**沒有產品程式變更**。

## 問題與風險

- **MUST FIX：無。** 前次提出的證據持久力 SHOULD FIX 已處理。[新增測試](../../../internal/daemon/memory_query_context_test.go#L177)以真實 Unix socket、`handleConn`、SQLite、授權 workspace 分別驗證 page/detail 在 `CloseWrite()` 後仍回覆正確資料；[malformed JSON 測試](../../../internal/daemon/memory_query_context_test.go#L220)驗證合法請求的回覆先於錯誤回覆，且 handler 結束。這些測試具有與實作獨立的具體期望，沒有 mock 核心回覆。
- **SHOULD FIX：無。** [watcher](../../../internal/daemon/daemon.go#L485) 仍在 `RawConn.Control` 回呼有效期內 `Dup` fd，`CloseOnExec` 並在錯誤及正常退出路徑關閉複本；新測試沒有改弱既有取消或半關閉驗收。
- **NICE TO HAVE：無。**

## 已查核維度

### 規格、驗收與完整 PR

[WEBMEM-001～010](README.md#gherkin-驗收劇本)於 2026-09-23 核准，[G1～G5](technical-analysis.md)由同日「核准 G-TBD-1～5 現行提案」核准；規格可觀察語意與基線核准未變。[任務證據](implementation-plan.md)及 PR Proof of Test 均涵蓋十項未豁免 Scenario。本次核對固定 BASE..HEAD 完整 diff、前輪獨立審查與本 HEAD 新測試／文件差異。

| Scenario | 程式、證據及失敗面 | 結論 |
|---|---|---|
| WEBMEM-001 | Web session／Host／變更方法 Origin；HTTP fixture 驗未登入、跨 Origin 拒絕且零寫入／backend 呼叫。合法 GET 按 G1。 | PASS |
| WEBMEM-002 | Daemon canonical 授權、workspace＋ID 精確 scope；整合 fixture 覆蓋 A/B、父子、symlink、越界、撤權及跨 scope／缺失 ID。 | PASS |
| WEBMEM-003 | SQLite keyset 50+1、512 bytes 摘要、64 KiB detail sentinel，IPC／HTTP 各 512 KiB；10k、相同 timestamp、過大正文／metadata fixture；page/detail context 到達兩種 storage `QueryContext`。 | PASS |
| WEBMEM-004 | Web 手動新增、確認刪除、重啟持久化；刪除不改舊 Task 密封快照。 | PASS |
| WEBMEM-005 | Web DTO allowlist，Daemon 解析 explicit IDs；跨 scope／缺失／已刪 ID 在 backend 前拒絕。 | PASS |
| WEBMEM-006 | 預設 off；明確 bounded 沿用 selector、額度、provenance、stage admission。 | PASS |
| WEBMEM-007 | 送出前顯示 Request ID；同 ID 原 payload 與 IDs 換序回同 Task，變動模式衝突；503 原 payload 重送 UI probe。 | PASS |
| WEBMEM-008 | 歷史稽核取保存 Task context／trace，分開顯示 queued `first_stage`、legacy／off／0 auto／unknown／estimated。G5 既有 status 大小界線已揭露。 | PASS |
| WEBMEM-009 | `textContent`、workspace／session／Task 世代 guard；UI `taskRace` 涵蓋同 workspace A 遲到、長結果分頁與輪詢。 | PASS |
| WEBMEM-010 | 固定 HEAD 全套 vet、race test、build、四種 UI probe 與 diff check 通過；完整 race 包含 WEBMEM 整合、PGID cancel、queue/redelivery 等既有套件。 | PASS |

### 新測試、取消路徑與相關失敗面

| 輸入／狀態 | 預期與實際證據 |
|---|---|
| Client 完整關閉，page/detail 查詢阻塞 | 真實 Unix socket 的 `POLLHUP` 取消連線 context；[9 秒子 context](../../../internal/daemon/daemon.go#L538) 傳到 [page `QueryContext`](../../../internal/storage/sqlite_memory_page.go#L102) 與 [detail `QueryRowContext`](../../../internal/storage/sqlite_memory_page.go#L144)。阻塞替身對兩 action 斷言 `context.Canceled`、deadline 與及時返回；race 20 輪通過。 |
| Client `CloseWrite()` 但保留讀取方向 | [新測試](../../../internal/daemon/memory_query_context_test.go#L177) 分別回覆 page 的 `m1` 與 detail 的 `body`；既有 `projects` 半關閉測試也通過。只監看 `POLLHUP`／`POLLERR`，未把單向 EOF 認作完整斷線。 |
| 合法 `projects` 緊接 malformed JSON | [新測試](../../../internal/daemon/memory_query_context_test.go#L220) 要求先成功、再固定 `invalid request payload`，handler 結束；單一同步 decoder 沒有舊並行 reader 的 channel/backpressure 競態。 |
| 舊 reader 負向對照 | 以暫存 Go overlay 將 `daemon.go` 換為 `c69ff9c`，因舊版沒有新 watcher／query helper，僅從 overlay 的測試檔去掉不相容的取消測試，保留兩項新測試原文與斷言；`go test -overlay=/tmp/issue21_review_oldreader_overlay.json ./internal/daemon -run '^Test(DaemonBoundedMemoryIPCReplyAfterClientHalfClose|DaemonIPCReplyBeforeMalformedPayload)$' -count=20 -timeout=120s` exit 1，兩類均實際出現 `EOF`。這是行為紅燈，不是編譯錯誤；目前 HEAD 同組測試 race 20 輪 exit 0。 |
| watcher fd 關閉／重用時序 | `Dup` 發生於 `RawConn.Control` callback 內，watcher poll 獨立 fd；`Control` 失敗、`Dup` 失敗及正常 watcher 結束皆有適當回收或無已開啟複本。先前使用原 fd 超出 callback 有效期的風險已消除。 |
| 授權、資源上限、重送、Task 競態 | 本次沒有產品邏輯變更；完整 WEBMEM 整合、storage、UI 探測及 race 全套仍通過。 |

本 HEAD 實際驗證：`go vet ./...` exit 0；`go test -race ./internal/daemon -run '^Test(BoundedMemoryQueryCanceledOnIPCDisconnect|DaemonIPCReplyAfterClientHalfClose|DaemonBoundedMemoryIPCReplyAfterClientHalfClose|DaemonIPCReplyBeforeMalformedPayload|DaemonBoundedMemoryIPC)$' -count=20 -timeout=120s` exit 0；`go test -count=1 -race ./...` exit 0；`go build -o /tmp/asura_issue21_review_dfaaf84 ./cmd/asura` exit 0；`node docs/issues/issue-21/evidence/ui-probe.cjs` 與 `audit`、`taskRace`、`retry503` 各 exit 0；`git diff --check BASE..HEAD` exit 0。暫存 overlay 只在 `/tmp`，未修改 repo 程式或測試。

架構與安全：Web 仍走 Daemon IPC，Daemon 每次 canonical 授權；CLI 全量 `memory_list/get` 與既有 Task status 路徑未改。x/sys Unix `Poll`／`Dup` 是直接依賴，未新增對外網路入口。

## 豁免、待確認與限制

- 無未核准 Scenario 或 gate 豁免。
- G5 已核准首版沿用現有 Task status，仍可能傳完整 `resolved_context`／result；新 page/detail 512 KiB 上限不適用該路徑。
- 取消測試的阻塞替身證明 socket 斷線至 query context 的傳遞，不模擬真實 SQLite busy-lock。既有 `PRAGMA busy_timeout=10000` 可能使鎖等待中對已取消 context 的反應晚於 9 秒；此 HEAD 沒有改全域鎖政策，不能宣稱鎖競爭下查詢一定在 9 秒內停止。
- UI probe 是最小 DOM 模擬；HTTP→IPC→SQLite 另由整合測試驗證，不冒稱真實瀏覽器。
- 遠端 CI run `35953130013` 對應本 HEAD；本報告撰寫時狀態為 `in_progress`，交付時仍需確認最終結果。

## 流程判定

**PASS**。完整 PR 驗收與重要失敗面具證據，無 MUST FIX 或未解決 SHOULD FIX。結論只綁定上述 BASE／HEAD／patch-id；若協調者新增唯一只含本報告的非 merge 提交，須按 `review-evidence.md` 及 `verify-artifact.py` 驗證 artifact 例外後才可沿用。其他產品、測試或文件變更須重新審查。
