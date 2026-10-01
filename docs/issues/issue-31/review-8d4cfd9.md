# Issue 31 獨立審查報告

- 審查日期：2026-10-01。
- 範圍：完整 PR #32；41 個異動檔案，含 production、測試、規格、常青文件與證據。
- Reviewed BASE SHA：`1b21d587a738b871e1dde48abceb17a8ce8c1bb2`。
- Reviewed HEAD SHA：`8d4cfd9cada0d55a740c59caec0f601fc6616dec`。
- Reviewed patch-id：`0e1b99c7c5e3d2b4310d7e978372711766fc325a`（git diff BASE HEAD | git patch-id --stable）。
- 獨立 reviewer：宿主隔離 reviewer agent `/root/review_issue31`；不是原實作者，未修改 production code。
- Review artifact：`docs/issues/issue-31/review-8d4cfd9.md`。
- 風險：High；主要失敗面為跨 scope existence hiding、交易半成功、歷史 context 被重算及刪除後復活。
- PR：https://github.com/raybird/Asura/pull/32 。協調者確認遠端 source HEAD 等於上述 HEAD，BASE 不變。

## 問題與風險

MUST FIX：`tests/integration/recall_test.go:492` 的 TestRECALL_QueryDeadline 慢速 memories view 在固定 HEAD 缺 lifecycle/is_durable/lifecycle_changed_at 欄位。RecallCandidates 新 SELECT 因 missing-column 立即失敗，原 assertion 只驗一般 unavailable 與時間上限，仍為綠燈，實際未保護 deadline cancellation。修正 fixture schema 並加上實際 deadline reached assertion，重跑 race 測試及更新證據。此缺口由協調者於 2026-10-01 自檢回報，reviewer 已用 git show 固定 HEAD 確認。主動揭露本身不構成缺失；必須修正的是原 deadline 測試判準未觸及失敗面。

- NICE TO HAVE（效能／一致性改善）：`internal/storage/sqlite_memory.go:59` 的 ListMemories 為每列額外載入 lifecycle，形成 N+1 查詢。一般 CLI list 無本期新增的容量 SLA，bounded recall/detail 已走受限路徑，本次不阻塞；後續若優化可把 lifecycle 與直接 successor 合併進原 SELECT，減少大量列表查詢及兩次讀取間的狀態競爭。
- NICE TO HAVE（證據持久力）：`internal/storage/sqlite_lifecycle_test.go:234` 已測 detail content 超限，其他 field sentinel、JSON escaping 超總量與 durable 冪等時間目前主要由明確 production guard／既有 bounded 測試保護。可追加這些 lifecycle 專用邊界 assertions，及兩個 store 並行初始化測試。這是提高回歸保護，非假綠燈；本次 SQL 投影、UTF-8／byte 驗證、marshal 總量檢查及 BEGIN IMMEDIATE 已逐行查核。

## 核准、規格與交付範圍

讀取流程契約 2.0、project、acceptance、verification、review-evidence，及四份 issue 文件。核准來源為使用者 2026-10-01 對 MLIFE-001～012 與 L-TBD 提案「審查通過」。比對核准基準 `56871b8de89bb8dc774185780fd48e4667b10d88` 與 HEAD README，差異僅核准 SHA 回填、流程敘述、空白及完成狀態；沒有條件、動作、結果或必要驗證語意變更。核准表與 12 個 Scenario 相等，Large 權威計畫各 Task 均有責任映射與證據。

完整範圍已查核，包括 lockfile／依賴未變更、migration、Git helper、daemon IPC、sealed snapshot、recall ranking、CLI、全部新增測試及文件。Web 保持既有 UI，未增加未核准 retention／GC 或解除 durable 功能。

## 驗收與證據

證據入口：[evidence/verification.md](evidence/verification.md)，所有引用 log 已讀。2026-10-01 執行 `sha256sum -c docs/issues/issue-31/evidence/tested-source.sha256` exit 0，190 個 Go 檔案均吻合。

| 驗收 | 查核的行為與證據 | 結論 |
|---|---|---|
| MLIFE-001 | LegacyMetadata、LegacyMigrationAndBounds、LegacySealedSnapshot；baseline／legacy snapshot red-green logs，created_at 回填而不改 ID/content | 通過 |
| MLIFE-002 | AtomicChainRetry、RollbackAndConcurrentCAS、IPCAtomicRecall；真實 SQL trigger 回滾、8 並行只一成功、原 operation retry 同 ID | 通過 |
| MLIFE-003 | CandidateLimitAndUtility 的 300 個 superseded fixture；SQL active WHERE 在 LIMIT 前，daemon auto 候選排除舊版本 | 通過 |
| MLIFE-004 | SealedTaskAndExplicitAudit；explicit source、superseded/by 與原內容字面 assertion | 通過 |
| MLIFE-005 | AtomicChainRetry；唯一 scoped predecessor/successor、只新建 active successor，detail 雙向逐節查核 | 通過 |
| MLIFE-006 | successor delete/retry、重開、predecessor delete 與 tombstone；delete 不更新舊 state | 通過 |
| MLIFE-007 | DurableDoesNotBoost；score=1、weight=1000000、a/b 固定 tie-breaker，ranking production 未修改 | 通過 |
| MLIFE-008 | durable predecessor 明確取代，保留 durable metadata，successor 獨立建立 | 通過 |
| MLIFE-009 | storage foreign/absent 同 ErrMemoryNotFound、daemon detail foreign/absent 相同錯誤；authorization 在查詢前、SQL 全部 scoped | 通過 |
| MLIFE-010 | sealed context 全字串比較；delete／daemon 重啟／status／同 Task redelivery 無額外 runner 調用 | 通過 |
| MLIFE-011 | predecessor Observed=1 保留，successor Observed=0、weight=1000000；utility ID 及 ledger 沒有繼承 | 通過 |
| MLIFE-012 | regression.log 19 個套件 race 綠燈，但既有 QueryDeadline fixture 因新 schema 即時失敗；必須恢復實際 cancellation 驗證 | 待修正 deadline fixture |

L-TBD-1～10 另核對：BEGIN IMMEDIATE migration 交易與 marker；scoped fingerprint ledger；無 relink／cycle API；hard delete 不 cascade relations；durable false→true；legacy snapshot 只解讀；Git approval 共用 tx、partial 拒絕、candidate/provenance 同交易及 lifecycle retry conflict；active 索引；detail SQL sentinel、content 64 KiB、總 JSON 128 KiB、2 秒 context；10k／100k probe 有正確答案、EXPLAIN、五次耗時及 RSS。除 deadline 回歸證據缺口外，實作符合提案。

## 相關失敗面與覆蓋判定

- 權限：已授權 A 使用 B 的 predecessor、foreign detail 與不存在 ID，預期相同 not-found 且無 mutation。storage 的 workspace 條件與 daemon canonical authorization 配合現有回歸；沒有全域 relation lookup。
- 原子／併發／重試：predecessor UPDATE 與 Git provenance INSERT 注入真實 SQLite ABORT，交易 defer rollback；relations/operations／successor 不留半套。唯一鍵與 CAS 阻止 fork；payload fingerprint 排除 server ID/time，允許同請求重試，拒絕不同 content/options。
- 刪除／chain：刪 successor 不改 predecessor，不重建同 operation；relations/operations 保留 ID 而不含內容。detail 可取 deleted 節點直接 pred/succ，不遞迴全鏈。已存在 bounded Git retry 對 deleted memory 回原 approved ID 的相容語意由 WEBGIT 測試明確固定，沒有 INSERT 或 revival；與 manual deleted retry 回錯誤不同，但不是本次新增分歧。
- Sealed／UFL：task 儲存原 JSON，redelivery/status 不重新解析現值。新 lifecycle metadata 是值拷貝；舊 snapshot Unmarshal 預設 active/non-durable，未改 persisted JSON。UFL 歷史由 workspace/memory ID 隔離。
- Git：Partial 在 bounded/unbounded 都拒絕；approved lifecycle payload 先驗 fingerprint；pending successor、relation、operation、candidate approval、provenance 同 tx，failure rollback。沒有繼承 predecessor telemetry。
- Bounds／資源：detail/provenance/relations 使用上限加一 SQL byte sentinel、UTF-8 檢核，拒絕超限而非把裁剪值當正式值；marshal 檢核包括 escaping，IPC wrapper 保留額外 JSON 空間。性能 fixture 為真實 SQLite 10k/100k；所有 query 有獨立正確答案，未只驗速度。
- 品質／架構：manual/Git 共用 transaction helper；不同 IPC 型別 wrapper 有必要，未找到造成正確性分歧的重複邏輯或需阻塞的過度設計。新增 metadata 與 durable 不改 ranking；沒有模型或新依賴調用。

## 測試真偽、限制與 reviewer 驗證

紅燈 logs 的失敗直接為缺 lifecycle JSON／retry 重複建立／legacy snapshot 缺 active／Git lifecycle payload 被忽略，非編譯或依賴錯誤。Git 舊行為探測方式已揭露，紅燈具有目標相關性。SQLite trigger、字面 ID/state/score、原 sealed string 等 assertions 來源獨立，沒有 mock 核心交易、跳過既有測試或同義反覆。但 QueryDeadline 既有 fixture 不相容新欄位，該測試綠燈不代表 cancellation 正確，需修正。code-simplify no-op 理由與最小實作後／最終綠燈重用有記錄。

2026-10-01 reviewer 自行執行：

```text
go test ./internal/storage ./internal/daemon ./internal/engine ./internal/recall ./cmd/asura -run TestLifecycle -count=1
exit 0
ok asura/internal/storage 2.311s
ok asura/internal/daemon 1.088s
ok asura/internal/engine 0.003s
ok asura/internal/recall 0.003s
ok asura/cmd/asura 1.084s
```

這次含 migration rollback、10k/100k probe 與 CLI；未重跑整個 race 套件，沿用已持久化 log 及協調者確認實際 exit 0。vet.log 為正常無輸出的成功執行，build/diff check 結果在 verification 記錄，未把空 log 自身當 success 的唯一理由。

完整 race 使用稍早相同 production 邏輯；之後追加 migration rollback test 與 CLI help 字串，差異已明確揭露，migration 新測試另跑 race、CLI 最終來源重跑。本次再跑全部 TestLifecycle，沒有未验证的行為變更。性能 RSS 包含 go tool/build 與子程序，為本機 fixture 證據，不能宣稱 production daemon RSS／效能保證。ledger 無 GC 的持續成長限制已在常青文件揭露。沒有 gate 豁免或未解決必要需求決策。

## 流程判定

**RETURN TO execute-task**。完整固定範圍審查已完成，報告已持久化；需修正 QueryDeadline fixture 與實際 cancellation 證據，提交新的固定 HEAD 後重新獨立審查。其餘 lifecycle production 與驗收查核未發現 MUST FIX；此報告不代表已合併或已結案。
