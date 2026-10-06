# Issue 17 - Phase 3E：Context Window Management Foundation（能力探測、預算與可觀測性）

## 概述

建立日期：2026-09-21。承接 Phase 3D [Issue #15](https://github.com/raybird/Asura/issues/15) / [PR #16](https://github.com/raybird/Asura/pull/16) 已合併的 OpenCode + Codex 共同 Backend 契約，並延續 Phase 3B 的 Sealed `ExecutionContext`、Phase 3C 的 Direct / Three-Face Pipeline。

讓 Asura 在執行前知道「送給哪個 backend、哪個 stage 的 Context 可能有多大」，並在執行後能審計實際可取得的 token usage 與預算判斷。本期採**能力證據優先、最小可行 Context 管理**，不把 CLI stdout/jsonl 事件誤當成已取得模型內部 Context 編輯權。

首版交付：
- 對固定版本 **OpenCode 1.18.23 / 1.18.31** 與 **Codex CLI 0.155.0 / 0.155.1** 做 Context contract probe，記錄各 backend 可觀測／可控制的能力與限制。
- 為 Direct 與 Three-Face 建立**任務級、stage-aware 的 Context Budget / Admission**（Oracle、Striker、Critic 的實際 prompt 組成不同，分別估算與判斷）。
- 建立 token 數據來源／可信度、估算值與實測值的分離表示，並將執行前判斷及執行後可取得的 usage 納入 Task / PipelineTrace 或關聯審計資料。
- 維持 Sealed Context、workspace scope、BackendID、Role、安全隔離、冪等重送與 No Automatic Failover 的既有契約。

## 文件清單

- [requirement-analysis.md](./requirement-analysis.md)
- [technical-analysis.md](./technical-analysis.md)
- [implementation-plan.md](./implementation-plan.md)
- [原提交審查報告](./review-405580c.md)（僅適用 `405580c`，修訂後未宣稱重新審查通過）

## 快速導覽

### 當前流程
1. `engine.ExecutionContext` 封裝原始 prompt、workspace 與記憶快照；`ComposePrompt` 將記憶完整附加至 prompt，無獨立 token/容量預算概念。
2. Three-Face Pipeline 各階段（Oracle 增加指示、Striker 增加 OraclePlan、Critic 增加 Plan 與 StrikerOutput）各自擴充提示詞，但 Orchestrator 執行前無法預判各階段是否會超出模型窗口限制。
3. `backend.ExecutionResult` 與 `pipeline.StageTrace` 缺乏標準化 TokenUsage 欄位，OpenCode 與 Codex 各自的事件流用量未被統一萃取與持久化。

### 重構後流程
1. 執行前（Preflight）：Orchestrator 根據即將送交後端的真實 Stage Prompt（含動態生成的 OraclePlan 或 StrikerOutput），透過 `contextwin.Estimator` 計算該階段預估 Token 與窗口占用，標記來源為 `estimated`。
2. 准入檢查（Stage Admission）：對照配置或靜態模型目錄之限制，判定是否核准執行（Admitted）或拒絕（Rejected）。若超量且啟用阻擋政策，立即 fail-closed 中斷，調用次數為 0，且不切換後端。
3. 執行後（Postflight）：後端執行器自 stdout/jsonl 中精確萃取原生 token usage，正規化為 `backend.TokenUsage`，標記來源為 `exact`。
4. 審計歷程（Trace & Provenance）：Task 與 `PipelineTrace` 完整記錄各 Stage 執行前的估算准入結果與執行後的真實用量，保留已發生 Stage 的成果，並支援 SQLite 歷史資料無縫讀取。

## 關鍵差異

| 項目 | 變更前 (Phase 3D) | 變更後 (Phase 3E) |
|---|---|---|
| **Context 預算感知** | 執行前完全無感知，直接將 Prompt 送交後端 | Stage-aware 預估，考量 OraclePlan 與 StrikerOutput 之動態膨脹 |
| **准入控制 (Admission)** | 無准入檢查，超量由 CLI 報錯或非預期失敗 | Explicit Stage Admission，超量可於調用前 fail-closed 拒絕 |
| **Token 用量審計** | `ExecutionResult` 與 `StageTrace` 缺失 TokenUsage | 標準化 `TokenUsage` 欄位，涵蓋 input, output, cached, total |
| **數據可信度 (Provenance)** | 無區分機制 | 明確區分 `exact` (實測)、`estimated` (估算)、`unknown` (未知) |
| **數值缺失處理** | N/A | 嚴格標記 `unknown`，絕不將缺失用量偽造為 0 |
| **上下文完整性防禦** | 密封不變 | 維持 Sealed Context 不變性，超量絕不私自裁剪 Prompt 或 Memory |
| **容錯與重送** | No Automatic Failover | 維持 No Automatic Failover，超量終止保留已完成 Stage Trace |

## 涉及檔案

```text
internal/
├── backend/
│   ├── backend.go           # 可改：ExecutionResult 擴充 TokenUsage 契約
│   ├── usage.go             # 新增：TokenUsage、DataProvenance 定義
│   ├── usage_test.go        # 新增：TokenUsage 與 Provenance 測試
│   ├── opencode/
│   │   ├── adapter.go       # 可改：聚合 usage 並寫入 ExecutionResult，失敗仍保留
│   │   ├── adapter_test.go  # 可改：fake CLI 到 ExecutionResult 驗證
│   │   ├── parser.go        # 可改：自 JSONL 事件萃取 TokenUsage
│   │   ├── parser_test.go   # 可改：驗證 OpenCode Token 抽取
│   │   └── probe_context_test.go # 新增：OpenCode Context Contract Probe
│   └── codex/
│       ├── adapter.go       # 可改：回傳 parser usage，失敗仍保留
│       ├── adapter_test.go  # 可改：fake CLI 到 ExecutionResult 驗證
│       ├── parser.go        # 可改：自 JSONL 事件萃取 TokenUsage
│       ├── parser_test.go   # 可改：驗證 Codex Token 抽取
│       └── probe_context_test.go # 新增：Codex Context Contract Probe
├── contextwin/              # 新增：Context Window Management 模組
│   ├── config.go            # 新增：程序級環境設定解析及驗證
│   ├── config_test.go       # 新增：設定有效值與錯誤路徑
│   ├── types.go             # 新增：ModelWindowInfo, StageEstimate, StageAdmission 型別
│   ├── catalog.go           # 新增：靜態模型目錄與未知降級政策
│   ├── catalog_test.go      # 新增：模型目錄單元測試
│   ├── estimator.go         # 新增：啟發式 Token 估算器
│   ├── estimator_test.go    # 新增：Token 估算器單元測試
│   ├── admission.go         # 新增：Stage 准入引擎與決策判定
│   └── admission_test.go    # 新增：准入判定單元測試
├── daemon/
│   ├── daemon.go            # 可改：預算設定注入正式 Orchestrator
│   └── daemon_test.go       # 可改：正式入口准入驗證
├── pipeline/
│   ├── stage_oracle.go      # 可改：Prepare／ExecutePrepared，回傳後端結果
│   ├── stage_striker.go     # 可改：共用已準備 request，保留 partial result
│   ├── stage_critic.go      # 可改：解析失敗仍回傳 usage
│   ├── stage_*_test.go      # 可改：prompt 相等、usage 及失敗路徑
│   ├── types.go             # 可改：StageTrace 擴充 Admission 與 Usage 欄位
│   ├── types_test.go        # 可改：StageTrace 序列化測試
│   ├── orchestrator.go      # 可改：整合 Stage-aware Admission 與 Trace 紀錄
│   └── orchestrator_test.go # 可改：Orchestrator 准入阻擋與容錯回歸測試
├── engine/
│   ├── engine.go            # 可改：維持 Sealed Context 與審計串接
│   └── engine_test.go       # 可改：重送與密封不變性測試
└── storage/
    ├── sqlite.go            # 可改：Task 與 Trace 向後相容持久化
    └── sqlite_test.go       # 可改：歷史任務讀取相容性測試
```

其他必要檔案：`cmd/asura/run.go`／`run_test.go`、`cmd/asura/daemon.go`（讀取設定並注入）、`tests/integration/context_window_test.go`（跨層驗收）。交付前更新根目錄 `README.md` 與 `docs/superpowers/specs/2026-09-11-asura-architecture-design.md`；責任歸 Task 6.2。

## Gherkin 驗收劇本

```gherkin
Feature: Context Window Management Foundation (Capability Probe, Budget and Observability)

  @CTXWIN-001
  Scenario: Backend context capability probe
    Given 固定版本 OpenCode 與 Codex CLI
    When 執行 Context Contract Probe 測試
    Then 系統產出雙後端 Context 能力矩陣
    And 明確標示可觀測之 token usage 來源與原生限制
    And 未支援能力（如模型前工具輸出攔截、動態會話歷史替換）明確記錄為 unsupported
    And 四個指定版本各有真實 probe artifact，未驗證不標為 unsupported 或 PASS
    And 測試結構清楚分層為 hermetic parser 測試與 live probe 測試

  @CTXWIN-002
  Scenario: Explicit provenance for token data
    Given 進行任務或 Stage 之 Context 評估
    When 模型、窗口限制或 token 用量未有精確後端回報數據
    Then 系統將其資料來源明確標示為 estimated 或 unknown
    And 絕不將字元換算之估算值標記為 backend 實測 token
    And 每個用量欄位可區分缺失 unknown 與後端明確回報的 0
    And 部分缺失、真實零值經 JSON 與 SQLite 往返後語意不變

  @CTXWIN-003
  Scenario: Dynamic stage-aware prompt evaluation
    Given 處於 Direct 或 Three-Face 管線執行流程
    When 評估各 Stage 之 Context 預算需求
    Then Oracle 依原始 Context 與 Planner 指令進行估算
    And Striker 依原始 Context 與實際產出之 OraclePlan 組合進行估算
    And Critic 依原始 Context、OraclePlan 與實際產出之 StrikerOutput 組合進行估算
    And 各 Stage 獨立執行准入檢查判斷
    And 估算輸入與 Backend.Run 實際收到的 request.Prompt 完全相同

  @CTXWIN-004
  Scenario: Preflight admission rejection
    Given 任務在某 Stage 評估超過核准之 Context 預算上限且啟用阻擋
    When Orchestrator 進行 Stage Admission 檢查
    Then 系統在調用該 Stage 後端前拒絕執行並記錄失敗原因
    And 該 Stage 之後端進程與模型呼叫次數為 0
    And 若 Striker 或 Critic 階段超量拒絕，已完成之 Stage Trace 與結果完整保留
    And 絕不自動更換 Backend 亦不重跑已完成之 Stage
    And 正式本機 run 與 daemon 入口皆可注入顯式上限及 reject 政策
    And 未知模型仍受顯式上限約束，observe 超量只警示，無可用上限才 bypass
    And 非法上限或政策在執行程序啟動時報錯

  @CTXWIN-005
  Scenario: Sealed context immutability
    Given 包含 Workspace Scope 與記憶快照之 Sealed ExecutionContext
    When 經過 Context 預算評估與審計流程
    Then 原始 Prompt、Memory 快照與 Scope 內容保持不可變
    And 系統絕不因預算限制私自裁剪或丟棄已密封之記憶內容
    And 絕不洩漏或注入跨工作區之 Context 資料

  @CTXWIN-006
  Scenario: Stage-level trace audit and backward compatibility
    Given 任務執行完成或在某 Stage 終止
    When 查詢 Task 與 PipelineTrace 審計資料
    Then 每個 Stage 均記錄獨立的估算值、實際觀測 usage 與資料來源
    And backend 失敗或 Oracle／Critic 結果解析失敗仍保留已取得的 usage
    And 歷史舊任務無 usage 欄位時保持相容可讀且不標為 0
    And 既有 SQLite 資料表與查詢保持向前向後相容

  @CTXWIN-007
  Scenario: Redelivery preserves context and admission
    Given 存在一筆已完成或已進行 Admission 決策之任務請求
    When 呼叫端以相同 Request ID 重送請求
    Then 系統返回既有任務資訊與決策結果
    And 不重跑估算或引發二次後端執行
    And 程序預算設定改變不覆寫已保存的 Admission 決策
    And 保持原密封上下文與 BackendID 不變

  @CTXWIN-008
  Scenario: Baseline regression safety
    Given 既有 Direct 與 Three-Face 模式任務
    When 在未啟用強制超量阻擋政策下執行
    Then 唯讀沙箱隔離、PGID 取消、工作區隊列等既有功能運作正常
    And MBACK-001 ～ MBACK-012、TFACE-001 ～ TFACE-009、CTXMEM-001 ～ CTXMEM-008 等回歸測試全部維持 PASS
```

## Gherkin 核准紀錄

- **核准 commit**: 1049dc5
- **核准來源**: 使用者於 2026-09-21 對話明確核准（原話：「1 核准\n2 無」）

| Scenario | 核准日期 | 狀態 |
|----------|---------|------|
| CTXWIN-001 | 2026-09-21 | 已核准 |
| CTXWIN-002 | 2026-09-21 | 已核准 |
| CTXWIN-003 | 2026-09-21 | 已核准 |
| CTXWIN-004 | 2026-09-21 | 已核准 |
| CTXWIN-005 | 2026-09-21 | 已核准 |
| CTXWIN-006 | 2026-09-21 | 已核准 |
| CTXWIN-007 | 2026-09-21 | 已核准 |
| CTXWIN-008 | 2026-09-21 | 已核准 |

## 重構步驟概要

1. ✅ **Phase 1** - 雙後端 Context 能力探測與 Token 正規化
2. ✅ **Phase 2** - Context 預算、估算器與模型目錄核心套件
3. ✅ **Phase 3** - Stage-Aware Prompt 組裝與准入引擎
4. ✅ **Phase 4** - Orchestrator 整合與 Fail-Closed 防禦
5. ✅ **Phase 5** - 持久化相容性與 Engine 審計串接
6. ✅ **Phase 6** - 外迴圈驗收與全系統回歸

## 風險與首要驗證

- **最大風險**：
  1. 誤把 CLI stdout/jsonl 粗估數值當成模型內部 Context 控制權，導致實作推測性功能（如動態裁剪或攔截失敗造成靜默損壞）。
  2. Three-Face 管線在後續 Stage 提示詞大幅膨脹時未及時捕捉，發生後端調用中途溢出或非預期崩潰。
  3. 實施 Context 限制時破壞 Sealed Context 不變性（例如為塞入窗口私自刪除記憶快照）或違反 No Automatic Failover（中途換後端重跑）。
- **風險等級與理由**：High — 涉及核心上下文模型假設、外部 CLI 事件契約、模型容量預檢與執行管線容錯語意。
- **首要驗證**：
  1. 對固定版本 OpenCode 與 Codex CLI 進行真實 Context Contract Probe 測試，確認其輸出之 JSONL 事件中 Token 回報精確度與原生邊界，明確標記 unsupported 項目。
  2. 建立 Hermetic 測試驗證 Three-Face 管線在 Striker / Critic 動態加入產物時能正確計算膨脹後 Context，並在超量時觸發 Fail-Closed 拒絕（0 calls），且完整保留前面已成功 Stage 的 Trace。
- **選擇理由**：能力探測能第一時間杜絕假裝擁有所謂「模型內部 context 編輯能力」的推測性設計；外迴圈准入測試則能確保在任何後端呼叫發生前阻斷溢出，杜絕副作用與 silent failover。
- **完成證據**：
  1. Probe 測試產出清晰的能力與限制矩陣，記錄於 `technical-analysis.md` 與專屬測試檔。
  2. BDD 測試證實超量 Stage 在 0 backend call 下乾淨中止，前序 Stage 產物與 Trace 完好儲存，且既有回歸測試網 100% 通過。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|------|------|------|------|
| TBD-1 | Gherkin CTXWIN-001 ～ CTXWIN-008 劇本確認與核准 | 已解決 | 使用者於 2026-09-21 明確核准全數 Gherkin 劇本 |
| TBD-2 | 預設 Context 超量政策之門檻數值與處置策略（觀測審計 vs 阻擋） | 已解決 | 使用者於 2026-09-21 確認無特殊調整，採建議預設 observe 政策與 20% 輸出保留空間 |

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-09-22 | PR #18 合併至 main（merge commit `8576dd0`），issue 結案 | Raybird |
| 2026-09-21 | 解決 PR #18 審查回饋：修復 OpenCode Scanner 大型事件遺失 usage 漏洞（擴大緩衝至 4MB 並顯式檢查 scanner.Err） | - |
| 2026-09-21 | 解決 PR #18 獨立審查回饋：修復 NaN 繞過、OpenCode nullable 聚合契約、live probe 產物與跳過語意、模型目錄與輸出保留空間基準釐清 | - |
| 2026-09-21 | 完成 Phase 1 至 Phase 6 全部實作，CTXWIN-001 ～ CTXWIN-008 與全系統回歸測試 100% 通過（全綠），更新文件待審查 | - |
| 2026-09-21 | 使用者核准 CTXWIN-001 ～ CTXWIN-008 全數驗收劇本並確認預設政策採 observe 與 20% 輸出保留空間 | - |
| 2026-09-21 | 依使用者要求修訂審查缺口：nullable 用量、Stage／adapter 資料流、設定與模型來源、live 證據；授權僅限文件修訂與提交，全數 Scenario 保持待核准 | - |
| 2026-09-21 | 建立 Issue 17 初始架構規範、需求分析、技術分析與實作計畫初稿 | - |

---
**建立日期**: 2026-09-21  
**分級**: Large  
**風險**: High  
**狀態**: 已完成 — PR #18 於 2026-09-22 合併（merge commit `8576dd0`）
