# Issue 19 獨立審查報告

- 審查日期：2026-09-22。
- 範圍：PR #20 完整差異；重點覆核 bounded explicit ID identity、明確記憶優先、接受時密封與重送冪等性。
- Reviewed BASE SHA：`8576dd0323750a1aa8f2c550c0f69e8edd30e55d`
- Reviewed HEAD SHA：`5a241152c8c3a18cbf77aebbe761e9c4e3b74bc9`
- Reviewed patch-id：`1ca3348237d6e2a6d6c72eec2380dd8ba8031470`
- 獨立 reviewer：Codex subagent `/root/review_issue19_fix`，未參與產品實作。
- Review artifact：`docs/issues/issue-19/review-5a24115.md`

## 問題與風險

MUST FIX：無。SHOULD FIX：無。NICE TO HAVE：無。

前次審查指出的 identity 不一致已修復。`internal/daemon/daemon.go:579` 僅在 bounded 模式於 fingerprint 前將 explicit IDs 逐筆 `TrimSpace`，再沿用既有集合去重與排序；同一份 `req.MemoryIDs` 隨後傳入 bounded fingerprint、explicit resolve、selector、`Recall.ExplicitIDs` 與 `MemoryRefs`。因此帶空白與未帶空白的同一 ID 不會再分別成為 explicit 與 auto，也不會重複 snapshot。off 模式仍使用原字串集合，舊 fingerprint 空白語意未改變。

## 版本與證據有效性

本次審查固定完整 `BASE..HEAD` 範圍，共 26 個檔案、2,243 行新增與 34 行刪除。已讀完整異動清單、核准規格、實作證據及先前完整獨立報告，並另讀 `9bd04e4..5a24115` 的全部增量。`6139463` 只新增歷史 review artifact；最新產品修復修改 Daemon canonicalization、整合測試及對應 issue 證據，未改變 RECALL-001～010 或 D1～D5 的核准行為。

修復具有目標行為的紅綠證據：修復前 `TestRECALL_BoundedCanonicalExplicitIDs` 因同一記憶產生兩份 snapshot 而失敗；修復後通過。實作者另保存全套 `go test -count=1 -race ./...`、`go vet ./...` 與 build 成功輸出於 `docs/issues/issue-19/evidence/implementation.log`。

2026-09-22 reviewer 獨立執行：

```sh
go test -count=1 -race ./internal/daemon ./internal/recall ./internal/storage ./tests/integration -run 'TestRECALL_BoundedCanonicalExplicitIDs|TestRECALL_OffScopeExplicitAndNoRequery|TestRECALL_BoundedAndExplicit|TestSelect|TestRecall'
```

四個套件均通過，exit code 0。另執行完整範圍 `git diff --check`；只有 issue README 既有 Markdown hard-break 尾端空白，與產品行為及本次修復無關。

## 已查核維度

### RECALL-005 — 明確記憶保留與預算優先序

- 輸入：`memory_ids = [" mem-ID ", "mem-ID"]`，同 workspace 候選亦含 `mem-ID`。
- 預期：bounded 將兩個表示法視為同一 explicit ID，explicit snapshot 保留一份，auto 排除該 ID，`ProjectMemories`、`Recall.ExplicitIDs` 與 `MemoryRefs` 身分一致。
- 覆蓋：`tests/integration/recall_test.go:238` 透過真實 Daemon IPC、SQLite 與密封 JSON 斷言上述結果；selector 仍以 canonical explicit 集合排除候選。目標 race 測試通過。
- 預算與優先序：修復只改 explicit ID 正規化時機，未改 selector 排名、ComposePrompt 增量、auto 4 筆／8 KiB／2,048 tokens 上限或首次 stage admission。完整 PR 原有 `TestRECALL_BoundedAndExplicit`、selector 與 budget 測試保持有效。
- 判定：通過。

### RECALL-006 — 接受時封存與重送

- 輸入：首次 bounded 請求以帶空白及未帶空白的重複 ID 提交；同 Request ID 再以 canonical ID 提交。
- 預期：兩次 payload 形成相同 bounded fingerprint，第二次直接回傳原 Task，不重選、不改 snapshot；off 模式帶空白與未帶空白仍視為不同舊 payload。
- 覆蓋：`tests/integration/recall_test.go:266` 驗證 bounded redelivery 的 Task ID 與 fingerprint 相同；同測試 `:271` 驗證 off 第二次提交仍回傳 payload conflict。既有 concurrent duplicate 分支使用相同 `fp` 比對，未另建 identity 路徑。
- 判定：通過。

### 相關失敗面

| 輸入／狀態 | 預期行為 | 覆蓋與結論 |
|---|---|---|
| bounded explicit ID 前後有空白 | 解析成 canonical ID，auto 不重複加入 | 新整合紅綠測試直接覆蓋；通過 |
| bounded 同時傳入帶空白與 canonical 重複 ID | 去重後只保留一份 provenance、snapshot 與 ref | 新整合測試直接覆蓋；通過 |
| bounded 重送改用等價 canonical 表示法 | fingerprint 相同，回傳原密封 Task | 新整合測試直接覆蓋；通過 |
| off 重送只改 ID 空白 | 維持舊 fingerprint identity，拒絕不同 payload | 新整合測試直接覆蓋；通過 |
| 全空白 bounded ID | canonical 後為空字串，explicit resolve 依既有契約拒絕，不保存 Task | `ResolveMemoriesForTask` 明確拒絕 empty memory ID；修復未繞過該檢查 |
| 跨 workspace 或未授權 ID | 授權／精確 workspace 解析仍拒絕，無 auto 洩漏 | canonicalization 發生在 workspace 授權後且不改 workspace；既有隔離回歸通過 |
| concurrent 相同 Request ID | 唯一 Task；競態失敗分支以同 canonical fingerprint 比對 | `CreateTask` duplicate 分支重用同一 `fp`；既有 concurrent 測試及全套 race 證據有效 |

### 需求、架構、安全與品質

- 需求：修復精確對應 RECALL-005 的「auto 排除 explicit IDs」及 RECALL-006 的正規化 payload 重送語意，沒有新增未核准行為。
- 架構：canonicalization 位於 Daemon 接受邊界，先於 fingerprint、解析、召回與密封，避免各層各自定義 identity；off 相容分支明確且範圍最小。
- 安全：不改 workspace canonicalization、授權順序或候選查詢 scope；未選候選內容仍不進入 Task／trace。
- 品質：新增測試使用外部可觀察的密封 Task 與 fingerprint 字面結果，斷言可與實作意見不合，不是同義反覆；修復 diff 集中，未見弱化或略過既有測試。

## 豁免、待確認與限制

無 gate 豁免；TBD-1～5 均已核准。既有已揭露限制維持：詞彙與候選前綴可能漏召回、250ms 非硬即時 SLA、原始 prompt／explicit 處理成本未由本期限制、benchmark 為固定單機合成資料，以及 restart 測試以 queued fixture 模擬程序中斷。

本報告審查的是 `5a24115`；若後續只新增本報告的單一直接後繼提交，可依 `review-evidence.md` 的 artifact 提交例外驗證。任何其他產品、測試、設定或規格變更均須重新審查。

## 流程判定

**PASS** — 前次 MUST FIX 已以共用 bounded canonical explicit ID 集合修復；RECALL-005／006 與相關安全、預算、密封及重送失敗面均有有效證據，完整固定範圍未見其他 MUST FIX，報告已持久化。
