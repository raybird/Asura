# 審查報告

- 審查日期：2026-09-21
- 範圍：完整 PR（Issue #17 / PR #18 交付範圍）
- Reviewed BASE SHA：b153c012b5fd67f12cdb34f29610ef64edfbbaef
- Reviewed HEAD SHA：7f08c6f0162f09756d7e3066f707b05d27dcdf98
- Reviewed patch-id：79609a579fca49179f12ab31e4c935c1c82a75b8
- 獨立 reviewer：獨立審查員（Independent Code Reviewer，宿主獨立 subagent）
- Review artifact：`docs/issues/issue-17/review-7f08c6f.md`

## 問題與風險

- **MUST FIX**：無
- **SHOULD FIX**：無
- **NICE TO HAVE**：
  1. `internal/contextwin/estimator.go`：目前的啟發式估算器採用字元／Rune 比例換算（ASCII: ~3.5 字元/token，CJK: ~2 token/字元），並透過 `ProvenanceEstimated` 明確標記可信度。未來若有更精細的離線 BPE 或特定模型 tokenizer 需求，可在 `Estimator` 介面下新增實作，保留現有啟發式演算法作為無 CGO 環境之後備方案。
  2. `internal/contextwin/catalog.go`：靜態模型目錄目前涵蓋主流 GPT-4o、Claude 3.5、Gemini 1.5/2.0 等模型窗口，未來可考慮支援使用者透過設定檔自訂新增私有部署或微調模型之窗口上限。

## 已查核維度

### 1. 驗收與證據

| 劇本編號 | 驗收劇本名稱 | 證據位置 | 查核結論 |
|---|---|---|---|
| **CTXWIN-001** | Backend context capability probe | `internal/backend/opencode/probe_context_test.go`<br>`internal/backend/codex/probe_context_test.go`<br>`tests/integration/context_window_test.go:21-51` | **PASS**：雙後端完成真實探測與離線去敏 fixture 測試，明確產出能力矩陣；unsupportedCapabilities 詳列工具輸出攔截、歷史熱插拔等原生限制；1.18.23 與 0.155.0 標記為 `unverified_not_installed`，不假裝已驗證。 |
| **CTXWIN-002** | Explicit provenance for token data | `internal/backend/usage.go`<br>`internal/backend/usage_test.go`<br>`tests/integration/context_window_test.go:53-84` | **PASS**：實作 `TokenUsage` 全欄位 nullable (`*int64`)，成功區分未提供缺失（`nil` / unknown）與明確回報為 0（`*v == 0`）；來源精確區分 `exact`、`estimated`、`unknown`，JSON 與 SQLite 往返語意不變。 |
| **CTXWIN-003** | Dynamic stage-aware prompt evaluation | `internal/pipeline/stage_*.go`<br>`internal/pipeline/stage_characterization_test.go`<br>`tests/integration/context_window_test.go:86-133` | **PASS**：Oracle、Striker、Critic 分開執行 Stage-Aware 預估，考量 OraclePlan 與 StrikerOutput 之動態膨脹；透過 `Prepare` 與 `ExecutePrepared` 機制保證估算字串與 `Backend.Run` 實際收到的 Prompt 100% 逐字相同。 |
| **CTXWIN-004** | Preflight admission rejection | `internal/pipeline/orchestrator.go`<br>`cmd/asura/run.go` / `run_test.go`<br>`tests/integration/context_window_test.go:135-202` | **PASS**：超量且政策為 `reject` 時，於調用後端前 fail-closed 中斷，該 Stage 呼叫次數為 0；保留已完成 Stage 之 Trace 與產物；絕無跨後端 fallback；非法環境變數在 CLI/Daemon 啟動時即刻報錯退出。 |
| **CTXWIN-005** | Sealed context immutability | `tests/integration/context_window_test.go:204-243`<br>`internal/engine/engine_test.go:1431-1501` | **PASS**：預算評估與審計流程僅讀取 Prompt 與記憶，絕不修改 `ExecutionContext` 內部任何欄位，絕不私自裁剪記憶或 Prompt，工作區隔離防護完好。 |
| **CTXWIN-006** | Stage-level trace audit and backward compatibility | `internal/storage/sqlite_contextwin_compat_test.go`<br>`tests/integration/context_window_test.go:245-348` | **PASS**：StageTrace 完整記錄獨立估算、AdmissionVerdict 與實測 TokenUsage；後端失敗或 JSON 解析失敗仍精確保留已回報之 usage；歷史任務讀取完全相容且不誤標為 0。 |
| **CTXWIN-007** | Redelivery preserves context and admission | `internal/engine/engine_test.go:1431-1501`<br>`tests/integration/context_window_test.go:350-407` | **PASS**：相同 Request ID 重送時由 SQLite 既有任務直接返回，不重複觸發估算或後端執行（呼叫次數保持 1）；重送不因程序預算設定變更而覆寫已保存決策。 |
| **CTXWIN-008** | Baseline regression safety | 全套測試網驗證（`go test -count=1 ./...` 通過）<br>`tests/integration/context_window_test.go:409-428` | **PASS**：全套既有整合驗收測試（MBACK-001～012、TFACE-001～009、CTXMEM-001～008、CRON-001～008、SCN-001～008）維持 100% 綠燈，無行為破壞。 |

### 2. 相關失敗面查核

- **部分用量缺失與真實零值**：Codex 原生不提供 `total_tokens`，parser 精確保留 `TotalTokens == nil`，且將 `cached_input_tokens: 0` 轉為 `*v == 0`，經序列化與資料庫往返後無語意偏差。判定：**通過**。
- **後端執行失敗或輸出解析失敗**：OpenCode 與 Codex adapter 在 Exit Code 非零、超時或 JSON 解析失敗時，回傳的 `ExecutionResult` 均保留了已從 stdout 事件流收集到的 `Usage`，未被丟棄。判定：**通過**。
- **未知模型與預算設定無效值**：未配置限制且為未知模型時，AdmissionVerdict 為 `bypassed`；若設有顯式全域上限，則未知模型仍受該上限保護；非法設定（負數、非法政策字串）在程式進入點直接阻斷。判定：**通過**。
- **後續 Stage 超量中斷**：Three-Face 模式中 Striker 或 Critic 超量拒絕時，前面已完成的 OraclePlan 完好保留在 `PipelineTrace` 中，且後續未執行的 Stage 調用次數為 0，無任何自動 failover。判定：**通過**。

### 3. 需求、架構、安全、品質

- **需求完整性**：完全滿足 Issue #17 規格要求及前次審查（`review-405580c.md`）列出之三項 MUST FIX 與一項 SHOULD FIX。
- **架構邊界**：新增之 `internal/contextwin` 套件職責單一、邊界清晰；Stage 透過 `Prepare` / `ExecutePrepared` 保持職責解耦；Orchestrator 負責 Fail-Closed 防禦調度；Storage 層完全無需修改既有 DDL 即可天然相容。
- **安全與隔離**：嚴格維持 Sealed Context 不變性，無任何記憶裁剪或跨工作區資料污染風險；無私自連網或外呼未授權端點。
- **程式碼品質**：代碼風格與既有程式庫高度一致，通過 `go vet ./...` 零警告，測試覆蓋包含單元測試、特徵化字對字測試、相容性測試與外迴圈端到端整合測試。

### 4. 豁免、待確認與限制

- **TBD-1 & TBD-2**：已確認全數解決，Gherkin 劇本與預設政策（`observe` 政策、20% 輸出保留比例）皆有明確核准紀錄。
- **未安裝版本標記**：OpenCode 1.18.23 與 Codex 0.155.0 因環境未安裝，依審查規範誠實標記為 `unverified_not_installed`，未偽造 live probe 通過。

## 驗證證據與限制

- **2026-09-21 測試執行記錄**：
  - `go test -count=1 ./...`：全套套件測試全數通過（PASS）。
  - `go test -v ./tests/integration -run "TestCTXWIN"`：CTXWIN-001 ～ CTXWIN-008 全數通過（PASS）。
  - `go vet ./...`：靜態分析零警告零錯誤。
- **版本比對驗證**：
  - Reviewed BASE SHA：`b153c012b5fd67f12cdb34f29610ef64edfbbaef`
  - Reviewed HEAD SHA：`7f08c6f0162f09756d7e3066f707b05d27dcdf98`
  - Reviewed patch-id：`79609a579fca49179f12ab31e4c935c1c82a75b8`（經 `git patch-id` 精確校驗一致）。

## 流程判定

**PASS**：所有驗收條件完整覆蓋，無任何 MUST FIX 或 SHOULD FIX，全系統回歸測試全綠，程式品質與架構邊界符合標準。可推進至下一步（建立 PR / 合併）。
