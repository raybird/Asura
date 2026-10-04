# Issue 39 — Runtime Inventory / Health / Availability

## 概述

來源：[GitHub #39](https://github.com/raybird/Asura/issues/39)。2026-10-04 依使用者 `/dev-cycle issue:39` 盤點與準備提案；2026-10-04 使用者回覆「核准」，授權全部技術提案、驗收與 TBD-1～5。以已合併 Phase 4C 的 main `625e62d` 為基準，建立 registered 與 admission-time availability 的獨立契約。

新 admission：Registry snapshot → Inventory probe/descriptor capture → available descriptors → capability filter → deterministic priority → StageBindingPlan。sealed execution 仍只用封存 runtime ID；不得重新選擇、fallback 或 retry。

## 文件清單

- [需求與現況分析](./requirement-analysis.md)
- [RI-TBD 技術提案](./technical-analysis.md)
- [實作計畫與任務](./implementation-plan.md)
- [驗證紀錄與原始日誌](./verification-evidence.md)

## 涉及檔案

- `internal/backend/{registry,requirements,selection,stage_binding}.go`、新增 inventory 模型／probe 與對應測試：註冊身分、封存及驗證。
- `internal/backend/{opencode,codex,omp,antigravity}/` 的 adapter 與 probe 測試：必要本機條件、context 與輸出界線；不改執行 prompt 策略。
- `internal/backend/clisandbox/`：僅必要的 context-aware 本機檔案驗證。
- `internal/pipeline/{orchestrator,stage_binding,types}.go` 與測試：admission 接線、sealed guard、trace。
- `internal/engine/engine.go` 與測試：契約辨識、local admission、封存資料驗證。
- `internal/daemon/daemon.go` 與測試：registry 接線、inventory IPC、admission、queue/redelivery/restart。
- `cmd/asura/{main,run,status}.go`、新增 inventory 命令與測試：local 共用 probe 與 audit。
- `internal/web/{server,page}.go` 與測試：最小 inventory/task audit。
- `internal/storage/`：只增封存／legacy 相容測試；沿用 ResolvedContext，預期無 schema migration。
- 根目錄 `README.md`、架構文件 `docs/superpowers/specs/2026-09-11-asura-architecture-design.md`：功能與契約更新。
- 本 issue 四份文件：規格、計畫、證據。Memory／Recall／UFL／Lifecycle／Git memory 的 production code 不在修改範圍。

## Gherkin 驗收劇本

```gherkin
Feature: 有界且可稽核的 runtime admission inventory

  @RTINV-001
  Scenario: 已註冊 runtime 探測成功
    Given runtime 已由 Registry 註冊且必要本機條件有效
    When 建立 admission inventory
    Then entry 為 registered 與 available 並有有效 descriptor
    And 不執行使用者 prompt 或產生 workspace 寫入

  @RTINV-002
  Scenario: 缺少 executable 或必要 dependency
    Given runtime 已註冊但 executable 或必要 dependency 不存在
    When 建立 inventory
    Then entry 為 unavailable 並有固定 reason code
    And 不出現在 auto selection candidates

  @RTINV-003
  Scenario: 單一失敗隔離
    Given A 與 B 可用且 C 的 probe 發生一般錯誤或 panic
    When auto admission
    Then inventory 保留三者結果而 C 被排除
    And A 或 B 能在能力相容時完成 admission

  @RTINV-004
  Scenario: OMP descriptor capture 不再阻擋其他 runtime
    Given OMP 已註冊但 version 或 descriptor capture 失敗
    And Codex 可用且滿足必要 stage 能力
    When auto admission
    Then OMP 為 unavailable 且 Task 可綁定 Codex
    And OMP 不被執行

  @RTINV-005
  Scenario: explicit unavailable 拒絕且可稽核
    Given explicit backend 是已註冊但 unavailable 的 OMP
    When admission
    Then 返回 OMP availability reason 並拒絕建立或派送 Task
    And 不 fallback 到其他 runtime

  @RTINV-006
  Scenario: explicit unknown runtime
    Given explicit ID 未註冊
    When admission
    Then 以 unknown runtime 拒絕且不動態建立 adapter

  @RTINV-007
  Scenario: Three-Face 使用同一 inventory
    Given Three-Face auto admission
    When 解析 Oracle 與 Striker 與 Critic 綁定
    Then 三者只使用同一封存 snapshot 的 available descriptors
    And 每個 registered runtime 只 probe 一次且不逐 stage reprobe
    And 任一必要 stage 無相容 candidate 時無 Task 或 Run 副作用

  @RTINV-008
  Scenario: timeout 與全域 budget
    Given runtime A 超時且 B 可用
    When auto admission probe
    Then A 為 unknown 且 reason 為 probe_timeout
    And B 能參與 selection
    And 每個 probe 最多 3 秒且整份 inventory 最多 5 秒
    And 全域 budget 未完成的 entries 為 unknown 與 inventory_timeout

  @RTINV-009
  Scenario: deterministic serialization
    Given 相同 registry identity 與固定觀測時戳及 probe 結果
    And registration insertion 與 probe completion 順序不同
    When 序列化 snapshot 並解析 stage bindings
    Then entries 與 capabilities 與決策順序及內容完全相同

  @RTINV-010
  Scenario: sealed runtime 在執行前消失
    Given Task 已封存 runtime B
    And B 被註銷或必要 executable 或 descriptor 條件失效
    When 執行封存 stage
    Then guard 或 adapter 在 prompt 執行前 fail-closed 並留下 trace
    And 不以新 inventory 改綁且其他 runtime Run 計數為零

  @RTINV-011
  Scenario: 恢復只影響新 admission
    Given A 在先前 inventory 為 unavailable
    And A 在後續 fresh probe 恢復
    When 新 Task admission
    Then A 可參與新 selection
    And 既有 Task 的 inventory 與 binding 不變

  @RTINV-012
  Scenario: 持久化界線與安全
    Given probe output 或 error 含大量內容或 secret sentinel
    When 捕獲與持久化 inventory
    Then 不保存 raw output 或 error 或 token 或環境值
    And 固定 diagnostic 最多 512 bytes
    And ID 最多 64 bytes 且 version 最多 256 bytes
    And inventory 最多 32 entries 與 64 KiB
    And 完整新 execution contract 最多 256 KiB
    And 超限或不一致封存資料 fail-closed

  @RTINV-013
  Scenario: cancellation
    Given admission context 在探測中取消
    When probe 收到 cancellation
    Then 返回 context cancellation 並拒絕建立或派送 Task
    And 所有內建 probe 與子程序在有界時間內結束
    And 不執行 prompt 或改動 workspace

  @RTINV-014
  Scenario: CLI 與 Web audit
    Given mixed available 與 unavailable 與 unknown inventory
    When 查詢 fresh inventory 或 Task status
    Then CLI 與 Web 可呈現 registration 與 availability 與 version 與 reason
    And Task audit 分開呈現 inventory 與 capability filtering 與 stage selection 與 sealed binding 與 actual guard
    And Task status 只讀 sealed snapshot 並不重新探測
    And legacy Task 明示 inventory 未封存

  @RTINV-015
  Scenario: 回歸與相容
    Given 新 inventory contract 及既有 capability-v1 和 stage-binding-v1 任務
    When 執行回歸驗證
    Then legacy 相容語意與 Direct 和 Three-Face 和 explicit 和 auto 保持有效
    And queue 與 redelivery 使用原封存契約且 restart 保留既有恢復政策
    And cancellation 和 Memory 和 Recall 和 UFL 和 Lifecycle 和 Git memory 和 Web 和 Cron 和 status/trace 回歸通過
    And go vet ./... 與 go test -count=1 -race ./... 與 go build ./cmd/asura 全部通過
```

## Gherkin 核准紀錄

- **核准 commit**: a88fe79（承載核准規格；2026-10-04 使用者「核准」）
- **核准來源**: 2026-10-04 使用者 `/dev-cycle issue:39` 授權盤點與提案。GitHub #39 明列「候選驗收劇本」與「待 technical analysis 固定 RI-TBD 與驗收規格後進入 implementation」；2026-10-04 使用者另回覆「核准」，核准 RI-TBD-1～15、RTINV-001～015 與 TBD-1～5。

| Scenario | 核准日期 | 狀態 |
|---|---|---|
| RTINV-001 | 2026-10-04 | 已核准 |
| RTINV-002 | 2026-10-04 | 已核准 |
| RTINV-003 | 2026-10-04 | 已核准 |
| RTINV-004 | 2026-10-04 | 已核准 |
| RTINV-005 | 2026-10-04 | 已核准 |
| RTINV-006 | 2026-10-04 | 已核准 |
| RTINV-007 | 2026-10-04 | 已核准 |
| RTINV-008 | 2026-10-04 | 已核准 |
| RTINV-009 | 2026-10-04 | 已核准 |
| RTINV-010 | 2026-10-04 | 已核准 |
| RTINV-011 | 2026-10-04 | 已核准 |
| RTINV-012 | 2026-10-04 | 已核准 |
| RTINV-013 | 2026-10-04 | 已核准 |
| RTINV-014 | 2026-10-04 | 已核准 |
| RTINV-015 | 2026-10-04 | 已核准 |

## 風險與首要驗證

- **最大風險**：單一 probe 的 timeout/error 污染整次 admission，或 inventory 更新導致已封存任務改綁 runtime 而執行非預期寫入。
- **風險等級與理由**：High；跨 admission、執行安全與持久化契約，失敗可能產生工作區寫入副作用。
- **首要驗證**：核准後先建立 daemon admission／pipeline 的 failure isolation、timeout/cancellation 與 sealed-runtime disappearance 負向測試，再建立 inventory 單元紅燈。
- **選擇理由**：直接驗證 Task/dispatch/Run 邊界，比 Web 垂直切片更直接；僅型別或 planner 測試無法證明沒有執行副作用。
- **完成證據**：獨立預期值、Task 數、probe/Run 計數、workspace sentinel、封存 snapshot 與 trace；記錄紅燈原因與修復後同組綠燈。逾時測試使用縮短的內部測試 budget，另驗證 production 常數。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|---|---|---|---|
| TBD-1 | 核准 technical-analysis 的 RI-TBD-1～15 與全部 RTINV-001～015 | 已解決 | 2026-10-04 使用者「核准」，全部驗收可依計畫實作 |
| TBD-2 | available 限於本機 admission evidence，不保證遠端登入／配額；probe 不讀取或驗證 token | 已解決 | 2026-10-04 使用者「核准」；RTINV-001/002/010，需接受成功不代表 Run 必定成功 |
| TBD-3 | 採 3 秒／runtime、5 秒／inventory、至多 32 個並行 probe；只有 available 可 admission | 已解決 | 2026-10-04 使用者「核准」；RTINV-003/008/013，逾時固定 unknown |
| TBD-4 | 新 runtime-inventory-v1 封存一次 inventory，允許沿用有界 stage candidate 重複資料；64 KiB inventory／256 KiB contract | 已解決 | 2026-10-04 使用者「核准」；RTINV-007/012/015，legacy 不回填 |
| TBD-5 | 新增 asura runtime inventory、daemon inventory IPC、Web 唯讀 inventory 與 task audit；四內建 adapter 明確註冊缺 binary 也保留 entry | 已解決 | 2026-10-04 使用者「核准」；RTINV-002/014，local/daemon 登錄集合一致 |

## 實作入口

[implementation-plan.md](./implementation-plan.md) 是唯一任務來源。2026-10-04 已核准並由 decompose 在同檔細化 Task，不新增第二份清單。

## Timeline

| 日期 | 異動 | 負責人 |
|---|---|---|
| 2026-10-04 | 使用者「核准」RI-TBD-1～15、全部驗收與 TBD-1～5 | 使用者 |
| 2026-10-04 | 讀取 #39 與已合併 Phase 4C，建立現況分析、RI-TBD 提案與待核准驗收；未修改 production code | Codex |

---

**建立日期**: 2026-10-04
**分級**: Large — 跨 runtime adapter、admission、execution guard、persistence 與 audit
**風險**: High
**狀態**: 實作與驗證完成，等待獨立審查及合併；最終審查判定以 `review-{HEAD7}.md` 為準
