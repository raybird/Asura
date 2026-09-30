# 技術分析與設計提案

分析日期：2026-09-30。2026-09-30 使用者核准以下方案；限制與真實 fixture 驗證結果見 [驗證證據](evidence/verification.md)。

## 可行性與方案取捨

既有 store 與原子交易可重用，不建第二套 scanner/store/checkpoint。直接重用 CLI list/detail 雖改動少，但沒有 cursor、欄位限制，capture 還會複送完整 facts；選擇新增 Web bounded IPC，維持 CLI 完整 detail 契約。

```diff
 Web → Daemon
- CLI candidate list/detail，完整 capture facts 回傳
+ bounded page/detail DTO，capture 回傳 bounded 結果與 candidate ID
```

## J-TBD-1：Route、DTO、permission matrix

| HTTP | Route | 輸入／回應 |
|---|---|---|
| POST | /api/git-memory/capture | workspace、range 或 since_last（二擇一）；candidate_id、outcome、reused、message |
| GET | /api/git-memory/candidates | workspace、status、limit、cursor；items、next_cursor |
| GET | /api/git-memory/candidates/{id} | workspace；完整 bounded draft、facts 與 issue 指定 provenance 欄位 |
| POST | /api/git-memory/candidates/{id}/approve | workspace、可選 content；approved_memory_id |
| POST | /api/git-memory/candidates/{id}/discard | workspace；持久化 discarded 結果 |

全部先驗 Host/session；POST 驗 Origin，新候選 GET 若帶入 Origin 也須同源（WEBGIT-001），未帶入 Origin 的合法 GET 保持可用。Daemon 每次重驗 canonical workspace；所有 body/query strict allowlist、拒絕重複 query、不合法 UTF-8、未知欄位（包括 force/ai_draft）與 trailing JSON。Browser 不得指定 Git binary/argv/backend。Approve 不接受 provenance 欄位。

HTTP：400 無效輸入/range/cursor、401 session、403 Host/Origin/撤權、404 同 scope 不存在或跨 scope ID、409 不可操作狀態/Partial、413 超限、422 不支援既有資料格式/非 Git capture、503 Daemon unavailable、504 timeout。Daemon 使用穩定錯誤分類，不把 raw 路徑或 Git stderr 暴露為跨 scope 資訊。Capture rejection 回傳明確錯誤分類；successful Complete/Partial 與 reused 分開表示。方法不符沿用既有 Web route 行為。

## J-TBD-2：有界分頁

預設 pending，可篩選 pending/approved/discarded，預設 20、最大 50。維持 created_at DESC/id ASC，以 created_at+id keyset；SQL LIMIT limit+1，不全量讀取後 slice。Cursor 綁 canonical workspace/status、長度最多 1024 bytes；錯誤 cursor 明確 400。固定資料集遍歷必須無遺漏/重複；並行新增時不承諾跨請求 snapshot，重新整理由第一頁開始。既有狀態更新可能使項目離開篩選集合，UI 明確重新整理。

## J-TBD-3：Byte contract

- request body 64 KiB；workspace/range 4096 bytes、ID 256 bytes、cursor 1024 bytes。
- list summary 最多 512 UTF-8 bytes（含省略標記）；其他文字 metadata 每欄最多 4096 bytes。
- detail draft 最多 32 KiB、facts/provenance payload 最多 256 KiB；必須完整回傳或 413，不以截斷 draft 冒充完整內容。
- HTTP JSON 最多 512 KiB、IPC JSON envelope 最多 1 MiB，包含 JSON escaping；編碼前與 reader/decode 層皆設 hard bound。
- SQL 先查 byte lengths，對文字投影作有界讀取，禁止先載入巨大欄位後才驗證。Web detail 超限時提示可改用 CLI，不改寫既有 candidate。
- Capture 只回結果與 ID，不回 raw diff/完整 candidate；已持久化但 detail 超限仍如實表示 captured，另標示 detail 無法在 Web 開啟。

## J-TBD-4：取消

沿用 HTTP 10 秒 exchange deadline、Daemon capture 10 秒硬期限及 connCtx/process group kill，不延長現有 capture limit。HTTP disconnect/timeout 關閉該 request 的 Unix socket，取消傳至 service/SQL/Git。需測試真實 subprocess 回收、checkpoint 不前進，含成功對照及半關閉 regression；完整交易已提交後取消不倒退，UI 回應遺失時重新查詢 candidate。既有 deadline 同時到期的失敗可如實回報，不保證完成至 deadline 的最後瞬間。

## J-TBD-5～7：UI、審核及競態

固定 range 與 since-last radio，切換時清空無效欄位；Complete/Partial、reused 與 Rejected 顯示不同文字。Detail 分為來源事實、草稿、狀態；全部純文字。Pending Complete 可編修與確認 approve；Partial 不提供 approve 且服務端再次拒絕。Discard 明確確認，approved/discarded 為唯讀。

content 省略沿用 draft；明確提供時須非空、合法 UTF-8、最多 32 KiB；不能把空白修訂悄悄變成原草稿。Approve 於交易前驗證有效內容大小，重送 approved 回原 memory ID，不建立第二筆；discard 重送維持 discarded。正式 memory 已被刪除仍保留原批准 ID，不復活 memory。草稿編修僅保留在目前畫面，approve 才送 content，不新增獨立 draft-save 功能。

工作區切換、登出/session 失效時 generation 遞增並清空 candidate list/detail/edit/capture form/pending action，abort 未完成請求。每個 response 與 action 都檢查 session/workspace generation、canonical scope、candidate ID；利用可控制延遲的 browser 測試驗證舊回應不污染目前 DOM。

## J-TBD-8：Web bounded IPC

新增 git_memory_page、git_memory_detail_bounded、git_memory_capture_bounded、git_memory_approve_bounded、git_memory_discard_bounded；共用既有 capture service 與 store transition。Web handler 不呼叫 Git/SQLite。Bounded approve 必須在原子交易內查核 scope/status/Complete 與有效 content；不得以先查後寫代替交易驗證。需要 store bounded query 與小範圍交易參數擴充；不新增 schema。

## 待驗證

全部 J-TBD 已於 2026-09-30 核准；詳細 API field naming 可等價調整，但 byte/error/permission 語意改變需重新核准。大量候選 cursor、超大 facts/draft 及 HTTP capture disconnect 的真實 probe 已於 2026-09-30 完成，證據見 verification；CLI 既有契約另以回歸測試確認。
