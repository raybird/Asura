# Issue 11 - 實作計畫 (Implementation Plan)

## 設計方案

### 1. 資料結構與簽名

#### 儲存層 (`internal/storage/sqlite.go`)
```go
// Task 擴充欄位以保存記憶參照與密封 Context
type Task struct {
    ID              string     `json:"id"`
    RequestID       string     `json:"request_id,omitempty"`
    Fingerprint     string     `json:"fingerprint,omitempty"`
    MemoryRefs      []string   `json:"memory_refs,omitempty"`      // 使用的 Memory IDs
    ResolvedContext string     `json:"resolved_context,omitempty"` // 密封之 ExecutionContext JSON 字串
    Prompt          string     `json:"prompt"`                     // 原始使用者 Prompt（不被改寫）
    Workspace       string     `json:"workspace"`
    Status          TaskStatus `json:"status"`
    Result          string     `json:"result"`
    ErrorMessage    string     `json:"error_message"`
    PGID            int        `json:"pgid"`
    Class           string     `json:"class,omitempty"`
    CreatedAt       time.Time  `json:"created_at"`
    UpdatedAt       time.Time  `json:"updated_at"`
}
```

#### Engine 層 (`internal/engine/engine.go`)
```go
// ProjectMemorySnapshot 代表密封進 Context 的單筆記憶快照
type ProjectMemorySnapshot struct {
    ID        string    `json:"id"`
    Workspace string    `json:"workspace"`
    Content   string    `json:"content"`
    Source    string    `json:"source"`
    CreatedAt time.Time `json:"created_at"`
}

// ExecutionContext 代表任務執行時之完整密封上下文
type ExecutionContext struct {
    OriginalPrompt string                  `json:"original_prompt"`
    Workspace      string                  `json:"workspace"`
    ProjectMemories []ProjectMemorySnapshot `json:"project_memories"`
    MemoryRefs     []string                `json:"memory_refs"`
    CreatedAt      time.Time               `json:"created_at"`
}

// SubmitRequest 擴充記憶參數
type SubmitRequest struct {
    ID              string            `json:"id,omitempty"`
    Workspace       string            `json:"workspace"`
    Prompt          string            `json:"prompt"`
    MemoryIDs       []string          `json:"memory_ids,omitempty"`
    ResolvedContext *ExecutionContext `json:"resolved_context,omitempty"`
}
```

#### Daemon IPC 層 (`internal/daemon/daemon.go`)
```go
type IPCRequest struct {
    Action     string          `json:"action,omitempty"`
    RequestID  string          `json:"request_id,omitempty"`
    TaskID     string          `json:"task_id,omitempty"`
    Workspace  string          `json:"workspace,omitempty"`
    Prompt     string          `json:"prompt,omitempty"`
    MemoryIDs  []string        `json:"memory_ids,omitempty"` // 新增：任務指定選取的 Memory IDs
    Class      TaskClass       `json:"class,omitempty"`
    // ...其餘既有欄位
}
```

---

## 實作步驟

> 依「風險優先決策流程」，Phase 1 先完成資料合約與儲存層 scope 解析紅燈驗證；Phase 2 與 Phase 3 聚焦密封 Context 與授權邊界；Phase 4 完成外迴圈整合與 CLI。
> 本計畫為 Large 分級，以下為 decompose 細化之可執行任務清單。

## Phase 1 — 儲存層 Schema 擴充與記憶解析服務

### Task 1.1 — tasks 資料表新增 memory_refs 與 resolved_context 欄位及 SQLiteStore 讀寫支援
- 產出與範圍：`internal/storage/sqlite.go`、`internal/storage/sqlite_test.go`。更新 `storage.Task` 結構加入 `MemoryRefs []string` 與 `ResolvedContext string`；在 `initSchema()` 實作容錯升級（`ALTER TABLE tasks ADD COLUMN`）；更新 `CreateTask`、`GetTask`、`GetTaskByRequestID` 讀寫邏輯。
- 相依：無
- 驗收編號：CTXMEM-006（責任 Task：底層持久化與查詢讀取）
- 完成判準：舊資料庫無損加載升級，新欄位讀寫正確，無記憶任務保持預設值且不影響既有欄位。
- 驗證：單元測試 `internal/storage/sqlite_test.go:TestSQLiteStore_MemoryContextColumnsMigration`，驗證加法欄位、既有庫升級無損與讀寫回傳。
- 狀態：✅ 已完成
- 證據：
  - 紅燈：`go test -v -run TestSQLiteStore_MemoryContextColumnsMigration ./internal/storage/...` (expected MemoryRefs ['mem-1', 'mem-2'], got [])
  - 綠燈：`go test -v -run TestSQLiteStore_MemoryContextColumnsMigration ./internal/storage/...` (PASS 0.01s)
  - 儲存全組：`go test -v ./internal/storage/...` (全部 13 項測試 PASS 0.140s)
  - 精煉：no-op（加法式相容且無多餘抽象）
  - 全專案回歸：`go test ./...` (全部 package PASS，包含 integration tests)

### Task 1.2 — 實作 ResolveMemoriesForTask 記憶解析與 scope 驗證
- 產出與範圍：`internal/storage/sqlite_memory.go`、`internal/storage/sqlite_memory_test.go`。在 `SQLiteStore` 實作 `ResolveMemoriesForTask(ctx context.Context, canonicalWorkspace string, memoryIDs []string) ([]Memory, error)`，依傳入之 `canonicalWorkspace` 逐筆驗證並讀取請求的 `memoryIDs`。
- 相依：Task 1.1
- 驗收編號：CTXMEM-005（責任 Task：不存在與已刪除記憶解析拒絕）、CTXMEM-002（支援責任：底層 scope 隔離）
- 完成判準：同專案存在之記憶成功解析回傳；跨專案 ID、不存在 ID、已刪除 ID 均返回明確錯誤且不洩漏任何記憶內容。
- 驗證：單元測試 `internal/storage/sqlite_memory_test.go:TestResolveMemoriesForTask`，驗證同專案解析、跨專案拒絕、不存在與已刪除 ID 之拒絕。
- 狀態：✅ 已完成
- 證據：
  - 紅燈：`go test -v -run TestResolveMemoriesForTask ./internal/storage/...` (unexpected error: not implemented)
  - 綠燈：`go test -v -run TestResolveMemoriesForTask ./internal/storage/...` (PASS 0.01s)
  - 儲存全組：`go test -v ./internal/storage/...` (全部 14 項測試 PASS 0.156s)
  - 精煉：no-op（複用既有 GetMemory，無冗餘抽象）
  - 全專案回歸：`go test ./...` (全部 package PASS，包含 integration tests)

## Phase 2 — Engine 記憶 Context 組裝與原始 Prompt 保護

### Task 2.1 — 定義 ExecutionContext 與 Prompt Markdown 組裝器
- 產出與範圍：`internal/engine/engine.go`、`internal/engine/engine_test.go`。定義 `ProjectMemorySnapshot`、`ExecutionContext`、擴充 `SubmitRequest`（加入 `MemoryIDs` 與 `ResolvedContext`）；實作 `ComposePrompt(prompt string, execCtx *ExecutionContext) string`，依 TBD-3 決策以 `### Project Context` Markdown 區塊格式組裝於 Prompt 之前；未指定記憶時完全原樣回傳。
- 相依：Task 1.1
- 驗收編號：CTXMEM-007（責任 Task：未選取記憶時維持既有任務語意，Prompt 完全原樣）
- 完成判準：未選取記憶時，傳入 runner 之 prompt 與原始 prompt 100% 相同；選取記憶時，以 Markdown 區塊組裝且包含每筆記憶內容。
- 狀態：✅ 已完成
- 證據：
  - 紅燈：`go test -v -run TestComposePrompt ./internal/engine/...` (expected prompt / Markdown block, got "")
  - 綠燈：`go test -v -run TestComposePrompt ./internal/engine/...` (PASS 0.00s)
  - 引擎全組：`go test ./internal/engine/...` (PASS 0.448s)
  - 精煉：no-op（strings.Builder 簡潔無冗餘）

### Task 2.2 — SubmitTask 整合 ExecutionContext 與原始 Prompt 保存
- 產出與範圍：`internal/engine/engine.go`、`internal/engine/engine_test.go`。修改 `Engine.SubmitTask`：接收 `SubmitRequest` 中的 `ResolvedContext`；若有 sealed context，將其序列化寫入 `storage.Task.ResolvedContext` 與 `storage.Task.MemoryRefs`；調用 `e.runner` 時傳入 `ComposePrompt(req.Prompt, req.ResolvedContext)`，但寫入儲存層之 `task.Prompt` 嚴格保持為 `req.Prompt` 不被污染或改寫。
- 相依：Task 1.1, Task 2.1
- 驗收編號：CTXMEM-001（責任 Task：同專案記憶可被任務使用，runner 取得內容且 task.Prompt 不被改寫）
- 完成判準：Runner 接收拼裝後的 prompt；DB 內 Task 之 Prompt 仍為原始輸入，且 Task 包含完整之 `ResolvedContext` 與 `MemoryRefs`。
- 驗證：單元測試 `internal/engine/engine_test.go:TestEngine_SubmitTaskWithMemoryContext`，透過 mock runner 斷言傳入之 prompt，並檢查 DB 內保存之 Task 欄位。
- 狀態：✅ 已完成
- 證據：
  - 紅燈：`go test -v -run TestEngine_SubmitTaskWithMemoryContext ./internal/engine/...` (Fail: runner did not receive memory context / build error)
  - 綠燈：`go test -v -run TestEngine_SubmitTaskWithMemoryContext ./internal/engine/...` (PASS 0.01s)
  - 引擎全組：`go test -v -race ./internal/engine/...` (全部 21 項測試 PASS 2.262s)
  - 精煉：檢查 DB task.Prompt 與 runner 傳入 prompt 確實分流隔離，無冗餘記憶拷貝

## Phase 3 — Daemon 調度閘門、指紋計算、Context 密封與 Redelivery 一致性

### Task 3.1 — Daemon IPC 擴充與 MemoryIDs 請求指紋計算
- 產出與範圍：`internal/daemon/daemon.go`、`internal/daemon/daemon_test.go`。在 `IPCRequest` 擴充 `MemoryIDs []string`；在 `submit` 處理器中，將排序後的 `MemoryIDs` 納入請求指紋計算：`sha256(Workspace + ":" + Prompt + ":" + strings.Join(sortedMemoryIDs, ","))`；支援 `SubmitRequest` 之 Client 與 IPC 編解碼。
- 相依：Task 1.1
- 驗收編號：CTXMEM-004（支援責任：指紋與重送識別）、CTXMEM-006（支援責任：IPC 傳遞記憶清單）
- 完成判準：相同 Request ID 提交相同 Prompt、Workspace 與 MemoryIDs 時，正確識別為 redelivery；若 MemoryIDs 不同，正確拒絕 `request ID reused with different payload`。
- 驗證：單元測試 `internal/daemon/daemon_test.go:TestDaemon_SubmitMemoryFingerprint`，測試同 Request ID 帶相同記憶與不同記憶時的行為。
- 狀態：✅ 已完成
- 證據：
  - 紅燈：`go test -v -run TestDaemon_SubmitMemoryFingerprint ./internal/daemon/...` (expected 'request ID reused with different payload', got err: <nil>)
  - 綠燈：`go test -v -run TestDaemon_SubmitMemoryFingerprint ./internal/daemon/...` (PASS 0.01s)
  - 精煉：使用 calculateTaskFingerprint 統一處理排序與可選記憶，維持既有任務指紋相容性

### Task 3.2 — Daemon 記憶解析閘門、授權驗證與 Fail-Closed 防禦
- 產出與範圍：`internal/daemon/daemon.go`、`internal/daemon/memory_test.go`。在 Daemon `submit` 處理器中：
  1. 透過 `d.policy.AuthorizeCanonicalWorkspace(req.Workspace)` 同步取得授權與 canonical workspace；若未授權或非法立即拒絕。
  2. 若 `len(req.MemoryIDs) > 0`：調用 `d.store.ResolveMemoriesForTask(ctx, canonicalWs, req.MemoryIDs)`。若有任何 Memory ID 不存在、已刪除、屬於其他專案，或 workspace 已撤權，立即回傳錯誤，絕不進入 WorkerPool，絕不啟動 Runner，0 後端子進程調用。
  3. 若解析成功，建立不可變之 Sealed `ExecutionContext`（包含原始 prompt、canonical workspace、ProjectMemorySnapshot 陣列、MemoryRefs 及時間戳），將其序列化並填入 `initialTask.MemoryRefs` 與 `initialTask.ResolvedContext`。
  4. 在 `dispatchTask` 與 `dispatchQueuedTask` 將 sealed context 傳遞至 `engine.SubmitTask`。
- 相依：Task 1.2, Task 2.2, Task 3.1
- 驗收編號：CTXMEM-002（責任 Task：跨專案記憶於 runner 前拒絕且後端啟動為 0）、CTXMEM-003（責任 Task：撤權與非法 workspace 不可使用記憶，不啟動 runner）
- 完成判準：跨專案記憶 ID、非法 workspace 或已撤權專案之任務請求，在 runner 啟動前即被拒絕，Runner 調用次數嚴格為 0；合規請求成功生成密封 Context 存入 DB 並調度執行。
- 驗證：整合測試 `internal/daemon/memory_test.go:TestDaemon_MemoryScopeRejection`，模擬專案 A 嘗試選取專案 B 記憶、已撤權專案選取記憶，斷言 runner 啟動次數為 0 且錯誤訊息符合預期。
- 狀態：✅ 已完成
- 證據：
  - 紅燈：`go test -v -run TestDaemon_MemoryScopeRejection ./internal/daemon/...` (expected error when selecting cross-project memory, got nil)
  - 綠燈：`go test -v -run TestDaemon_MemoryScopeRejection ./internal/daemon/...` (PASS 0.03s: CTXMEM-001, CTXMEM-002, CTXMEM-003, CTXMEM-005 全部 PASS)
  - 精煉：在容量獲取與任務儲存前嚴密閘門校驗，非法請求 runner 執行次數嚴格為 0

### Task 3.3 — 驗證 Context 密封不可變性與 Redelivery 一致性
- 產出與範圍：`internal/daemon/memory_test.go`、`internal/daemon/daemon.go`。在 Daemon 測試中建立情境：
  1. 專案提交任務解析記憶 M1 並成功接受；
  2. 記憶 M1 隨後在底層被修改或刪除；
  3. 以相同的 Request ID 重新提交該任務；
  4. 驗證回傳的任務其 `ResolvedContext` 仍為接受時密封的原始內容，且底層不因重送而重新查詢已被異動/刪除的記憶，亦不產生第二次執行副作用。
- 相依：Task 3.2
- 驗收編號：CTXMEM-004（責任 Task：任務接受後 Context 保持一致，不因重送或記憶異動而改變）
- 完成判準：記憶修改或刪除後，已接受任務仍使用當時已解析的相同 context，重送不重新解析，不造成二次執行。
- 驗證：Daemon 測試 `internal/daemon/memory_test.go:TestDaemon_TaskContextSealedAndImmutable`，驗證記憶異動前後的任務重送一致性與密封 Context。
- 狀態：✅ 已完成
- 證據：
  - 紅燈：`go test -v -run TestDaemon_TaskContextSealedAndImmutable ./internal/daemon/...` (redelivery submit should succeed even after memory deleted, but got: memory not found)
  - 綠燈：`go test -v -run TestDaemon_TaskContextSealedAndImmutable ./internal/daemon/...` (PASS 0.03s)
  - 精煉：redelivery 識別前置於記憶解析，確保已接受任務密封 context 永不因底層異動受損，且 runner 呼叫次數嚴格保持 1

## Phase 4 — CLI 參數支援與外迴圈端到端整合

### Task 4.1 — CLI asura ask 支援 --memory-id 參數
- 產出與範圍：`cmd/asura/ask.go`、`cmd/asura/run_test.go`。在 `handleAsk` 增加 `--memory-id` 旗標支援（依 TBD-2 決策，支援重複 `--memory-id M1 --memory-id M2` 以及逗點分隔 `--memory-id M1,M2`）；將解析後之 `MemoryIDs` 傳入 `engine.SubmitRequest` / Daemon Client `Submit`；當 Daemon 回傳記憶不存在或授權失敗時，輸出至 stderr 並以非零狀態碼退出。
- 相依：Task 3.2
- 驗收編號：CTXMEM-001（支援責任：CLI 端輸入傳遞）、CTXMEM-005（支援責任：CLI 錯誤輸出與退出碼）
- 完成判準：CLI 支援多種 `--memory-id` 指定格式；未傳入時維持原 ask 語意；失敗時輸出明確錯誤且以非零狀態碼退出。
- 驗證：單元測試 `cmd/asura/run_test.go:TestHandleAsk_MemoryFlags`，驗證參數解析、字串切割與錯誤退出碼。
- 狀態：✅ 已完成
- 證據：
  - 紅燈：`go test -v -run TestHandleAsk_MemoryFlags ./cmd/asura/...` (flag provided but not defined: -memory-id)
  - 綠燈：`go test -v -run TestHandleAsk_MemoryFlags ./cmd/asura/...` (PASS 0.00s)
  - CLI 全組：`go test -v -race ./cmd/asura/...` (全部 5 項測試 PASS 1.012s)
  - 精煉：實作 stringSliceFlag 同時支援多值重複指定與逗點分隔，簡潔無多餘相依

### Task 4.2 — 外迴圈端到端整合測試與全迴圈驗收
- 產出與範圍：`tests/integration/memory_context_test.go`。撰寫外迴圈端到端整合測試，啟動真實 Daemon 與 CLI binary，完整覆蓋 CTXMEM-001 ～ CTXMEM-007 全部情境：
  1. CTXMEM-001：同專案記憶可被任務使用，prompt 拼裝正確且 DB 中原始 prompt 不變。
  2. CTXMEM-002：跨專案記憶 ID 傳入，CLI 非零退出，runner 0 次調用，記憶內容不外洩。
  3. CTXMEM-003：撤權 workspace 提交帶記憶任務，context resolution 失敗，不啟動 runner。
  4. CTXMEM-004：任務接受後記憶被修改或刪除，重送仍回傳當時密封 context，無二次執行。
  5. CTXMEM-005：傳入已刪除或不存在記憶 ID，CLI 明確報錯且 runner 不啟動。
  6. CTXMEM-006：查詢 Task 可識別使用的 Memory IDs 與 canonical workspace scope。
  7. CTXMEM-007：未傳入 `--memory-id` 時，完全維持 Phase 3A 前既有任務語意，不注入任何記憶。
  執行 `go test -race ./...` 確認全專案無 data race、既有測試無回歸。
- 相依：Task 4.1, Task 3.3
- 驗收編號：CTXMEM-001, CTXMEM-002, CTXMEM-003, CTXMEM-004, CTXMEM-005, CTXMEM-006, CTXMEM-007（全體驗收外迴圈端到端整合責任）
- 完成判準：外迴圈全數通過，`go test -race ./...` 全綠，0 data race，輸出並保存完整驗證日誌。
- 驗證：執行端到端整合測試與全專案 race 回歸測試。
- 狀態：✅ 已完成
- 證據：
  - 外迴圈全套整合：`go test -v -run "TestCTXMEM" ./tests/integration/...` (PASS 0.595s: CTXMEM-001～CTXMEM-007 與 CLI 端到端全數通過)
  - 整合套件回歸：`go test -v ./tests/integration/...` (全部 46 項整合測試 PASS 61.462s)
  - 全專案無痛回歸：`go test ./...` (全模組 PASS，0 錯誤)
  - 精煉：全迴圈整合確認端到端與 CLI 參數無痛協同，原始 Prompt 保持隔離，Sealed Context 一致性驗證無懈可擊

---

## 使用方式對照

### 變更前 (Phase 3A)
```bash
# 僅支援文字 Prompt，無法選取專案記憶
asura ask --workspace /path/to/project "幫我分析系統架構"
```

### 變更後 (Phase 3B)
```bash
# 明確指定注入之記憶 ID（支援單一或多個）
asura ask --workspace /path/to/project --memory-id mem-1 --memory-id mem-2 "幫我分析系統架構"

# 亦支援逗點分隔
asura ask --workspace /path/to/project --memory-id mem-1,mem-2 "幫我分析系統架構"

# 未指定記憶時，維持原有行為不變
asura ask --workspace /path/to/project "幫我分析系統架構"
```

---

## 測試策略

1. **單元測試 (Unit Tests)**：
   - `internal/storage/sqlite_memory_test.go`：測試 `ResolveMemoriesForTask` 在同專案、跨專案、不存在 ID、已刪除 ID 下的解析結果。
   - `internal/engine/engine_test.go`：測試 `ComposePrompt` 的組裝格式，驗證原始 `task.Prompt` 不被覆寫，驗證 runner 接收內容。
2. **Daemon 整合測試 (Daemon Tests)**：
   - `internal/daemon/memory_test.go`：測試提交任務帶入跨專案記憶、撤權 workspace 時在 runner 啟動前的失敗攔截。
   - 測試任務接受後，底層記憶被修改或刪除，使用同 Request ID 重送時取得相同密封 Context，runner 不重複執行或使用舊 Context。
3. **CLI 外迴圈整合測試 (Integration Tests)**：
   - `tests/integration/memory_context_test.go`：真實執行 Daemon 與 CLI binary，驗證真實端到端資料流與錯誤碼。

---

## 驗收編號對應的測試層級與策略

| 驗收編號 | 測試層級 | 測試策略 |
|---|---|---|
| **CTXMEM-001** | Integration / E2E | 同專案記憶注入測試：驗證 runner 接收到包含記憶與 prompt 的組裝內容，且 `task.Prompt` 維持原始輸入。 |
| **CTXMEM-002** | Integration / Daemon | 跨專案記憶隔離測試：專案 A 嘗試使用專案 B 的記憶 ID，驗證在 runner 啟動前回傳錯誤，後端啟動計數為 0。 |
| **CTXMEM-003** | Integration / Daemon | 撤權與非法 workspace 測試：已撤權專案帶記憶提交，驗證 context resolution 失敗，不啟動 runner。 |
| **CTXMEM-004** | Unit / Integration | Context 不可變與重送一致性測試：任務接受後修改或刪除記憶，重送相同 Request ID 驗證仍取得接受時密封的 Context。 |
| **CTXMEM-005** | Unit / Daemon | 不存在或已刪除記憶測試：傳入不存在 ID，驗證拒絕且 runner 不啟動。 |
| **CTXMEM-006** | Unit / Storage | 歷史可追溯測試：查詢已建立 Task，驗證能完整取得 `MemoryRefs` 與 `ResolvedContext`。 |
| **CTXMEM-007** | Integration / CLI | 向後相容回歸測試：未指定記憶時，驗證既有 ask 任務行為完全一致，不自動注入記憶。 |

---

## 風險與首要驗證

> 引用 `README.md` 之「風險與首要驗證」，五項定義以 `README.md` 為唯一真相來源。

- 最大風險為 **Context 不可重現**。
- 首要驗證聚焦於 runner 啟動前的解析、密封與授權防禦測試。

---

## 檢查清單

- [ ] 儲存層任務表具備向後相容加法升級支援
- [ ] 記憶解析僅限於當前 canonical workspace
- [ ] 跨專案、已撤權、不存在之記憶於 runner 啟動前被阻擋
- [ ] 任務指紋納入記憶清單
- [ ] 任務接受時密封 Context，保證重送與記憶變更時不變
- [ ] 原始 prompt 完好保留於資料庫
- [ ] 未指定記憶時完全保持既有任務相容性
- [ ] 全專案測試與 race detection 通過
