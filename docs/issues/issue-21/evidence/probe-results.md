# 初始風險探測

2026-09-23 16:20 CST。僅使用隔離的 SQLite `:memory:` fixture 與既有測試；沒有修改產品程式或使用者資料。此證據降低規劃不確定性，不代表 WEBMEM-001～010 已驗收。

## 環境與查詢

- 工作區 HEAD：`9d5c2a3`；系統 SQLite CLI 3.45.1。既有 Go 專案使用 `modernc.org/sqlite`，故 CLI 查詢計畫仍需在產品實作時以 Go driver 重新驗證。
- fixture：完整建表、插入及斷言 SQL 見 [probe.sql](probe.sql)。`/a` 10,000 筆，`/b` 一筆 `SECRET`；`/a` 首筆內容 524,288 bytes，其餘 `normal`。初始 `created_at` 依分鐘／秒循環，第二次分頁探測把 `/a` 全部設為 `2026-09-23T00:00:00Z`，以驗證 ID tie-breaker。
- 重跑命令：`sqlite3 :memory: '.read docs/issues/issue-21/evidence/probe.sql'`。2026-09-23 執行 exit code 0，完整輸出如下；腳本只使用記憶體資料庫。

```text
10000|524288
QUERY PLAN
`--SEARCH memories USING INDEX idx_memories_workspace_created_id (workspace=? AND (created_at,id)>(?,?))
51|513|813
50|m00000|m00049
50|m00050|m00099
0
524288|513
524288|65537
```
- 查詢計畫原文：

```sql
CREATE INDEX idx_memories_workspace_created_id
ON memories(workspace,created_at,id);
EXPLAIN QUERY PLAN
SELECT id, substr(CAST(content AS BLOB),1,513)
FROM memories
WHERE workspace='/a'
  AND (created_at,id)>('2026-09-23T00:00:00Z','')
ORDER BY created_at,id LIMIT 51;
```

SQLite 輸出為 `SEARCH memories USING INDEX idx_memories_workspace_created_id (workspace=? AND (created_at,id)>(?,?))`，無 temp sort。資料庫中 `/a` 筆數 10,000、最大 content 524,288 bytes。`LIMIT 51` 回傳 51 筆；摘要欄位單筆最多 513 bytes，該頁摘要欄位合計 813 bytes。這只量到欄位 payload，未包含 JSON、ID、source、timestamp 或 driver 開銷；單筆 513 bytes 是提案 512 bytes 加 sentinel，尚非核准上限。

同時間戳頁面探測：第一頁 50 筆為 `m00000`～`m00049`，用 `(created_at,'m00049')` 查下一頁得到 `m00050`～`m00099`，兩頁沒有重複或缺漏；`/a` 中 `SECRET` 筆數為 0。首筆 524,288 bytes 的 content 經 SQL `substr(...,1,513)` 後為 513 bytes；擬議 detail 的 65,537 bytes sentinel 則回 65,537 bytes。兩者只證明 SQL 結果欄位受限，不能據此宣稱 SQLite 內部沒有讀取完整 BLOB。資料更新穿插分頁、metadata 超限、有界 detail 的 Go／IPC 行為及整體 HTTP 大小仍待核准後驗證。

## 既有邊界測試

```text
go test ./internal/web ./internal/auth ./internal/storage
exit 0：web 0.006s、auth 0.003s、storage 0.308s

go test -run TestRecallCandidates -v ./internal/storage
exit 0：10001 rows，returned=256，elapsed=606.712µs；storage 0.168s

go test -run 'TestWEB001Unit|TestWEB010Unit' -v ./internal/web
exit 0：WEB-001 登入／cookie、WEB-010 Origin／Host／登出拒絕通過

go test -run 'TestDaemon_MemoryScopeRejection|TestDaemon_SubmitMemoryFingerprint' -v ./internal/daemon
exit 0：跨 scope／不存在／已刪除 ID 與未授權 workspace 在 runner 前拒絕，既有指紋測試通過
```

第二個測試是 Phase 3F 既有 `RecallCandidates`；其 fixture 含 10,000 筆同 scope、1 MiB 首筆與一筆跨 scope，驗證 256 筆上界、8193 bytes sentinel、取消後連線釋放與 `(workspace,id)` 查詢計畫。它證明現有 recall 路徑的界線，不能代替新 memory page 的 SQL／IPC／HTTP 驗收。WEB 與 Daemon 測試也只刻畫既有入口；新增 Web 記憶 API 尚無紅燈或綠燈。既有 Web 拒絕測試使用無 Daemon socket，拒絕不會進入 IPC；Daemon scope 測試以 runner 計數 0 驗證拒絕。

## 後續需要的證據

核准具體頁大小、metadata／detail 限制與 cursor 後，以 Go driver 的真實 `PageMemories` 與有界 detail 查詢重做 SQL 計畫、同值排序、插入／刪除穿插、response bytes 及超大 detail 在 IPC 前拒絕；並以真實 Web HTTP → Unix IPC → Daemon 測未登入、跨 Origin 變更請求、跨 workspace、0 寫入與 0 backend calls。瀏覽器跨站 GET 的不可讀取性另用瀏覽器行為驗證。
