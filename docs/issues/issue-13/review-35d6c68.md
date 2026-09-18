# 程式碼審查報告 (PR #14 / Issue #13)

## 變更摘要
針對 Phase 3C Three-Face Fixed Pipeline（Oracle → Striker → Critic）於最新 HEAD `35d6c68` 之實作進行第七輪獨立全量嚴格審查。本次審查特別針對 Raybird 提出的第七輪 REQUEST_CHANGES 審查意見（認證衍生遠端配置注入、模型目錄 catalog 任意代碼執行漏洞、Striker 環境變數語意誤傷，以及多配置檔案 deep merge 相容性）進行深度的安全性與架構檢驗：

1. **🔴 MUST FIX 1（已修復）：Auth 衍生遠端配置注入徹底阻斷與隔離空間重構**
   - 建立獨立的 `<isolated>/data/opencode` 目錄，將唯讀子進程的 `XDG_DATA_HOME` 指向該隔離資料目錄，不再直接繼承使用者全域的真實 `~/.local/share`。
   - 實作 `sanitizeAuthData` 與 `hasWellknownField`：深度掃描使用者本機既有之 `auth.json`，保留合法的 API key（`api`）與 OAuth token（`oauth`），徹底剔除所有具有遠端配置拉取能力之 `wellknown` 項目，阻斷透過 `/.well-known/opencode` 下載外部惡意 agent 規則以提權的通道。
   - 新增單元測試 `TestAdapter_SanitizeAuthJSON_DropsWellknown`。

2. **🔴 MUST FIX 2（已修復）：模型目錄（ModelsDev Catalog）代碼執行通道徹底封鎖**
   - 不採用脆弱的特定變數逐一黑名單，而在唯讀沙盒環境構建（`buildReadOnlyExecutionEnv`）中採用全面防禦原則：除了 Asura 明確受控之變數外，徹底過濾並剔除所有外部以 `OPENCODE_` 開頭之環境變數（包含 `OPENCODE_MODELS_PATH`、`OPENCODE_MODELS_URL`、`OPENCODE_AUTH_CONTENT`、`OPENCODE_CONFIG`、`OPENCODE_CONFIG_CONTENT` 等）。
   - 杜絕透過外部 models catalog 引入包含 `npm` 套件之自訂 provider，防止 OpenCode 透過 `Npm.add` 與動態 `import()` 執行任意 JavaScript 代碼。
   - 新增單元測試 `TestAdapter_BuildExecutionEnv_DropsAllHostileOpenCodeEnv`。

3. **🔴 MUST FIX 3（已修復）：解除 Striker / Direct mode 誤傷，恢復向後相容性**
   - `AdapterConfig` 增加 `Isolated bool` 欄位，明確區分隔離沙盒（Oracle/Critic，`Isolated: true`）與標準執行環境（Striker，`Isolated: false`）。
   - 在 `buildExecutionEnv` 中進行模式分流：
     - 若為一般 Striker / Direct mode，調用 `buildStandardExecutionEnv`，完整保留父環境的 `OPENCODE_CONFIG`、`OPENCODE_CONFIG_CONTENT`、`OPENCODE_MODELS_PATH` 等變數，維持 Phase 3B 與 Direct mode 既有語意，符合 TFACE-008 驗收條件。
     - 僅在 `isIsolatedRuntime`（Oracle/Critic/Isolated）時，調用 `buildReadOnlyExecutionEnv` 執行嚴格沙盒隔離。
   - 新增單元測試 `TestAdapter_Striker_PreservesOpenCodeConfigEnv`。

4. **🟡 SHOULD FIX 1（已修復）：全域配置多檔依序 Deep Merge 相容性支援**
   - 實作 `loadGlobalOpenCodeConfig` 與 `deepMergeMaps`，嚴格遵循 OpenCode 1.18.23 載入規範：依序讀取 `config.json` → `opencode.json` → `opencode.jsonc`，後載入之設定遞迴覆寫先前設定。
   - 支援分散於各檔案之設定（例如 `config.json` 定義 provider，`opencode.jsonc` 定義 model），並完整支援 JSONC 註解與尾隨逗號。
   - 新增單元測試 `TestAdapter_LoadGlobalOpenCodeConfig_PrecedenceMerge`。

5. **🟡 SHOULD FIX 2（已修復）：補齊針對不同配置來源的安全測試覆蓋**
   - 單元測試套件全面覆蓋 hostile auth content、hostile models path、hostile config content、wellknown 憑證過濾以及 Striker 環境變數保留。

## 審查版本
- Reviewed BASE SHA：`799625c77ffb38e6e0c2302d407f20e38f22ea5c` (origin/main)
- Reviewed HEAD SHA：`35d6c68e1c6fc896263df21fc577fa39f506385d` (issue-13-three-face-pipeline HEAD)
- 短 SHA：`35d6c68`
- 獨立 reviewer：Independent Code Reviewer (Subagent)
- Review artifact：`docs/issues/issue-13/review-35d6c68.md`
- Reviewed patch-id：`ff545b216bfba4b06d21b6987def954b8e69e49a`
- 審查日期：2026-09-18

## 整體評估
本輪修復將 OpenCode runtime 的所有外部輸入來源（檔案設定、認證檔案、模型目錄、環境變數）完整收斂為 Asura-controlled namespace：
1. **完整封閉的 Readonly 沙盒邊界**：
   - 唯讀子進程擁有獨立且經消毒的 `data/opencode/auth.json`（已剔除任何 remote wellknown 注入點），徹底阻絕由認證層衍生遠端配置繞過權限。
   - 唯讀環境變數阻斷一切外部 `OPENCODE_*` 輸入，杜絕了 models catalog、auth content、config content 等所有語言層與執行層注入手段。
2. **Striker / Direct 語意零破壞**：
   - 通過 `isIsolatedRuntime` 模式分流，Striker 完全恢復對既有 OpenCode 環境變數的正常讀取，Direct mode 保持 100% 向後相容，完美履行 TFACE-008 契約。
3. **配置合併相容性**：
   - `loadGlobalOpenCodeConfig` 實作了符合 OpenCode 規範的 precedence deep merge，兼顧靈活性與相容性。
4. **測試覆蓋齊備**：
   - 4 項新增的安全單元測試直接針對攻擊面進行雙向斷言，無假綠燈，證據充分。

程式碼架構嚴謹、職責單一，Three-Face capability isolation 已形成完整閉環。

## 需求符合度
- 需求描述：Phase 3C Three-Face Fixed Pipeline（Oracle → Striker → Critic）架構實作、安全權限邊界、Fail-Closed 與審計持久化，收斂所有外部輸入來源至受控命名空間，保持 Direct 模式既有語意。
- 是否符合需求：**是（完全符合）**。
- 缺漏或過度實作說明：無。維持最小改動量與 100% 向後相容。

## 問題與風險
- 🔴 **MUST FIX**：無（3 項均已解決）。
- 🟡 **SHOULD FIX**：無（2 項均已解決）。
- 🟢 **NICE TO HAVE**：無。

## 驗收標準與測試證據
- **核准狀態覆核**：`docs/issues/issue-13/README.md` 核准表逐項查核，10 項 Scenario（@TFACE-001 ~ @TFACE-010）狀態均為 `已核准`（核准日期 2026-09-17）。
- **測試驗證全綠**：
  - `go test -v -count=1 ./internal/backend/opencode/...`：全數 PASS（含所有新增安全測試）。
  - `go test -race ./internal/backend/opencode/... ./internal/engine/... ./internal/pipeline/... ./cmd/...`：全數 PASS（零 Data Race）。
  - `go vet ./...`：全數 PASS，零 Warning、零 Error。
  - `go build ./cmd/asura`：編譯成功。
- **假綠燈與同義反覆防範檢查**：
  - 安全測試三階段完整閉環，Striker 與 Oracle 分別進行隔離性與相容性雙向斷言，徹底根絕假綠燈。
- **Gate 豁免查核**：**無**（未申請亦無任何豁免紀錄）。

## 破壞性邊界條件
1. **父進程環境注入惡意 OPENCODE_AUTH_CONTENT 或 OPENCODE_MODELS_PATH**：唯讀沙盒以白名單機制徹底 drop 所有外部 `OPENCODE_*`，子進程無法讀取。判定：**已覆蓋且安全（通過）**。
2. **本機真實 auth.json 含有 wellknown 遠端配置項目**：`sanitizeAuthData` 在複製前將其剔除，唯讀環境無法加載遠端配置。判定：**已覆蓋且安全（通過）**。
3. **使用者於 Direct / Striker 模式使用自訂 OPENCODE_CONFIG**：`buildStandardExecutionEnv` 完整保留該變數，直接調用 Striker 正常運作。判定：**已覆蓋且相容（通過）**。
4. **全域配置分散於 config.json 與 opencode.jsonc**：`loadGlobalOpenCodeConfig` 依序 deep merge，完整繼承 provider 與 model。判定：**已覆蓋且相容（通過）**。

## 流程判定
### **`PASS`**
- 獨立 reviewer 已完成當前 HEAD（`35d6c68`）之全量審查。
- 0 個 MUST FIX、0 個 SHOULD FIX。
- 報告已依規範持久化於 `docs/issues/issue-13/review-35d6c68.md`。
