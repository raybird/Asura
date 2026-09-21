# Issue 15 - 技術分析 (Technical Analysis)

## 技術可行性分析

### 1. Codex CLI 0.155.0 探測（Contract Probe）
針對本機安裝之 `codex-cli 0.155.0` 進行探測，確認具備以下關鍵能力：

1. **非互動執行模式**：
   - 指令：`codex exec [OPTIONS] [PROMPT]`
   - 支援工作目錄切換：`-C, --cd <DIR>`，確保在 Asura Canonical Workspace 內執行。
2. **沙箱模式（Sandbox Mode）**：
   - 參數：`-s, --sandbox <SANDBOX_MODE>`，支援 `read-only`、`workspace-write`、`danger-full-access`。
   - 在 Oracle 與 Critic 階段，可使用 `-s read-only` 與 `--ask-for-approval never`，達成分析階段禁止寫入之安全邊界。
   - 在 Striker 階段，使用 `-s workspace-write` 與 `--approve-for-me`（或 `--ask-for-approval never`），允許對 workspace 進行副作用修改。
3. **結構化事件流（Event Streaming）**：
   - 參數：`--json`，以 JSONL 格式輸出事件到 stdout，可解析事件類型（text, tool, item, status 等）。
   - 支援 `-o, --output-last-message <FILE>` 或從 JSONL 解析最終訊息。
4. **進程生命週期與 Cancellation**：
   - 與 OpenCode 相同，以 `exec.CommandContext` 啟動，使用獨立 Process Group（`syscall.Setpgid`）。
   - 當 Context Cancel 時，向進程組發送 `SIGTERM` / `SIGKILL`，可確實回收底層 process tree，避免孤兒進程。
5. **會話與磁碟隔離**：
   - 參數：`--ephemeral`（不持久化 session 到磁碟）、`--skip-git-repo-check`（允許在非 git repo 目錄下執行）。

## 方案選型與取捨

### 取捨 1：RunnerFunc 薄介面 vs. 結構化 Backend 契約

- **方案 A (現狀微調)**：保留 `RunnerFunc`，僅將 Backend 名稱拼接入 Prompt 或透過 Context Value 傳遞。
- **方案 B (本期採納：結構化 Backend Contract)**：建立標準化 `Backend` 介面與 `BackendRegistry`。

```diff
- // 舊架構：無狀態、無型別、無身份的薄函式
- type RunnerFunc func(ctx context.Context, workspace string, prompt string) (string, error)

+ // 新架構：具備身份、角色、能力與結構化結果的後端契約
+ type Backend interface {
+     ID() string
+     Version(ctx context.Context) (string, error)
+     Capabilities() Capabilities
+     Run(ctx context.Context, req *ExecutionRequest) (*BackendResult, error)
+ }
```

**取捨理由**：
方案 A 看似改動小，但無法提供能力預檢（Capability Preflight）、無法區分角色安全等級，且無法回傳正規化的 Event 與 Failure Classification。方案 B 能徹底將 Pipeline 與具體 CLI 解耦，並為未來 Phase 3D-2（Antigravity / agy）鋪平道路。

---

### 取捨 2：容錯處理（Automatic Failover vs. Fail-Closed）

- **方案 A (Automatic Failover)**：當指定後端執行失敗時，自動退回使用 OpenCode 重跑。
- **方案 B (本期採納：Strict Fail-Closed, No Automatic Failover)**：無論任何原因（後端不存在、認證失敗、速率限制、執行失敗），一律 Fail-Closed 並保存 Partial Output，嚴格禁止自動換後端重跑。

```diff
  // 處理 Striker 執行失敗
  if err != nil {
-     // 方案 A：危險！可能造成雙重副作用
-     log.Warn("Codex failed, falling back to OpenCode")
-     return fallbackToOpenCode(ctx, req)
+     // 方案 B：安全！副作用隔離與責任單一
+     trace.Status = StageFailed
+     return nil, trace, NewBackendExecutionError(backendID, err, partialOutput)
  }
```

**取捨理由**：
在 Striker 執行過程中，後端可能已經在 workspace 內建立了檔案或修改了代碼。若在網路逾時或執行退出後自動換 OpenCode 重試，可能引發重複執行或狀態衝突。因此「No Automatic Failover」是本期最重要的副作用安全邊界。

---

### 取捨 3：單一任務多後端混合 (Mixed Per-Stage) vs. 任務級後端鎖定

- **方案 A (Per-Stage Mixed Backend)**：允許 Oracle 用 OpenCode、Striker 用 Codex、Critic 用 OpenCode。
- **方案 B (本期採納：Task-Level Backend Binding)**：整趟任務（Direct 或 Three-Face）由呼叫端明確指定單一後端，該後端貫穿所有 stage。

**取捨理由**：
任務級後端鎖定概念清晰，Request Fingerprint、Redelivery 與 Cancellation 語意單純。Per-stage backend binding 留待後續 Phase 3D-3 再獨立立項。

## 架構影響評估

1. **模組依賴關係解耦**：
   - 變更前：`cmd/asura` → `daemon` → `engine` → `opencode`，且 `pipeline` 依賴 `engine`。
   - 變更後：`engine` 與 `pipeline` 僅依賴 `internal/backend` 抽象介面。具體實作 `backend/opencode` 與 `backend/codex` 於主程式進入點註冊進 `BackendRegistry`。
2. **持久化與 Fingerprint**：
   - `SubmitRequest` 增加 `Backend string`（預設 `"opencode"`）。
   - Request Hash 必須加入 `Backend` 欄位，確保更換 backend 時被視為不同 payload 而拒絕。
   - `PipelineTrace` 的 `StageTrace` 增加 `BackendID` 與 `BackendVersion`。
3. **向下相容性**：
   - 既有呼叫未帶 `--backend` 時，自動指派 `"opencode"`。
   - 既有測試案例使用既有 mock runner 或直接調用時，透過預設 registry 與 opencode 保持 100% 行為相容。

## 實作細節與待釐清項目

1. **Codex CLI 的認證與環境傳遞**：
   - Codex 依賴 `$HOME/.codex` 或環境變數（如 `OPENAI_API_KEY`、`CODEX_HOME` 等）。
   - 適配器應允許繼承當前環境的認證設定，並在缺少 binary 或憑證無效時，精確分類為 `FailureClassificationAuth` 或 `FailureClassificationUnavailable`。
2. **能力清單（Capabilities）**：
   - `CapReadOnlySandbox`: OpenCode（`asura-readonly` 設定檔）與 Codex（`-s read-only`）均支援。
   - `CapWorkspaceWrite`: 兩者均支援。
   - `CapStructuredEvents`: 兩者均支援（OpenCode ndjson, Codex jsonl）。
   - `CapProcessCancellation`: 兩者均支援 Process Group 終止。
