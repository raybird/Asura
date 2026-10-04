# Issue 41 — Control Plane Execution Audit

## 概述

來源：[GitHub #41](https://github.com/raybird/Asura/issues/41)，Phase 4E Execution Provenance & Decision Chain。2026-10-04 依使用者 `/dev-cycle issue:41` 建立技術提案；2026-10-04 使用者回覆「核准」，授權 EA-TBD-1～16、EXAUD-001～019 及 TBD-1～4；基準 main `2f583c7f257fa9fd3b974ff94f5bbd82c787e4b5`，PR #40 已於 2026-10-04 合併。

建立 bounded、machine-readable、persisted audit，說明 intent → admission → sealed binding → guard → observed execution → terminal。ExecutionContract / StageBindingPlan 仍是 execution truth，Task lifecycle 仍有終態宣告權。提案採 immutable admission header + append-only events + read-only fold，不引入 routing、retry、failover、federation、memory 新功能或 metrics 平台。

## 文件清單

- [需求與現況分析](./requirement-analysis.md)
- [EA-TBD-1～16 技術提案](./technical-analysis.md)
- [唯一實作計畫](./implementation-plan.md)

## 涉及檔案

```text
internal/
├── audit/                 新增安全值模型、正規化、fold、validation 與測試
├── backend/               必要 typed failure/Run observation；不改 routing 或四 adapter 策略
├── pipeline/              orchestrator、stage_binding、types、stage_* 與測試
├── storage/               audit tables/migration、CAS/recovery 與既有 recall 交易接線
├── engine/                engine、cancel 與測試：Task lifecycle 與 recorder 接線
├── daemon/                daemon 與測試：admission/cron/queue/query/restart
├── web/                   server、page 與測試：最小 audit projection
└── memory/recall/gitmem/   不修改產品行為
cmd/asura/                 新增 audit 命令、run 命令入口與相關測試
README.md                  使用方式；實作完成才更新
docs/superpowers/specs/2026-09-11-asura-architecture-design.md
                           persistence/truth boundary；實作完成才更新
docs/issues/issue-41/       規格、計畫與後續真實證據
```

storage/sqlite_utility.go 的變更僅為 Task + recall + audit 同交易，不變更 recall/utility 策略。跨模組的原因是 admission、Run、Task 終態與讀取介面分屬上述邊界，必須共同保留可追溯歷史。

## Gherkin 驗收劇本

以下保留來源 EXAUD-001～016，補上提案必要的故障與分層驗收 EXAUD-017～019；全部於 2026-10-04 核准。

```gherkin
Feature: 有界且可信的 Control Plane execution provenance

  @EXAUD-001
  Scenario: Direct completed
    Given Direct 任務的 admission 已封存 Striker runtime
    When guard 通過且 runtime 與 Task 完成
    Then persisted audit 可重建 intent、admission、binding、guard、Run 與 terminal
    And stage completed 與 Task completed 各有獨立的觀測依據

  @EXAUD-002
  Scenario: Three-Face mixed execution
    Given 封存的 Oracle/Striker/Critic 綁定為 Codex/OpenCode/Codex
    When 三個 stage 依序完成
    Then audit 固定按 Oracle/Striker/Critic 排序顯示 sealed 與 observed runtime/version
    And guard 與 outcome 完整且不依賴查詢時的 registry 或 priority

  @EXAUD-003
  Scenario: Availability exclusion
    Given admission inventory 的 OMP unavailable 且 Codex 可用又滿足能力
    When auto admission 接受任務
    Then persisted admission 摘要區分 OMP availability exclusion 與後續 selection
    And 能以封存的 policy、priority、candidates 解釋選擇 Codex

  @EXAUD-004
  Scenario: Capability rejection
    Given runtime availability 通過但缺少該 stage 的必要能力
    When admission 接受另一個相容 runtime
    Then audit 分別呈現 availability pass 與 capability reject 及缺失能力 enum

  @EXAUD-005
  Scenario: Explicit unavailable rejection
    Given explicit runtime 已註冊但不可用
    When submission 被拒絕
    Then 即時回應提供 bounded admission rejection 摘要與 correlation digest
    And Task 與 persisted task audit 均不建立且 Run 為零

  @EXAUD-006
  Scenario: Guard failure before Run
    Given 已封存 runtime 在執行前消失或不可用
    When 執行 guard
    Then audit guard failed 且 reason 為 runtime_missing 或 runtime_unavailable
    And Run 為零且不重新選擇或 fallback

  @EXAUD-007
  Scenario: Identity mismatch
    Given guard 取得的 runtime identity 不符合 sealed binding
    When guard 查核 identity
    Then audit 保留 sealed identity 與 mismatch 的安全觀測
    And guard failed/identity_mismatch 且 actual Run 未發生

  @EXAUD-008
  Scenario: Runtime failure
    Given guard passed 且 Run 被呼叫
    When runtime 返回失敗
    Then stage failed 與 Task failed 的 failure layer 明確為 runtime
    And audit 不把失敗誤分類為 admission 或 guard，也不保存任意 error 內容

  @EXAUD-009
  Scenario Outline: Cancellation observations
    Given 任務處於 <phase>
    When 使用者取消並由既有協調器確認執行資源已回收
    Then audit 記錄取消 phase 且 terminal 與 Task 一致
    And 不 fallback、不新增假 completed、已完成 stage 不被改寫且 terminal event 唯一
    Examples:
      | phase         |
      | before_stage  |
      | during_guard  |
      | during_run    |

  @EXAUD-010
  Scenario: Crash and restart interruption
    Given Oracle completed 已保存且 Striker 的 run intent 已保存但尚未返回
    When 程序中斷並重新開啟資料庫執行 recovery
    Then Oracle 保持 completed、Striker interrupted、Critic not_started/not_reached
    And Task 沿既有規則 failed，audit partial 且不 resume 或補造 execution

  @EXAUD-011
  Scenario: Accepted request redelivery and event idempotency
    Given request 已接受且原 Task 與 provenance 已保存
    When 相同 request 重送或相同 observation 重複 append
    Then 返回原 Task 與 audit 且不重新 admission/probe/select/Run
    And event 不重複，不同 payload 使用同一 event identity 時明確 conflict

  @EXAUD-012
  Scenario: Read-only authorized query
    Given 有已保存 audit 的 Task 與有權限的查詢者
    When 透過 Store、IPC、CLI 或 HTTP 讀取 audit
    Then probe、selection、Run 與 audit mutation 計數全部為零
    And 無權限查詢被拒絕且不洩露 audit

  @EXAUD-013
  Scenario: Legacy task completeness
    Given 升級前的 capability-v1、stage-binding-v1 或 runtime-inventory-v1 任務無新 audit header
    When 讀取 audit
    Then 安全呈現 legacy 與可驗證的 sealed 摘要
    And 不根據 trace 偽造 historical events，不 backfill 或 probe

  @EXAUD-014
  Scenario: Bounds and secret safety
    Given runtime/guard 提供含敏感 sentinel 的巨大 error、output 或非法 descriptor
    When 產生、保存或透過各 surface 讀取 audit
    Then sentinel 不出現在新增 audit persistence、IPC、HTTP、CLI 或 Web projection
    And count/payload/diagnostic/total/query 均遵守 EA-TBD-15，超界回 bounded failure

  @EXAUD-015
  Scenario: Structural tamper
    Given audit 的 seq、order、stage identity、refs 或 contract digest 被意外修改
    When 讀取 audit
    Then 明確回 invalid 或 unavailable 且不顯示可信 completed history
    And 不宣稱 cryptographic non-repudiation

  @EXAUD-016
  Scenario: Full regression
    Given 本期實作與證據已提交
    When 執行 go vet ./...、go test -count=1 -race ./... 與 go build ./cmd/asura
    Then 三者皆通過且 Phase 4A～4D 與既有功能無 regression

  @EXAUD-017
  Scenario: Authoritative persistence failure
    Given admission、observation、terminal 或 recovery 交易被故障注入
    When 相應交易寫入失敗
    Then admission 的 Task/recall/header 全 rollback 且不 Run
    And observation 失敗不啟動後續 Run/stage，terminal 不宣稱已保存
    And recovery 失敗不開始 daemon 派送且不損壞原 durable prefix

  @EXAUD-018
  Scenario: Stage outcome differs from Task terminal outcome
    Given Run 已返回但 stage 解析失敗、Critic 判定失敗或 engine 偵測空輸出
    When 產生 terminal audit
    Then runtime、stage processing、critic verdict 與 Task validation failure 可區分
    And Task final state 與 completed stage count 均符合各自真實 lifecycle

  @EXAUD-019
  Scenario: Honest invocation evidence
    Given stage 記錄 run intent 後可能在 Run 前或返回前中斷
    When 查看 call summary 與 actual runtime
    Then intent 與 confirmed return 分開且未確認部分顯示 unknown/incomplete
    And 不把 pipeline ModelCalls 當 provider 模型呼叫次數
```

## Gherkin 核准紀錄

- **核准 commit**: 6adb11c — 使用者核准的提案規格基線；核准紀錄由本次提交保存。
- **需求來源**: GitHub #41（2026-10-04 建立）R1～R18 與 EXAUD-001～016；本次使用者 `/dev-cycle issue:41` 授權調查與準備規格，沒有定案 EA-TBD 的原話。
- **核准來源**: 2026-10-04 使用者回覆「核准」，承接前一回合完整提案及驗收規格的核准問題。

| Scenario | 核准日期 | 狀態 |
|---|---|---|
| EXAUD-001 | 2026-10-04 | 已核准 |
| EXAUD-002 | 2026-10-04 | 已核准 |
| EXAUD-003 | 2026-10-04 | 已核准 |
| EXAUD-004 | 2026-10-04 | 已核准 |
| EXAUD-005 | 2026-10-04 | 已核准 |
| EXAUD-006 | 2026-10-04 | 已核准 |
| EXAUD-007 | 2026-10-04 | 已核准 |
| EXAUD-008 | 2026-10-04 | 已核准 |
| EXAUD-009 | 2026-10-04 | 已核准 |
| EXAUD-010 | 2026-10-04 | 已核准 |
| EXAUD-011 | 2026-10-04 | 已核准 |
| EXAUD-012 | 2026-10-04 | 已核准 |
| EXAUD-013 | 2026-10-04 | 已核准 |
| EXAUD-014 | 2026-10-04 | 已核准 |
| EXAUD-015 | 2026-10-04 | 已核准 |
| EXAUD-016 | 2026-10-04 | 已核准 |
| EXAUD-017 | 2026-10-04 | 已核准 |
| EXAUD-018 | 2026-10-04 | 已核准 |
| EXAUD-019 | 2026-10-04 | 已核准 |

## 風險與首要驗證

- **最大風險**：audit 與 Task／sealed contract 形成雙重真相；storage failure、cancel/crash 競態產生假完成，或新增保存洩露秘密。
- **風險等級與理由**：High；涉及持久化歷史可信度、安全資料與跨模組生命週期。
- **首要驗證**：核准後先以真 SQLite transaction/CAS 故障注入與 crash boundary characterization，驗證 Task + recall + header 原子性、terminal 唯一與 durable prefix 完整度；typed allowlist fixture 先驗證敏感資料不進模型。
- **選擇理由**：原子性與既有 lifecycle 的交互作用是最大未知；交易／競態對照實驗比先做 happy-path UI 垂直切片更直接。
- **完成證據**：已知成功／刻意失敗兩組控制，檢查 DB rows、Task state、events、Run counter；故障不留下孤立 header/recall、雙 terminal 或未觀測 completed，secret sentinel absent。保留命令與實際輸出，不以紀錄筆數單獨驗收。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|---|---|---|---|
| TBD-1 | 核准 EA-TBD-1～16 的完整模型、bounded normalization、schema/bounds/integrity 與 API 提案 | 已解決：2026-10-04 使用者核准 | EXAUD-001～019，全部實作相依 |
| TBD-2 | rejection 僅即時返回，不新增 persisted AdmissionAudit 或 rejected Task | 已解決：2026-10-04 使用者核准 | EXAUD-005、011；保存範圍與拒絕重送語意 |
| TBD-3 | audit 寫入採 fail-closed：未能保存觀測不啟動後續副作用；Task terminal 與 event 同交易，recovery 失敗停止 startup | 已解決：2026-10-04 使用者核准 | EXAUD-006～011、017；成功／失敗行為 |
| TBD-4 | crash/legacy 用 partial/legacy；run intent 不等於精確呼叫，provider model calls 可 unknown；保留既有 trace 診斷 | 已解決：2026-10-04 使用者核准 | EXAUD-010、013、018、019；完整度與使用者呈現 |

## 本次規劃驗證

2026-10-04：純文件與唯讀程式盤點；`python3 docs/issues/issue-41/evidence/check_planning.py` exit 0：四份文件、本地連結存在、19 個唯一 Scenario 與核准表集合相等、EA-TBD-1～16 與 R1～R18 全覆蓋、六階段均待核准。暫存後 `git diff --cached --check` exit 0。沒有執行 audit 或 regression 測試，不宣稱實作／獨立審查完成。

## Timeline

| 日期 | 異動 | 負責人 |
|---|---|---|
| 2026-10-04 | 使用者「核准」完整提案、驗收與四項政策，開始實作 | 使用者 |
| 2026-10-04 | 依 GitHub #41 盤點已合併 Phase 4D，建立四份規劃文件與 EA-TBD 提案，等待安全／失敗政策及驗收核准 | Codex |

---
**建立日期**: 2026-10-04

**分級**: Large — backend、pipeline、storage、engine、daemon、CLI/Web 與新 SQLite schema

**風險**: High — provenance 原子性、crash/cancel 一致性與秘密保存邊界

**狀態**: 修正與完整驗證完成，等待新固定 HEAD 的獨立重審判定；PASS 後等待合併

2026-10-04：實作證據與逐項 EXAUD 映射見 [verification.md](./evidence/verification.md)；核准 Gherkin 保持不變。
