# Issue 11 - Phase 3B：專案記憶與 AI 任務 Context 整合

## 概述

將 Phase 3A 已具備的專案級基礎記憶能力，正式接入 AI 任務執行路徑。追蹤 [Issue #11](https://github.com/raybird/Asura/issues/11)，分支 `issue-11-context-memory`，基底 main `987e47a`（PR #10 已合併）。

使 `asura ask` 及共用任務中樞在調度執行任務前，能依目前 canonical workspace 解析可使用的專案記憶，組成可追溯、受限且可重現的 Execution Context，再交由既有 Runner / OpenCode 後端執行。本期第一版採確定性／明確選取（Deterministic / Explicit Selection），由任務提交時明確指定一組 Memory IDs，經服務端完成 scope 驗證與 context 密封（sealed context）後執行，為後續 Three-Face Pipeline（Oracle → Striker → Critic）提供穩定的記憶輸入契約。

## 文件清單

- [requirement-analysis.md](./requirement-analysis.md)
- [technical-analysis.md](./technical-analysis.md)
- [implementation-plan.md](./implementation-plan.md)

## 快速導覽

### 當前流程
1. 使用者執行 `asura ask --workspace /path "prompt"`。
2. CLI 僅將 Prompt 與 Workspace 送往 Daemon。
3. Daemon 僅校驗 Workspace 授權與全域容量，直接寫入 `storage.Task`。
4. Engine 直接將原始 Prompt 傳入 Runner，任務無法引用任何專案記憶。

### 重構後流程
1. 使用者執行 `asura ask --workspace /path --memory-id mem-1 "prompt"`。
2. CLI 傳遞 Prompt、Workspace 與 Memory IDs 至 Daemon。
3. Daemon 透過 `AuthorizeCanonicalWorkspace` 正規化 scope，調用 `ResolveMemoriesForTask` 嚴格驗證所有 Memory IDs：
   - 若任何 Memory ID 不存在、已刪除、跨專案或已撤權，立即拒絕且 0 後端子進程調用。
   - 若驗證通過，將 Memory IDs 納入請求指紋，並生成不可變之 Sealed `ExecutionContext` 儲存於 `tasks` 表。
4. Engine 讀取 Sealed Context，由 Prompt Composer 統一組裝記憶上下文與原始 Prompt，傳入 Runner。
5. 原始使用者 Prompt 在資料庫中完整保留，不被覆寫；任務可完全重現當時執行的 Context。

## 關鍵差異

| 項目 | 變更前 (Phase 3A) | 變更後 (Phase 3B) |
|---|---|---|
| **任務記憶選取** | 無法指定記憶，AI 執行與專案記憶完全脫節 | 支援 CLI 明確指定一組 Memory IDs 注入任務 |
| **Context 結構** | 僅原始純文字 Prompt 直通 Runner | 正式封裝 `ExecutionContext`（含 Prompt、Workspace、記憶快照與參照清單） |
| **可重現性 (Redelivery)** | 無記憶狀態 | 任務接受時密封 Context，記憶後續變動或重送仍保持當時 Context |
| **未授權/跨專案防禦** | 僅檢查 Workspace 是否在 allowed roots | 在 runner 啟動前強制驗證 Memory ID 之 canonical scope，非法即 fail-closed 拒絕 |
| **向後相容性** | N/A | 未指定記憶時完全保持既有 ask 任務行為，不自動注入無關記憶 |

## 涉及檔案

```text
cmd/asura/
├── ask.go                                 # 新增 --memory-id 參數解析並傳遞至 Daemon
└── run_test.go                            # CLI 參數解析與分派測試
internal/storage/
├── sqlite.go                              # tasks 資料表擴充 memory_refs 與 resolved_context 欄位
└── sqlite_memory.go                       # 實作 ResolveMemoriesForTask 記憶解析與驗證
internal/engine/
├── engine.go                              # 定義 ExecutionContext、SubmitRequest 擴充與 Prompt 組裝器
└── engine_test.go                         # 測試 Context 組裝與原始 Prompt 保護
internal/daemon/
├── daemon.go                              # IPC 擴充、Fingerprint 含記憶、記憶授權校驗與 Context 密封
├── memory_test.go                         # 測試記憶 Context 解析、跨專案阻擋與密封一致性
└── daemon_test.go                         # 測試任務 Redelivery 記憶指紋一致性
tests/integration/
└── memory_context_test.go                 # 外迴圈端到端整合測試 (CTXMEM-001 ~ CTXMEM-007)
README.md                                  # 更新 CLI 使用說明
```

## Gherkin 驗收劇本

```gherkin
Feature: 專案記憶與 AI 任務 Context 整合

  @CTXMEM-001
  Scenario: 同專案記憶可被任務使用
    Given 專案 A 有已保存記憶 M1
    When 使用者在 A 提交任務並明確選取 M1
    Then 任務執行前建立包含原始 prompt 與 M1 的 Execution Context
    And runner 可取得 M1 的內容
    And Task 原始 Prompt 不被改寫

  @CTXMEM-002
  Scenario: 跨專案記憶不可注入
    Given 專案 A 與 B 各有記憶
    When A 的任務嘗試選取 B 的 Memory ID
    Then 請求在 runner 啟動前被拒絕
    And B 的記憶內容不洩漏
    And 後端啟動次數為零

  @CTXMEM-003
  Scenario: 撤權與非法 workspace 不可使用記憶
    Given 專案已有記憶但其 workspace 已撤權或越界
    When 提交帶有該記憶的任務
    Then context resolution 失敗
    And 不啟動 runner
    And 不建立假成功結果

  @CTXMEM-004
  Scenario: 任務接受後 Context 保持一致
    Given 任務已接受且解析得到記憶 M1
    When M1 之後被修改、刪除或同一提交識別被重送
    Then 已接受任務仍使用當時已解析的相同 context
    And 不因重送而重新解析成不同內容
    And 不造成第二次執行副作用

  @CTXMEM-005
  Scenario: 已刪除或不存在記憶不可新注入
    Given Memory ID 不存在或已被刪除
    When 新任務嘗試選取該 ID
    Then 任務拒絕或明確回報無法解析該記憶
    And runner 不啟動

  @CTXMEM-006
  Scenario: 記憶 Context 可追溯
    Given 任務成功使用一組專案記憶
    When 使用者查詢該 Task
    Then 系統可識別該任務實際使用的 Memory IDs 與 workspace scope
    And 不需要重新依目前記憶狀態推導歷史 context

  @CTXMEM-007
  Scenario: 未選取記憶時維持既有任務語意
    Given 使用者提交未指定任何記憶的既有 ask 任務
    When 任務執行
    Then 行為與 Phase 3A 前既有正式任務路徑相容
    And 不自動注入所有 workspace 記憶
```

## Gherkin 核准紀錄

- **核准 commit**: `1e6cf4a`
- **核准來源**: 2026-09-17 對話完成 TBD-1～TBD-4 逐項確認，正式核准 CTXMEM-001～CTXMEM-007 全數驗收劇本

| Scenario | 核准日期 | 狀態 |
|----------|---------|------|
| CTXMEM-001 | 2026-09-17 | 已核准 |
| CTXMEM-002 | 2026-09-17 | 已核准 |
| CTXMEM-003 | 2026-09-17 | 已核准 |
| CTXMEM-004 | 2026-09-17 | 已核准 |
| CTXMEM-005 | 2026-09-17 | 已核准 |
| CTXMEM-006 | 2026-09-17 | 已核准 |
| CTXMEM-007 | 2026-09-17 | 已核准 |

## 重構步驟概要

1. ✅ **Phase 1**：儲存層 Schema 擴充與記憶解析服務（Task 1.1, Task 1.2）
2. ✅ **Phase 2**：Engine 記憶 Context 組裝與原始 Prompt 保護（Task 2.1, Task 2.2）
3. ✅ **Phase 3**：Daemon 調度閘門、指紋計算、Context 密封與 Redelivery 一致性（Task 3.1, Task 3.2, Task 3.3）
4. ✅ **Phase 4**：CLI 命令整合與全迴圈整合驗證（Task 4.1, Task 4.2）

## 風險與首要驗證

- **最大風險**：Context 不可重現（若任務每次啟動或重送時動態查詢「最新記憶」，記憶修改/刪除或重送時將得到不同輸入，破壞既有 redelivery/idempotency 契約；或跨專案/撤權記憶外洩）。
- **風險等級與理由**：High，涉及授權安全、資料隔離、任務重送一致性與歷史可追溯性。
- **首要驗證**：在 runner 執行前，建立跨專案、撤權、記憶變更與同 Request ID 重送的整合與單元測試網（紅燈先行），嚴格比對 Context resolution、授權拒絕與 sealed context 的不變性。
- **選擇理由**：直接在生命週期起點（提交/解析/封裝邊界）鎖定 context seal 與授權契約，比執行後端真實模型調用更能精準且快速暴露不一致與安全漏洞。
- **完成證據**：
  1. 當跨專案、已撤權或不存在記憶 ID 傳入時，請求在 runner 啟動前被拒絕，runner 調用次數為 0；
  2. 任務接受後修改或刪除記憶，重送同 Request ID 時回傳既有任務且使用的 Context 保持與接受時一致；
  3. 查詢 Task 時能準確取得記憶參考資訊（Memory IDs 與 workspace）；
  4. 未指定記憶之任務維持 Phase 3A 行為與相容性。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|------|------|------|------|
| TBD-1 | Sealed Context 保存方案取捨（在 `tasks` 表新增 `resolved_context` 與 `memory_refs` 欄位） | 已解決 | 2026-09-17 使用者確認採用方案 A：於 `tasks` 資料表新增欄位保存 JSON 快照，單表維護且加法相容；關聯 CTXMEM-004, CTXMEM-006 |
| TBD-2 | CLI 參數格式確認（`asura ask` 支援 `--memory-id` 多選與逗點分隔） | 已解決 | 2026-09-17 使用者確認旗標採用 `--memory-id`，同時支援多次指定與逗點分隔；關聯 CTXMEM-001 |
| TBD-3 | Runner prompt 組裝格式確認（Engine 統一在 prompt 前附加標準 Context 區塊，原始 prompt 留存於 Task.Prompt） | 已解決 | 2026-09-17 使用者確認採用 Markdown 區塊格式（以 `### Project Context` 開頭標列每筆記憶），原始 Prompt 留存於 `task.Prompt`；關聯 CTXMEM-001, CTXMEM-007 |
| TBD-4 | 候選驗收劇本 CTXMEM-001 ~ CTXMEM-007 逐項核准 | 已解決 | 2026-09-17 使用者確認全數核准 CTXMEM-001～CTXMEM-007 驗收劇本，正式授權進入實作流程 |

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-09-17 | 讀取 GitHub Issue #11 啟動 new-issue，完成現況分析、技術選型取捨與實作計畫，建立 Issue 11 文件與候選驗收規格 | - |
| 2026-09-17 | 逐題確認 TBD-1～TBD-4；核准持久化方案、CLI 旗標規格、Prompt 組裝格式，並正式核准 CTXMEM-001～CTXMEM-007 全部驗收劇本 | - |
| 2026-09-17 | 依 TDD 完成 Phase 1～4 全部 Task，通過 CTXMEM-001～007 外迴圈端到端整合與全專案回歸驗證 | - |

---
**建立日期**: 2026-09-17  
**分級**: Large  
**風險**: High  
**狀態**: 已完成（Phase 1～4 全部驗收通過）
