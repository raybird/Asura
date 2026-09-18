# Issue 13 - 需求分析：Phase 3C Three-Face Fixed Pipeline

建立日期：2026-09-17

## 1. 現況調查與既有架構分析

在 Phase 3B（Issue #11）完成後，Asura 系統已具備統一的任務執行與專案記憶整合架構：

1. **統一任務中樞與生命週期**：
   - 任務由 Daemon 接收，透過 `SQLiteStore` 進行持久化，狀態經歷 `pending`/`queued` → `running` → `completed`/`failed`/`cancelled`。
   - 支援 Workspace 規範化與授權檢核（`AuthorizeCanonicalWorkspace`）。
   - 支援同 Request ID 之冪等 Redelivery 防禦，以 Fingerprint 確保請求內容未被篡改。

2. **Sealed ExecutionContext**：
   - 在任務接受時解析關聯的專案記憶，密封為不可變的 `engine.ExecutionContext`（包含 `OriginalPrompt`, `Workspace`, `ProjectMemories`, `MemoryRefs`, `CreatedAt`）。
   - `ExecutionContext` 被序列化為 JSON 儲存於 `tasks.resolved_context` 欄位。
   - 任務執行時由 `engine.ComposePrompt` 統一組裝記憶與 Prompt。

3. **目前執行瓶頸與挑戰**：
   - 目前 `engine.Engine` 僅支援單一 `RunnerFunc` 直通執行（Direct mode）。
   - 面對複雜或高風險的程式修改任務，單次模型呼叫缺乏前期結構化規劃（Planning）以及後期的獨立審查（Review），難以確保產出品質。
   - 若直接將多角色邏輯硬寫入既有 OpenCode Adapter，會破壞單一職責原則，使未來的 Multi-Backend（Phase 3D）與 Dynamic Routing（Phase 3C-2）無法演進。

## 2. 需求邊界與核心原則

### 原則 1：固定 Pipeline（Fixed Pipeline），非動態路由
第一版僅提供兩種明確的執行模式（`ExecutionMode`）：
- `direct`：直通既有 Striker / Runner，完全向後相容。
- `three-face`：固定依序執行 Oracle → Striker → Critic。
不做 AI 自動複雜度判斷、不做動態模型選擇、不做動態階段跳過，確保執行行為 100% 可重現與可測試。

### 原則 2：三角色共用同一 Sealed ExecutionContext
Three-Face Pipeline 的所有 Stage 均基於同一份 Task execution identity：
- 不重新查詢或解析最新專案記憶。
- 不重新計算 Workspace scope。
- 不重新生成 task identity。
確保即使底層資料庫或專案記憶在執行過程中被更新或刪除，三個角色看到的上下文完全一致。

### 原則 3：Oracle 輸出必須為結構化 Plan
禁止以非結構化自由文字作為角色間的隱式約定。Oracle 必須產出結構化的 `OraclePlan`：
- `Goal`：任務核心目標。
- `Constraints`：約束條件清單。
- `Steps`：具體執行步驟規劃。
- `ExpectedOutcome`：預期產出與驗證指標。
- `Metadata`：規劃耗時與模型資訊。
該 Plan 需可被 Striker 消費、可持久化、可查詢且可測試。

### 原則 4：Striker 保留既有 Runner 契約與副作用獨佔性
- 只有 Striker 具備操作 Workspace 的主要執行副作用能力。
- Oracle 與 Critic 均為唯讀／純分析（Analysis-only）階段，不自行操作 Workspace。
- 將既有 `RunnerFunc` 包裝為 Striker Stage，將 `ExecutionContext + OraclePlan` 組裝為 Striker 輸入。

### 原則 5：Critic 第一版只做 Review，不自動 Repair Loop
Critic 負責對 Striker 的執行結果進行審查，產出結構化 `CriticReview`：
- `Verdict`：審查判定（`pass`、`needs_attention`、`failed`）。
- `Findings`：審查發現或建議項目清單。
- `Summary`：審查摘要。
- `Metadata`：審查耗時與模型資訊。
第一版不做自動修復循環（Repair Loop），避免非預期的重複副作用與失控的模型呼叫成本。

### 原則 6：嚴格 Fail-Closed 失敗語意
- **Oracle 失敗**：Striker 不啟動、Critic 不啟動，Task 標記為 Failed。嚴禁 silent fallback 為 Direct 模式。
- **Striker 失敗**：Critic 不啟動，不自動重試，保存 Oracle Plan 與 Striker 錯誤，Task 標記為 Failed。
- **Critic 失敗**：保存既有 Striker 產出，但 Task 不得標記為完整成功，標記為 Failed 或 Review Failed。

### 原則 7：完整 Pipeline Trace 審計與成本量測
任務查詢必須能回答：
- 執行模式（Direct 或 Three-Face）？
- 各 Stage（Oracle, Striker, Critic）的狀態、耗時與錯誤？
- Oracle Plan 與 Critic Review 的具體內容？
- 總模型呼叫次數（Direct 應為 1，Three-Face 應為 3）與管線總耗時。

## 3. 明確不包含（Out of Scope）

為維持 YAGNI 原則與最小修改邊界，本期明確排除：
1. 動態複雜度路由（Complexity Router）或 AI 自動判斷執行模式。
2. Multi-Backend 支援與模型故障自動轉移（Failover）。
3. Critic 自動 Repair / Recursive Loop。
4. 多層或多角色 Critic 投票機制。
5. Web 專屬 Three-Face 圖形介面（維持 API/CLI 與資料契約相容）。
