# Issue 33 — Runtime Capability & Execution Requirements Foundation

## 概述

Phase 4A 建立 Agent Runtime Control Plane 的執行需求、共同 capability matcher 與接受時封存契約。Asura 管理 runtime execution；skills、MCP、workflow 與 domain logic 由 runtime 自行處理。

來源：[GitHub issue 33](https://github.com/raybird/Asura/issues/33)，2026-10-03 讀取；基準 `41b5220b2100ad85fdb4913c7c8e73ea57657464`（Issue 31／PR 32 已合併）。來源明列「核心方向已核准；先完成 RC-TBD 與驗收規格落地，再進 production implementation」。使用者於 2026-10-03 對本提案回覆「核准」，涵蓋 RC-TBD-1～10 與 RTCAP-001～016。

## 文件清單

- [需求與現況分析](./requirement-analysis.md)
- [RC-TBD 方案與取捨](./technical-analysis.md)
- [實作計畫](./implementation-plan.md)：唯一任務來源；已依 decompose 細化 Phase／Task。
- [完整獨立審查](./review-c550048.md)：16 項驗收 PASS，固定被審查版本見報告。
- [PR #34](https://github.com/raybird/Asura/pull/34)：等待合併。

## 關鍵差異

| 項目 | 現況 | 提案 |
|---|---|---|
| 能力需求 | Pipeline 模式 if/else | deterministic stage requirements 與 union |
| admission | 任務建立後 Pipeline 預檢 | 新任務接受前共同 matcher；執行前安全防護 |
| 歷史能力 | trace 只有執行時 backend version | 接受時完整 immutable contract |
| 無記憶／Cron | 接受時沒有 sealed context | 同樣封存 capability contract |
| 重送 | request identity 去重 | 維持原指紋，先去重再取得新快照 |

## 涉及檔案

- `internal/backend/requirements.go`、`requirements_test.go`（新增）：需求型別、canonicalization、descriptor、共同 matcher。
- `internal/backend/errors.go`：契約驗證錯誤，保留既有錯誤鏈。
- `internal/pipeline/orchestrator.go`、`types.go` 與對應測試：policy、preflight、execution guard 與 trace。
- `internal/engine/engine.go` 與對應測試：sealed contract 與本機新任務 admission。
- `internal/daemon/daemon.go` 與對應測試：IPC／Cron 接受前封存、重送與佇列。
- `internal/storage/` 的契約相容測試：使用既有 `resolved_context`／`pipeline_trace` JSON 欄位；不預設需要 schema migration。
- `cmd/asura/status.go` 與對應測試：保存資料的需求、判定與快照摘要。
- `cmd/asura/run.go`、`internal/daemon/daemon.go`：共用 admission wiring。
- `internal/web/page.go` 與對應測試：沿用 Task 稽核區顯示契約，不建 runtime 管理頁。
- 根目錄 `README.md`、`docs/superpowers/specs/2026-09-11-asura-architecture-design.md`：功能與架構常青文件。
- `internal/backend/opencode/adapter.go`、`capability_version_test.go`：版本解析傳遞 admission deadline，維持能力與既有公開 helper 語意。
- 本 issue 文件與 evidence。

四個 adapter、Registry lookup 及既有隔離邊界保持現有能力語意；只補共同契約相容測試。Phase 4A 不包含 auto routing、fallback、mixed runtime、健康／認證輪詢、skills／MCP discovery、ACP／A2A、retry 或語意記憶。

## Gherkin 驗收劇本

```gherkin
Feature: 可稽核且 fail-closed 的 explicit runtime 執行契約

  @RTCAP-001
  Scenario: Direct 相容
    Given 原本能執行的 Direct Task 與相同 explicit backend
    When 以 Phase 4A 提交
    Then matcher 在 backend Run 前完成
    And 輸出與錯誤契約相容且只執行一次 Striker

  @RTCAP-002
  Scenario: 需求可重複建立
    Given 相同 mode 與 policy version
    When 重複建立需求
    Then stage 與能力集合相同且能力排序固定無重複
    And 不呼叫模型

  @RTCAP-003
  Scenario: 缺少能力
    Given 指定 backend 缺少必要能力
    When 提交新任務
    Then 接受前拒絕且列出 required available missing 與 backend identity
    And 所有 backend Run 次數為零且工作區沒有 execution side effect

  @RTCAP-004
  Scenario: Three-Face 完整需求
    Given Three-Face Task
    When 預檢與執行
    Then Oracle 與 Critic 要求 read_only_sandbox 且 Striker 要求 workspace_write
    And 接受前檢查完整 union
    And 所有 stage 使用相同 resolved backend

  @RTCAP-005
  Scenario Outline: 不擴張 OMP 與 Antigravity 能力
    Given backend <id> 未宣告 read_only_sandbox
    When 提交 Three-Face
    Then Stage 0 拒絕且所有 runtime 的模型呼叫為零
    Examples:
      | id          |
      | omp         |
      | antigravity |

  @RTCAP-006
  Scenario: 指定 backend 權威性
    Given A 缺少能力而 B 具有能力
    When 明確指定 A
    Then 拒絕結果指出 requested backend A
    And 不執行 B 且不改用 default backend

  @RTCAP-007
  Scenario: 歷史快照封存
    Given T 在 V1 與 C1 下被接受
    When runtime 升級為 V2 與 C2 或 daemon 重啟
    Then status 與 audit 仍讀出接受時 V1 C1 判定與 policy version
    And 不重寫 T 的 sealed contract

  @RTCAP-008
  Scenario: 相同請求重送
    Given Request ID R 已建立 Task T
    When 相同 fingerprint 重送且 runtime 已改變或移除
    Then 沿用既有授權與取消規則回傳原 T
    And 不重新取得 capability snapshot 或建立第二次 execution
    And 並行同 ID 請求最多建立一個 Task 且勝者快照不被覆寫

  @RTCAP-009
  Scenario: 未知 runtime
    Given 不存在的 backend ID
    When 提交新任務
    Then 接受前 fail-closed 且模型呼叫為零
    And 不 fallback default backend

  @RTCAP-010
  Scenario: 集合順序獨立
    Given 內容相同但排列或重複不同的 capability 集合
    When matcher 比較相同需求
    Then required available missing 與 verdict 的 canonical 結果相同

  @RTCAP-011
  Scenario: 歷史資料相容
    Given 舊 Task 沒有 capability contract
    When status 或 Web 查詢
    Then 可讀取原 Task 並明示 legacy/unavailable
    And 不呼叫 backend 或回填歷史資料

  @RTCAP-012
  Scenario: 完整回歸
    Given 完成交付版本
    When 執行 go vet ./... 與 go test -count=1 -race ./... 及 go build ./cmd/asura
    Then 全部通過
    And Direct Three-Face MBACK BADAPT CTXWIN Memory Recall UFL Lifecycle Web Cron queue redelivery cancel 與 trace 維持契約

  @RTCAP-013
  Scenario: 無法理解的契約
    Given 未知 mode role capability workspace access 或 policy/contract version
    When 建立需求 比對能力 或執行新契約
    Then 明確拒絕而不 silently ignore
    And backend Run 次數為零

  @RTCAP-014
  Scenario: 版本不可取得
    Given backend Version 失敗或回傳空字串
    When 接受新的 runtime Task
    Then fail-closed 且回報版本不可取得
    And 不偽造已安裝版本且 backend Run 次數為零

  @RTCAP-015
  Scenario: 接受後安全能力退化
    Given T 已有 compatible sealed contract
    When 執行前 backend 消失或現有能力不再滿足封存需求
    Then 在 stage Prepare 與 Run 前拒絕
    And 保留接受時快照並分開記錄 execution guard 拒絕
    And 不重跑或 fallback

  @RTCAP-016
  Scenario: 所有入口封存與稽核
    Given 真正 runtime 的本機 engine IPC ask Web 或 Cron 提交且沒有選取記憶
    When 新任務被接受並排隊或執行
    Then 接受時已保存 mode requirements requested/resolved backend version capability snapshot match 與 policy/contract version
    And CLI status IPC Task 與 Web Task 稽核可讀出同一接受時契約
    And 不增加使用者必填參數或 runtime 管理 UI
    And opencode/codex 本機 run 保留既有 shell 行為且不探測 runtime 或偽造快照
    And shell run 的 CLI status 與 Web 稽核明示 legacy/unavailable
```

## Gherkin 核准紀錄

- **核准 commit**: 665e409（RTCAP-001～015）；fcb58947607759801f38d999504cfaf2e1289565（RTCAP-016 修訂）。
- **核准來源**: 2026-10-03 使用者於本對話回覆「核准」，核准本次完整方案及 RTCAP-001～016；同日對 RC-TBD-11 回覆「保留」，核准保留 shell 行為與 unavailable 稽核的 RTCAP-016 修訂。

| Scenario | 核准日期 | 狀態 |
|---|---|---|
| RTCAP-001 | 2026-10-03 | 已核准 |
| RTCAP-002 | 2026-10-03 | 已核准 |
| RTCAP-003 | 2026-10-03 | 已核准 |
| RTCAP-004 | 2026-10-03 | 已核准 |
| RTCAP-005 | 2026-10-03 | 已核准 |
| RTCAP-006 | 2026-10-03 | 已核准 |
| RTCAP-007 | 2026-10-03 | 已核准 |
| RTCAP-008 | 2026-10-03 | 已核准 |
| RTCAP-009 | 2026-10-03 | 已核准 |
| RTCAP-010 | 2026-10-03 | 已核准 |
| RTCAP-011 | 2026-10-03 | 已核准 |
| RTCAP-012 | 2026-10-03 | 已核准 |
| RTCAP-013 | 2026-10-03 | 已核准 |
| RTCAP-014 | 2026-10-03 | 已核准 |
| RTCAP-015 | 2026-10-03 | 已核准 |
| RTCAP-016 | 2026-10-03 | 已核准 |

## 風險與首要驗證

- **最大風險**：admission 晚於接受與派送，或 status／redelivery 重算目前能力，破壞安全拒絕及歷史封存。
- **風險等級與理由**：High，涉及唯讀權限邊界、不可變執行證據與跨入口行為。
- **首要驗證**：先以既有 preflight、sealed context 與 trace 相容測試刻畫基線；核准後先補接受、runtime mutation、restart、redelivery 及缺少能力的整合紅燈，再做 production 改動。
- **選擇理由**：最大未知是既有持久化與 admission 順序；契約／回歸測試可直接驗證，不需要付費真實模型呼叫或先做 UI 垂直切片。
- **完成證據**：缺能力時所有 Run 計數 0、工作區 sentinel 不變；成功接受的 persisted JSON 在 registry mutation／restart／redelivery 後逐 byte 不變，Task ID 與執行次數不增加。基線只刻畫現有行為，不能代替新增能力的紅綠證據。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|---|---|---|---|
| RC-TBD-1 | backend 型別 ownership 提案 | 已解決 | RTCAP-002／013，見 technical-analysis |
| RC-TBD-2 | stage requirements 加 task union | 已解決 | RTCAP-004 |
| RC-TBD-3 | canonicalize 並拒絕 unknown | 已解決 | RTCAP-010／013 |
| RC-TBD-4 | descriptor 由既有 Backend 建立，版本失敗拒絕 | 已解決 | RTCAP-007／014 |
| RC-TBD-5 | 共同 matcher 與可辨識錯誤鏈 | 已解決 | RTCAP-003／006／009 |
| RC-TBD-6 | resolved_context 為封存權威、trace 記 execution guard | 已解決 | RTCAP-007／015／016 |
| RC-TBD-7 | 舊 Task 查詢 legacy/unavailable，不回填 | 已解決 | RTCAP-011 |
| RC-TBD-8 | 保存 raw requested 與 resolved；空值固定 opencode | 已解決 | RTCAP-006／008 |
| RC-TBD-9 | redelivery 之後、新任務接受前 union 預檢 | 已解決 | RTCAP-003／004／008／016 |
| RC-TBD-10 | capability-v1 與未知版本 fail-closed | 已解決 | RTCAP-013 |
| RC-TBD-11 | 本機 run opencode/codex 為 shell runner；保留其行為並明示 runtime contract unavailable，真正 runtime 路徑完整封存 | 已解決 | RTCAP-016；2026-10-03 使用者回覆「保留」，採相容方案 |

2026-10-03：RC-TBD-1～10 依 technical-analysis 提案定案；RTCAP-001～015 核准有效。RC-TBD-11 已依使用者「保留」定案，RTCAP-016 修訂已核准；shell 稽核沿用 legacy/unavailable，不聲稱 shell 使用 runtime。

## Timeline

| 日期 | 異動 | 負責人 |
|---|---|---|
| 2026-10-03 | 讀取 issue 33 與 main 基準，建立現況、設計提案與待核准驗收 | Codex |
| 2026-10-03 | 使用者回覆「核准」，全部方案與驗收定案 | 使用者 |
| 2026-10-03 | 進一步發現 run opencode/codex 使用 shell；提出 RC-TBD-11，只暫停 RTCAP-016 的該入口決策 | Codex |

| 2026-10-03 | 使用者回覆「保留」，核准 RC-TBD-11 相容方案與 RTCAP-016 修訂 | 使用者 |

| 2026-10-03 | 16 項驗收完成，PR #34 建立，完整獨立審查 PASS；未合併 | Codex |

| 2026-10-03 | PR #34 合併至 main（merge commit `37e034b`），issue 結案 | Raybird |

---

**建立日期**: 2026-10-03  
**分級**: Large — backend／pipeline／engine／daemon／CLI／Web 跨模組契約  
**風險**: High  
**狀態**: 已完成 — PR #34 於 2026-10-03 合併（merge commit `37e034b`）
