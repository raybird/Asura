# 程式碼審查報告 (PR #14 / Issue #13)

## 變更摘要
針對 Phase 3C Three-Face Fixed Pipeline（Oracle → Striker → Critic）於 HEAD `378b680` 之實作進行第四輪增量與全量獨立嚴格審查。本次審查特別針對 Raybird 提出的最新審查意見「OpenCode Agent-level permission isolation 真正成立」進行徹底架構檢驗：
- `internal/backend/opencode/adapter.go`：
  - 定義 `RuntimeRole`（`RoleOracle`, `RoleStriker`, `RoleCritic`）。
  - 定義專屬唯讀 Agent `asura-readonly` 與獨立配置 `AsuraReadOnlyConfigJSON`（唯讀白名單 `read`, `glob`, `grep`, `list`, `lsp`，預設 `*` 為 `deny`）。
  - 實作 `CreateIsolatedReadOnlyConfigDir` 建立專屬暫存目錄並寫入安全配置，提供清理閉包。
  - 擴充 `AdapterConfig` 支援 `Agent`, `Pure`, `DisableProjectConfig`, `Role`。
  - `Adapter.Run` 於 CLI 參數注入 `--agent asura-readonly`, `--pure`，並於環境變數注入 `OPENCODE_DISABLE_PROJECT_CONFIG=1`, `OPENCODE_PURE=1`。
- `internal/daemon/daemon.go`：
  - Daemon 生命週期自動建立與管理獨立 isolated read-only config 目錄，並於 `Close()` 時透過 `sync.Once` 安全清理釋放。
  - `readOnlyAdapter`（供 Oracle 與 Critic 共享）配置完整的五重隔離機制（`RoleOracle`, `Agent: asura-readonly`, `Pure: true`, `DisableProjectConfig: true`, `ConfigDir: isolatedDir`, `DisableAuto: true`, `Permission: DefaultReadOnlyPermissionPolicy`）。
  - Striker 維持使用一般專案 config 與正常執行許可權限。
- `internal/daemon/daemon_test.go`：
  - 新增真實敵意配置攻擊測試 `TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly`：在工作區包含惡意 `.opencode/opencode.json`（設定 `agent.build.permission` 與 `agent.asura-readonly.permission` 為 allow）的情境下執行 Oracle 任務，誘使其修改敏感檔案，驗證真實 OpenCode 回應「無法修改：目前工作區僅提供唯讀工具，沒有可用的檔案編輯功能」，且 `target.txt` 的 SHA256 在執行前後完全一致。
- `internal/backend/opencode/adapter_test.go`：
  - 新增 `TestAdapter_IsolatedReadOnlyConfig` 驗證獨立設定檔內容與權限白名單。
  - 新增 `TestAdapter_Run_IsolatedCommandAndEnv` 驗證參數與環境變數組裝。

## 審查版本
- Reviewed BASE SHA：`799625c77ffb38e6e0c2302d407f20e38f22ea5c` (origin/main)
- Reviewed HEAD SHA：`378b68081bb64a2f153f8a31d5a0417ff4d151fb` (issue-13-three-face-pipeline HEAD)
- 短 SHA：`378b680`
- 獨立 reviewer：Independent Code Reviewer (Subagent)
- Review artifact：`docs/issues/issue-13/review-378b680.md`
- Reviewed patch-id：`25877041ae2d5ca559695601f249207669c9b15b`
- 審查日期：2026-09-17

## 整體評估
本次審查針對上一輪提出的核心安全疑慮（工作區專案配置可能透過 agent 權限定義繞過全域唯讀限制）進行了徹底的架構級重構與防禦驗證。
修復成果極為顯著：
1. **五重縱深防禦（Defense-in-Depth）**：
   - 第一重：獨立暫存目錄（`OPENCODE_CONFIG_DIR`）定義專屬 `asura-readonly` agent，權限採最小白名單（`read`, `glob`, `grep`, `list`, `lsp`），預設 `*` deny。
   - 第二重：環境變數 `OPENCODE_DISABLE_PROJECT_CONFIG=1` 徹底阻斷 OpenCode 讀取工作區內的 `.opencode/opencode.json`，使工作區內的權限提權設定完全無效。
   - 第三重：CLI 參數 `--pure` 與環境變數 `OPENCODE_PURE=1` 阻斷使用者家目錄等全域配置干擾。
   - 第四重：CLI 參數 `--agent asura-readonly` 強制指定唯讀 agent，阻斷 fallback 至具備寫入權限的 `build` 或自訂 agent。
   - 第五重：CLI/環境變數 `OPENCODE_PERMISSION`（`DefaultReadOnlyPermissionPolicy`）在執行器最外層顯式拒絕所有 mutating capabilities。
2. **真實攻擊測試佐證（Proof of Test）**：
   - 攻擊測試 `TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly` 在真實具備敵意工作區配置下執行真實 OpenCode CLI，實際觀察到模型反饋「無法修改：目前工作區僅提供唯讀工具，沒有可用的檔案編輯功能」，且 `target.txt` 之 SHA256 與內容保持 100% 完整，證明防禦真實有效，無假綠燈。
3. **全專案迴歸全綠**：
   - 執行 `go test -race -count=1 ./cmd/... ./internal/...` 全部通過（無 Data Race）。
   - 執行 `go test -v -race -count=1 ./tests/integration -run TestTFACE` 10 項 Gherkin 端到端測試全部通過。

本次審查判定為 **PASS**，完全達到生產級安全與可靠性標準。

## 需求符合度
- 需求描述：Phase 3C Three-Face Fixed Pipeline（Oracle → Striker → Critic）架構實作、安全權限邊界、Fail-Closed 與審計持久化，落實 Agent-level 權限隔離。
- 是否符合需求：**是（完全符合）**。
- 缺漏或過度實作說明：無。嚴格遵循 YAGNI 與手術式修改原則，未添加不必要的抽象；Direct 模式維持 100% 向後相容。

## 問題與風險

嚴重度標籤說明：
- 🔴 **MUST FIX** — 邏輯錯誤、安全漏洞，必須在 merge 前修正
- 🟡 **SHOULD FIX** — 潛在問題、邊界未處理，建議修正
- 🟢 **NICE TO HAVE** — 可讀性、最佳實踐，可列入後續 backlog

### 🔴 MUST FIX
- **無**。

### 🟡 SHOULD FIX
- **無**。

### 🟢 NICE TO HAVE
1. **取消狀態下的 Partial PipelineTrace 補齊**
   - **位置**：`internal/engine/engine.go:432`
   - **現況**：任務被外部取消（Cancelled）時，Engine 會持久化 partial output 至 `tasks.result`，但未持久化當前已完成階段之 `PipelineTrace`。
   - **影響與建議**：不影響安全性與正確性。後續若需於 CLI 查核取消發生在哪一個 stage，可考慮在取消路徑中補寫當前的 traceJSON。

## 驗收標準與測試證據
- **核准狀態覆核**：`docs/issues/issue-13/README.md` 核准表逐項查核，10 項 Scenario（@TFACE-001 ~ @TFACE-010）狀態均為 `已核准`（核准日期 2026-09-17），無任何殘留之「待核准」或「待重新核准」。
- **測試驗證全綠**：
  - `go test -race -count=1 ./cmd/... ./internal/...` 全數通過。
  - 外迴圈劇本測試 `go test -v -race -run "TestTFACE" ./tests/integration`（10/10 通過）。
  - 敵意攻擊專項測試 `TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly` 通過（真實 OpenCode 執行）。
  - 新增配置隔離測試 `TestAdapter_IsolatedReadOnlyConfig`、`TestAdapter_Run_IsolatedCommandAndEnv` 全數通過。
- **假綠燈與同義反覆防範檢查**：
  - 攻擊測試中，SHA256 驗證直接計算原始字節陣列之 SHA256，並比對執行前後磁碟檔案內容與哈希，斷言獨立且具備明確否定性，無同義反覆。
- **Gate 豁免查核**：**無**（未申請亦無任何豁免紀錄，所有驗收標準均為實質測試通過）。

## 破壞性邊界條件

### Edge Case 1：工作區敵意設定注入攻擊（Hostile Workspace Config Injection）
- **輸入／狀態**：攻擊者在代碼庫中預先埋入 `.opencode/opencode.json`，宣告 `agent.build.permission` 與 `agent.asura-readonly.permission` 均為 `{"*": "allow", "edit": "allow", "bash": "allow"}`。誘導 Prompt 試圖在 Oracle / Critic 唯讀階段修改 `target.txt` 或執行 `bash` 外傳機密。
- **預期結果**：五重隔離機制（`OPENCODE_DISABLE_PROJECT_CONFIG=1`、`--agent asura-readonly`、`--pure`、`OPENCODE_CONFIG_DIR`、`OPENCODE_PERMISSION`）全面生效。工作區設定完全被忽略，OpenCode 拒絕提供任何 mutating tools，檔案內容與 SHA256 完全不變，工作區安全邊界不被突破。
- **現有覆蓋**：`internal/backend/opencode/adapter.go`；`internal/daemon/daemon.go`；`internal/daemon/daemon_test.go:TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly`。真實 OpenCode 執行輸出驗證：「無法修改：目前工作區僅提供唯讀工具，沒有可用的檔案編輯功能。`target.txt` 內容維持不變。」
- **判定**：**已覆蓋且安全（通過）**。

### Edge Case 2：暫存隔離目錄在 Daemon 意外終止或多次重啟時的生命週期洩漏與競爭
- **輸入／狀態**：Daemon 實例多次建立與銷毀（如單元測試頻繁啟動，或 Daemon 服務重啟），或多個 Daemon 同時運行。
- **預期結果**：`CreateIsolatedReadOnlyConfigDir()` 使用 `os.MkdirTemp` 生成具唯一隨機後綴的獨立目錄（`asura-opencode-readonly-*`），權限嚴格設為 `0600`，不同 Daemon 實例間互不干擾、無命名競爭；且 `Daemon.Close()` 內部由 `sync.Once` 保護，確保暫存目錄被安全刪除（`cleanupReadOnlyConfig()`），無資源洩漏。
- **現有覆蓋**：`internal/backend/opencode/adapter.go`；`internal/backend/opencode/adapter_test.go:TestAdapter_IsolatedReadOnlyConfig`；`internal/daemon/daemon.go`。
- **判定**：**已覆蓋且安全（通過）**。

### Edge Case 3：Critic 審查階段若遭遇敵意回傳或 prompt injection 試圖透過 bash/edit 竄改 Striker 產出或工作區
- **輸入／狀態**：Striker 執行完畢後，工作區可能已包含未編譯的惡意代碼片段，或 Prompt 中誘導 Critic「如果審查發現錯誤，請直接替我修改修復它」。
- **預期結果**：Critic 階段與 Oracle 共享同一個 `readOnlyAdapter`，具備相同的五重隔離防禦（`RoleOracle/Critic`、`asura-readonly`、`Pure`、`DisableProjectConfig`、`isolatedDir`）。OpenCode 在 Critic 階段同樣沒有 `edit` 或 `bash` 工具可用，即便 LLM 試圖執行修復，OpenCode 亦無法發起寫入。Critic 只能透過純文字事件回傳 JSON verdict（`approved` / `failed`），若不合格則輸出 failed 判定，保障審查階段之無副作用純粹性。
- **現有覆蓋**：`internal/daemon/daemon.go`；`internal/pipeline/stage_critic.go:41-65`；`tests/integration/three_face_pipeline_test.go:TestTFACE007_CriticFailureMustNotClaimSuccess`。
- **判定**：**已覆蓋且安全（通過）**。

### Edge Case 4：孤立/殘留 Agent 名稱或版本不相容導致 OpenCode 無法識別 --agent asura-readonly
- **輸入／狀態**：環境中安裝的 OpenCode binary 版本較舊或不支援 `--agent` 參數或 `--pure` 旗標，或 `ResolveOpenCodeBinary` 解析到的版本不符合預期。
- **預期結果**：`ValidateOpenCodeVersion` 與 `ResolveOpenCodeBinary` 在執行前會嚴格檢查 OpenCode 版本（固定預期 `1.18.23`）。若版本不符或找不到 binary，執行前即立即報錯退出（Fail-Closed），絕對不降級或略過 `--agent` 參數直接以預設 full permission 執行。
- **現有覆蓋**：`internal/backend/opencode/adapter.go`；`internal/backend/opencode/adapter_test.go`。
- **判定**：**已覆蓋且安全（通過）**。

## 架構符合度
- **分層邊界與依賴方向**：
  - `internal/backend/opencode` 專責封裝 OpenCode CLI 之低階行程管理、參數組裝與環境隔離，不依賴上層 daemon 或 engine。
  - `internal/daemon` 負責將 isolated read-only adapter 實例化並注入 pipeline stage，生命週期管理明確。
  - Striker 保留專案原生配置支援，Oracle 與 Critic 嚴格套用唯讀隔離，職責邊界清楚分明。

## 安全性檢查
- **存取控制與授權**：達成嚴格的 Agent-level 權限隔離，成功阻絕了工作區惡意設定提權的路徑。
- **輸入驗證與防禦**：Daemon IPC Ingress 與 Engine 雙層防禦 `ExecutionMode`；結構化輸出嚴格校驗非空與有效列舉。
- **敏感資料保護**：ExecutionContext 與專案記憶採密封快照機制，保持任務執行上下文不變。

## 程式品質與效能
- 程式碼遵循 Go 慣例，錯誤回傳與資源釋放路徑清晰。
- 全量測試開啟 `-race` 檢驗全綠，無資料競爭（Data Race）。
- 唯讀暫存設定檔以靜態常數生成，檔案體積極小，初始化與清理均在微秒級完成，無效能負擔。

## 改進建議
- 本次修復已徹底解決 Agent-level 權限逃逸問題，實作嚴謹且有完整攻擊測試防護。
- 未來若支援自訂 Multi-Agent 角色，可沿用目前 `RuntimeRole` 與隔離目錄機制進行動態派發。

## 流程判定
### **`PASS`**
- 獨立 reviewer 已完成當前 HEAD（`378b680`）之全量審查。
- 0 個 MUST FIX、0 個 SHOULD FIX。
- 所有 10 項 Gherkin 驗收標準均具備真實通過證據且無假綠燈。
- 所有破壞性邊界條件（特別是敵意工作區配置攻擊）均通過實證驗收。
- 報告已依規範持久化於 `docs/issues/issue-13/review-378b680.md`。
