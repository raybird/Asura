# 技術提案與取捨

2026-09-23。WEBMEM-001～010 的外部行為已由使用者核准；以下 G1–G5 的具體協定與上限仍是待決提案。基線見 [需求與現況](requirement-analysis.md)及[隔離資料探測](evidence/probe-results.md)。

## 技術可行性與安全能力矩陣

既有 HTTP → Unix IPC → Daemon → SQLite 鏈可沿用；Web 不直接查 SQLite。Web session／Host 是全部 API 的 HTTP 入口條件，現有 Origin 比對只用於非 GET／HEAD；Daemon canonical 授權是每次 workspace 操作的最後判定。UI 選單與 client 提交的 ID 皆不構成授權證據。

| 操作 | HTTP 入口條件 | Daemon 授權／scope | 資料或副作用 |
|---|---|---|---|
| list/detail | session、Host；GET 無服務端 Origin 比對 | 每次 canonical workspace；workspace 精確相等 | list 只傳有界摘要；detail 以 Web 專用有界 IPC 按 ID 取得 |
| add/delete | session、Host、Origin；變更方法 | 每次 canonical workspace；workspace＋ID 精確相等 | add 固定 manual；delete 回報真實結果 |
| submit explicit/bounded | session、Host、Origin；POST | Daemon submit 再授權及解析 IDs | 接受前密封；無效 ID 0 backend calls |
| task audit | session、Host；GET 無服務端 Origin 比對 | 既有 `authorizeTask` | 只讀所屬 Task 已保存資料 |

跨 Origin 的 POST／DELETE 由既有入口拒絕；GET 即使帶不同 Origin，持有有效 session 與正確 Host 時也不因 Origin 被服務端拒絕。瀏覽器跨站頁面受 `SameSite=Strict` cookie 與同源讀取限制，不能取得 GET 回應內容；不把瀏覽器限制寫成服務端 Origin 拒絕。WEBMEM-001 須分別驗證這兩種可觀察結果。HTTP 與 IPC 的拒絕訊息應固定，避免輸出 daemon 原始錯誤或其他 workspace ID；同 scope 的缺失與跨 scope ID 應有同一外部結果。新路由仍透過 `ServeHTTP` 共用入口檢查。

## G1 — HTTP、IPC 與錯誤映射

建議在 `/api/memories` 下以 workspace query 或 body 明確指定目標：`GET /api/memories?workspace=...&limit=...&cursor=...`、`GET /api/memories/{id}?workspace=...`、`POST /api/memories`（JSON 僅 `workspace/content`）、`DELETE /api/memories/{id}?workspace=...`。ID 在路徑中須編碼為單一 segment，解碼後含 `/` 或超過核准 ID 上限即拒絕。Web 以獨立 DTO 解碼，只接受該操作欄位；Task submit 亦改為只接受 `id/workspace/prompt/memory_ids/memory_recall` 的 DTO，拒絕 `resolved_context/backend/mode` 等 client 控制欄位。若既有 client 曾送這些欄位且仰賴被忽略語意，需由相容性探測確認。

新增 `memory_page` 及 Web 專用的 `memory_detail_bounded` IPC 動作與有界回應型別；add/remove 沿用既有 IPC 動作與 Daemon 授權。既有 CLI `memory_get` 保持完整讀取語意，不用於 Web detail。任何 memory 項目不從 Web cache 認定權限。建議固定外部錯誤：無效 DTO／cursor 400、未登入 401、未授權 workspace 403、跨 scope／不存在 ID 404、request ID 內容衝突 409、超過 Web 內容／回應上限 413、既有資料的 metadata 或 UTF-8 格式不支援 422、Daemon 不可用或容量滿 503，其餘內部錯誤 500；回應只含固定短訊息，不回傳原始 Daemon 錯誤。成功的 add 為 201、detail/list 為 200、delete 為 204；刪除失敗不得回 204。此錯誤矩陣與 route/DTO 仍待 G-TBD-1 核准。

## G2 — 有界分頁與內容政策

現有清單按 `(created_at,id)` 排序，但 SQLite 尚無相應複合索引。建議新增 `(workspace,created_at,id)` 索引，以 keyset cursor `(created_at,id)` 查下一頁，`LIMIT page_size+1` 判斷 has_more；同一排序保留既有 CLI 語意。相較用 `OFFSET`，keyset 不必掃過所有前頁；相較只按 `id`，維持使用者目前看到的建立順序。建議 cursor 為 base64url 編碼的版本化 JSON，含 SQLite 原始 `created_at` 文字、完整 ID 與 canonical workspace 的 SHA-256；伺服端拒絕格式錯誤、超長或跨 workspace cursor。穩定資料集須無重複／遺漏；插入或刪除穿插時採 keyset 即時視圖，不保證跨頁快照，新插入於 cursor 以前的資料需重新從首頁瀏覽。

```diff
 ListMemories(workspace): SELECT id,workspace,content,source,created_at ... 全量
+PageMemories(workspace,cursor,limit): SELECT 有界摘要欄位 ...
+  WHERE workspace=? AND (created_at,id)>cursor
+  ORDER BY created_at,id LIMIT limit+1
```

清單 SQL 必須在列數與每欄 bytes 層實際限制，不能讀完整 `content` 再在 Go 切片。可沿用 Phase 3F 的 `substr(CAST(... AS BLOB),...,max+1)` sentinel 思路，摘要只保留完整 UTF-8 rune 並附 `truncated`。Web detail 亦須在 SQLite 查詢中依核准的 `maxDetailBytes+1` 取得 content sentinel，於 Daemon 內判定超限並回固定錯誤；超限內容不得先由 `GetMemory` 全量讀入或經 IPC 送至 Web。

建議核准下列首版固定值；已有 [50 筆／512 bytes 探測](evidence/probe-results.md)，其餘是控制單次 Web 操作大小的保守產品政策，並非效能基準推出的最佳值。

| 項目 | 建議值與失敗行為 |
|---|---|
| 清單頁 | 預設 20 筆，client 可要求 1～50 筆，hard limit 50；SQL 只取 `limit+1` 作 has_more |
| 摘要 | 每筆最多 512 UTF-8 bytes；SQL 取 513 bytes sentinel，Go 去除不完整 rune 並標示截斷 |
| metadata | ID 256 bytes、source 128 bytes、原始 `created_at` 64 bytes；每欄 SQL 取上限加一個 sentinel。既有超限或非 UTF-8 資料使該頁回 422 固定錯誤，不假裝清單完整；CLI 可處理該筆 |
| cursor | 編碼後最多 1024 bytes，版本與 workspace hash 必須相符；無效回 400 |
| detail | content 最多 65,536 UTF-8 bytes；SQL 最多取 65,537 bytes，超限、無效 UTF-8 或 metadata 超限回 413／422 固定錯誤，不經 IPC 傳全文 |
| 新增 | Web content 最多 32,768 UTF-8 bytes，且整個 JSON body 仍受既有 64 KiB 限制；任一超限回 413 |
| 傳輸 | 新 memory page/detail IPC 與 HTTP JSON 回應各自最多 512 KiB；Daemon 編碼前檢查，超限回固定 413，不回部分資料 |

此政策允許既有 CLI 完整列出及取得超大記憶；Web 只在遇到超限資料時明確報錯。新 Web add 產生的 ID/source 應低於 metadata 限制。需測剛好等於與超過上限、JSON 逸出膨脹、非 UTF-8 與已存在長資料；以上數值及超限語意仍待 G-TBD-2 核准。

## G3 — Web explicit／bounded 控制

表單從同 workspace 的有界清單多選 IDs，建議 Web 最多選 32 個、每個 ID 最多 256 bytes，超限回 400；頁面切換 workspace、登出或 session 失效即清除記憶清單、detail、選取與未送出的記憶 payload，並令進行中的 list／detail／Task 查詢結果失效。每個非同步回應都要核對發起時的 workspace 與 session 世代；過期回應不得重新填入畫面或下一次 submit payload。模式明列 `off|bounded`，預設 off；空選取＋off 省略兩個 memory 欄位，保持原請求身分。explicit 與 bounded 可同時使用，交由 Daemon 既有 explicit 優先規則處理。Web 不送 backend、mode、budget 或 sealed context。32 個 ID 上限及空選取形狀仍待 G-TBD-3 核准。

## G4 — Request ID 與回應遺失

建議按下提交時一次產生 ID、在網路請求前顯示可複製的 Request ID，並凍結完整 payload；網路／回應失敗時提供「原請求重試」，只送同 ID＋同 payload。表單內容若改動，必須明確轉成新提交並產生新 ID，不能在同 ID 上偷偷改欄位；已知 4xx 衝突不自動重試。登入、登出或 workspace 切換時清除未送與待重送的敏感 payload；若已送請求的結果未知，使用者保留先前顯示的 ID，重新登入後可在 Task 查詢欄找回結果。若使用者未保存 ID 或頁面關閉，首版不能保證找回未知結果，須在 UI 說明。是否保留跨重新整理的待重送 payload 有隱私與副作用取捨，首版建議只在當前頁面記憶體保留；G-TBD-4 待核准。

## G5 — 歷史稽核與隱私

從 Task 的 `resolved_context.recall`、`memory_refs` 與 `pipeline_trace` 呈現 compact 決策，區分 legacy/off、explicit-only、bounded 但 0 auto；缺失欄位顯示「未提供」，估算明標「估算」，context window 不明標「未知」。稽核區建議只顯示 IDs／分數／政策／略過計數及 stage admission，不主動展開保存的完整記憶正文；明確查看任務快照時仍依既有 Task 授權，且不重新查最新 memory。所有使用者文字透過 `textContent`。首版沿用既有 Task status IPC，故舊路徑可能傳送完整 `resolved_context` 與結果；新增 memory page/detail 的 512 KiB 上限不會修正該既有路徑。是否另建有界 audit DTO 或接受此已知限制，須由 G-TBD-5 決定。

## 架構影響與待釐清項目

- storage／Daemon 增設 paged 與有界 detail 路徑，不改 CLI 的 `memory_list/get` 全量契約。新索引是加法式 SQLite 變更，須驗證既有資料庫啟動成本與 query plan。
- Web 使用現有 10 秒獨立 IPC 連線；頁大小和 detail 限制須讓單次 IPC 與 HTTP 回應受控。現有 Task status 本身會傳完整 `resolved_context` 與 `result`，G-TBD-5 要確認新增展示是否需要獨立有界 audit DTO；不能將目前的 result pagination 誤當整個 Task 已有上限。
- 大量記憶、超大內容、created_at 同值、插入／刪除穿插分頁、非 UTF-8 位元組、失效 session、跨 scope ID、重送回應遺失，均需探測或核准後的驗收測試。
- 不調整 Phase 3F selector、固定額度、Direct／Three-Face prompt 或 backend 呼叫語意。
