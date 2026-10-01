# Issue 31 — 技術方案提案

分析日期：2026-10-01。以下提案經使用者於 2026-10-01「審查通過」核准。

## 可行性與取捨
SQLite transaction 可容納 memory、lifecycle relation、Git provenance 與 retry identity；Daemon 沿用 canonical scope authorization。ranking 與 UFL 演算法不需改動。

```diff
 memory creation
- INSERT memory
+ BEGIN
+ verify scoped predecessor is active
+ INSERT successor + retry identity + relation
+ CAS predecessor to superseded
+ COMMIT
```

比較只把 superseded_by 放在 memories 與獨立 relation ledger：前者較少 schema，但硬刪 predecessor 後遺失反向關聯；後者多一張表，能保留 ID/時間/reason 而不保留 deleted content。提案選 ledger，避免把 delete 改成 soft delete。

比較僅 CAS active predecessor 與持久化 operation ID：僅 CAS 可阻止第二個 successor，卻無法讓 commit 後失聯的原請求重試取得原結果；提案選持久化 operation ID，payload 不一致時回 conflict。

## L-TBD 提案
| ID | 建議決策 | 可檢查條件 |
|---|---|---|
| L-TBD-1 | memories 加 lifecycle（active 預設）、is_durable（false 預設）、lifecycle_changed_at；relation 提供 superseded_by。legacy changed_at 初值採原 created_at；migration 交易與 marker 防止半套升級 | legacy ID/content/provenance 不變；重開與並行初始化成功 |
| L-TBD-2 | scoped operation ledger 唯一鍵(workspace, operation_id)，保存 request fingerprint 與 successor ID；relations 對(workspace, predecessor_id)、(workspace, successor_id)各唯一。memory/relations/operation/Git approval 同交易 | 相同請求重試回原 ID；不同 payload conflict；不同 operation 競爭同 predecessor 只有一個成功 |
| L-TBD-3 | 僅新建 successor，predecessor 必須 active；無任意 relink，故不產生 cycle。detail 只取直接 predecessor/successor，不遞迴讀全鏈；使用者逐節 audit | A←B←C 可逐節查；拒絕 self、重複 successor 與非法 state |
| L-TBD-4 | 沿用 hard delete；relation 與 operation ledger 不 cascade，保留 ID/時間/reason、不保存 deleted content。detail 可由同 scope relation 顯示已刪節點的 tombstone；刪 successor 永不改 predecessor state | 刪 A 或 B 後關聯仍可追溯；deleted 不可 explicit recall；同 operation 不重建 deleted successor |
| L-TBD-5 | add 可選 --durable；durable 子命令只做顯式 false→true，已 durable 為冪等；不新增解除 durable 操作。授權沿用 exact workspace；supersede/delete 均允許 durable | metadata 更新不改內容、utility 或 ranking；未授權無 mutation |
| L-TBD-6 | 新 Task snapshot 與 explicit recall audit 保存 lifecycle/is_durable/superseded_by/changed_at，標示 source=explicit；legacy snapshot 缺欄位按 legacy active/non-durable 解讀，保持原 persisted JSON | supersede/delete 後原 resolved_context 原文不變；status/redelivery 不查現值覆寫 |
| L-TBD-7 | 擴充 ApproveOptions；transaction 內共用 memory creation helper。candidate identity 繼續作既有重試來源；核准後不同 supersedes/durable payload conflict。Partial 禁止 approve | provenance 指向原 candidate；失敗時 candidate 仍 pending，無 successor/relations |
| L-TBD-8 | recall 索引(workspace,lifecycle,id)，SQL active 篩選先於 LIMIT；utility 仍以 workspace/memory_id 查，不複製 observations | superseded 257 筆不擠掉 active；successor weight=1000000；durable 正常 UFL |
| L-TBD-9 | CLI memory add --supersedes ID --operation-id ID [--durable] [--reason TEXT]；supersedes 必須帶 operation ID。memory durable --workspace PATH ID；memory detail --workspace PATH ID。IPC memory_add 擴充及 memory_durable/memory_detail | 新 ID≤256 bytes、operation ID≤128 bytes、reason≤1024 bytes；detail content≤65536 bytes、總 JSON≤131072 bytes、deadline 2 秒；超限明確拒絕、不無聲裁剪 |
| L-TBD-10 | 真實 SQLite 10k/100k relation fixture，EXPLAIN 與多次 probe 記錄 migration/query elapsed、配置與 RSS；建議 detail/recall 各次在 2 秒內、migration 100k 在 30 秒內 | 任一超門檻失敗；scope/source/active 答案也必須正確，不能只驗速度 |

## 架構影響與必要確認
只由 Daemon 執行 state transitions，storage 交易負責原子性。Web 原操作走相容預設，不增加 lifecycle UI。Git service 預留 options，不要求本期 CLI Git approve 暴露 supersedes。

2026-10-01 已核准的可觀察行為：operation ID 必填與衝突、deleted relation tombstone、durable 不提供解除、CLI/IPC 命名與 response/performance 上限。所有 bounds 為本提案，不宣稱來自既有契約。reason 為選填，預設記錄操作類型；Git provenance 保持原欄位。

2026-10-01 依使用者 CI 審查允許，性能 probe 與標準 correctness race 分離：CI 以 lifecycleperf tag 獨立非 race 執行；原 30 秒 migration／2 秒 query 與正確性斷言保留，沒有門檻豁免。

大資料門檻須在指定執行環境驗收；若基準不適合門檻，先取得決策，不降低測試 assertion 製造綠燈。
