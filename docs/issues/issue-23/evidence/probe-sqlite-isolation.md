# SQLite 記憶隔離與相容性探測報告（Task 1.2）

2026-09-25 11:26 CST。工作區基準：Commit `04441ec`。執行測試套件：`tests/integration/probe_sqlite_isolation_test.go`。
本探測使用隔離的臨時 SQLite 資料庫，未修改任何生產資料庫或程式碼。

---

## 一、探測目標與方法

為驗證 H-TBD-4 之資料表設計與 Phase 3F 召回整合假設，進行以下驗證：
1. **來源欄位相容性**：寫入 `source='git-approved'` 至 `memories` 表，測試既有 `ListMemories`、`GetMemory` 與 `ResolveMemoriesForTask` 之讀取行為。
2. **Phase 3F 詞彙召回相容性**：執行 `recall.Select`，驗證 `git-approved` 記憶在詞彙重疊計數（term overlap count）與 Context Window 預估中的表現。
3. **候選暫存庫實體隔離**：建立 `memory_candidates` 並插入待審草稿，驗證既有 `ListMemories` 是否維持 0 洩漏。
4. **Provenance 外鍵串聯刪除**：建立 `memory_provenance` 外鍵 `FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE`，驗證刪除正式記憶時來源紀錄是否自動清理。

---

## 二、實際執行結果與日誌

執行命令：
```bash
go test -v -run TestProbe_SQLite ./tests/integration/
```

輸出紀錄：
```text
=== RUN   TestProbe_SQLite_GitApprovedMemoryCompat
    probe_sqlite_isolation_test.go:110: PASS: Recall correctly selected git-approved memory with score: 5
--- PASS: TestProbe_SQLite_GitApprovedMemoryCompat (0.01s)
=== RUN   TestProbe_SQLite_CandidateIsolationAndProvenanceCascade
    probe_sqlite_isolation_test.go:193: PASS: Candidate storage is 100% isolated from ListMemories.
    probe_sqlite_isolation_test.go:229: PASS: ON DELETE CASCADE cleanly removed provenance row upon memory deletion.
--- PASS: TestProbe_SQLite_CandidateIsolationAndProvenanceCascade (0.01s)
PASS
ok  	asura/tests/integration	0.031s
```

---

## 三、關鍵實證發現

1. **現有儲存層無痛相容**：
   - 既有 `memories` 資料表（五欄位：id, workspace, content, source, created_at）在寫入 `source='git-approved'` 時，既有查詢完全不需修改程式碼即可正確解析與回傳。
2. **Phase 3F 自動召回自然整合**：
   - `recall.Select` 直接對傳入的候選記憶計算 prompt 詞彙重疊計數；在包含 architecture、decision、memory_candidates 等關鍵字時，`git-approved` 記憶獲得最高評分（score=5）並被優先選入，證實無需改動召回核心。
3. **候選庫 100% 實體隔離**：
   - `memory_candidates` 獨立建表後，所有待審核草稿僅存於候選表；`ListMemories` 查詢輸出為 0 筆，徹底杜絕草稿進入 prompt 的安全風險。
4. **ON DELETE CASCADE 有效性**：
   - 經測試，在 SQLite 啟用 `PRAGMA foreign_keys = ON;` 下，刪除正式記憶會自動級聯刪除對應的 `memory_provenance` 紀錄，維持專案儲存庫衛生；而 `memory_candidates` 表維持不可變審核稽核紀錄。

---

## 四、結論

Task 1.2 所規劃的 SQLite 資料庫設計與 Phase 3F 召回整合路徑完全可行。
可直接推進 Phase 1 完成收尾與 Phase 2 核心實作。
