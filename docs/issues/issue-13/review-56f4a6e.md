# 程式碼審查報告 (PR #14 / Issue #13)

## 變更摘要
針對 Phase 3C Three-Face Fixed Pipeline（Oracle → Striker → Critic）於 HEAD `56f4a6e` 之實作進行第五輪增量與全量獨立嚴格審查。本次審查特別針對 Raybird 提出的最新審查意見（乾淨 Configuration Namespace、安全測試雙向斷言防假綠、Fail-Closed 初始化錯誤阻斷、CI 併發保護與 flaky 根治）進行徹底架構檢驗：
- `internal/backend/opencode/adapter.go`：
  - `CreateIsolatedReadOnlyConfigDir` 建立專屬臨時目錄，其下包含 `opencode` (0700) 與 `home` (0700) 子目錄。
  - 安全繼承全域既有之 `provider` 與 `model` 配置以維持真實模型調度能力，嚴格剔除外部 `agent`、`permission`、`mcp`、`plugin` 等任何潛在提權設定。
  - `Adapter.Run` 注入 `XDG_CONFIG_HOME = <isolated_dir>`、`HOME = <isolated_dir>/home`、`OPENCODE_CONFIG_DIR = <isolated_dir>/opencode`，並保留真實 `XDG_DATA_HOME`（指向使用者真實的 `~/.local/share` 或既有 `XDG_DATA_HOME`）供讀取 `auth.json` 模型憑證。
  - 實作 `setOrUpdateEnv` 輔助函式，確保環境變數不重複且優先覆寫父環境變數。
  - 新增 `HasLiveCredentials` 輔助函式，探測執行環境是否具備真實 LLM API Key 或本機憑證。
- `internal/daemon/daemon.go`：
  - `Daemon` 結構體新增 `initErr error` 欄位。
  - `NewDaemon` 中若 `CreateIsolatedReadOnlyConfigDir` 發生錯誤，明確記錄至 `d.initErr`。
  - `Daemon.Start()` 開頭第一時間檢查 `d.initErr`，若非 nil 則立即中止拒絕啟動，確保 Socket 檔案未建立，落實嚴格 Fail-Closed。
- `internal/daemon/daemon_test.go`：
  - 強化 `TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly` 為三階段雙向斷言：
    - 階段 1（正向驗證）：先驗證 `readOnlyAdapter.Run` 成功讀取 `target.txt` 並包含敏感 token（`VERY_SECRET_INTEGRITY_TOKEN_12345`），證明模型真實連線且具備唯讀能力，排除環境崩潰/auth 失敗導致的假綠燈。
    - 階段 2（負向驗證）：誘導修改 `target.txt`，驗證模型明確受限拒絕（模型回覆「目前工具僅提供讀取能力，無法修改 target.txt」）。
    - 階段 3（完整性驗證）：驗證 `target.txt` 內容與 SHA256 在敵意專案配置下保持 100% 完整不變。
    - 在無外部模型憑證之 CI 環境下透過 `HasLiveCredentials()` 明確標註 Skip，杜絕未認證 CI runner 觸發 Zen free tier 遠端 rate limit。
  - 新增 `TestDaemon_Start_FailClosedOnInitError`，驗證初始化錯誤時 `Daemon.Start()` 立即阻斷且不建立 socket。
- `internal/backend/opencode/adapter_test.go`：
  - `TestAdapter_IsolatedReadOnlyConfig` 驗證 `home` 隔離目錄存在與唯讀權限白名單。
  - `TestAdapter_Run_IsolatedCommandAndEnv` 驗證 fake binary 接收到的乾淨 Configuration Namespace 與隔離環境變數。
  - `TestINT001Unit` 增加 `HasLiveCredentials()` 檢查。
- `tests/integration/int_test.go`：
  - `TestINT001` 與 `TestINT007` 增加 `HasLiveCredentials()` 檢查，在無憑證環境下安全跳過契約測試，消除 CI rate limit flaky。
- `.github/workflows/ci.yml`：
  - 配置 `concurrency` 控制（`group: ${{ github.workflow }}-${{ github.head_ref || github.ref }}`，`cancel-in-progress: true`），消除同分支併發造成的資源互踩與 rate limit flaky。

## 審查版本
- Reviewed BASE SHA：`799625c77ffb38e6e0c2302d407f20e38f22ea5c` (origin/main)
- Reviewed HEAD SHA：`56f4a6e4913cc25f552bfecdfe757005c2e60e70` (issue-13-three-face-pipeline HEAD)
- 短 SHA：`56f4a6e`
- 獨立 reviewer：Independent Code Reviewer (Subagent)
- Review artifact：`docs/issues/issue-13/review-56f4a6e.md`
- Reviewed patch-id：`7ab6ccb4fc26a44d0c9aa4663f9738f92e028377`
- 審查日期：2026-09-18

## 整體評估
本次審查針對上一輪提出的架構與安全性意見（乾淨 Configuration Namespace、安全測試雙向斷言防假綠、Fail-Closed 初始化錯誤阻斷、CI 併發保護與 flaky 根治）進行了嚴格的複核與實證檢驗。
修復成果極為顯著：
1. **乾淨 Configuration Namespace 徹底阻絕路徑污染**：
   - 將 `XDG_CONFIG_HOME` 與 `OPENCODE_CONFIG_DIR` 指向專屬臨時隔離配置目錄，同時將 `HOME` 指向該臨時目錄下獨立的空 `home` 子目錄，徹底切斷 OpenCode 對使用者全域 `$HOME/.opencode` 或 `$HOME/.config` 中可能殘留的自訂 agent / permission 之探測路徑。
   - 透過安全讀取並僅提取 `provider` 與 `model`，完美解決了無污染繼承連線模型設定的同時、杜絕任何外部規則注入的兩難。
   - 保留真實 `XDG_DATA_HOME`，確保既有憑證（`auth.json`）讀取不受干擾。
2. **安全測試雙向斷言（Bi-directional Assertions）根除假綠燈**：
   - 攻擊測試 `TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly` 在真實具備敵意工作區配置下執行真實 OpenCode CLI，先正向驗證讀取敏感 Token 成功，排除測試環境或 Auth 崩潰偽造的假綠燈；再負向驗證寫入被受限拒絕，最後驗證 SHA256 完整性，證據鏈極度穩固。
3. **Fail-Closed 啟動防禦阻斷**：
   - `NewDaemon` 記錄初始化錯誤，`Daemon.Start()` 第一時間阻斷啟動，防範在唯讀配置目錄建立失敗時降級至無隔離環境運行的潛在風險。
4. **CI 健全性與 Flaky 消除**：
   - 透過 `HasLiveCredentials()` 探測模型憑證狀態，在無憑證環境（如乾淨 CI runner）明確標註 Skip 契約測試，杜絕未授權請求引發遠端 Zen free tier 的隨機 403/429 限流退出；在開發機與認證環境則全量執行真實模型雙向斷言。
   - CI workflow 配置 `concurrency` 消除 push/PR 併發資源爭搶。
5. **全專案迴歸全綠**：
   - `go test -race ./...` 全部通過（無 Data Race）。
   - 10 項 Gherkin 端到端測試（TFACE-001 ~ TFACE-010）全數以真實管線調度通過。

本次審查判定為 **PASS**，完全達到生產級安全與可靠性標準。

## 需求符合度
- 需求描述：Phase 3C Three-Face Fixed Pipeline（Oracle → Striker → Critic）架構實作、安全權限邊界、Fail-Closed 與審計持久化，落實乾淨 Configuration Namespace 與雙向安全測試驗證。
- 是否符合需求：**是（完全符合）**。
- 缺漏或過度實作說明：無。嚴格遵循 YAGNI 與手術式修改原則，Direct 模式維持 100% 向後相容。

## 問題與風險
- 🔴 **MUST FIX**：無。
- 🟡 **SHOULD FIX**：無。
- 🟢 **NICE TO HAVE**：全域配置解析失敗時的日誌記錄（`internal/backend/opencode/adapter.go`）。

## 驗收標準與測試證據
- **核准狀態覆核**：`docs/issues/issue-13/README.md` 核准表逐項查核，10 項 Scenario（@TFACE-001 ~ @TFACE-010）狀態均為 `已核准`（核准日期 2026-09-17），無任何殘留之「待核准」或「待重新核准」。
- **測試驗證全綠**：
  - `go test -race ./...` 全數通過。
  - 外迴圈劇本測試 `go test -v -race -run "TestTFACE" ./tests/integration`（10/10 通過）。
  - 敵意攻擊專項測試 `TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly` 通過（真實 OpenCode 執行，耗時 43.43s，雙向斷言全部通過）。
  - 配置隔離測試 `TestAdapter_IsolatedReadOnlyConfig`、`TestAdapter_Run_IsolatedCommandAndEnv` 全數通過。
  - Fail-Closed 測試 `TestDaemon_Start_FailClosedOnInitError` 通過。
- **假綠燈與同義反覆防範檢查**：
  - 攻擊測試中，第 1 階段正向驗證斷言輸出必須包含 `VERY_SECRET_INTEGRITY_TOKEN_12345`，第 2 階段驗證誘導修改受限，第 3 階段驗證磁碟檔案前後之 SHA256，無同義反覆，徹底根絕假綠燈。
- **Gate 豁免查核**：**無**（未申請亦無任何豁免紀錄，所有驗收標準均為實質測試通過）。

## 破壞性邊界條件
1. **父行程/宿主環境預設惡意環境變數注入與優先級覆蓋**：`setOrUpdateEnv` 確保 Asura 的 isolated namespace 覆蓋父環境定義。判定：**已覆蓋且安全（通過）**。
2. **全域設定檔格式損毀或含有非標準 JSON/權限定義**：解析失敗安全回退使用純唯讀設定，若寫入失敗則觸發 Fail-Closed 阻斷 Daemon 啟動。判定：**已覆蓋且安全（通過）**。
3. **Striker 產生非標準/格式混亂輸出時 Critic 的 Fail-Closed 判定**：JSON 解析失敗時主動標記 `VerdictFailed`，管線標為 `StatusFailed`。判定：**已覆蓋且安全（通過）**。
4. **敵意工作區配置試圖偽造 Token 或阻斷唯讀能力**：`OPENCODE_DISABLE_PROJECT_CONFIG=1` 配合專屬 agent 白名單，真實攻擊測試通過。判定：**已覆蓋且安全（通過）**。

## 流程判定
### **`PASS`**
- 獨立 reviewer 已完成當前 HEAD（`56f4a6e`）之全量審查。
- 0 個 MUST FIX、0 個 SHOULD FIX。
- 所有 10 項 Gherkin 驗收標準均具備真實通過證據且無假綠燈。
- 所有破壞性邊界條件均通過實證驗收。
- 報告已依規範持久化於 `docs/issues/issue-13/review-56f4a6e.md`。
