# 程式碼審查報告 (PR #14 / Issue #13)

- 範圍：PR #14 (Issue #13: Phase 3C Three-Face Pipeline) 安全修復與環境隔離覆核
- Reviewed BASE SHA：`799625c77ffb38e6e0c2302d407f20e38f22ea5c` (origin/main)
- Reviewed HEAD SHA：`1583b0fed5adcd614fbfcf683646f750d8e1e470`
- Reviewed patch-id：`ac717fd92eeb39e6bdab18fe908131ff1592aaa2`（針對本次修復提交）／`13ea0c2234d45b92e5925d2be089aedfb2cd9e5e`（完整 PR BASE..HEAD）
- 獨立 reviewer：Independent Code Reviewer (Subagent)
- Review artifact：`docs/issues/issue-13/review-1583b0f.md`
- 審查日期：2026-09-18
- 審查檔案：
  - `internal/backend/opencode/adapter.go`
  - `internal/backend/opencode/adapter_test.go`

---

## 變更摘要

針對 Phase 3C Three-Face Fixed Pipeline 於最新 HEAD `1583b0f` 進行第九輪獨立嚴格程式碼審查。本輪變更主要落實前輪審查意見提出之 2 項 MUST FIX 安全加固：

1. **MUST FIX 1：XDG_CACHE_HOME 與 XDG_STATE_HOME 完整隔離**
   - `CreateIsolatedReadOnlyConfigDir()` 新增建立 `cache` 與 `state` 目錄（權限 `0700`），並在錯誤清理路徑與 defer 清理函式中確保完整銷毀。
   - `buildReadOnlyExecutionEnv()` 在白名單過濾時徹底剔除父進程環境中的 `XDG_CACHE_HOME` 與 `XDG_STATE_HOME`（連同 `XDG_CONFIG_HOME`、`HOME`、`XDG_DATA_HOME` 及所有 `OPENCODE_*`）。
   - 在唯讀沙盒環境中，分別注入 `XDG_CACHE_HOME=<isolated>/cache` 與 `XDG_STATE_HOME=<isolated>/state`，徹底阻斷攻擊者利用父環境快取目錄預先放置惡意 `models.json`、進而透過可執行自訂 provider (`npm.add` / dynamic import) 逃逸沙盒的攻擊面。

2. **MUST FIX 2：動態替換防禦全面化（hasUnsafeDynamicRef）**
   - 將原 `hasDynamicFileRef` 升級為遞迴檢測函式 `hasUnsafeDynamicRef`，全面涵蓋動態檔案代換語法（`{file:...}`）與環境變數代換語法（`{env:...}`）。
   - 全面攔截點：
     - 全域配置頂層 `model`（`globalData["model"]`）若含有 `{file:...}` 或 `{env:...}`，直接觸發 Fail-Closed 拒絕啟動，清理臨時目錄並返回錯誤。
     - Fallback 模型變數 `OPENCODE_MODEL` 若含有動態代換語法，同樣觸發 Fail-Closed 拒絕啟動。
     - `sanitizeProviderConfig()` 對所有 provider（無論是否被選用）進行遞迴檢查，若包含 `{file:...}` 或 `{env:...}` 均遵循 Fail-Closed 原則直接返回錯誤拒絕啟動。

---

## 問題與風險

- 🔴 **MUST FIX**：無（2 項 MUST FIX 均已徹底修復並通過驗證）。
- 🟡 **SHOULD FIX**：無。
- 🟢 **NICE TO HAVE**：無。

---

## 已查核維度

### 1. 驗收與 MUST FIX 查核

| 項目 | 審查重點與實作位置 | 驗證測試與證據 | 結論 |
|---|---|---|---|
| **MUST FIX 1** | `adapter.go#L84-L93`（建立 isolated cache & state 空間）<br>`adapter.go#L653-L655`（過濾父進程 `XDG_CACHE_HOME` / `XDG_STATE_HOME`）<br>`adapter.go#L664-L678`（注入沙盒專屬 cache & state 目錄） | `adapter_test.go#L137-L150`（目錄存在性檢查）<br>`adapter_test.go#L201-L202, L273-L280`（命令執行時環境變數覆寫檢驗）<br>`adapter_test.go#L725-L777`（`TestAdapter_BuildExecutionEnv_IsolatesAllXDGAndDropsHostileCache` 驗證父環境惡意快取與狀態目錄被剔除） | **PASS** |
| **MUST FIX 2** | `adapter.go#L828-L849`（`hasUnsafeDynamicRef` 遞迴檢查 `{file:` 與 `{env:`）<br>`adapter.go#L130-L133`（頂層 model 防禦）<br>`adapter.go#L150-L157`（`OPENCODE_MODEL` 防禦）<br>`adapter.go#L868-L871`（`sanitizeProviderConfig` 全面攔截） | `adapter_test.go#L659-L723`（`TestAdapter_DisallowUnsafeDynamicRef_ModelAndProvider`：驗證 provider options 之 `{file:...}`、`{env:...}` 及頂層 model 之 `{file:...}`、`{env:...}` 均 Fail-Closed） | **PASS** |

### 2. 相關失敗面與邊界條件檢查

1. **父進程傳入惡意快取目錄指向預先植入之 models.json**：
   - 輸入：父環境設有 `XDG_CACHE_HOME=/tmp/hostile-cache`，內含自訂 executable npm provider 的 `models.json`。
   - 預期：`buildReadOnlyExecutionEnv` 徹底剝除該環境變數，並以乾淨空的 `<isolated>/cache` 覆蓋注入；OpenCode 無法讀取主機快取中的惡意模型定義。
   - 覆蓋與判定：`TestAdapter_BuildExecutionEnv_IsolatesAllXDGAndDropsHostileCache` 雙向斷言通過（符合預期）。
2. **頂層 model 混入動態環境變數代換 `{env:AWS_SECRET_ACCESS_KEY}`**：
   - 輸入：全域 `opencode.json` 定義 `{"model": "openai/{env:AWS_SECRET_ACCESS_KEY}"}`。
   - 預期：`CreateIsolatedReadOnlyConfigDir()` 於解析時直接 Fail-Closed 中止，清理已建立之目錄並報錯，不啟動沙盒進程。
   - 覆蓋與判定：`TestAdapter_DisallowUnsafeDynamicRef_ModelAndProvider` 案例 4 命中，無機密洩漏風險。
3. **未被當前模型選用之 Provider 定義動態代換語法**：
   - 輸入：模型選用 `anthropic/claude-3-5-sonnet`，但背景存在另一 Provider 定義了 `apiKey: "{file:/etc/shadow}"` 或 `apiKey: "{env:TOKEN}"`。
   - 預期：`sanitizeProviderConfig` 全局 Fail-Closed 報錯，不啟動沙盒進程。
   - 覆蓋與判定：`TestAdapter_DisallowUnsafeDynamicRef_ModelAndProvider` 案例 1 & 2 命中，杜絕配置盲區。
4. **臨時目錄清理完整性**：
   - 輸入：在 `CreateIsolatedReadOnlyConfigDir` 過程中觸發任何驗證失敗（例如模型不合法或憑證損毀）。
   - 預期：立即呼叫 `os.RemoveAll(dir)` 清理已建立的所有子目錄（`opencode`、`home`、`data`、`cache`、`state`），無孤兒臨時目錄殘留。
   - 覆蓋與判定：所有錯誤分支均有顯式 `os.RemoveAll(dir)` 處理。

### 3. 架構、安全與程式碼品質

- **架構邊界**：OpenCode Adapter 維持清晰職責邊界，沙盒唯讀環境（Oracle / Critic）與標準執行環境（Striker / Direct）邏輯互不干擾，維持 Phase 3B 向後相容性。
- **安全性原則**：
  - 白名單環境變數過濾：剔除所有外部 `OPENCODE_*`、`HOME` 及五大 XDG 基準目錄（`CONFIG`, `DATA`, `CACHE`, `STATE`）。
  - Fail-Closed 原則：對所有自訂可執行 provider (`npm`)、動態檔案參照（`{file:...}`）與動態環境變數參照（`{env:...}`）嚴格拒絕。
  - 憑證消毒：徹底剔除 `wellknown` 外部配置注入風險。
- **程式碼風格**：遵循 Minimal Surgery 原則，改動精準且符合專案風格。

### 4. 測試證據與真偽性查核

於 2026-09-18 在當前程式庫 HEAD (`1583b0f`) 執行全套驗證：
- `go vet ./...`：全數 PASS（零 Warning、零 Error）。
- `go build ./cmd/asura`：編譯成功。
- `go test -race -count=1 ./internal/backend/opencode/...`：PASS (ok asura/internal/backend/opencode 11.764s)。
- `go test -race ./internal/...`：全數 PASS（包含 auth, opencode, cron, daemon, engine, pipeline, storage, web）。
- `go test -race -count=1 ./tests/integration/...`：PASS (ok asura/tests/integration 71.502s，零 Data Race)。
- **假綠燈防範**：所有新增安全檢查測試均包含明確斷言（正向與反向驗證），回退任何一項修復均會立即導致測試紅燈。

### 5. 豁免、待確認與限制

- **Gate 豁免**：無。
- **待確認事項**：無未解決之阻塞事項。

---

## 流程判定

### **`PASS`**

- 獨立 reviewer 已完成當前 HEAD（`1583b0fed5adcd614fbfcf683646f750d8e1e470`）全量審查。
- 本輪提出的 2 項 MUST FIX（XDG 快取與狀態隔離、動態代換全面化防禦）均已徹底修復，驗證完整且無安全漏洞。
- 測試覆蓋齊備，Race Detector 與全量測試均為綠燈，無假綠燈現象。
- 審查報告已持久化儲存於 `docs/issues/issue-13/review-1583b0f.md`。
