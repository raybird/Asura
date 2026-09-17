# Issue 13 - Phase 3C：Three-Face Fixed Pipeline（Oracle → Striker → Critic）

## 概述

承接 Phase 3B「專案記憶與 AI 任務 Context 整合」（Issue #11 / PR #12），在既有統一 Task / Daemon / Engine / Sealed `ExecutionContext` 基礎上，建立第一版固定式 Three-Face Pipeline：

```text
ExecutionContext
      │
      ▼
    Oracle
      │
      ▼
    Striker
      │
      ▼
    Critic
      │
      ▼
 Final Result
```

本期重點為確立三個角色的責任、資料契約、失敗語意、事件追蹤與持久化邊界，並以可量測方式比較「Direct（單一 Striker）」與「Three-Face（Oracle → Striker → Critic）」的執行流程、延遲與模型呼叫次數。

## 文件清單

- [requirement-analysis.md](./requirement-analysis.md)
- [technical-analysis.md](./technical-analysis.md)
- [implementation-plan.md](./implementation-plan.md)

## 快速導覽

### 當前流程（Phase 3B）
1. 使用者提交任務（可指定 Memory IDs）。
2. Daemon 驗證授權，解析記憶生成密封不可變之 `ExecutionContext`。
3. Engine 調用單一 `RunnerFunc`（Striker），直接由 Prompt Composer 組裝記憶與 Prompt 執行並產出結果。

### 重構後流程（Phase 3C）
1. 使用者提交任務時可指定執行模式（`direct` 或 `three-face`，預設向後相容 Direct）。
2. Daemon/Engine 統一保持 Sealed `ExecutionContext` 輸入不變。
3. 若為 `direct` 模式：
   - 僅執行既有 Striker 路徑，不啟動 Oracle 與 Critic，保持 100% 既有行為。
4. 若為 `three-face` 模式：
   - 嚴格固定執行順序：**Oracle → Striker → Critic**。
   - **Oracle**：消費 Sealed ExecutionContext，產出結構化 `OraclePlan`。若失敗則立即 fail-closed，終止後續 stage。
   - **Striker**：依 `ExecutionContext` + `OraclePlan` 執行副作用工作，產出執行結果。若失敗則終止 Critic。
   - **Critic**：審查 Striker 輸出是否符合需求與 Plan，產出結構化 `CriticReview`。若 Critic 失敗不得標記為完整成功。
   - **Finalization**：完整持久化 Pipeline Trace（包含各 stage 狀態、資料契約、耗時與模型呼叫計數），更新 Task 終態。

## 關鍵差異

| 項目 | 變更前 (Phase 3B) | 變更後 (Phase 3C) |
|---|---|---|
| **執行架構** | 單一 Runner 直通執行 | 支援 Direct 與固定 Three-Face Pipeline 兩種模式 |
| **角色協同** | 無角色拆分，Prompt 直接給 Runner | Oracle 結構化規劃 → Striker 執行 → Critic 審查 |
| **資料契約** | 僅 ExecutionContext | 新增結構化 `OraclePlan`、`CriticReview`、`PipelineExecution` |
| **失敗防禦** | 僅單一 Runner 執行失敗處理 | 嚴格 Fail-Closed（Oracle 失敗不啟動 Striker/Critic，Striker 失敗不啟動 Critic，Critic 失敗不假裝成功） |
| **執行審計** | 僅 Task Result 與 ErrorMessage | 完整保存 Pipeline Trace，各階段耗時、狀態與 ModelCalls 可量測可查詢 |
| **向後相容性** | N/A | Direct 模式維持既有 1 個 Striker 呼叫語意與全部契約 |

## 涉及檔案

```text
internal/pipeline/                             # 新增：Three-Face 管線調度核心
├── types.go                                   # 定義 ExecutionMode, OraclePlan, CriticReview, PipelineTrace
├── orchestrator.go                            # 實作 PipelineOrchestrator 固定順序調度與 Fail-Closed 邏輯
├── stage_oracle.go                            # Oracle Stage 定義與實作
├── stage_striker.go                           # Striker Stage 包裝既有 RunnerFunc
└── stage_critic.go                            # Critic Stage 定義與實作
internal/storage/
└── sqlite.go                                  # tasks 資料表擴充 execution_mode 與 pipeline_trace 欄位
internal/engine/
├── engine.go                                  # SubmitRequest 擴充 Mode，整合 Pipeline 調度
└── engine_test.go                             # 測試 Direct 與 Three-Face 執行路徑
internal/daemon/
├── daemon.go                                  # IPCRequest 擴充 Mode，Task Redelivery 冪等防禦與 Trace 傳遞
└── daemon_test.go                             # 測試 Three-Face 任務提交、查詢與 Redelivery
cmd/asura/
├── run.go                                     # 支援 --mode 選項
├── ask.go                                     # 支援 --mode 選項
└── run_test.go                                # CLI 參數解析測試
tests/integration/
└── three_face_pipeline_test.go                # 外迴圈端到端整合測試 (TFACE-001 ~ TFACE-010)
```

## Gherkin 驗收劇本

```gherkin
Feature: Three-Face Fixed Pipeline

  @TFACE-001
  Scenario: Three-Face 任務依固定順序完成
    Given 一個合法且已密封 ExecutionContext 的任務
    When 任務以 three-face mode 執行
    Then Oracle 只執行一次
    And Striker 在 Oracle 成功後只執行一次
    And Critic 在 Striker 成功後只執行一次
    And 執行順序嚴格為 Oracle -> Striker -> Critic
    And Task 最終完成且保存 pipeline trace

  @TFACE-002
  Scenario: Oracle Plan 為結構化且可追溯
    Given three-face mode 任務
    When Oracle 成功
    Then 產生結構化 OraclePlan
    And Striker 使用該 Plan 執行
    And 使用者查詢 Task 時可取得當時使用的 Plan

  @TFACE-003
  Scenario: 所有角色使用同一 ExecutionContext
    Given Task 已密封 Workspace、Prompt 與 Project Memory
    When Three-Face Pipeline 執行
    Then Oracle、Striker、Critic 使用同一 Task execution identity
    And 不重新解析最新 Memory
    And 不因 Memory 後續修改或刪除改變已接受 Task 的輸入

  @TFACE-004
  Scenario: Oracle 失敗時 Fail-Closed
    Given three-face mode 任務
    When Oracle 失敗
    Then Striker 不啟動
    And Critic 不啟動
    And Task 標記為 Failed
    And Oracle error 可追溯

  @TFACE-005
  Scenario: Striker 失敗不進行 Critic 或自動重試
    Given Oracle 已成功產生 Plan
    When Striker 執行失敗
    Then Critic 不啟動
    And Striker 不自動 retry
    And 保存 Oracle Plan、partial output 與 error
    And Task 標記為 Failed

  @TFACE-006
  Scenario: Critic 成功 Review Striker Output
    Given Oracle 與 Striker 成功
    When Critic 完成 review
    Then 保存 CriticReview
    And 可識別 verdict 與 findings
    And final result 與 Striker output / Critic review 的關係可追溯

  @TFACE-007
  Scenario: Critic 失敗不得假裝完整成功
    Given Oracle 與 Striker 已成功
    When Critic 本身執行失敗
    Then 保存既有 Striker output
    And Task 不得被標示為完整 Three-Face success
    And Critic failure 可查詢

  @TFACE-008
  Scenario: Direct mode 維持既有任務語意
    Given 使用者選擇 direct mode
    When 任務執行
    Then 不啟動 Oracle
    And 不啟動 Critic
    And 僅執行既有 Striker / Runner 路徑
    And Phase 3B 的 Prompt、Memory、Redelivery 契約保持不變

  @TFACE-009
  Scenario: Three-Face Redelivery 不造成重複執行
    Given 一個已接受或已完成的 three-face Task
    When 相同 Request ID 與相同 payload 被重送
    Then 回傳既有 Task
    And 不再次呼叫 Oracle、Striker 或 Critic
    And pipeline trace 保持與第一次執行一致

  @TFACE-010
  Scenario: Pipeline Metrics 可識別額外模型成本
    Given direct 與 three-face 任務各成功執行一次
    When 查詢 execution metadata
    Then direct 可識別為 1 個 Striker stage
    And three-face 可識別 Oracle、Striker、Critic 三個 stage
    And 可取得至少 stage call count 與 pipeline duration
```

## Gherkin 核准紀錄

- **核准 commit**: 待提交
- **核准來源**: 使用者於 2026-09-17 對話明確核准（原話：「好那就開始吧」）

| Scenario | 核准日期 | 狀態 |
|----------|---------|------|
| TFACE-001 | 2026-09-17 | 已核准 |
| TFACE-002 | 2026-09-17 | 已核准 |
| TFACE-003 | 2026-09-17 | 已核准 |
| TFACE-004 | 2026-09-17 | 已核准 |
| TFACE-005 | 2026-09-17 | 已核准 |
| TFACE-006 | 2026-09-17 | 已核准 |
| TFACE-007 | 2026-09-17 | 已核准 |
| TFACE-008 | 2026-09-17 | 已核准 |
| TFACE-009 | 2026-09-17 | 已核准 |
| TFACE-010 | 2026-09-17 | 已核准 |

## 重構步驟概要

1. ⏳ **Phase 1** - 契約定義與首要風險驗證（Outer-loop tests）
2. 📝 **Phase 2** - Pipeline Orchestrator 與 Stage 抽象實作
3. 📝 **Phase 3** - 儲存層 Trace 持久化與 Engine 整合
4. 📝 **Phase 4** - Daemon 調度、Redelivery 防禦與 Metrics 收集
5. 📝 **Phase 5** - CLI 整合、端到端整合測試與回歸驗證

## 風險與首要驗證

- **最大風險**：階段失敗時語意不明確（例如 silent fallback 到 Direct mode，使任務行為無法重現）以及過早把 Three-Face 邏輯侵入既有 Runner / Adapter（導致後續 Multi-Backend 無法抽換）。
- **風險等級與理由**：High — 涉及跨階段調度、模型呼叫與持久化邊界，若語意模糊或失敗未隔離，將造成不可逆副作用與審計漏洞。
- **首要驗證**：在建立真實模型呼叫前，先建立外迴圈管線調度測試（outer-loop tests），以 mock stages 驗證固定順序（Oracle → Striker → Critic）與 Fail-Closed 行為（Oracle 失敗拒絕執行 Striker、Striker 失敗不執行 Critic、Critic 失敗不標記為完整成功）。
- **選擇理由**：透過獨立的 Orchestrator 測試驗證固定順序與各 stage 的 fail-closed 語意，能以最小代價消除最大架構與安全未知，防止後續實作發生 silent fallback。
- **完成證據**：outer-loop 測試通過，確認 Oracle 失敗、Striker 失敗與 Critic 失敗均符合預期狀態，且無任何 silent fallback。

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-09-17 | 建立 Issue 13 架構規範文件並核准 10 項 Gherkin 劇本 | - |

---
**建立日期**: 2026-09-17  
**分級**: Large  
**風險**: High  
**狀態**: 進行中
