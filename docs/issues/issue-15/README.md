# Issue 15 - Phase 3D：Multi-Backend Foundation（OpenCode + Codex CLI）

## 概述

承接 Phase 3C「Three-Face Fixed Pipeline」（Issue #13 / PR #14），在既有 Task / ExecutionContext / Pipeline Stage 契約上，建立第一版真正可替換的 Backend 層，讓 Asura 不再把執行能力等同於 OpenCode。

第一版目標：
- 定義可被不同 CLI / Agent backend 實作的共同契約（`internal/backend`）。
- 保留既有 OpenCode 行為與安全邊界。
- 接入第二個真實 backend：Codex CLI（固定版本 `codex-cli 0.155.0`）。
- 呼叫端可明確指定 backend（`asura ask --backend opencode|codex`），預設向後相容 `opencode`。
- Backend identity、version、capability、event、cancel、failure 與 result 語意完全可追溯。
- 相同 Request ID 的 redelivery 保持相同 backend execution identity。

## 文件清單

- [requirement-analysis.md](./requirement-analysis.md)
- [technical-analysis.md](./technical-analysis.md)
- [implementation-plan.md](./implementation-plan.md)

## 快速導覽

### 當前流程（Phase 3C）
1. Engine 與 Pipeline 直接依賴 OpenCode adapter 與薄介面 `RunnerFunc func(ctx, workspace, prompt) (string, error)`。
2. Pipeline stages（Oracle、Striker、Critic）直接接收 `RunnerFunc`，無法識別後端 identity、version、capability 與細緻 failure classification。
3. Request fingerprint 與 Task trace 未記錄 BackendID，預設假定執行器恆為 OpenCode。

### 重構後流程（Phase 3D）
1. 使用者提交任務時可選擇 `--backend`（`opencode` 或 `codex`，未指定預設 `opencode`）。
2. BackendID 被 sealed 進入 `ExecutionContext` 與 Request Fingerprint。
3. Pipeline Orchestrator 透過 `BackendRegistry` 取得指定後端，在執行前執行 Capability Preflight。
4. Stages 透過統一 `Backend.Run(ctx, request)` 執行，獲得標準化 `BackendResult`、`BackendEvent` 與 Failure Classification。
5. 任務取消、排程喚醒與 redelivery 均嚴格鎖定同一 BackendID，不發生自動切換（No Automatic Failover）。
6. Pipeline Trace 完整記錄各階段實際使用的 BackendID 與版本。

## 關鍵差異

| 項目 | 變更前 (Phase 3C) | 變更後 (Phase 3D) |
|---|---|---|
| **後端架構** | OpenCode 深度耦合，Stage 依賴 `RunnerFunc` 薄介面 | 共同 `internal/backend` 契約與 `BackendRegistry` |
| **支援後端** | 僅 OpenCode | OpenCode + Codex CLI 雙後端驗證 |
| **後端識別** | 無 BackendID，未進 Fingerprint 與 Task | BackendID 納入 Fingerprint、Task、Trace、Status |
| **能力預檢** | 隱式假設 OpenCode 支援唯讀隔離 | Explicit Preflight，不支援時 fail-closed（0 calls） |
| **錯誤分類** | 依賴字串比對或裸 error | 結構化 Failure Classification（Auth, RateLimit, Exec 等） |
| **容錯語意** | 單一後端失敗直接失敗 | Fail-Closed：嚴格禁止 partial execution 後切換後端重跑 |
| **向後相容** | N/A | 預設 backend=opencode，完全保持 Phase 3C 行為 |

## 涉及檔案

```text
internal/
├── backend/
│   ├── backend.go           # 新增：共同 Backend、Registry、Event、Failure 等契約定義
│   ├── backend_test.go      # 新增：共同契約與 Registry 測試
│   ├── opencode/
│   │   ├── adapter.go       # 修改：實作共同 Backend 介面，封裝既有安全設定
│   │   └── adapter_test.go  # 修改：驗證符合共同 Backend 契約
│   └── codex/
│       ├── adapter.go       # 新增：Codex CLI adapter 實作
│       ├── adapter_test.go  # 新增：Codex adapter 單元與 probe 測試
│       ├── parser.go        # 新增：Codex jsonl 事件正規化
│       └── parser_test.go   # 新增：Codex 事件解析測試
├── pipeline/
│   ├── orchestrator.go      # 修改：透過 Backend 契約執行並記錄 BackendTrace
│   ├── orchestrator_test.go # 修改：管線調度測試
│   ├── stage_oracle.go      # 修改：呼叫 Backend 傳遞 RoleOracle
│   ├── stage_striker.go     # 修改：呼叫 Backend 傳遞 RoleStriker
│   ├── stage_critic.go      # 修改：呼叫 Backend 傳遞 RoleCritic
│   └── types.go             # 修改：StageTrace 擴充 BackendID 與 Version
├── engine/
│   ├── engine.go            # 修改：SubmitRequest 支援 Backend，解耦 opencode 依賴
│   ├── engine_test.go       # 修改：Fingerprint、Redelivery 測試
│   └── cancel.go            # 修改：共同 Cancel 契約
├── daemon/
│   ├── daemon.go            # 修改：狀態查詢顯示 BackendID
│   └── workspace_queue.go   # 修改：喚醒時保留 BackendID
cmd/
└── asura/
    └── main.go              # 修改：CLI 增加 --backend 參數
```

## Gherkin 驗收劇本

```gherkin
Feature: Multi-Backend Foundation (OpenCode + Codex CLI)

  @MBACK-001
  Scenario: Default OpenCode backward compatibility
    Given 呼叫端提交任務且未指定 BackendID
    When Daemon 接收並調度執行
    Then 系統自動選用 opencode 作為執行後端
    And 既有 Direct 與 Three-Face 模式行為與安全限制保持相容
    And 產出之 Task 與 Trace 明確記錄 BackendID 為 opencode

  @MBACK-002
  Scenario: Explicit Codex selection
    Given 呼叫端提交任務並指定 backend 為 codex
    When Pipeline 執行該任務
    Then 僅透過 Codex adapter 執行進程
    And OpenCode 執行器呼叫次數為 0
    And 執行結果正規化為共同 BackendResult

  @MBACK-003
  Scenario: Unknown backend fail-closed
    Given 呼叫端提交任務並指定未註冊之 backend 名稱 "unknown-backend"
    When 系統進行前置檢查
    Then 系統在任何後端進程啟動前拒絕請求並返回錯誤
    And 後端模型與進程呼叫次數為 0
    And 絕不 fallback 至預設後端

  @MBACK-004
  Scenario: Backend identity in fingerprint
    Given 存在一筆已提交之任務請求
    When 呼叫端以相同 Request ID、Workspace、Prompt、Mode 但不同 BackendID 重送
    Then 系統將其判定為 Payload 衝突並拒絕
    And 不覆蓋既有任務亦不執行新後端

  @MBACK-005
  Scenario: Redelivery preserves backend identity
    Given 存在一筆正在執行或已完成之任務
    When 呼叫端以完全相同之 Request ID 與相同 BackendID 重送請求
    Then 系統返回既有 Task 資訊
    And 不重複調用後端執行器
    And 不重新選擇後端

  @MBACK-006
  Scenario: Common failure classification
    Given OpenCode 與 Codex 分別遇到認證失敗、速率限制、非零退出或空結果
    When Adapter 處理錯誤輸出
    Then 兩種後端之錯誤均映射為標準 FailureClassification
    And 保留可取得之 partial output
    And 不依賴上層解析後端特定字串

  @MBACK-007
  Scenario: Common cancellation contract
    Given 任務正在指定後端（OpenCode 或 Codex）中執行
    When 呼叫端發出取消請求
    Then 被選定後端之 context 被取消
    And 其進程樹被確實終止回收
    And 任務進入標準 Cancellation 終態並保存已產生之 partial output

  @MBACK-008
  Scenario: Three-Face capability preflight
    Given 呼叫端指定之後端不支援 Oracle 或 Critic 所需之唯讀分析安全能力
    When Three-Face 管線啟動前置檢查
    Then 管線在 Oracle 啟動前 fail-closed
    And 所有 stage 模型呼叫次數為 0
    And 絕不 fallback 至 OpenCode

  @MBACK-009
  Scenario: Pipeline trace records actual backend
    Given 任務在 Direct 或 Three-Face 模式下執行完成
    When 查詢 PipelineTrace 審計歷程
    Then 每個實際執行之 stage 均記錄實際使用的 BackendID 與版本號
    And 保留各 stage 耗時與狀態

  @MBACK-010
  Scenario: No failover after partial execution
    Given 指定 backend=codex 之任務在執行部分階段或步驟後失敗
    When 系統處理失敗事件
    Then 任務標記為 Failed 並保存 partial output
    And OpenCode 呼叫次數保持為 0
    And 絕不切換至另一後端重新嘗試

  @MBACK-011
  Scenario: Event normalization
    Given 後端（OpenCode 或 Codex）在執行期間產生原生事件流
    When Adapter 接收輸出並解析
    Then 原生事件被標準化映射為共同之 BackendEvent
    And Pipeline 與 Engine 無需識別特定後端之原始格式

  @MBACK-012
  Scenario: Queue wake preserves backend
    Given 指定 backend=codex 之任務因工作區排隊而進入 Queued 狀態
    When 排程器喚醒並調度該任務
    Then 該任務依然使用 codex 執行
    And 不重新套用預設值 opencode
    And 其 request fingerprint 與 trace 保持一致
```

## Gherkin 核准紀錄

- **核准 commit**: 37a2084
- **核准來源**: 使用者於 2026-09-18 對話明確核准（原話：「核准」）

| Scenario | 核准日期 | 狀態 |
|----------|---------|------|
| MBACK-001 | 2026-09-18 | 已核准 |
| MBACK-002 | 2026-09-18 | 已核准 |
| MBACK-003 | 2026-09-18 | 已核准 |
| MBACK-004 | 2026-09-18 | 已核准 |
| MBACK-005 | 2026-09-18 | 已核准 |
| MBACK-006 | 2026-09-18 | 已核准 |
| MBACK-007 | 2026-09-18 | 已核准 |
| MBACK-008 | 2026-09-18 | 已核准 |
| MBACK-009 | 2026-09-18 | 已核准 |
| MBACK-010 | 2026-09-18 | 已核准 |
| MBACK-011 | 2026-09-18 | 已核准 |
| MBACK-012 | 2026-09-18 | 已核准 |

## 重構步驟概要

1. ✅ **Phase 1** - 現有契約審計與 Codex CLI 合約探測（Contract Probe）
2. 📝 **Phase 2** - 建立 `internal/backend` 共同合約（Types, Registry, Errors, Events）
3. 📝 **Phase 3** - 遷移 OpenCode Adapter 至共同合約（保留 Phase 3C 安全邊界）
4. 📝 **Phase 4** - 實作 Codex CLI Adapter（支援 non-interactive, jsonl, sandbox, cancel）
5. 📝 **Phase 5** - Pipeline Orchestrator 解耦與 Capability Preflight 整合
6. 📝 **Phase 6** - Engine 整合、SubmitRequest、Fingerprint、Task/Trace 持久化
7. 📝 **Phase 7** - CLI 整合（`--backend` 旗標）與 Daemon 佇列喚醒保護
8. 📝 **Phase 8** - 外迴圈測試、MBACK-001 ～ MBACK-012 驗收與回歸驗證

## 風險與首要驗證

- **最大風險**：
  1. Codex CLI 執行語意與 OpenCode 假設不一致（例如進程樹回收、sandbox 唯讀隔離、事件串流），導致退化成粗糙的字串執行介面。
  2. 失敗時發生 Silent Failover，在副作用執行後自動換後端重跑，造成不可逆損害。
  3. 后端能力（Capability）假裝一致，使未提供隔離能力的後端被誤用於 Oracle/Critic。
- **風險等級與理由**：High — 涉及跨進程調度、外部 CLI 工具交互、安全邊界控制及不可逆副作用防禦。
- **首要驗證**：
  1. 針對本機固定版本 `codex-cli 0.155.0` 進行真實探測（Contract Probe），驗證 non-interactive invocation (`codex exec`)、working directory (`-C`)、sandbox 模式 (`-s read-only` / `-s workspace-write`)、jsonl 事件輸出 (`--json`) 與 context cancellation。
  2. 建立 outer-loop 單元與管線測試，驗證未知後端 fail-closed、能力不符 fail-closed 以及執行中失敗絕不自動容錯（0 calls to alternative backend）。
- **選擇理由**：唯有先取得真實 Codex CLI 的具體行為證據，才能確定共同合約的形狀與安全能力宣告，避免猜測 CLI 行為所產生的重工；外迴圈測試則能第一時間阻斷 silent failover。
- **完成證據**：
  1. Probe 測試輸出確認 Codex CLI 支援所需參數與行為，記錄於 `technical-analysis.md`。
  2. Outer-loop 測試通過，證實未註冊後端在 0 process 下立即拒絕，且 partial execution 失敗後絕無第二次 execution attempt。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|------|------|------|------|
| TBD-1 | Gherkin MBACK-001 ～ MBACK-012 劇本確認與核准 | 已解決 | 使用者於 2026-09-18 明確核准全數劇本 |
| TBD-2 | Codex CLI 執行時憑證環境變數傳遞與預設 model 政策 | 待確認 | 關聯 Phase 4 Codex Adapter 實作細節，不阻礙架構合約建立 |

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-09-18 | 建立 Issue 15 架構規範、需求分析、技術分析與實作計畫初稿 | - |
| 2026-09-18 | 使用者核准 MBACK-001 ～ MBACK-012 全部驗收劇本 | - |

---
**建立日期**: 2026-09-18  
**分級**: Large  
**風險**: High  
**狀態**: 待實作
