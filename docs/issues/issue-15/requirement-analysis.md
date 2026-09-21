# Issue 15 - 需求分析 (Requirement Analysis)

## 需求描述

Asura 於 Phase 3C（Issue #13 / PR #14）實現了 Three-Face Pipeline（Oracle → Striker → Critic）固定模式、Sealed `ExecutionContext`、Pipeline Trace 與審計機制。然而，目前系統內部將 AI 執行能力實質綁定在 OpenCode 上。

本期（Phase 3D）的核心需求為：
1. 建立真正可替換的 `internal/backend` 共同執行合約。
2. 保留既有 OpenCode 行為與 Phase 3C 已建立的安全邊界。
3. 接入第二個真實後端：Codex CLI（`codex-cli 0.155.0`）。
4. 允許呼叫端明確指定後端（`--backend`），並保證相同 Request ID 的重送（redelivery）與佇列喚醒（queue wake）嚴格維持相同後端執行身份。
5. 建立結構化錯誤分類（Failure Classification）與事件正規化（Event Normalization），並嚴格落實 Fail-Closed 與 No Automatic Failover 原則。

## 現況分析

### 1. 執行器介面過度簡化且與 OpenCode 緊密耦合
在現有程式碼中：
- `internal/engine/engine.go` 定義：
  ```go
  type RunnerFunc func(ctx context.Context, workspace string, prompt string) (string, error)
  ```
  並且在 `import` 中直接引用了 `"asura/internal/backend/opencode"`。
- `internal/pipeline/stage_striker.go`、`stage_oracle.go`、`stage_critic.go` 也各自定義了相容於 `RunnerFunc` 的函式型別，只吃 `(ctx, workspace, prompt)` 並回傳 `(string, error)`。
- 這個極簡介面無法承載：
  - 後端身份（BackendID）與版本（BackendVersion）。
  - 後端角色宣告（RoleOracle / RoleStriker / RoleCritic）。
  - 後端能力元資料（BackendCapabilities，如 ReadOnly Sandbox、Structured Events、Cancellation）。
  - 結構化事件串流（Events）與 Partial Output。
  - 細緻的錯誤分類（認證失敗、速率限制、執行退出等）。

### 2. Request 與 Task 缺少 BackendID 身份
- `engine.SubmitRequest` 目前只包含 `ID`, `Workspace`, `Prompt`, `Mode`, `MemoryIDs`, `ResolvedContext`。
- Request Fingerprint 只雜湊了 Prompt, Workspace, MemoryIDs, Mode，若呼叫端改變指定後端，相同的 Request ID 會被判定為同一請求，可能造成錯誤的 redelivery 命中。
- `pipeline.StageTrace` 僅記錄 `Name`, `Status`, `DurationMs`, `Error`, `Output`，未記錄該階段究竟是由哪個後端以及哪個版本執行。

### 3. 安全前置檢查（Capability Preflight）缺位
- Phase 3C 針對 OpenCode 設計了 `asura-readonly` 專屬配置與環境隔離，以確保 Oracle 與 Critic 不會產生寫入或修改副作用。
- 但此安全保證完全是 OpenCode 專屬實作。如果未來直接替換其他後端，而未檢查該後端是否具備同等的唯讀隔離能力，Oracle/Critic 可能會在未受控環境下執行副作用操作。

### 4. 容錯語意邊界尚未嚴格宣告
- 當指定後端執行失敗或產生部分輸出時，必須絕對禁止自動切換至另一後端（Failover）重跑。因為 Striker 可能已經在 Workspace 產生了部分不可逆修改，換後端重跑將產生重複副作用。

## 問題點總結

1. **抽象層缺位**：沒有共同 Backend Contract，Engine 與 Pipeline 直接相依 OpenCode。
2. **身份未密封**：BackendID 未進入 Fingerprint、Task 與 Trace，無法保證冪等性與審計追溯。
3. **能力未預檢**：無法在執行前驗證後端是否支援特定角色（如 Oracle 唯讀邊界），存在安全性盲區。
4. **錯誤與事件未正規化**：上層無法以統一方式處理不同後端的進程輸出、事件流與失敗原因。

## 目標

1. **定義共同 Backend 合約**：建立 `internal/backend` 套件，包含 `Backend` 介面、`BackendRegistry`、`BackendResult`、`BackendEvent`、`FailureClassification` 及 `BackendCapabilities`。
2. **遷移 OpenCode 適配器**：將現有 OpenCode 封裝為符合共同契約的實作，Phase 3C 安全測試保持全數 PASS。
3. **實作 Codex CLI 適配器**：以本機 `codex-cli 0.155.0` 為基準，透過 `codex exec` 提供 non-interactive, jsonl, sandbox 隔離與進程回收能力。
4. **解耦 Pipeline 與 Engine**：Pipeline 依賴共同 Backend 介面，依 Stage 傳遞角色；Engine 負責 BackendID 的解析、Fingerprint 納入與持久化。
5. **落實 Fail-Closed 與防容錯**：能力不足或未註冊後端立即在進程啟動前拒絕；執行失敗保存 Partial Output 並終止，0 次自動切換重跑。
