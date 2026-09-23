# Issue #21 PR #22 獨立審查報告

- 日期：2026-09-24
- 範圍：完整 PR #22；TARGET `main`
- Reviewed BASE SHA：`9d5c2a3ecae3a59341ec63b69fb9e573ef1b5c79`
- Reviewed HEAD SHA：`0cd9e29dc041c75e42e620f6c53ac9f77afc07d3`
- Reviewed patch-id：`bad5a316a160033e2b0eb5ff13b68e84d4f22721`
- 獨立 reviewer：Codex 隔離 reviewer `/root/issue21_independent_review`（非原實作者）
- Review artifact：`docs/issues/issue-21/review-0cd9e29.md`
- 版本核對：本機 HEAD、PR source HEAD 與上述 SHA 一致；建立本報告前工作區乾淨。以固定 BASE..HEAD 完整 diff、[核准規格](README.md#gherkin-驗收劇本)、[G1～G5](technical-analysis.md)及 [Task 證據](implementation-plan.md)審查；既有 `review-ddd8c1e.md` 的 PASS 不沿用至此新 HEAD。

## 問題與風險

- **MUST FIX：無。** 2026-09-24 審查指出的同 workspace Task A 遲到覆寫 B，已用 [Task 顯示世代](../../../internal/web/page.go#L132)修正。新查詢／提交／取消及 workspace／session 清除都使舊回應失效；長結果分頁與輪詢也檢查相同 token。[`taskRace` 探測](evidence/ui-probe.cjs#L133)先紅（A `failed` 覆寫 B `completed`），修正後本次 exit 0，並涵蓋 A 長結果分頁與 A 輪詢晚於 B。原 G4 的 503 原 payload 重試亦維持通過。
- **SHOULD FIX：Daemon 記憶查詢取消傳遞。** [daemon.go](../../../internal/daemon/daemon.go#L1000) 的 `memory_page`／`memory_detail_bounded` 以 `context.Background()` 查 SQLite；[Web IPC](../../../internal/web/server.go#L289) 設定 10 秒 deadline 只限制 Web 等待，客戶端離開或逾時不直接取消正在執行的 SQLite 查詢。建議在後續工作使 Daemon 每條連線的查詢 context 隨連線失效而取消，並加入逾時／鎖等待測試。現有 keyset 50+1、欄位 sentinel、512 KiB 回應界線及 IPC deadline 保留；沒有證據顯示本 PR 的正常查詢越界，此為已揭露的資源效率改善，不阻塞本次驗收。
- **NICE TO HAVE：無。**

## 已查核維度

### 規格、驗收與證據

2026-09-23 使用者「$dev-cycle 核准」與「核准 G-TBD-1～5 現行提案」分別核准 WEBMEM-001～010 及具體技術政策；核准基線 `21aafa8` 到本 HEAD 的 Gherkin 行為未見語意變更。PR Proof of Test 的十列與未豁免 Scenario 集合相等。2026-09-24 修訂集中在顯示競態、接受時 `first_stage`、UI probe 與 CI，未擴大產品授權或資料界線。

| Scenario | 實作與證據核對 | 結論 |
|---|---|---|
| WEBMEM-001 | `ServeHTTP` 共用 session／Host／變更方法 Origin；真實 HTTP→IPC→SQLite `TestWEBMEMHTTPBoundary` 驗證未登入／跨站變更拒絕且無寫入／backend 呼叫；GET 符合 G1。 | PASS |
| WEBMEM-002 | Daemon 每次 canonical 授權；A/B、父子、symlink、越界及撤權由真實 IPC fixture 覆蓋；跨 scope／缺失 ID 同外部結果。 | PASS |
| WEBMEM-003 | SQLite 50+1 keyset、欄位 sentinel、複合索引；detail 在 IPC 前有界，Daemon／Web 編碼大小檢查；10k、同時間戳、長內容、超限 metadata/detail 測試。 | PASS |
| WEBMEM-004 | Web 手動新增／確認刪除與失敗映射；SQLite 重啟及刪除後已密封 Task 快照由整合／既有測試覆蓋。 | PASS |
| WEBMEM-005 | DTO allowlist 只傳核准欄位，Daemon 解析 explicit IDs 並密封；跨 scope、缺失、已刪 ID 在 backend 前拒絕。 | PASS |
| WEBMEM-006 | 空選取預設 off，明確 bounded 使用既有 selector／額度／provenance／stage admission；真實提交保存 context 測試。 | PASS |
| WEBMEM-007 | 同 ID 原 payload／變更順序回同 Task，不同 payload 409，backend 一次；UI `retry503` 與原 retry probe 確認可見入口及凍結 JSON。 | PASS |
| WEBMEM-008 | UI 讀已保存 Task context／trace，呈現 IDs、policy、score、skip、stage admission；`audit` probe 新增 queued Task 的 `recall.budget.first_stage` 與尚無 trace 的區分，先紅後綠。 | PASS，保留 G5 既有 status 大小限制 |
| WEBMEM-009 | `textContent` 與 workspace／session generation 清除敏感狀態；新 Task token 阻止同 workspace 舊查詢、長結果、輪詢、提交／取消回應覆寫當前顯示。`taskRace`、`race` 及完整 UI probe 覆蓋。 | PASS |
| WEBMEM-010 | 本次 `go vet ./...`、`go test -count=1 -race ./...`、`go build -o /tmp/asura_issue21_review_0cd9e29 ./cmd/asura`、WEBMEM integration、四種 CI UI probe 對應命令及固定 diff check 均 exit 0；CI 已設定 Node 20 並執行 probe。 | PASS |

### 重要失敗面

| 輸入／狀態 | 預期、覆蓋與判定 |
|---|---|
| 無 session、跨 Origin 變更、帶不同 Origin 的 GET | 未授權不寫入／不呼叫 backend；GET 按 G1 不作服務端 Origin 拒絕，瀏覽器同源與 SameSite 限制另成立。入口與整合測試符合。 |
| A/B、父子、symlink 別名／撤權、跨 scope ID | 每次 canonical 授權與精確 scope，不洩漏 ID 存在性；真實 IPC／HTTP fixture 符合。 |
| 10k、同 timestamp、超大 detail、錯誤 cursor／metadata、JSON 膨脹 | SQL、IPC、HTTP 有界；storage／Daemon／HTTP 測試及 query plan 符合。SQL 投影不證明 SQLite 內部 BLOB I/O，文件已如實揭露。 |
| 回應遺失或 503、同 ID 重送、表單改動 | Request ID 請求前顯示，原 JSON 重試，同 Task／409 衝突及 backend 一次；UI／整合測試符合。 |
| **同 workspace** A 查詢晚於 B、A 長結果分頁晚於 B、提交 A 輪詢晚於 B | 目前顯示只由最新 Task token 更新；`taskRace` 依 B 的固定 status／result／audit 字面值斷言，三路均符合。 |
| workspace／session 切換後舊回應、腳本樣式記憶 | generation 失效舊回應、清除選取與 pending payload；使用文字節點；既有 `race`／完整 probe 符合。 |
| queued bounded Task 尚無執行 trace | 顯示保存的接受時 `first_stage` 預估與「Stage trace：未提供」，不把尚未執行寫成已執行；`audit` probe 符合。 |
| Web 客戶端中止、Daemon SQLite 查詢尚在執行 | Web 有 10 秒 IPC deadline，Daemon 仍以 Background context 查詢；已揭露為 SHOULD FIX，沒有把 deadline 宣稱成 SQLite 取消。 |

架構與安全：Web 仍不直連 SQLite；Daemon canonical 授權、CLI 原全量 `memory_list/get` 契約及 G5 status 路徑未變。新 UI token 只控制顯示，不改 Task 身分或後端執行。測試真偽：`taskRace` 以可控延遲製造 A/B 反序，斷言 B 的固定 status／result／audit；`audit` 驗證明確的 `first_stage` 內容；探測為最小 DOM 模擬，PR 已揭露非真實瀏覽器。CI UI 步驟的四條命令可重跑且本機均通過。

## 豁免、待確認與限制

- 無未核准 Scenario 或 gate 豁免。
- G5 首版沿用既有 Task status IPC，完整 `resolved_context`／result 大小不受新 memory page/detail 的 512 KiB 上限保護；此界線已由使用者核准。
- Daemon 記憶查詢取消傳遞列為 SHOULD FIX；本次沒有改 IPC 協定，保留上述資源界線與明確揭露。
- 本次沒有真實瀏覽器自動化；真實 HTTP→IPC→SQLite 由整合測試驗證，UI probe 檢查 DOM 更新與回應順序。

## 流程判定

**PASS**。此結論只綁定上述 Reviewed BASE／HEAD／patch-id；若新增唯一一個只含本報告的非 merge 提交，須按 `review-evidence.md` 的本機 artifact 例外核對後才可沿用 PASS。其後產品、測試或其他文件變更均需重審。
