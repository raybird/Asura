# Issue 31 — 現況與需求分析

調查日期：2026-10-01；被讀取版本：`1b21d587a738b871e1dde48abceb17a8ce8c1bb2`。

## 需求與目標
依來源 L1～L7 建立可稽核 lifecycle；不加入 GC、expiry、ranking boost、AI supersedes 或 Web lifecycle UI。候選驗收與核准狀態只維護在 README。

## 現況分析
- `sqlite_memory.go` 的 Memory 僅 ID/workspace/content/source/created_at。CreateMemory 單筆 INSERT；DeleteMemory 以 workspace + id 硬刪除，錯 scope 與不存在均為 ErrMemoryNotFound。
- `sqlite.go` 的 memories 有 workspace、workspace/id、workspace/created_at/id 索引；現有 migration 使用欄位探測及 ALTER，Git migration 另有 marker 與重啟隔離語意。不能僅改 CREATE TABLE 假裝 legacy 已升級。
- `RecallCandidates` 在 SQL 以 workspace/source 篩選後 ORDER BY id LIMIT 257；上限 256 筆候選，content 8192 bytes。superseded 必須在 LIMIT 前排除，不能只在 selector 跳過。
- `daemon.go` 的 memory_add 先 AuthorizeCanonicalWorkspace，再產生 memory ID；每次請求生成新 ID，目前無 manual creation retry identity。
- `ApproveCandidateTx` 已把 candidate CAS、memory、provenance 放同一交易。一般與 bounded 路徑的已核准重試不同：一般路徑查 memory 是否被刪；bounded 路徑直接回 approved_memory_id。新增 lifecycle 不得意外改掉這些既有行為；衝突的新 supersedes 參數不能被靜默忽略。
- `daemon/recall.go` 與 `daemon.go` 均組裝 ProjectMemorySnapshot，目前無 lifecycle 欄位；Task 的 resolved_context 已持久化。新增欄位須容忍舊 JSON，禁止重算舊快照。
- `sqlite_utility_query.go` 允許同 scope 已刪記憶從 recall ledger 查 utility，fresh memory 為 neutral。successor 使用新 ID 即可隔離 observations，不能複製 aggregate。
- `sqlite_memory_page.go` 已採 SQL bounded projection；detail content 超過 65536 bytes 拒絕。新 provenance/chain detail 不能退回無界讀取。

## 問題點與邊界
硬刪除會移除 memory row；若 relation 只存在 row 中，刪 predecessor 後 successor 無法完整稽核。需要選擇保留不含內容的 relation ledger，或變更 delete 契約；後者超出來源既有 delete semantics，提案採前者，待核准。

新 ID 保證 chain 不形成 cycle 的前提是只允許「新建 successor」，不能提供任意既有 ID relink API。仍須測 self-supersede、並行分叉與非法持久化資料 fail-closed。

以上是程式碼調查，未執行 migration、性能 probe 或 production 測試，不代表 MLIFE 已驗證。
