# Issue 19 — 有界自動專案記憶召回（Phase 3F）

## 概述

在 `asura ask` 明確啟用時，從相同 canonical workspace 的既有記憶選出有限內容，在 Daemon 接受任務時密封，供 Direct／Three-Face 共用。

來源：[GitHub #19](https://github.com/raybird/Asura/issues/19)。2026-09-22 僅授權需求／技術規劃及必要探測，2026-09-22 使用者回覆「核准」，核准 D1–D5 與 RECALL-001～010，開始實作。

## 文件清單

- [需求與現況](requirement-analysis.md)
- [技術提案與取捨](technical-analysis.md)
- [權威實作計畫](implementation-plan.md)
- [探測證據](evidence/probe-results.md)
- [實作驗證](evidence/verification.md)

## 關鍵差異

| 項目 | 現況 | 提案 |
|---|---|---|
| 選取 | 僅 explicit IDs | 預設不變；ask 可指定 bounded |
| 接受 | 封存 explicit snapshots | explicit 後加 auto，並保存決策 |
| 重送 | 先比對 fingerprint | 沿用，bounded 模式另入指紋；政策版本不入指紋 |
| 資料讀取 | ListMemories 全量 | 獨立有界查詢；不改 memory list |

## 涉及檔案

以下為核准後預計的最小修改邊界；各路徑既有測試或新增對應測試納入同一範圍。

```text
cmd/asura/ask.go、status.go          # opt-in 與稽核顯示
internal/daemon/daemon.go、recall.go # IPC、指紋、接受與密封
internal/storage/sqlite.go          # workspace,id 複合索引
internal/storage/sqlite_memory.go   # 有界候選查詢
internal/engine/engine.go           # 可選 recall 決策結構、舊 JSON 相容
internal/recall/                    # 新增純確定性 selector
internal/pipeline/                  # 共用首次 stage prompt 準備，保持字串契約
internal/contextwin/                # 沿用 estimator/admission；測試增量預算
 tests/integration/                 # RECALL 驗收與既有回歸
README.md                          # 交付時補使用說明
 docs/superpowers/specs/2026-09-11-asura-architecture-design.md # 接受流程與索引決策
 docs/issues/issue-19/              # 本次規劃、探測與交付證據
```

Web/Cron 不新增啟用入口；backend/session/history、記憶寫入、跨專案繼承不在範圍內。

## Gherkin 驗收劇本

以下數值及失敗語意對應 technical-analysis.md 的 D1–D5，已於 2026-09-22 核准。

```gherkin
Feature: 明確啟用且有界的專案記憶召回

  @RECALL-001
  Scenario: 預設關閉且向後相容
    Given 既有 CLI、IPC、Web、Cron 或 explicit-only 請求
    When 未指定 recall 或指定 off
    Then prompt、fingerprint、舊 JSON 語意保持不變且 selector 呼叫為 0
    And ask 與 IPC 對未知 recall 模式拒絕且 backend 呼叫為 0

  @RECALL-002
  Scenario: 授權與精確 workspace 隔離
    Given 不同專案、父子目錄、symlink 別名及撤權的記憶
    When 提交 bounded 任務或重送既有任務
    Then 僅允許授權的同 canonical workspace 且別名共用同 scope
    And 未授權提交或跨專案 explicit ID 被拒絕且 backend 呼叫為 0
    And 回應與 trace 不包含其他 workspace 的候選內容

  @RECALL-003
  Scenario: 確定性文字相關性
    Given 固定資料、prompt 與 lexical-v1 政策
    When 反覆以中英混合、大小寫、重複詞及同分案例召回
    Then 依不同匹配詞數降序及 ID 升序得到相同選取
    And 無匹配、只有單一中文字或不支援的同義詞時不加入任何記憶
    And 明列 query 截斷與候選截斷，不宣稱語意檢索

  @RECALL-004
  Scenario: 掃描及自動增量有硬上限
    Given 超過 256 筆候選、超長內容或超長查詢
    When 啟用 bounded
    Then 僅評分 ID 升序前 256 筆並以第 257 筆判定截斷
    And 每筆 content 最多讀取 8193 bytes 作為 8192 bytes 上限哨兵
    And 每筆過大的 metadata 或 content 整筆略過且不截斷注入
    And 最多新增 4 筆且 ComposePrompt 增量不超過 8192 bytes 與 2048 estimated prompt tokens
    And 查詢及選取超過 250ms 期限或儲存錯誤時拒絕提交且不保存 Task、不呼叫 backend

  @RECALL-005
  Scenario: 明確記憶保留與預算優先序
    Given 已指定完整 explicit IDs 且存在重複或超量 auto 候選
    When 解析並依排名逐筆嘗試加入
    Then explicit 先解析且不被裁切、替換或因 auto 額度刪除
    And auto 排除 explicit IDs，超量候選整筆略過並繼續嘗試下一筆
    And 已設 ContextLimit 時 auto 不令首次實際 stage 含 reserve 的估算超限
    And 原始 prompt 與 explicit 已超限時 auto 為 0，原始內容仍由既有 stage observe 或 reject 處理
    And 未設窗口時只套用有限 auto 額度並標記窗口 unknown

  @RECALL-006
  Scenario: 接受時封存與重送
    Given bounded 任務已保存 snapshot 與決策，之後記憶或管理者政策改變
    When 同 Request ID 與相同正規化 payload 再提交
    Then 回傳原 Task 且不再查候選、不改 snapshot、不重跑 backend
    And 改變 recall 模式或 explicit IDs 時衝突拒絕
    And cancelled 重送維持既有拒絕行為

  @RECALL-007
  Scenario: 排隊及重啟保持快照
    Given 已接受任務排隊且記憶隨後刪除或修改
    When 喚醒或 Daemon 重啟後依既有恢復流程執行
    Then 所有執行使用接受時快照且不重新召回
    And opt-in 但 0 added 仍保存 recall 決策並與 off 可區分

  @RECALL-008
  Scenario: 共用快照及逐 stage admission
    Given Direct 或 Three-Face 任務使用 sealed memories
    When 後續 OraclePlan 或 StrikerOutput 令下一 stage 超限
    Then 依當時 stage 的實際 prompt 重新 admission
    And reject 阻擋該 stage 並保留前序 trace，observe 維持既有警示語意
    And 不裁切 context、不換 backend、不增加召回用 AI 呼叫

  @RECALL-009
  Scenario: 稽核與舊資料相容
    Given 舊 SQLite、舊 task JSON 及新 bounded 任務
    When 啟動、查詢 Task、status 或讀取 trace
    Then 舊 JSON 缺 recall 欄位視為 off 且原有記憶視為 explicit
    And 新任務可追溯 explicit/auto 順序、分數、政策、預算與略過原因計數
    And 不保存未選候選全文、片段或匹配詞
    And 索引升級不改寫舊 Task 或 memories 資料

  @RECALL-010
  Scenario: 有界壓力與完整回歸
    Given 10001 筆資料、超大單筆、跨專案內容及固定混合語料
    When 執行查詢計畫、benchmark、timeout 與整合測試
    Then 查詢使用 workspace,id 索引而不全量排序，讀取量符合固定上限
    And 期限到期確實取消查詢並釋放單連線，無額外模型呼叫
    And CTXMEM、CTXWIN、TFACE、MBACK、CRON、WEB、PGID cancel 與 redelivery 回歸通過
```

## Gherkin 核准紀錄

2026-09-22 對話來源：在完整 D1–D5 與 RECALL-001～010 提案送審後，使用者明確回覆「核准」。下列 issue 原文保留為立項歷史，後續對話核准已解除該 gate。

- **核准 commit**: aeaa652
- **核准來源**: 2026-09-22 issue 原文：「這個 issue 授權需求／技術規劃與必要的可重現檢索探測；不代表 RECALL-001～010、任何自動注入預設值、排序權重、token 配額或 production code 行為已核准。」

| Scenario | 核准日期 | 狀態 |
|---|---|---|
| RECALL-001 | 2026-09-22 | 已核准 |
| RECALL-002 | 2026-09-22 | 已核准 |
| RECALL-003 | 2026-09-22 | 已核准 |
| RECALL-004 | 2026-09-22 | 已核准 |
| RECALL-005 | 2026-09-22 | 已核准 |
| RECALL-006 | 2026-09-22 | 已核准 |
| RECALL-007 | 2026-09-22 | 已核准 |
| RECALL-008 | 2026-09-22 | 已核准 |
| RECALL-009 | 2026-09-22 | 已核准 |
| RECALL-010 | 2026-09-22 | 已核准 |

## 風險與首要驗證

- **最大風險**：候選讀取跨越授權／scope，或重送重新選取，造成敏感內容洩漏與快照漂移。
- **風險等級與理由**：High；涉及資料隔離、請求冪等性及單 SQLite 連線可用性。
- **首要驗證**：先跑既有 CTXMEM scope／撤權／redelivery／queued／symlink 測試，再以隔離資料庫探測有界 SQL。
- **選擇理由**：既有邊界刻畫及真實 SQL 比新增端到端流程更直接定位風險；檢索 benchmark 只回答資源與排序，不取代安全驗收。
- **完成證據**：[2026-09-22 探測](evidence/probe-results.md)記錄既有測試通過、同 scope SQL、超大內容 sentinel、索引計畫；新功能安全、timeout、restart 與完整回歸仍待核准後紅綠驗證。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|---|---|---|---|
| TBD-1 | 核准 D1 lexical-v1 及候選截斷造成的漏召回 | 已解決（2026-09-22 使用者核准提案） | RECALL-003、004、010 |
| TBD-2 | 核准 D2 僅 ask/IPC 開放，Web/Cron/run 不新增入口 | 已解決（2026-09-22 使用者核准提案） | RECALL-001、002 |
| TBD-3 | 核准 D3 固定額度、逾時拒絕及首次 stage 預算優先序 | 已解決（2026-09-22 使用者核准提案） | RECALL-004、005、008、010 |
| TBD-4 | 核准 D4 JSON 決策、略過計數與 metadata 上限 | 已解決（2026-09-22 使用者核准提案） | RECALL-007、009 |
| TBD-5 | 核准 D5 bounded 指紋、政策漂移與全部 Gherkin | 已解決（2026-09-22 使用者核准提案） | RECALL-006；RECALL-001～010 整體核准 |

## Timeline

| 日期 | 異動 | 負責人 |
|---|---|---|
| 2026-09-22 | 使用者核准 D1–D5 與全部十個 Scenario | 使用者 |
| 2026-09-22 | 依 #19 授權建立現況分析、探測及待核准規格；尚未授權產品實作 | Codex |

---
**建立日期**: 2026-09-22  
**分級**: Large — 涉及儲存、Daemon、CLI 與 execution context 邊界  
**風險**: High — 資料隔離、成本及冪等性  
**狀態**: 實作中
