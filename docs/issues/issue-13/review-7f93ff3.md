# 程式碼審查報告 (PR #14 / Issue #13)

## 變更摘要
針對 Phase 3C Three-Face Fixed Pipeline（Oracle → Striker → Critic）於最新 HEAD `7f93ff3` 之實作進行第六輪獨立嚴格審查。本次審查專注針對 Raybird 提出的最新 REQUEST_CHANGES 審查意見（環境變數配置注入漏洞、全域 provider npm 任意代碼執行漏洞、hostile edit 測試防假綠強化、CI concurrency group 修正，以及全域配置相容性擴充）進行全方位的程式碼安全與架構檢驗：

1. **🔴 MUST FIX 1（已修復）：環境變數配置注入徹底阻絕**
   - 在 `internal/backend/opencode/adapter.go` 實作 `removeEnv` 與 `buildExecutionEnv`。
   - 強制過濾並剔除外部父環境中的 `OPENCODE_CONFIG` 與 `OPENCODE_CONFIG_CONTENT`（不區分大小寫匹配），徹底阻斷透過環境變數注入 `agent.asura-readonly.permission.edit=allow` 之提權路徑。
   - 新增單元測試 `TestAdapter_BuildExecutionEnv_DropsConfigInjection`，以真實被污染的父環境變數實測驗證，確認子進程環境絕對不含任何外部設定注入標記。

2. **🔴 MUST FIX 2（已修復）：全域 Provider npm 任意代碼執行防禦**
   - 在 `internal/backend/opencode/adapter.go` 實作 `hasNPMField`、`isModelUsingProvider` 與 `sanitizeProviderConfig`。
   - 深度遞迴掃描 provider 與 models 設定，嚴禁包含 `"npm"` 套件之 custom executable provider 進入唯讀隔離環境。
   - 若使用者選取之模型依賴此 custom npm provider，遵循 Fail-Closed 原則，拒絕啟動並明確報錯；若未被當前模型選用，則自動過濾排除，僅保留純資料設定（如 `baseURL`, `options` 等）。
   - 新增單元測試 `TestAdapter_SanitizeProviderConfig_DisallowNPM`，覆蓋率 100%。

3. **🔴 MUST FIX 3（已修復）：Hostile Edit 測試三階段嚴格斷言，徹底杜絕假綠燈**
   - 在 `internal/daemon/daemon_test.go` 中，針對誘導修改測試加上顯式斷言：
     - `if editErr != nil { t.Fatalf("edit attempt must complete normally under readonly runtime: %v", editErr) }`
     - `if strings.TrimSpace(editOut) == "" { t.Fatal("expected readonly refusal or non-empty output from edit attempt") }`
   - 完整構建三重不可穿透之證據鏈：
     1. `readOnlyAdapter.Run` 成功執行且輸出包含機密 token（排除憑證或進程崩潰）。
     2. 誘導修改請求正常執行完成且回傳非空結果（排除 429、超時或網路斷線導致的無效執行）。
     3. 目標檔案前後內容與 SHA256 保持 100% 嚴格一致（證明修改行為被完全阻擋）。

4. **🟡 SHOULD FIX 1（已修復）：CI Concurrency Group 消除 push 與 PR 重複構建**
   - 在 `.github/workflows/ci.yml` 將 concurrency group 由 `${{ github.workflow }}-${{ github.head_ref || github.ref }}` 修正為 `${{ github.workflow }}-${{ github.head_ref || github.ref_name }}`。
   - 使 push 與 pull_request 事件在相同分支上解析出完全一致的 group key，落實取消進行中重複任務的保護。

5. **🟡 SHOULD FIX 2（已修復）：全域配置相容性擴充支援 JSONC 與 config.json**
   - 在 `internal/backend/opencode/adapter.go` 實作 `findGlobalOpenCodeConfigPath`，依序探測 `opencode.jsonc`、`opencode.json` 與 `config.json`。
   - 實作 `stripJSONC`，可精確去除單行註解 `//`、多行註解 `/* */` 與陣列/物件結尾前的尾隨逗號（trailing comma），同時安全保留字串內部之引號、跳脫字元與 URL 雙斜線。
   - 新增單元測試 `TestAdapter_StripJSONC` 與 `TestAdapter_CreateIsolatedReadOnlyConfigDir_SupportsJSONCAndSanitizesNPM`。

## 審查版本
- Reviewed BASE SHA：`799625c77ffb38e6e0c2302d407f20e38f22ea5c` (origin/main)
- Reviewed HEAD SHA：`7f93ff3f813f77c820ba4b2dd04b22560d79535e` (issue-13-three-face-pipeline HEAD)
- 短 SHA：`7f93ff3`
- 獨立 reviewer：Independent Code Reviewer (Subagent)
- Review artifact：`docs/issues/issue-13/review-7f93ff3.md`
- Reviewed patch-id：`de86c0cc543fff5c20fa3eaa4bb2395e453b0d13`
- 審查日期：2026-09-18

## 整體評估
本輪修復精準到位，無冗餘抽象，嚴格遵循手術式修改與簡單優先原則。
針對 Raybird 提出的 3 個 MUST FIX 與 2 個 SHOULD FIX，實作了完整的閉環防禦：
1. 子進程環境中任何 `OPENCODE_CONFIG`、`OPENCODE_CONFIG_CONTENT` 均在父進程調用前被物理剔除，徹底消除了透過環境變數繞過 `asura-readonly` 權限的攻擊面。
2. 全域 provider 的複製過程加入了深層消毒，將帶有任意 Node.js / JavaScript 執行能力的 npm 套件徹底阻擋在唯讀沙盒之外，選用模型依賴時更是直接 Fail-Closed 拒絕啟動，守住了語言層 Arbitrary Code Execution 的核心安全邊界。
3. 測試斷言補齊了第二階段 `editErr` 與 `editOut` 的檢查，確保測試唯有在真實調用成功且磁碟未受修改時方能為綠，不存在任何由請求超時或中斷偽裝成的假綠燈。
4. CI 流程的 concurrency group 正確將 push 與 PR 合併為單一組別，消除了同一 SHA 同時觸發兩次 CI 的資源消耗與潛在衝突。
5. 配置解析支援標準 JSONC 與多種設定檔名稱，確保使用者即使採用帶有註解的設定檔亦能無縫繼承模型能力。

程式碼質量、架構邊界與安全防線均已臻成熟。

## 需求符合度
- 需求描述：Phase 3C Three-Face Fixed Pipeline（Oracle → Striker → Critic）架構實作、安全權限邊界、Fail-Closed 與審計持久化，落實嚴格環境變數過濾、Provider 程式碼隔離與無假綠燈之安全驗證。
- 是否符合需求：**是（完全符合）**。
- 缺漏或過度實作說明：無。維持最小改動量與 100% 向後相容。

## 問題與風險
- 🔴 **MUST FIX**：無（3 項均已解決）。
- 🟡 **SHOULD FIX**：無（2 項均已解決）。
- 🟢 **NICE TO HAVE**：無。

## 驗收標準與測試證據
- **核准狀態覆核**：`docs/issues/issue-13/README.md` 核准表逐項查核，10 項 Scenario（@TFACE-001 ~ @TFACE-010）狀態均為 `已核准`（核准日期 2026-09-17）。
- **測試驗證全綠**：
  - `go test -v ./internal/backend/opencode/...`：全數 PASS（包含新增之環境變數過濾、JSONC 支援、Provider npm 消毒等 4 項新測試）。
  - `go test -race ./internal/backend/opencode/... ./internal/engine/... ./internal/pipeline/... ./cmd/...`：全數 PASS，零 Data Race。
  - `go vet ./...`：全數 PASS，零 Warning。
  - `go build ./cmd/asura`：編譯成功。
- **假綠燈與同義反覆防範檢查**：
  - 安全測試三階段完整閉環，包含正向能力證明、負向執行完成斷言與磁碟雜湊不變，徹底根絕假綠燈。
- **Gate 豁免查核**：**無**（未申請亦無任何豁免紀錄）。

## 破壞性邊界條件
1. **父進程環境變數植入 OPENCODE_CONFIG_CONTENT**：`buildExecutionEnv` 與 `removeEnv` 在啟動子進程前徹底清除該變數，子進程無法讀取。判定：**已覆蓋且安全（通過）**。
2. **全域 config 宣告含有 npm package 之 custom provider 並在 model 中引用**：`CreateIsolatedReadOnlyConfigDir` 立即回傳明確錯誤，Daemon 於啟動前 fail-closed 拒絕執行。判定：**已覆蓋且安全（通過）**。
3. **全域 config 包含未使用的 npm provider**：`sanitizeProviderConfig` 自動剔除該 provider，僅複製安全的純資料 provider。判定：**已覆蓋且安全（通過）**。
4. **全域 config 採用 JSONC 格式並含有單行/多行註解與尾隨逗號**：`stripJSONC` 正確過濾並成功轉為標準 JSON。判定：**已覆蓋且安全（通過）**。
5. **在唯讀模式下執行誘導修改且遇到網路或模型中斷**：測試斷言 `editErr != nil` 立即判為失敗，不再因 target.txt 未變而產生假綠燈。判定：**已覆蓋且安全（通過）**。

## 流程判定
### **`PASS`**
- 獨立 reviewer 已完成當前 HEAD（`7f93ff3`）之全量審查。
- 0 個 MUST FIX、0 個 SHOULD FIX。
- 報告已依規範持久化於 `docs/issues/issue-13/review-7f93ff3.md`。
