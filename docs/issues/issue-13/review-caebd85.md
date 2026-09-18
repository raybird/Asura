# 程式碼審查報告 (PR #14 / Issue #13)

- 範圍：PR #14 (Issue #13: Phase 3C Three-Face Pipeline) 安全修復與邊界強化覆核
- Reviewed BASE SHA：`799625c77ffb38e6e0c2302d407f20e38f22ea5c` (origin/main)
- Reviewed HEAD SHA：`caebd854858e27551edb54e91bd3b11cc3791af3`
- Reviewed patch-id：`ca104ff489179fab451077bbefa50a053b13b240`
- 獨立 reviewer：Independent Code Reviewer (Subagent)
- Review artifact：`docs/issues/issue-13/review-caebd85.md`
- 審查日期：2026-09-18
- 審查檔案：
  - `internal/backend/opencode/adapter.go`
  - `internal/backend/opencode/adapter_test.go`

---

## 變更摘要

針對 Phase 3C Three-Face Fixed Pipeline 於最新 HEAD `caebd85` 進行第八輪獨立嚴格程式碼審查。本輪變更主要針對上一輪提出的 3 項 MUST FIX 與 1 項 SHOULD FIX 進行精確的手術式修復：

1. **MUST FIX 1：沙盒判定邊界修正與 Striker / Direct 原語意維護**
   - 修正 `isIsolatedRuntime(cfg)` 邏輯，僅當 `cfg.Isolated` 為真或 `isReadOnlyRole(cfg.Role)` 為真時才判定為沙盒執行，不再誤將 `cfg.ConfigDir != ""` 當作沙盒。
   - 在 `buildStandardExecutionEnv` 中主動注入 `OPENCODE_CONFIG_DIR=cfg.ConfigDir`，使 Striker / Direct 模式在指定自訂設定目錄時能正常生效，同時保留父進程完整環境變數，維持 Phase 3B 原語意。
2. **MUST FIX 2：落實 XDG Base Directory 單一基準目錄規範**
   - 調整 `loadGlobalOpenCodeConfig()` 載入邏輯：當 `$XDG_CONFIG_HOME` 存在時僅讀取該目錄（`$XDG_CONFIG_HOME/opencode`），絕不併入或回退讀取 `~/.config/opencode`，杜絕跨目錄隱式配置汙染與外洩風險。
3. **MUST FIX 3：Provider 動態檔案代換語法 Fail-Closed 防禦**
   - 實作遞迴檢測函式 `hasDynamicFileRef`，掃描字串、Map 與 Slice 中是否含有 OpenCode 動態檔案代換語法（`{file:...}`）。
   - 在 `sanitizeProviderConfig` 中全面攔截：只要任何 provider 包含 `{file:...}`，不論是否被當前模型選用，一律依 Fail-Closed 原則拒絕啟動並回傳錯誤。
4. **SHOULD FIX：憑證處理錯誤路徑強化**
   - `CreateIsolatedReadOnlyConfigDir()` 完整檢查 `auth.json` 讀取、解析、JSONC 清理、結構序列化及寫入過程中的所有錯誤，不再以 `_ =` 靜默忽略。發生錯誤時主動清理已建立之臨時目錄並回傳詳細錯誤訊息。
5. **除錯資訊改善**：
   - 子進程執行失敗且 `stderr` 為空時，若 `rawStdout` 有輸出，則附加於錯誤訊息中，避免無訊息靜默失敗。

---

## 問題與風險

- 🔴 **MUST FIX**：無（3 項均已徹底修復）。
- 🟡 **SHOULD FIX**：無（1 項已完整解決）。
- 🟢 **NICE TO HAVE**：無。

---

## 已查核維度

### 1. 驗收與前輪問題核對

| 項目 | 審查重點與實作位置 | 驗證測試與證據 | 結論 |
|---|---|---|---|
| **MUST FIX 1** | `adapter.go#L581-L583`（修正 `isIsolatedRuntime` 條件）<br>`adapter.go#L598-L600`（`buildStandardExecutionEnv` 注入 `OPENCODE_CONFIG_DIR`） | `adapter_test.go#L309-L357`（`TestAdapter_Striker_PreservesOpenCodeConfigEnv`）<br>斷言 `OPENCODE_CONFIG_DIR` 存在且絕無沙盒 `HOME`/`XDG` 覆寫 | **PASS** |
| **MUST FIX 2** | `adapter.go#L510-L539`（`loadGlobalOpenCodeConfig` 依 XDG 規範單一目錄判定） | `adapter_test.go#L594-L633`（`TestAdapter_LoadGlobalOpenCodeConfig_StrictXDGPrecedence`）<br>在 `~/.config` 與 `$XDG_CONFIG_HOME` 同時存在時斷言 `leaked` 鍵值未被匯入 | **PASS** |
| **MUST FIX 3** | `adapter.go#L797-L818`（`hasDynamicFileRef` 遞迴檢測）<br>`adapter.go#L838-L840`（`sanitizeProviderConfig` Fail-Closed 拒絕啟動） | `adapter_test.go#L635-L669`（`TestAdapter_SanitizeProviderConfig_DisallowDynamicFileRef`）<br>針對模型依賴及未選用 provider 之 `{file:...}` 皆觸發 Fail-Closed | **PASS** |
| **SHOULD FIX** | `adapter.go#L86-L108`（`CreateIsolatedReadOnlyConfigDir` 完整錯誤捕獲與清理） | `adapter_test.go#L671-L718`（`TestAdapter_CreateIsolatedReadOnlyConfigDir_SanitizesAndPersistsAuthJSON`）<br>驗證憑證消毒與寫入成功且無靜默吞錯誤 | **PASS** |

### 2. 相關失敗面與邊界條件檢查

1. **Striker / Direct 模式傳入自訂 ConfigDir**：
   - 輸入：`Role: RoleStriker`, `ConfigDir: "/custom/dir"`, `Isolated: false`。
   - 預期：維持 Phase 3B 語意，注入 `OPENCODE_CONFIG_DIR=/custom/dir`，不建立唯讀沙盒，不覆寫 HOME 與 XDG_DATA_HOME。
   - 覆蓋與判定：`TestAdapter_Striker_PreservesOpenCodeConfigEnv` 明確驗證雙向條件（符合預期）。
2. **XDG_CONFIG_HOME 設定時本機舊配置存在**：
   - 輸入：`XDG_CONFIG_HOME=/tmp/xdg`，且使用者目錄下存在 `~/.config/opencode/config.json`。
   - 預期：僅讀取 `/tmp/xdg/opencode` 下的設定，絕不讀取 `~/.config/opencode`。
   - 覆蓋與判定：`TestAdapter_LoadGlobalOpenCodeConfig_StrictXDGPrecedence` 驗證成功，未發生目錄混合或配置洩漏。
3. **Provider 配置注入動態檔案參照 `{file:/...}`**：
   - 輸入：Provider 結構內任何欄位或巢狀物件含有 `{file:...}`。
   - 預期：Fail-Closed 立即中止並報錯，不啟動沙盒進程。
   - 覆蓋與判定：`TestAdapter_SanitizeProviderConfig_DisallowDynamicFileRef` 雙向斷言命中（包含被依賴與未被依賴之 provider）。
4. **損毀或無權限的 auth.json**：
   - 輸入：`findRealAuthJSONPath()` 指向存在但格式錯誤或不可讀的檔案。
   - 預期：`CreateIsolatedReadOnlyConfigDir()` 立即回傳包裝後的錯誤，並以 `os.RemoveAll(dir)` 移除已建立的臨時目錄。
   - 覆蓋與判定：程式碼嚴格遵循錯誤回傳與清理邏輯，無任何孤兒檔案或靜默降級。

### 3. 架構、安全與程式碼品質

- **架構邊界**：OpenCode Adapter 保持純粹的執行器職責，唯讀沙盒（Oracle / Critic）與標準執行環境（Striker / Direct）職責切分清晰，無跨模組侵入。
- **安全性原則**：
  - 白名單環境變數過濾杜絕外部 `OPENCODE_*` 配置竄改。
  - 憑證消毒阻斷 `wellknown` 外部配置注入。
  - 動態檔案替換 `{file:...}` 與自訂可執行 provider (`npm`) 均落實 Fail-Closed 阻斷。
  - 符合 XDG Base Directory Specification 標準。
- **程式碼品質**：手術式修改僅動必要邏輯，無無關排版重構或推測性設計。

### 4. 測試證據與真偽性查核

於 2026-09-18 在當前程式庫 HEAD (`caebd85`) 執行全套驗證：
- `go vet ./...`：全數 PASS（零 Warning、零 Error）。
- `go build ./cmd/asura`：編譯成功。
- `go test -race -count=1 ./...`：全數 PASS（零 Data Race，耗時約 77 秒）：
  - `asura/cmd/asura`: ok (1.109s)
  - `asura/internal/auth`: ok (1.015s)
  - `asura/internal/backend/opencode`: ok (12.735s)
  - `asura/internal/cron`: ok (1.455s)
  - `asura/internal/daemon`: ok (34.294s)
  - `asura/internal/engine`: ok (3.506s)
  - `asura/internal/pipeline`: ok (1.029s)
  - `asura/internal/storage`: ok (2.319s)
  - `asura/internal/web`: ok (1.030s)
  - `asura/tests/integration`: ok (76.997s)
- **假綠燈防範**：所有新增安全與相容性測試皆具備雙向斷言，若回退修復邏輯必將觸發失敗紅燈，無套套邏輯或同義反覆。

### 5. 豁免、待確認與限制

- **Gate 豁免**：無。
- **待確認事項**：無未解決之阻塞事項。

---

## 流程判定

### **`PASS`**

- 獨立 reviewer 已完成當前 HEAD（`caebd85`）全量審查。
- 前輪提出的 3 項 MUST FIX 與 1 項 SHOULD FIX 全數通過嚴格驗證。
- 全套測試（包含 race detector 與外迴圈整合測試）均全綠通過。
- 審查報告已持久化儲存於 `docs/issues/issue-13/review-caebd85.md`。
