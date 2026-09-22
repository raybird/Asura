# 審查報告

- 審查日期：2026-09-21
- 範圍：完整 PR（Issue #17 / PR #18 包含修復 commit `8fc9afe` 之最新完整交付）
- Reviewed BASE SHA：b153c012b5fd67f12cdb34f29610ef64edfbbaef
- Reviewed HEAD SHA：8fc9afe30c243d763a581c3f9d0d6a7dd6f10c4c
- Reviewed patch-id：1ce41f87df7389ff3c2535702a2dfc708a9c5c5f
- 獨立 reviewer：獨立審查員（Independent Code Reviewer，宿主獨立 subagent）
- Review artifact：`docs/issues/issue-17/review-8fc9afe.md`

## 問題與風險

- **MUST FIX**：無
- **SHOULD FIX**：無
- **NICE TO HAVE**：
  1. `internal/backend/opencode/adapter.go`：目前的 4MB 緩衝（`maxScanCapacity = 4 * 1024 * 1024`）已足以處理極大型工具輸出與事件串流，且比照 Codex parser 標準；未來若有超過 4MB 之超大工具輸出（如大型二進制 dump），可考慮在 tool event 階段流式落地或摘要，以進一步降低記憶體負擔。

## 最新修復重點查核（OpenCode Scanner 緩衝與錯誤處理）

| 查核項目 | 實作檔案與位置 | 測試驗證位置 | 查核結論 |
|---|---|---|---|
| **4MB Scanner 緩衝配置** | `internal/backend/opencode/adapter.go:269,283,503` | `internal/backend/opencode/adapter_test.go:1078-1139,1199-1212` | **PASS**：明確定義 `const maxScanCapacity = 4 * 1024 * 1024`；`ExtractResultFromEvents` 與 `Run` 之 `bufio.Scanner` 皆明確配置 `scanner.Buffer(make([]byte, 64*1024), maxScanCapacity)`，大於 64KB（如 75KB）之合法工具輸出不再觸發 `bufio.ErrTooLong`。 |
| **顯式檢查 `scanner.Err()` 與 Fail-Closed** | `internal/backend/opencode/adapter.go:311-313,582-584,653-656` | `internal/backend/opencode/adapter_test.go:1141-1197` | **PASS**：掃描迴圈結束後顯式檢查 `scanner.Err()`；若發生掃描錯誤（如超過 4MB 之超大事件），立即設置 `lastErr = fmt.Sprintf("stdout scanner error: %v", scanErr)`，並於後續將 `FailureClass` 標記為 `FailureExecution`，回傳可識別之 execution error，杜絕「任務成功但用量遺失」之靜默假成功漏洞。 |
| **失敗路徑 PartialOutput 與 Events 保留** | `internal/backend/opencode/adapter.go:586-589,615-626,653-656` | `internal/backend/opencode/adapter_test.go:1189-1196` | **PASS**：在 Scanner 失敗中斷時，`ExecutionResult` 依然完整保留出錯前已成功收集到的 `PartialOutput` 與 `Events`，符合容錯與可觀測性防護要求。 |

## 已查核維度

### 1. 驗收劇本 CTXWIN-001 ～ CTXWIN-008 完整覆蓋

| 劇本編號 | 驗收劇本名稱 | 證據位置 | 查核結論 |
|---|---|---|---|
| **CTXWIN-001** | Backend context capability probe | `internal/backend/opencode/matrix.go`<br>`internal/backend/codex/matrix.go`<br>`internal/backend/opencode/probe_context_test.go`<br>`internal/backend/codex/probe_context_test.go`<br>`tests/integration/context_window_test.go:21-71` | **PASS**：四個指定版本（OpenCode 1.18.31 / 1.18.23、Codex 0.155.1 / 0.155.0）皆具備真實探測產物或即時探測驗證，能力矩陣狀態標註客觀誠實；離線去敏 fixture 100% 可重現；unsupported 能力明確記載。 |
| **CTXWIN-002** | Explicit provenance for token data | `internal/backend/usage.go`<br>`internal/backend/opencode/parser.go`<br>`internal/backend/codex/parser.go`<br>`tests/integration/context_window_test.go:73-104` | **PASS**：雙後端所有用量欄位全數為 `*int64`，精確區分未提供缺失（`nil`）與明確回報 0（`*v == 0`）；多步驟聚合若有步驟缺失該欄位則整體保持 `nil`，不偽造 exact；來源標示精確（`exact`、`estimated`、`unknown`），序列化與資料庫往返語意不變。 |
| **CTXWIN-003** | Dynamic stage-aware prompt evaluation | `internal/pipeline/stage_*.go`<br>`internal/pipeline/stage_characterization_test.go`<br>`tests/integration/context_window_test.go:106-153` | **PASS**：Oracle、Striker、Critic 透過 `Prepare` 與 `ExecutePrepared` 分離設計，證實估算 Prompt 與 `Backend.Run` 實際接收之 Prompt 100% 逐字相同，並正確反映 OraclePlan 與 StrikerOutput 動態膨脹。 |
| **CTXWIN-004** | Preflight admission rejection | `internal/pipeline/orchestrator.go`<br>`cmd/asura/run.go` / `run_test.go`<br>`tests/integration/context_window_test.go:155-222` | **PASS**：超量且政策為 `reject` 時，於後端進程調用前阻斷，該 Stage 呼叫次數為 0；保留已完成 Stage 之 Trace 與產物；絕無自動換後端 fallback；CLI/Daemon 啟動時即刻校驗非法環境變數（含 NaN / Inf）。 |
| **CTXWIN-005** | Sealed context immutability | `tests/integration/context_window_test.go:224-263`<br>`internal/engine/engine_test.go:1431-1501` | **PASS**：預算評估絕不修改 `ExecutionContext` 內部欄位，絕不私自裁剪記憶或 Prompt，工作區隔離防護完好。 |
| **CTXWIN-006** | Stage-level trace audit and backward compatibility | `internal/storage/sqlite_contextwin_compat_test.go`<br>`tests/integration/context_window_test.go:265-368` | **PASS**：`StageTrace` 完整記錄獨立估算、准入決策與實測用量；後端失敗或 JSON 解析失敗仍保留已取得之 usage；歷史舊任務相容讀取且不誤標為 0。 |
| **CTXWIN-007** | Redelivery preserves context and admission | `internal/engine/engine_test.go:1431-1501`<br>`tests/integration/context_window_test.go:370-427` | **PASS**：相同 Request ID 重送時由 SQLite 直接返回既有任務，不引發二次後端調用（呼叫次數保持 1），設定改變不覆寫既有決策。 |
| **CTXWIN-008** | Baseline regression safety | 全套測試網驗證（`go test -count=1 ./...` 通過）<br>`tests/integration/context_window_test.go:429-448` | **PASS**：全套既有整合驗收測試（MBACK-001～012、TFACE-001～009、CTXMEM-001～008、CRON-001～008、SCN-001～008）維持 100% 綠燈。 |

### 2. 需求、架構、安全、品質

- **架構與邊界契約**：`contextwin` 套件職責純粹，無多餘抽象或推測性設計；Stage `Prepare` / `ExecutePrepared` 資料流清晰；Storage 完全相容歷史資料。
- **安全防護**：Sealed Context 不變性維持良好；輸入驗證防禦 NaN/Inf 浮點數滲透，杜絕 Fail-Open 風險；Scanner 顯式錯誤處理杜絕事件截斷引發之假成功。
- **代碼品質**：代碼風格高度統一，通過 `go vet ./...` 零警告，測試覆蓋包含邊界大事件、超大事件異常、單元測試、特徵化測試與外迴圈端到端整合測試。

## 驗證證據與限制

- **2026-09-21 測試執行記錄**：
  - `go test -count=1 ./...`：全套套件測試全數通過（PASS）。
  - `go test -count=1 -v ./tests/integration -run "TestCTXWIN"`：CTXWIN-001 ～ CTXWIN-008 全數通過（PASS）。
  - `go vet ./...`：靜態分析零警告零錯誤。
- **版本比對驗證**：
  - Reviewed BASE SHA：`b153c012b5fd67f12cdb34f29610ef64edfbbaef`
  - Reviewed HEAD SHA：`8fc9afe30c243d763a581c3f9d0d6a7dd6f10c4c`
  - Reviewed patch-id：`1ce41f87df7389ff3c2535702a2dfc708a9c5c5f`（經 `git patch-id` 精確校驗一致）。

## 流程判定

**PASS**：本輪查核之 MUST FIX（Scanner 4MB 緩衝、顯式錯誤檢查與假成功防禦）已獲完整修復並具備單元測試驗證；全套驗收劇本 CTXWIN-001 ～ CTXWIN-008 與全系統回歸測試網 100% 綠燈通過；無任何殘留 MUST FIX 或 SHOULD FIX。本交付完全達到生產交付與合併標準。
