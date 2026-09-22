# 審查報告

- 審查日期：2026-09-21
- 範圍：完整 PR（Issue #17 / PR #18 包含修復 commit `c3d2dd3` 之最新完整交付）
- Reviewed BASE SHA：b153c012b5fd67f12cdb34f29610ef64edfbbaef
- Reviewed HEAD SHA：c3d2dd307d60c3e7371466304805fa045a7e62c1
- Reviewed patch-id：addf910ce4f49c90456124cc55ab949c4e6cd08c
- 獨立 reviewer：獨立審查員（Independent Code Reviewer，宿主獨立 subagent）
- Review artifact：`docs/issues/issue-17/review-c3d2dd3.md`

## 問題與風險

- **MUST FIX**：無
- **SHOULD FIX**：無
- **NICE TO HAVE**：
  1. `internal/contextwin/catalog.go`：目前的靜態目錄已支援主流模型窗口，未來若引入自訂設定檔或動態 CLI 參數，可進一步支援呼叫端於請求中指定 model 名稱以命中 catalog。
  2. `internal/backend/opencode/matrix.go` 與 `internal/backend/codex/matrix.go`：未來若特定 CLI 版本釋出可於 preflight 查詢 model 或 context limit 之參數，可隨版次升級擴充矩陣與探測項目。

## 最新修復重點查核

| 查核項目 | 實作檔案與位置 | 測試驗證位置 | 查核結論 |
|---|---|---|---|
| **MUST FIX 1：`NaN` / `Inf` Fail-Open 防禦** | `internal/contextwin/config.go:67`<br>`internal/contextwin/estimator.go:33,57,82`<br>`internal/contextwin/admission.go:31` | `cmd/asura/run_test.go:576-599`<br>`internal/contextwin/config_test.go:88-104`<br>`internal/contextwin/estimator_test.go:106-130` | **PASS**：`LoadConfigWithLookup`、`HeuristicEstimator` 與 `AdmissionEngine` 均明確以 `math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio <= 0 || ratio >= 1.0` 進行阻絕；環境變數傳入 `NaN` 時程序啟動直接 exit 1 報錯，徹底杜絕浮點數轉整數下溢（`-9223372036854775808`）繞過預算限制之漏洞。 |
| **MUST FIX 2：OpenCode Nullable TokenUsage 契約** | `internal/backend/opencode/parser.go:9-25`<br>`internal/backend/opencode/adapter.go:477-598` | `internal/backend/opencode/adapter_test.go:934-1075` | **PASS**：`parser.TokenUsage` 欄位全數改為 nullable `*int64`；在 `adapter.Run` 多步驟累計時，引入 `tokenFieldAcc` 精準追蹤各欄位 `seenCount` 與 `missingCount`，若有步驟缺失該欄位則整體嚴格保持 `nil`（unknown），絕不以不完整加總或捏造 `0` 偽充 `exact`；原生 `input: 0` 亦精確保留為 `&0`。 |
| **MUST FIX 3：固定版本探測產物與 Live 測試語意** | `internal/backend/opencode/matrix.go:45-67`<br>`internal/backend/codex/matrix.go:45-67`<br>`internal/backend/opencode/probe_context_test.go`<br>`internal/backend/codex/probe_context_test.go` | `internal/backend/opencode/probe_context_test.go:120-126`<br>`internal/backend/codex/probe_context_test.go:138-142`<br>`tests/integration/context_window_test.go:21-71` | **PASS**：OpenCode 1.18.23 與 Codex 0.155.0 均完成真實探測產物收集，納入去敏 fixture，能力矩陣狀態明確修正為 `verified_probe_artifact`；Live probe 測試在環境版本不符時改用 `t.Skipf` 誠實跳過，杜絕直接 `return` 導致的假 PASS。 |
| **SHOULD FIX 1：靜態模型目錄來源與正式路徑邊界** | `docs/issues/issue-17/implementation-plan.md:33-40` | `tests/integration/context_window_test.go:288-348` | **PASS**：文件清楚闡明正式路徑因 CLI 後端呼叫前無法可靠辨識模型而維持 `req.Model == ""`，准入約束完全來自顯式 `ASURA_CONTEXT_LIMIT`（`LimitSourceConfig`），無顯式上限且模型未知時則以 `VerdictBypassed` 處理，靜態目錄作為預留擴充。 |
| **SHOULD FIX 2：20% 輸出保留空間計算基準釐清** | `docs/issues/issue-17/implementation-plan.md:28-32`<br>`internal/contextwin/estimator.go:80-84` | `internal/contextwin/estimator_test.go:89-103` | **PASS**：文件與實作清楚確立輸出保留空間係以「Prompt 估算量的 20%」為基準（`ceil(estimatedPromptTokens * 0.20)`），而非模型容量上限的 20%，避免在超大窗口下無端過度保留，且具備等比增長之數學合理性。 |

## 已查核維度

### 1. 驗收劇本 CTXWIN-001 ～ CTXWIN-008 完整覆蓋

| 劇本編號 | 驗收劇本名稱 | 證據位置 | 查核結論 |
|---|---|---|---|
| **CTXWIN-001** | Backend context capability probe | `internal/backend/opencode/matrix.go`<br>`internal/backend/codex/matrix.go`<br>`internal/backend/opencode/probe_context_test.go`<br>`internal/backend/codex/probe_context_test.go`<br>`tests/integration/context_window_test.go:21-71` | **PASS**：四個指定版本（OpenCode 1.18.31 / 1.18.23、Codex 0.155.1 / 0.155.0）皆具備真實探測產物或即時探測驗證，能力矩陣狀態標註客觀誠實；離線去敏 fixture 100% 可重現；unsupported 能力明確記載。 |
| **CTXWIN-002** | Explicit provenance for token data | `internal/backend/usage.go`<br>`internal/backend/opencode/parser.go`<br>`internal/backend/codex/parser.go`<br>`tests/integration/context_window_test.go:73-104` | **PASS**：雙後端所有用量欄位全數為 `*int64`，精確區分未提供缺失（`nil`）與明確回報 0（`*v == 0`）；來源標示精確（`exact`、`estimated`、`unknown`），序列化與資料庫往返語意不變。 |
| **CTXWIN-003** | Dynamic stage-aware prompt evaluation | `internal/pipeline/stage_*.go`<br>`internal/pipeline/stage_characterization_test.go`<br>`tests/integration/context_window_test.go:106-153` | **PASS**：Oracle、Striker、Critic 透過 `Prepare` 與 `ExecutePrepared` 分離設計，證實估算 Prompt 與 `Backend.Run` 實際接收之 Prompt 100% 逐字相同，並正確反映 OraclePlan 與 StrikerOutput 動態膨脹。 |
| **CTXWIN-004** | Preflight admission rejection | `internal/pipeline/orchestrator.go`<br>`cmd/asura/run.go` / `run_test.go`<br>`tests/integration/context_window_test.go:155-222` | **PASS**：超量且政策為 `reject` 時，於後端進程調用前阻斷，該 Stage 呼叫次數為 0；保留已完成 Stage 之 Trace 與產物；絕無自動換後端 fallback；CLI/Daemon 啟動時即刻校驗非法環境變數（含 NaN）。 |
| **CTXWIN-005** | Sealed context immutability | `tests/integration/context_window_test.go:224-263`<br>`internal/engine/engine_test.go:1431-1501` | **PASS**：預算評估絕不修改 `ExecutionContext` 內部欄位，絕不私自裁剪記憶或 Prompt，工作區隔離防護完好。 |
| **CTXWIN-006** | Stage-level trace audit and backward compatibility | `internal/storage/sqlite_contextwin_compat_test.go`<br>`tests/integration/context_window_test.go:265-368` | **PASS**：`StageTrace` 完整記錄獨立估算、准入決策與實測用量；後端失敗或 JSON 解析失敗仍保留已取得之 usage；歷史舊任務相容讀取且不誤標為 0。 |
| **CTXWIN-007** | Redelivery preserves context and admission | `internal/engine/engine_test.go:1431-1501`<br>`tests/integration/context_window_test.go:370-427` | **PASS**：相同 Request ID 重送時由 SQLite 直接返回既有任務，不引發二次後端調用（呼叫次數保持 1），設定改變不覆寫既有決策。 |
| **CTXWIN-008** | Baseline regression safety | 全套測試網驗證（`go test -count=1 ./...` 通過）<br>`tests/integration/context_window_test.go:429-448` | **PASS**：全套既有整合驗收測試（MBACK-001～012、TFACE-001～009、CTXMEM-001～008、CRON-001～008、SCN-001～008）維持 100% 綠燈。 |

### 2. 需求、架構、安全、品質

- **架構與邊界契約**：`contextwin` 套件職責純粹，無多餘抽象或推測性設計；Stage `Prepare` / `ExecutePrepared` 資料流清晰；Storage 完全相容歷史資料。
- **安全防護**：Sealed Context 不變性維持良好；輸入驗證防禦 NaN/Inf 浮點數滲透，杜絕 Fail-Open 風險。
- **代碼品質**：代碼符合 Go 慣用語法，無孤兒代碼；`go vet ./...` 零警告，測試覆蓋邊界完整。

## 驗證證據與限制

- **2026-09-21 測試執行記錄**：
  - `go test -count=1 ./...`：全套套件測試全數通過（PASS）。
  - `go test -count=1 -v ./tests/integration -run "TestCTXWIN"`：CTXWIN-001 ～ CTXWIN-008 全數通過（PASS）。
  - `go vet ./...`：靜態分析零警告零錯誤。
- **版本比對驗證**：
  - Reviewed BASE SHA：`b153c012b5fd67f12cdb34f29610ef64edfbbaef`
  - Reviewed HEAD SHA：`c3d2dd307d60c3e7371466304805fa045a7e62c1`
  - Reviewed patch-id：`addf910ce4f49c90456124cc55ab949c4e6cd08c`（經 `git patch-id` 精確校驗一致）。

## 流程判定

**PASS**：先前審查提出之 MUST FIX 1、2、3 與 SHOULD FIX 1、2 均已獲完整修復並具備嚴謹自動化測試驗證；全套驗收劇本與回歸測試網維持 100% 通過；無新增 MUST FIX 或 SHOULD FIX。本交付已達交付合併標準。
