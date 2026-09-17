# Issue 11 - 現況與需求分析 (Requirement Analysis)

## 需求描述

本議題目標為將 Phase 3A 已具備的專案級記憶能力，正式接入 AI 任務執行路徑。讓 `asura ask` 及共用任務中樞在調度執行任務前，能依目前 canonical workspace 解析可使用的專案記憶，組成可追溯、受限且可重現的 `ExecutionContext`，再交由既有 Runner / OpenCode 後端執行。

本期第一版採確定性／明確選取（Deterministic / Explicit Selection），由任務提交時明確指定一組 Memory IDs，經服務端完成 scope 驗證與 context 密封（sealed context）後執行，為後續 Three-Face Pipeline 提供穩定的記憶輸入契約。

## 現況分析

詳細檢視目前程式碼邏輯（基準 main `987e47a`）：

1. **CLI 任務提交端 (`cmd/asura/ask.go`)**：
   - 僅支援 `--socket`、`--workspace`、`--id`、`--wait` 旗標與純文字 prompt。
   - 直接將使用者輸入的 `Prompt` 與 `Workspace` 封裝為 `engine.SubmitRequest` 透過 Daemon Client 發送。
   - 尚未提供任何參數供使用者指定要注入任務的 Memory IDs。

2. **Daemon 任務調度中樞 (`internal/daemon/daemon.go`)**：
   - IPC 請求 `IPCRequest` 包含 `Prompt`、`Workspace`、`RequestID` 等，但 `submit` 動作未包含 `MemoryIDs`。
   - 請求指紋（Fingerprint）計算僅以 `sha256(Workspace + ":" + Prompt)` 產生，未納入 Memory IDs。
   - 授權僅調用 `d.policy.AuthorizeWorkspace(req.Workspace)`，尚未在任務提交點透過 `AuthorizeCanonicalWorkspace` 將 scope 鎖定為不可變 canonical 鍵值。
   - 收到請求後直接寫入 `storage.Task` 並透過 `WorkerPool` 調度 `d.engine.SubmitTask`。

3. **Engine 任務協調層 (`internal/engine/engine.go`)**：
   - `SubmitRequest` 結構僅有 `ID`、`Workspace`、`Prompt`。
   - `SubmitTask` 在通過 policy 授權檢查後，將 `req.Workspace` 與 `req.Prompt` 直接傳入 `e.runner(taskCtx, req.Workspace, req.Prompt)`。
   - 完全沒有記憶解析、封裝與組裝邏輯；`Task.Prompt` 直通後端。

4. **Storage 儲存層 (`internal/storage/sqlite.go`, `sqlite_memory.go`)**：
   - `tasks` 資料表包含 `id, request_id, fingerprint, prompt, workspace, status, result, error_message, pgid, class, created_at, updated_at`。
   - `memories` 資料表包含 `id, workspace, content, source, created_at`。
   - 兩表之間完全獨立，沒有記錄某個 Task 實際使用了哪些 Memory IDs，也沒有保存執行當時解析出的記憶內容快照。

5. **授權與 Scope 隔離 (`internal/auth/policy.go`)**：
   - Phase 3A 已實作 `AuthorizeCanonicalWorkspace`，可將路徑正規化為精確的 canonical scope。
   - 目前記憶 CRUD 操作嚴格限定單一 canonical scope，跨專案或已撤權會直接回傳錯誤。
   - 任務執行路徑尚未接入此項 scope 驗證機制。

## 問題點總結

1. **記憶資料平面與任務執行平面脫節**：
   Phase 3A 完成了專案記憶的儲存與 CLI CRUD，但 AI 任務無法引用任何記憶，使用者無法將保存的架構或規範注入 AI 任務。
2. **Context 不具重現性與不變性保證（最大風險）**：
   若任務在執行時才動態查詢「最新」記憶，當記憶後續被修改、刪除，或發生網路逾時重送（Redelivery）時，同一 Task ID 會得到不同輸入，破壞既有冪等性與可追溯性契約。
3. **缺乏統一的 Context Resolution 邊界**：
   若由 CLI、Web、Cron 各自實作記憶讀取與字串拼裝，將導致安全驗證分散、格式不一致、且容易繞過服務端授權。
4. **跨專案記憶外洩與非法注入風險**：
   若未在 runner 啟動前嚴格檢查 Memory ID 是否屬於目前 canonical workspace，可能導致攻擊者藉由指定記憶 ID 跨專案竊取受保護的記憶內容。

## 目標

1. **定義不可變 `ExecutionContext`**：包含原始 Prompt、Canonical Workspace、已解析專案記憶快照、記憶參照清單及 Metadata。原始 `Task.Prompt` 保持原樣不被覆寫。
2. **Deterministic Explicit Selection**：任務提交時允許明確傳入一組 Memory IDs，由服務端解析並驗證。
3. **嚴格 Scope 驗證與 Fail-Closed 拒絕**：若指定的 Memory ID 不存在、已刪除、跨專案或 workspace 已撤權，在 Runner 啟動前直接拒絕，0 後端子進程調用，0 假成功。
4. **Context 密封與重送一致性 (Sealed Context)**：任務被接受時即完成記憶解析與密封保存；記憶後續修改／刪除或同 Request ID 重送時，始終維持當時密封之 Context。
5. **歷史可追溯性**：查詢 Task 時能明確得知本次任務使用了哪些 Memory IDs 與 canonical workspace scope。
6. **Engine 統一組裝與向前相容**：記憶組裝在 Engine 統一完成，保持 Runner 介面相容；未指定記憶時維持 Phase 3A 行為。
