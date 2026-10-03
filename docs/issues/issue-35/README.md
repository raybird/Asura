# Issue 35 — Phase 4B：Capability-aware Runtime Selection

## 概述

來源：[GitHub #35](https://github.com/raybird/Asura/issues/35)。2026-10-03 確認 PR #34 已合併，基準為 `37e034b667700a7ced3f22919fb26b2a0d824a6c`。

本期加入明確 opt-in 的能力感知選擇：共同 matcher 篩選 eligible runtimes，再由 deterministic policy 選出一個 Task-level runtime。explicit backend 維持 authoritative，Three-Face 三個 stage 共用同一 binding。保存接受時決策，歷史查詢與重送不重選。

不含 health/load/cost/intelligence routing、fallback、retry、mixed-stage binding、discovery 或 Runtime Management UI。Issue 原文的驗收為「候選」；2026-10-03 使用者「提案也核准」後，RTSEL-001～015 與 RS-TBD 全部成為實作契約。

## 文件清單

- [需求盤點](requirement-analysis.md)
- [技術提案及 RS-TBD 對照](technical-analysis.md)
- [實作計畫（唯一任務來源）](implementation-plan.md)

## 關鍵差異

| 入口 | Phase 4A | Phase 4B 提案 |
|---|---|---|
| explicit／省略 backend | 指定 runtime；local opencode/codex run 為 shell | 保持既有語意 |
| ask/run backend=auto | 不支援 | matcher → 固定 policy → 一個 runtime |
| status/trace/Web audit | capability contract | 加上 sealed selection decision |

## 涉及檔案

```text
internal/backend/registry.go、requirements.go、selection.go（新增）及對應測試
internal/backend/codex/adapter.go、opencode/adapter.go（local auto 啟動取消 handshake）
internal/engine/engine.go、internal/daemon/daemon.go 及契約／重送測試
internal/pipeline/orchestrator.go、types.go 及 Three-Face 契約測試
internal/storage/ 既有 SQLite 持久化相容性測試（不新增 schema）
internal/web/server.go、page.go 及 submission/audit 測試
cmd/asura/ask.go、run.go、runtime_process.go（新增）、status.go 及 CLI 測試
README.md、docs/superpowers/specs/2026-09-11-asura-architecture-design.md
docs/issues/issue-35/ 規劃、驗證及審查證據
```

以上跨 backend、admission、pipeline 與 presentation 的原因是 selection intent 必須在接受、執行、持久化及查詢間一致。adapter 能力宣告、Memory/Recall/UFL/Lifecycle、Git memory、Cron 政策與權限規則不擴充；以回歸測試保護。

## Gherkin 驗收劇本

```gherkin
Feature: deterministic capability-aware runtime selection

  @RTSEL-001
  Scenario: explicit 與既有 shell run 相容
    Given Phase 4A 的 explicit 與省略 backend 請求
    When 升級後執行相同請求
    Then backend 與既有執行行為保持相容
    And local opencode/codex run 仍執行 shell 且不偽造 runtime snapshot

  @RTSEL-002
  Scenario: 能力篩選
    Given registered runtimes 只有部分滿足 requirements
    When 明確要求 auto
    Then 只有符合共同 matcher 的候選進入 eligible set
    And rejected 候選保存 required available missing 與身分

  @RTSEL-003
  Scenario: 固定選擇
    Given 相同 requirements registry snapshot 有效 priority 與 policy version
    When 重複選擇
    Then 預設優先 opencode 其餘以 ID 字典序排序且 omp 最後
    And 使用者指定排序時只在 eligible 集合依該排序選擇
    And 未列出的已註冊 ID 依預設規則補齊
    And 不根據模型 prompt health load cost 或 Registry.Default 排序

  @RTSEL-004
  Scenario: 註冊順序無關
    Given 相同 runtime set 以不同順序註冊
    When auto selection
    Then candidate audit 依 ID 排序且 eligible set 與 selected runtime 一致

  @RTSEL-005
  Scenario: explicit 不 fallback
    Given explicit A 不相容且 B 相容
    When 提交 Task
    Then 拒絕並顯示 A 的能力不足
    And B 不執行且不改用 auto

  @RTSEL-006
  Scenario: 無 eligible runtime
    Given 所有候選均缺必要能力
    When auto admission
    Then 顯示 requirements inspected candidates 及 missing capability 摘要
    And model call 與 runtime Run 次數為零
    And workspace 沒有 execution side effect
    And 不降低 requirements 或改 execution mode

  @RTSEL-007
  Scenario: Three-Face 共用 runtime
    Given Oracle Striker Critic requirements union
    When auto admission 並執行
    Then 一次選出的 runtime 滿足完整 union
    And 三個 stage 都使用該 runtime

  @RTSEL-008
  Scenario: 唯讀能力不足
    Given OMP 與 Antigravity 不宣告 read_only_sandbox
    When Three-Face auto selection
    Then 兩者均被能力 matcher 排除並揭露 missing capability

  @RTSEL-009
  Scenario: 決策封存與相容讀取
    Given Task 已原子保存 intent requirements candidates eligible rejected selected policy 與有效 priority
    When registry version capability policy 改變或 daemon 重啟
    Then status trace 與 Web audit 保留原 decision 且不重算
    And 執行前對同一 selected runtime 做既有 safety guard 且不能換 runtime
    And 舊 Task 缺 selection decision 時顯示 unavailable 而不回填

  @RTSEL-010
  Scenario: 重送及 request identity
    Given Request ID 已接受 auto Task
    When 相同 fingerprint 重送
    Then 回傳原 Task decision 且不重新 enumerate select 或執行
    And 同 ID 改成 explicit 被拒絕
    And 並行首次提交僅保存一個 Task 且派送一次

  @RTSEL-011
  Scenario: opt-in 與入口一致
    Given CLI IPC 或 Web task submission
    When backend 省略或 explicit
    Then 保持原 default 與 explicit 語意
    When backend 是 auto
    Then ask 及 run 使用真實 runtime 而非把 prompt 當 shell 命令
    And Web API 可傳遞 backend intent 且不新增 management UI
    When intent 未知或格式無效
    Then 在 runtime Run 前拒絕

  @RTSEL-012
  Scenario: 完整回歸
    When 執行 go vet ./... 與 go test -count=1 -race ./... 及 go build ./cmd/asura
    Then 全部通過
    And Direct Three-Face 四個 adapter capability context memory recall UFL lifecycle Git memory Web Cron queue redelivery cancel status trace version snapshot 均受既有測試保護

  @RTSEL-013
  Scenario: 候選版本取得失敗不形成 availability routing
    Given 至少一個已註冊候選的 Version 回傳錯誤 空值或逾時
    When auto 建立完整 candidate snapshot
    Then 整次 admission 拒絕並說明候選與版本取得失敗
    And 不移除此候選後改選其他 runtime
    And model call 與 runtime Run 次數為零

  @RTSEL-014
  Scenario: 有界快照與未知能力
    Given candidate 數量超過 32 或 selection decision JSON 超過 64 KiB
    When auto admission
    Then 拒絕且不截斷候選後繼續選擇
    Given requirements 或 descriptor 含未知 capability
    When auto admission
    Then fail-closed 而不忽略未知能力
```

## 可調排序驗收

```gherkin
  @RTSEL-015
  Scenario: 使用者設定 runtime priority
    Given 使用者透過 ASURA_RUNTIME_PRIORITY 指定 runtime ID 偏好列表
    When daemon 啟動或 local run 載入設定
    Then 設定覆蓋預設順序且未列項依預設規則補齊
    And 設定不得繞過能力 matcher 或改動 explicit 行為
    And daemon ask 使用 daemon 設定而非 client 環境
    When 設定含空項 重複或不支援的 ID
    Then 設定被拒絕而非默默忽略
```

## Gherkin 核准紀錄

- **核准 commit**: c3381d4（完整規格核准；排序舊基線 4a1a7af）
- **核准來源**: 2026-10-03 使用者 `/dev-cycle issue:35` 授權盤點與準備提案；GitHub #35 明示候選驗收與待定 RS-TBD。未將 issue ID 視為新增行為核准。2026-10-03 使用者先指定「Omp 預設放最後好了」，再對可調排序提案回覆「好核准」，核准 RTSEL-003、RTSEL-015 與有效排序封存方向；未推定為其餘 Scenario 全部核准。

| Scenario | 核准日期 | 狀態 |
|---|---|---|
| RTSEL-001 | 2026-10-03 | 已核准 |
| RTSEL-002 | 2026-10-03 | 已核准 |
| RTSEL-003 | 2026-10-03 | 已核准 |
| RTSEL-004 | 2026-10-03 | 已核准 |
| RTSEL-005 | 2026-10-03 | 已核准 |
| RTSEL-006 | 2026-10-03 | 已核准 |
| RTSEL-007 | 2026-10-03 | 已核准 |
| RTSEL-008 | 2026-10-03 | 已核准 |
| RTSEL-009 | 2026-10-03 | 已核准 |
| RTSEL-010 | 2026-10-03 | 已核准 |
| RTSEL-011 | 2026-10-03 | 已核准 |
| RTSEL-012 | 2026-10-03 | 已核准 |
| RTSEL-013 | 2026-10-03 | 已核准 |
| RTSEL-014 | 2026-10-03 | 已核准 |
| RTSEL-015 | 2026-10-03 | 已核准 |

2026-10-03 使用者回覆「提案也核准」，核准本文件 RTSEL-001～015 及 technical-analysis 全部提案，包括版本失敗整次拒絕、local auto 真實 runtime 與快照上限。

## 風險與首要驗證

- **最大風險**：auto 與 explicit 或重送身分混用，導致換 runtime、重複執行或越過 read-only 安全契約。
- **風險等級與理由**：High；涉及執行權限與 workspace side effects。
- **首要驗證**：核准後先新增 daemon／engine 負向整合測試，覆蓋 explicit incompatible、auto 無 eligible、同 ID 不同 intent、重送後 registry 改變。
- **選擇理由**：既有 admission 與去重已存在；直接刻畫這些邊界比端到端 UI 切片更能阻止隱性 fallback 及重複執行。
- **完成證據**：先記目標行為紅燈，再驗證 Task 數、派送／Run／model 次數、workspace sentinel 與 sealed decision；綠燈不得只判斷錯誤字串。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|---|---|---|---|
| TBD-1 | 2026-10-03 使用者核准 RTSEL-001～015 與 RS-TBD-1～12 | 已解決 | 全部 Scenario 已核准，Task 依 implementation-plan 執行 |
| TBD-2 | OMP 預設最後、opencode 優先、其餘 ID 字典序；ASURA_RUNTIME_PRIORITY 可調排序入口 | 已解決 | 2026-10-03 使用者回覆「好核准」；RTSEL-003、015。2026-10-03 其餘 admission 行為亦已核准 |
| TBD-3 | local run auto 走真實 AI runtime，explicit shell 保持 | 已解決 | 2026-10-03 完整提案核准；RTSEL-001／011 |
| TBD-4 | 任一候選版本失敗拒絕整次 auto；32 candidates／64 KiB 上限 | 已解決 | 2026-10-03 完整提案核准；RTSEL-013／014；未安裝 OMP/Antigravity 但已註冊時 auto 也會失敗 |

## Timeline

| 日期 | 異動 | 負責人 |
|---|---|---|
| 2026-10-03 | 依 issue 35 建立 Phase 4A 現況盤點、待核准方案與驗收規格 | Codex |
| 2026-10-03 | 使用者指定 OMP 預設最後；補充可調 priority 的設定入口提案，未將詢問可行性視為核准新增設定功能 | Codex |
| 2026-10-03 | 使用者回覆「好核准」，分批核准排序及設定入口；其餘行為另行確認 | 使用者 |

| 2026-10-03 | 使用者「提案也核准」，核准完整驗收與技術方案 | 使用者 |

---

**建立日期**: 2026-10-03
**分級**: Large — 跨 runtime registry、admission、execution 與 audit contract
**風險**: High
**狀態**: 實作與驗證完成，交付審查中
