# Issue 11 - 技術方案取捨 (Technical Analysis)

## 技術可行性分析

本議題的核心任務是完成「專案記憶資料平面（Phase 3A）→ 任務執行 Context（Phase 3B）」的正式接入。

在現有系統中：
1. `internal/storage/sqlite.go` 使用現代 SQLite（WAL 模式、單一連線池、加法式 `initSchema` 升級），具備良好的原子性與輕量擴充能力。
2. `internal/auth/policy.go` 提供了 `AuthorizeCanonicalWorkspace`，已在 Phase 3A 驗證能正確消除符號連結與路徑多型性。
3. `internal/daemon/daemon.go` 作為任務調度的唯一入口，具備 Request ID 冪等防護、請求指紋校驗與容量控制。
4. `internal/engine/engine.go` 負責任務生命週期、狀態機轉移、取消協調與調用後端 Runner。

技術上，在任務接受時完成記憶解析與 Context 封裝，並在 Engine 統一完成記憶組裝，完全可行且架構邊界清晰。

---

## 方案選型與取捨

### 取捨一：Sealed Context 的生命週期點與持久化架構

#### 方案比較

| 方案 | 說明 | 優點 | 缺點 |
|---|---|---|---|
| **方案 A：動態延遲解析（參照制）** | Task 僅保存 `MemoryIDs` 清單。每次執行或重送時，才至 `memories` 資料表查詢最新記憶內容。 | Schema 無需儲存記憶快照，實作極簡。 | 破壞不變性與可重現性。若記憶在任務接受後被修改或刪除，Redelivery 或後續重跑將取得不同 Context，違反 CTXMEM-004。 |
| **方案 B-1：單表 JSON 密封快照（選定方案）** | 在任務被接受（pending）時，立即解析目標記憶並將其不可變快照序列化為 JSON，存於 `tasks.resolved_context`，同時儲存 `tasks.memory_refs`。 | 1. 保證 100% Context 可重現與不可變。<br>2. 查詢 Task 可單行取得完整 Context，無 JOIN 開銷。<br>3. 加法式 schema 升級簡單、安全且符合 YAGNI。 | 當單一任務注入巨量記憶時，tasks 表單列體積增加（但第一版為明確選取，記憶容量受控）。 |
| **方案 B-2：關聯式快照表 (`task_memories`)** | 建立獨立資料表 `task_memories`，每個任務與記憶對應一列，記錄 snapshot content。 | 關聯正規化。 | 增加額外資料表與關聯維護複雜度；對於絕大多數未指定記憶或僅少量記憶的任務，過度工程化。 |

#### 選定結論
**採用方案 B-1**。在任務接受時完成解析，並以 JSON 密封保存至 `tasks` 表中。

```diff
  CREATE TABLE tasks (
      id TEXT PRIMARY KEY,
      request_id TEXT NOT NULL DEFAULT '',
      fingerprint TEXT NOT NULL DEFAULT '',
+     memory_refs TEXT NOT NULL DEFAULT '',
+     resolved_context TEXT NOT NULL DEFAULT '',
      prompt TEXT NOT NULL,
      workspace TEXT NOT NULL,
      ...
  );
```

---

### 取捨二：請求指紋 (Fingerprint) 與 Redelivery 冪等邊界

#### 現況與問題
現有指紋計算：
```go
fp := fmt.Sprintf("%x", sha256.Sum256([]byte(req.Workspace+":"+req.Prompt)))
```
若同一個 Request ID 先後提交相同的 Prompt 與 Workspace，但分別帶有不同的 Memory IDs（例如第一次不帶記憶，第二次帶記憶），若未納入指紋比對，將會被誤判為相同請求而回傳第一次未帶記憶的任務。

#### 選定方案
將正規化排序後的 `MemoryIDs` 納入指紋計算：
```go
sort.Strings(req.MemoryIDs)
memPart := strings.Join(req.MemoryIDs, ",")
fp := fmt.Sprintf("%x", sha256.Sum256([]byte(req.Workspace+":"+req.Prompt+":"+memPart)))
```
- 若同一 Request ID 之 `Workspace + Prompt + MemoryIDs` 完全相符，允許 Redelivery 並回傳當時密封之 Task（CTXMEM-004）。
- 若 MemoryIDs 不同，明確拒絕 `request ID reused with different payload`。

---

### 取捨三：Runner 呼叫簽名與 Context 組裝契約

#### 方案比較

| 方案 | 說明 | 評估 |
|---|---|---|
| **方案 1：改寫 RunnerFunc 簽名** | 改為 `type RunnerFunc func(ctx context.Context, execCtx ExecutionContext) (string, error)` | 破壞現有所有測試與 Runner 實作，改動範圍過大，不符合手術式修改原則。 |
| **方案 2：Engine 統一組裝 Prompt + Context 注入（選定方案）** | 既有 `RunnerFunc` 簽名維持 `(ctx, workspace, composedPrompt)` 不變。由 Engine 在調用 runner 前，根據 `ExecutionContext` 將記憶區塊與原始 Prompt 進行組裝；同時將 `ExecutionContext` 放進 `context.Context`。 | 1. 既有 Runner 零改動相容。<br>2. 資料庫中 `Task.Prompt` 永遠保持原始 prompt，不被改寫。<br>3. 未指定記憶時完全保持既有 prompt 傳入。<br>4. 未來 Three-Face Pipeline 可直接自 context 或 Engine 取得結構化 `ExecutionContext`。 |

#### Context 組裝格式設計
組裝於 Engine 端執行，範例格式：
```text
=== Project Context ===
[Memory: mem-1]
Project Architecture: SQLite WAL mode with single connection pool.

[Memory: mem-2]
Coding Rule: Always return explicit errors.
=======================

{Original Prompt}
```
未指定記憶時：
直接傳入 `{Original Prompt}`，保持與 Phase 3A 之前的行為完全相容（CTXMEM-007）。

---

## 架構影響評估

```text
User CLI (asura ask --memory-id M1)
    │
    ▼ IPC (submit: Workspace, Prompt, MemoryIDs)
Daemon
    ├── 1. AuthorizeCanonicalWorkspace(req.Workspace)
    ├── 2. ResolveRequestedMemories(canonicalWs, req.MemoryIDs)
    │      └── 嚴格校驗：存在性、未刪除、同 canonical workspace scope
    │          （任一失敗立即在 runner 啟動前拒絕，0 後端調用）
    ├── 3. Calculate Fingerprint (含 MemoryIDs)
    ├── 4. Redelivery Check (若命中回傳既有 sealed Task)
    ├── 5. Seal ExecutionContext (原始 Prompt, Workspace, ProjectMemory[], MemoryRefs)
    ├── 6. Store.CreateTask(task with memory_refs, resolved_context)
    │
    ▼ dispatchTask
Engine.SubmitTask
    ├── 讀取或接收 ExecutionContext
    ├── 組裝 Prompt (記憶區塊 + 原始 Prompt)
    └── runner(taskCtx, workspace, composedPrompt)
```

1. **Storage**：僅在 `tasks` 表新增兩個預設為空字串的文字欄位，加法式向後相容。
2. **Daemon**：在 `submit` 處理器前半段加入記憶解析閘門；若無記憶參數則走既有快速路徑。
3. **Engine**：新增 `ExecutionContext` 定義與組裝器；不改動狀態機、取消流程與 Runner 介面。
4. **CLI**：`asura ask` 擴充 `--memory-id` 參數。

---

## 實作細節與待釐清項目

1. **CLI 參數規格**：支援多重 `--memory-id` 旗標與逗點分隔（如 `--memory-id M1 --memory-id M2` 或 `--memory-id M1,M2`）。
2. **錯誤訊息一致性**：當 Memory ID 不存在、跨專案或已刪除時，統一回報 `memory not found: <id>` 或 `unauthorized workspace`，不洩漏其他專案的記憶內容與長度。
3. **資料表加法升級**：在 `initSchema()` 中加入 `memory_refs` 與 `resolved_context` 欄位的容錯升級（`ALTER TABLE tasks ADD COLUMN ...`），避免影響既有資料庫。
