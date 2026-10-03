# Issue 37 — Stage-specific Runtime Binding

## 概述

2026-10-04 依 [GitHub #37](https://github.com/raybird/Asura/issues/37) 建立提案，使用者已核准。基準為 `origin/main` 的 Phase 4B merge `d3a40e6ead080d7ab74accf02aadba192aedb48e`。將 Three-Face 的 task runtime 提升為 admission 時一次封存的 stage bindings；重用能力 matcher 與 deterministic priority，execution 不重選。

## 快速導覽與關鍵差異

| 項目 | Phase 4B | Phase 4C 提案 |
|---|---|---|
| auto | requirements union 選一個 runtime | 同一值快照依各 stage requirements 選擇 |
| explicit | 一個 runtime 執行全部 stage | 每個必要 stage 明確綁定同一指定 runtime |
| execution truth | ExecutionContract.Runtime / BoundBackend | 新契約以 StageBindingPlan 為準；舊契約走原單一 runtime |
| Task.Backend | selected runtime | Striker runtime 的相容投影，不代表全部 stage |

## 文件清單

- [現況盤點](requirement-analysis.md)
- [技術提案與 SB-TBD 對照](technical-analysis.md)
- [實作計畫](implementation-plan.md)

## 涉及檔案

- `internal/backend/requirements.go`、`selection.go`、新增 `stage_binding.go` 與相關測試：資料模型、planner、validation。
- `internal/engine/engine.go`、`internal/daemon/daemon.go` 與相關測試：admission、intent、context 封存、queue/redelivery；重用 SQLite resolved_context，不新增 schema。
- `internal/pipeline/orchestrator.go`、`types.go`、`stage_oracle.go`、`stage_striker.go`、`stage_critic.go` 與相關測試：唯一 runtime 來源、逐階段 guard、trace。
- `cmd/asura/run.go`、`runtime_process.go`、`status.go` 與相關測試：local composition、status；既有 shell run 相容。
- `internal/web/page.go` 與相關 audit 測試：既有查詢頁呈現 stage 資訊。
- `tests/integration/three_face_pipeline_test.go`：跨層回歸。
- 根目錄 `README.md`、`docs/superpowers/specs/2026-09-11-asura-architecture-design.md`：交付時更新 CLI 契約與架構。
- 本 issue 文件：規格、計畫、證據與獨立 review。

adapter 能力宣告、Memory 演算法、Cron 政策、SQL schema 不在修改範圍。Runtime health、availability、fallback、retry、通用 workflow DSL、per-stage CLI override 均不包含。

## Gherkin 驗收劇本

```gherkin
Feature: admission 封存 stage runtime 並依原 plan 執行

  @STGBIND-001
  Scenario: Direct 相容
    Given 既有 Direct explicit/auto 請求及 Phase 4A/4B 已封存契約
    When 以 Phase 4C 接受新請求或執行舊 Task
    Then 選擇結果及單次 Striker 執行維持相容，舊契約不重新 admission

  @STGBIND-002
  Scenario: Three-Face mixed auto
    Given 候選 Codex 支援唯讀與寫入、OMP 僅支援寫入，priority 為 omp,codex
    When 提交 Three-Face auto
    Then 封存 Oracle=codex、Striker=omp、Critic=codex，實際 Run 依此順序與身分執行

  @STGBIND-003
  Scenario: 按階段篩選能力
    Given 候選 OMP 僅支援 workspace-write
    When 建立 Three-Face auto 計畫
    Then OMP 可列為 Striker eligible，且在 Oracle/Critic 均遭拒絕，不降低需求

  @STGBIND-004
  Scenario: 原子拒絕
    Given registry 僅含支援寫入、不支援唯讀的候選
    When 提交 Three-Face auto
    Then 整個請求在 Task 建立與派送前拒絕，model calls、runtime Run、workspace 執行副作用均為零

  @STGBIND-005
  Scenario: Explicit 單一 runtime
    Given 指定 opencode 且其能力符合三階段需求
    When 提交 Three-Face explicit
    Then 三階段均綁定 opencode，省略 backend 仍 explicit opencode

  @STGBIND-006
  Scenario: Explicit 能力不足
    Given 指定 OMP 且 registry 另有符合唯讀需求的 Codex
    When 提交 Three-Face explicit
    Then 整次拒絕且 Run 為零，不以 Codex 補位

  @STGBIND-007
  Scenario: Deterministic 計畫
    Given 相同已捕獲 descriptors（含 captured_at）、需求、priority 及 policy version
    When 重複建立計畫
    Then 序列化計畫完全相同，順序固定為 Direct 的 Striker 或 Three-Face 的 Oracle/Striker/Critic

  @STGBIND-008
  Scenario: 註冊順序無關
    Given 兩份 membership 相同而 insertion order 不同的 registry，使用相同固定 descriptors
    When 建立計畫
    Then 候選與階段的 canonical ordering 使計畫相同，時間戳不參與排名

  @STGBIND-009
  Scenario: 封存後不變
    Given Task 已封存 mixed plan
    When 改變 priority、加入候選或重排 registry 後執行 Task
    Then plan 與歷史版本快照不變，僅使用封存 runtime，不對目前候選重新選擇

  @STGBIND-010
  Scenario: 封存 runtime 消失
    Given queued Task 已封存 Striker=omp，但執行前 OMP 被移除
    When 喚醒執行
    Then 任何階段 Run 前即因必要 runtime 缺失拒絕，不 fallback，trace 記錄失敗 stage

  @STGBIND-011
  Scenario: 能力退化與版本語意
    Given 必要 runtime 在 admission 後失去該階段能力，或能力不變但版本更新
    When 執行該 Task
    Then 能力退化在該階段 Run 前拒絕且不 fallback；僅版本更新不因版本不同拒絕，歷史快照保持原值

  @STGBIND-012
  Scenario: 重送身分
    Given request R 已 admission 並保存 plan
    When 以相同 intent 重送，或同 request ID 改用另一 intent
    Then 相同請求回原 Task/plan 且不重新捕獲候選；不同 intent 拒絕，不重複 Run

  @STGBIND-013
  Scenario: 重啟持久化
    Given SQLite 保存含 mixed plan 的 pending/queued/running Task
    When 關閉重開 storage 並啟動 daemon
    Then plan 原樣可讀，沿既有 recovery 將殘留任務標 failed，不重新 selection 或 Run，也不自動續跑

  @STGBIND-014
  Scenario: Trace 與 audit
    Given 完成或 guard 拒絕的 mixed Three-Face Task
    When 讀取 trace、CLI status 與 Web audit
    Then 可區分 intent、各 stage 封存 runtime/version/requirements/decision、actual runtime 及 guard 結果，查詢不探測 runtime

  @STGBIND-015
  Scenario: 契約結構完整性與上限
    Given 封存 plan 的 ID/version/requirements/decision/policy/order 任一單欄被改動，或候選/JSON 超限
    When admission 或 execution validation
    Then 矛盾、缺漏、未知版本或超限 fail-closed，Run 為零；完整一致重寫不宣稱可被非密碼學驗證偵測

  @STGBIND-016
  Scenario: 完整回歸
    Given 完成 Phase 4C 交付工作區
    When 執行 go vet ./...、go test -count=1 -race ./...、go build -o /tmp/asura-issue37 ./cmd/asura
    Then 三者均成功，既有 Direct/Three-Face、四 runtime、context、memory/recall/utility/lifecycle/Git memory、Web/Cron、queue/redelivery/restart/cancel/status/trace/local/daemon 測試均無回歸
```

## Gherkin 核准紀錄

- **核准 commit**: d27ace3
- **核准來源**: 2026-10-04 使用者 `/dev-cycle issue:37` 授權盤點及準備提案。GitHub #37 的驗收為「候選驗收劇本」，明列先固定 technical analysis；未將 issue ID 視為整份新提案核准。2026-10-04 使用者回覆「核准」，核准技術提案 SB-TBD-1～15、完整 STGBIND-001～016、README TBD-1～4。

| Scenario | 核准日期 | 狀態 |
|---|---|---|
| STGBIND-001 | 2026-10-04 | 已核准 |
| STGBIND-002 | 2026-10-04 | 已核准 |
| STGBIND-003 | 2026-10-04 | 已核准 |
| STGBIND-004 | 2026-10-04 | 已核准 |
| STGBIND-005 | 2026-10-04 | 已核准 |
| STGBIND-006 | 2026-10-04 | 已核准 |
| STGBIND-007 | 2026-10-04 | 已核准 |
| STGBIND-008 | 2026-10-04 | 已核准 |
| STGBIND-009 | 2026-10-04 | 已核准 |
| STGBIND-010 | 2026-10-04 | 已核准 |
| STGBIND-011 | 2026-10-04 | 已核准 |
| STGBIND-012 | 2026-10-04 | 已核准 |
| STGBIND-013 | 2026-10-04 | 已核准 |
| STGBIND-014 | 2026-10-04 | 已核准 |
| STGBIND-015 | 2026-10-04 | 已核准 |
| STGBIND-016 | 2026-10-04 | 已核准 |

## 風險與首要驗證

- **最大風險**：Task.Backend、constructor backend 與 stage plan 形成雙重來源，使寫入 runtime 執行唯讀 stage，或在 execution 隱性重選。
- **風險等級與理由**：High；涉及工作區執行權限與不可回復的 runtime 寫入副作用。
- **首要驗證**：核准後先建立 daemon admission 與 pipeline 的負向整合紅燈：唯讀 stage 無候選、explicit OMP、缺失 sealed Striker、與 constructor backend 衝突的 plan。
- **選擇理由**：直接查核 admission/Run 邊界及安全能力，比 UI 垂直切片更能降低雙重來源風險；純 planner 測試不足以證明沒有派送副作用。
- **完成證據**：Task 數、dispatch/Run/model 計數、workspace sentinel、sealed plan 與 trace 均有獨立斷言；失敗情境必須實際出現紅燈，之後以同組測試取得綠燈。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|---|---|---|---|
| TBD-1 | 核准技術提案 SB-TBD-1～15 與 STGBIND-001～016 | 已解決 | 2026-10-04 使用者「核准」，依計畫實作 |
| TBD-2 | 接受 mixed fixture 與 atomic failure 前提修正 | 已解決 | STGBIND-002/004：共同 policy 下 Oracle/Critic 不可能 eligibility 不同 |
| TBD-3 | 保留 restart 標 failed；完整性檢查不承諾偵測一致的全契約重寫 | 已解決 | STGBIND-013/015；不新增自動復跑或密碼學簽章 |
| TBD-4 | plan 上限 3 bindings、32 candidates/decision、64 KiB/decision、192 KiB/plan | 已解決 | STGBIND-015；新 plan 的合計上限須核准 |

## Timeline

| 日期 | 異動 | 負責人 |
|---|---|---|
| 2026-10-04 | 使用者「核准」完整提案與驗收 | 使用者 |
| 2026-10-04 | 完成 stage contract、pipeline guards、queue/restart/audit 驗收，最終 race/vet/build 全綠 | Codex |
| 2026-10-04 | 盤點 Phase 4B，建立待核准規格與 SB-TBD 提案；未修改 production code | Codex |

---

**建立日期**: 2026-10-04
**分級**: Large — 跨 admission、pipeline、persistence 與 audit 邊界
**風險**: High
**狀態**: 實作與完整驗證完成，待 PR 獨立審查
