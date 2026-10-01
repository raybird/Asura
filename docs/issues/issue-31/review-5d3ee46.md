# Issue 31 獨立審查報告

- 審查日期：2026-10-01
- 範圍：完整 PR #32，固定 BASE 到 HEAD 的全部 46 個異動檔案
- Reviewed BASE SHA：`1b21d587a738b871e1dde48abceb17a8ce8c1bb2`
- Reviewed HEAD SHA：`5d3ee46aafeede0767f1d066126a56cd1a8b3d42`
- Reviewed patch-id：`d251f57e803bc15543a50d35ca3b9a39af098677`
- 獨立 reviewer：宿主隔離 agent `/root/review_issue31`，不是原實作者，未修改 production code
- Review artifact：`docs/issues/issue-31/review-5d3ee46.md`
- 風險：High，跨 scope 授權、持久化原子性及 sealed context 不變性
- PR：https://github.com/raybird/Asura/pull/32

## 問題與風險

MUST FIX：無。

非阻塞 NICE TO HAVE 沿用 [review-23b3248.md](review-23b3248.md)：`internal/storage/sqlite_memory.go:59` 列表 N+1 lifecycle 查詢可於後續改善；`internal/storage/sqlite_lifecycle_test.go:234` 可追加 metadata sentinel、JSON escaping 超總量、durable 時間冪等及並行初始化回歸測試。這些是效能／證據持久力建議，現有 guard 和真實行為證據有效，不屬假綠燈。

## 固定範圍與版本查核

2026-10-01 執行 `git diff --name-status 23b32480ee6fe6814a7db318503c1e9168e16255 5d3ee46aafeede0767f1d066126a56cd1a8b3d42`，唯一差異為新增 `docs/issues/issue-31/review-23b3248.md`（一般檔案）；已讀取該報告全文並確認它是上次 reviewer 產物。production、測試、設定、核准規格與 evidence 全部相同。

前次報告 Reviewed HEAD SHA 行末句號不符合 verify-artifact parser，協調者揭露 INVALID 並要求重新審查，本次沒有把 invalid artifact 提交例外當作有效放行。重新固定新 HEAD、計算完整 BASE 到 HEAD patch-id，合併前次完整查核與本次新增報告查核涵蓋全部範圍；沒有改工具或改寫既有報告／遠端歷史。

核准來源仍為使用者 2026-10-01 對 MLIFE 與 L-TBD 提案「審查通過」，核准基準 `56871b8de89bb8dc774185780fd48e4667b10d88`。流程契約 2.0 與 project/acceptance/verification/review-evidence 已查核，規格無語意變更、全部 12 Scenario 已核准、權威計畫 Task 已映射且具證據。

## 驗收與證據

詳細查核、失敗面及實際測試輸出見固定 HEAD 可讀的 [review-23b3248.md](review-23b3248.md) 與 [evidence/verification.md](evidence/verification.md)。下列集合與核准 Scenario 完全相等；此為重新判定完整範圍，不只審查新增文件。

| Scenario | 證據與可觀察判定 | 結論 |
|---|---|---|
| MLIFE-001 | LegacyMetadata／LegacyMigrationAndBounds／LegacySealedSnapshot，舊資料 active/non-durable，sealed 原 JSON 不重寫 | 通過 |
| MLIFE-002 | AtomicChainRetry／RollbackAndConcurrentCAS／IPCAtomicRecall，真實 SQL rollback、並行唯一 successor、重試同 ID | 通過 |
| MLIFE-003 | CandidateLimitAndUtility，300 superseded 不佔 SQL LIMIT 候選 | 通過 |
| MLIFE-004 | SealedTaskAndExplicitAudit，explicit audit 保存 superseded／successor 與舊內容 | 通過 |
| MLIFE-005 | AtomicChainRetry，A/B/C 單鏈、逐節雙向 audit，僅 C active | 通過 |
| MLIFE-006 | AtomicChainRetry，delete／重開／deleted retry 與 tombstone，不復活 | 通過 |
| MLIFE-007 | DurableDoesNotBoost，literal score=1、weight=1000000，a/b deterministic tie-break | 通過 |
| MLIFE-008 | durable predecessor 可取代並保留 historical durable metadata | 通過 |
| MLIFE-009 | storage foreign/absent 相同 not-found、IPC detail existence hiding、canonical authorization 先於查詢 | 通過 |
| MLIFE-010 | SealedTaskAndExplicitAudit，status／restart／redelivery 原 context 字串相同、不再調用 runner | 通過 |
| MLIFE-011 | predecessor observation 保留、successor 0／neutral；Git provenance 與 utility identity 隔離 | 通過 |
| MLIFE-012 | vet、完整 race、build、10k/100k probes；QueryDeadline 已用專用真實紅綠及 reviewer race 取代失效證據 | 通過 |

## 重要失敗面、架構與品質

- 權限：canonical workspace 先授權，memory/relation/operation/provenance SQL scoped；foreign 與 missing 相同錯誤，無跨 workspace 存在性洩漏。
- 狀態／交易：active predecessor 限制、scoped unique relation 與 CAS 防 fork/cycle；SQL trigger 在 UPDATE／provenance INSERT 中斷，memory、relations、operations、candidate 全回滾。payload conflict 拒絕、同 payload 冪等。
- 刪除／歷史：hard delete 不 cascade ledger；deleted successor 不重建、predecessor 不恢復。sealed snapshot 為接受時值拷貝，historical redelivery 不查現值替換；UFL 不複製 observations。
- Git：partial 拒絕，lifecycle retry fingerprint 保護，candidate/provenance 共用交易。bounded Git deleted retry 回原 approved ID 是既有 WEBGIT 相容行為且無 revival，非新增分歧。
- Bounds／migration／效能：byte sentinel SQL 投影及 UTF-8 validation 拒絕超限，detail content 64 KiB、JSON 128 KiB、2 秒 context；BEGIN IMMEDIATE migration 與 marker 同交易可回滾重試。10k/100k fixture 五次正確答案、索引 EXPLAIN、耗時與 RSS 已讀。
- 架構／簡化：manual/Git 共用 transaction helper；IPC 型別 wrapper 有不同公開回傳責任；未找到造成正確性風險的跨 Task 重複或過度設計。ranking／GC／Web UI 未增加未核准行為，依賴無變更。
- 證據真實性：SQL、真實 IPC、literal expected ID/state/score、原 sealed string 均非 mock 核心或同義反覆。前次 QueryDeadline 缺欄位造成假綠燈的缺口已修正，新 guard 的紅燈能抓 missing-column／context=nil，最終 race 驗真實 deadline 且單連線恢復可用。原 RETURN 歷史如實保留。

## 證據重用與限制

2026-10-01 上次 reviewer 在 production 相同版本執行全部 TestLifecycle exit 0；在 `23b3248` 執行 QueryDeadline 針對性 race exit 0（1.441 秒）、190 個來源 checksum 全 OK、diff check exit 0。本次新 HEAD 與該版本唯一差異為新增報告，來源與測試環境條件相同，因此不重跑程式測試；完整 race logs 按前報告明列的版本差異與專用補測沿用，沒有宣稱全套 race 在本次新 SHA 重跑。

性能 RSS 含 go tool/build 與子程序，僅本機 fixture，非 production daemon SLA。ledger 持續成長／無 GC 已揭露。沒有 gate 豁免或未解決必要決策；主動揭露測試限制不作為缺失。

## 流程判定

**PASS**。固定完整範圍已由獨立 reviewer 查核並持久化，無 MUST FIX，驗收、重要失敗面與真實證據均齊備。協調者只新增本報告提交後，先以 skill verify-artifact 核對直接父子 SHA、唯一新增一般檔案與 metadata 格式，再同步 PR 並等待合併。此報告不代表已合併或已結案。
