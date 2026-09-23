# Issue #21 PR #22 獨立審查報告

- 日期：2026-09-23
- 範圍：完整 PR #22；TARGET `main`
- Reviewed BASE SHA：`9d5c2a3ecae3a59341ec63b69fb9e573ef1b5c79`
- Reviewed HEAD SHA：`ddd8c1ec1b3477604a3110345c4782209fe3f0cc`
- Reviewed patch-id：`d7770f4019f11e53ddec9b498d0bda86457a671f`
- 獨立 reviewer：Codex 隔離 reviewer `/root/issue21_independent_review`（非原實作者）
- Review artifact：`docs/issues/issue-21/review-ddd8c1e.md`
- 版本核對：本機 HEAD 與 PR source HEAD 均為 `ddd8c1e`；建立本報告前工作區乾淨。固定 BASE..HEAD 的完整 diff 與 2026-09-23 已核准 [WEBMEM-001～010](README.md#gherkin-驗收劇本)、[G1～G5](technical-analysis.md)及 [Task 證據](implementation-plan.md)逐項對照；首輪 [RETURN 報告](review-682193c.md)亦納入。

## 問題與風險

- **MUST FIX：無。** 首輪 WEBMEM-007 的 HTTP 5xx 重試入口缺口已修正：[page.go](../../../internal/web/page.go#L402) 對已知 4xx 清除待重送 payload，對 5xx／其他未確認回應顯示原請求重試；[UI 探測](evidence/ui-probe.cjs#L96) 新增 `retry503` 與可見性斷言，核對重送 JSON 與原請求完全相同。Task 3.3 記錄 `retry503` 先紅（`true !== false`）後綠。本次執行 `node docs/issues/issue-21/evidence/ui-probe.cjs retry503`、`retry` 及完整 probe 均 exit 0。
- **SHOULD FIX：無。**
- **NICE TO HAVE：無。**

## 已查核維度

### 驗收與證據

核准來源是 2026-09-23 使用者「$dev-cycle 核准」及「核准 G-TBD-1～5 現行提案」；核准基線 `21aafa8` 到本 HEAD 未改 WEBMEM-001～010 的可觀察語意。PR Proof of Test 恰涵蓋十個未豁免 Scenario；G1～G5 已定案。完整 diff 包含新增的有界 SQLite／IPC／HTTP 路徑、UI、文件與測試，未把舊版 `review-9d5c2a3.md` 或首輪 RETURN 當成通過證據。

| Scenario | 實作、測試與限制核對 | 結論 |
|---|---|---|
| WEBMEM-001 | `ServeHTTP` 共用 session／Host／變更方法 Origin；`TestWEBMEMHTTPBoundary` 確認拒絕路徑無寫入／backend 呼叫，合法 GET 可讀；SameSite 與瀏覽器同源限制按 G1 分開描述。 | PASS |
| WEBMEM-002 | Daemon 每次 canonical 授權，storage 以 workspace＋ID 精確比對；IPC fixture 覆蓋 A/B、父子、symlink、越界、撤權，跨 scope 與缺失 ID 同外部結果。 | PASS |
| WEBMEM-003 | SQLite 50+1 keyset、欄位 sentinel 與索引；IPC 編碼前、HTTP 編碼後均量測 512 KiB；10k／長 content／65,536 與 65,537 detail／legacy metadata fixture。 | PASS |
| WEBMEM-004 | HTTP CRUD、`manual` 來源及失敗不回 204；SQLite 重啟與已接受 Task 的密封快照由 integration／既有 storage 與 Daemon 測試核對；UI 確認刪除。 | PASS |
| WEBMEM-005 | Web DTO 僅接受核准欄位，Daemon 解析 IDs 並密封快照；跨 scope、缺失、已刪 ID 在 backend 前 404 且零額外 Task／backend。 | PASS |
| WEBMEM-006 | 空選取預設 off；明確 bounded 沿用原 selector／額度／provenance／stage admission，保存的 mode 與 policy 由真實提交測試核對。 | PASS |
| WEBMEM-007 | 同 ID／改順序重送同 Task、改內容或模式 409、backend 僅一次；UI 在請求前顯示 ID，網路例外及 **503** 均顯示可用重試並沿用原 JSON。 | PASS |
| WEBMEM-008 | UI 僅讀已保存 Task context／trace，顯示 explicit／auto IDs、政策、分數、略過及 stage admission；probe 涵蓋 legacy／off／0 auto／unknown／estimated。 | PASS，保留核准 G5 界線 |
| WEBMEM-009 | `textContent` 顯示記憶與稽核；generation 與 workspace 檢查排除切換或 session 失效後遲到回應，清除選取與待重送 payload；UI probe 覆蓋。 | PASS |
| WEBMEM-010 | 本次 `go vet ./...`、`go build -o /tmp/asura_issue21_review_build ./cmd/asura`、第二次 `go test -count=1 -race ./...`、完整 UI probe、WEBMEM integration 與固定 diff check 均 exit 0。 | PASS；見下方首輪 race 執行紀錄 |

### 相關失敗面與品質

| 輸入／狀態 | 預期與覆蓋判定 |
|---|---|
| 無 session、跨 Origin 變更、跨站 GET | HTTP 拒絕無副作用；有效 cookie 的 GET 不被錯誤地當成 Origin 拒絕。integration 與入口程式一致。 |
| 父子 scope、symlink 別名／撤權、跨 scope 或缺失 ID | Daemon canonical 授權及精確 storage scope；IPC／HTTP 測試確認不洩漏 ID 存在性。 |
| 10k 記憶、同 timestamp、超大 detail／metadata／body、JSON 逸出 | SQL 投影、IPC 與 HTTP 各層有界並有測試；`EXPLAIN QUERY PLAN` 使用新索引。Go 結果有界不等於證明 SQLite 引擎內部 BLOB I/O，計畫已如實揭露。 |
| 未知接受結果後網路例外或 HTTP 503、表單改動、同 ID 改模式 | 原 payload 重試、409 衝突、不產生另一 Task；UI probe 的可見性與 payload 斷言及 integration 指紋／backend 計數均通過。 |
| 舊 workspace detail／Task 遲到、腳本樣式內容、session 401 | UI generation 排除回填，使用文字節點且清除選取、detail、待重送 payload；probe 有具體斷言。 |
| 歷史 Task context／result 很大 | G5 已核准沿用既有 status IPC，新增 memory page/detail 的 512 KiB 不適用；文件與程式一致。 |

需求與架構：Web 不直連 SQLite；CLI 原全量 `memory_list/get` 契約保留，新增 Web 專用有界路徑；README／架構文件描述相符。安全與權限：未發現 client 密封 context 進入執行路徑或跨 workspace 讀寫。證據真偽：新測試使用真實 SQLite／IPC／HTTP，UI probe 是已揭露的最小 DOM 模擬；紅燈對應缺少的行為，綠燈有獨立輸出／固定字面值斷言。

本次第一次執行完整 `go test -count=1 -race ./...` 時，與本 PR 無直接修改關係的 `TestCLI_LatestReview_H01_PathEscapedTaskIDHandshake` 曾因 side-effect marker 斷言失敗，命令 exit 1。該測試單獨重跑 exit 0，第二次相同完整 race 命令所有套件（含 integration）exit 0；本 PR 沒有修改 `tests/integration/cli_test.go` 或該 CLI 握手路徑。此間歇失敗如實保留，未將第一次結果寫成全綠，也無證據將它歸因於 Issue #21 變更。

## 豁免、待確認與限制

- 無未核准 Scenario、未解決 G-TBD 或 gate 豁免。
- UI 探測不是實際瀏覽器；真實 HTTP→IPC→SQLite 行為有 integration 測試，瀏覽器同源限制依平台與 cookie 設定判定，PR 已揭露此限制。
- G5 已核准的既有 Task status 完整 context／result 大小限制仍在，未宣稱被新 memory response 上限修正。

## 流程判定

**PASS**。此結論只綁定上述 Reviewed BASE／HEAD／patch-id；本機報告須按 `review-evidence.md` 的單一 artifact 提交例外檢查後，才可沿用至新增報告提交的 HEAD。其後若有產品、測試或其他文件變更，需重新審查。
