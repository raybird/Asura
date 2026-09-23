# Issue 21 — Web Console 專案記憶管理與召回操作（Phase 3G）

## 概述

在既有單人 Web Console 管理授權 workspace 的手動專案記憶，提交任務時選取 explicit IDs 或明確啟用 `bounded` 召回，並查看已保存任務的記憶與召回決策。來源為 [GitHub #21](https://github.com/raybird/Asura/issues/21)。2026-09-23 使用者以「$dev-cycle 核准」核准本文件的 WEBMEM-001～010 行為；HTTP／IPC 欄位與大小上限尚待 G-TBD-1～5 的具體決策，不以概括核准代填數值。

## 文件清單

- [需求與現況](requirement-analysis.md)
- [技術提案與取捨](technical-analysis.md)
- [權威實作計畫](implementation-plan.md)
- [初始風險探測](evidence/probe-results.md)
- [探測 SQL](evidence/probe.sql)
- [修訂前獨立審查](review-9d5c2a3.md)

## 關鍵差異

| 項目 | 現況 | 已核准的目標 |
|---|---|---|
| Web 記憶操作 | 無入口 | 授權後有界列出、查看、手動新增與確認刪除 |
| Web 任務提交 | 僅傳 ID、workspace、prompt | 可選 explicit IDs 與預設 off 的 bounded 召回 |
| 歷史稽核 | 查 Task 狀態與結果 | 依保存的 context／trace 呈現當次決策 |

## 涉及檔案

```text
internal/web/server.go、page.go       # HTTP 邊界、任務欄位、UI 與稽核呈現
internal/daemon/daemon.go             # 新的有界 IPC；沿用授權、記憶與提交流程
internal/storage/sqlite_memory.go     # 有界清單查詢
internal/storage/sqlite.go            # 依分頁排序決策增設索引
internal/web/*_test.go                # Web 行為與 session／Origin 回歸
internal/daemon/*_test.go             # IPC 授權與重送驗證
internal/storage/*_test.go            # SQL 上界、排序與 scope 驗證
tests/integration/                    # 真實 HTTP → IPC → SQLite 驗收
README.md                             # 交付時補 Web 使用說明
docs/superpowers/specs/2026-09-11-asura-architecture-design.md # 交付時補邊界與資料流
docs/issues/issue-21/                # 規格、探測與驗證證據
```

CLI `memory list/get`、Phase 3F selector／額度、Runner、多後端與 Cron 行為不在修改範圍；不新增 AI 自主記憶、跨 workspace 分享、記憶更新或第二套檢索引擎。

## Gherkin 驗收劇本

以下依 #21 候選驗收整理的外部行為已於 2026-09-23 核准；具體 API、分頁與大小上限仍以 G-TBD-1～5 決議為準。

```gherkin
Feature: 已登入 Web Console 的專案記憶與召回操作

  @WEBMEM-001
  Scenario: 登入與同站請求邊界
    Given 未登入、已登出，或從跨站頁面發出的 Web 請求
    When 讀取記憶，或變更記憶與提交帶召回的任務
    Then 未登入及已登出的讀取與變更請求被服務端拒絕
    And 跨 Origin 的變更請求被服務端拒絕，跨站頁面無法讀取 GET 回應
    And 拒絕路徑沒有記憶寫入或 backend 呼叫

  @WEBMEM-002
  Scenario: Canonical workspace 精確隔離
    Given A/B、父子、symlink 別名或越界及撤權的 workspace
    When 操作記憶或提交 explicit IDs
    Then Daemon 重新授權 canonical workspace 且記憶只屬於精確 scope
    And 拒絕跨專案內容或 ID，且不洩漏其存在性

  @WEBMEM-003
  Scenario: 服務端有界清單與明確 detail
    Given 單一 workspace 有大量記憶及超大內容
    When 分頁瀏覽並按 ID 查看記憶
    Then 儲存查詢、IPC 與 HTTP 列表均受核准上限約束且排序穩定
    And 清單只含有界摘要，detail 在進入 IPC 前以核准上限取得完整內容或拒絕
    And 超大內容不會因 HTTP 最後拒絕而先全量穿過 IPC

  @WEBMEM-004
  Scenario: 手動新增、確認刪除及持久化
    Given 已授權 workspace 與已接受的密封任務
    When 新增、查看或明確確認刪除記憶，並重啟 Daemon
    Then 成功與失敗如實顯示，來源保持 manual，資料按 SQLite 結果持久化
    And 刪除只影響新任務，不改寫已接受任務的快照

  @WEBMEM-005
  Scenario: Web explicit IDs 由 Daemon 解析
    Given Web 選取同 scope 記憶或提交跨 scope／已刪除 ID
    When 送出任務
    Then 有效 ID 的完整快照進入 resolved_context
    And 無效 ID 在 backend 呼叫前被拒絕，client 自填 sealed context 不進入執行路徑

  @WEBMEM-006
  Scenario: bounded 明確啟用且預設 off
    Given 原 Web 請求未啟用召回
    When 原樣提交或明確選擇 bounded
    Then 原樣請求保持 off 行為
    And bounded 使用既有 Daemon selector、額度、provenance 與 stage admission

  @WEBMEM-007
  Scenario: 回應遺失後重送維持請求身分
    Given Web 提交後未收到接受回應
    When 原 payload 重送、IDs 順序變更或使用相同 ID 改變內容／模式
    Then 原 payload 依既有 fingerprint 回傳同一任務且僅一次執行
    And 不同 payload 同 ID 被拒絕，Web 不暗中換新 ID 重試
    And 使用者在送出前可取得 Request ID，切換專案或重新登入後仍可憑該 ID 查詢未知結果

  @WEBMEM-008
  Scenario: 查詢歷史任務的召回稽核
    Given 已接受任務的記憶後來變更或刪除
    When 在 Web 查詢該 Task
    Then 顯示保存的 explicit／auto IDs、政策、分數、略過原因與 stage trace
    And legacy、off、0 auto、unknown 與 estimated 資料各自如實標示

  @WEBMEM-009
  Scenario: 純文字呈現及專案狀態清除
    Given 含腳本樣式的記憶、已選取的專案記憶及尚未返回的舊專案請求
    When 切換 workspace、登出或 session 失效，之後舊請求才返回
    Then 內容不執行腳本，舊內容與選取不殘留或誤送至下一任務
    And 延遲回應不會重新填入舊專案資料

  @WEBMEM-010
  Scenario: 回歸及資源邊界
    Given Web、記憶、召回與執行管線既有契約
    When 驗證大量記憶、長內容、授權與重送並執行完整回歸
    Then WEB、MEM、CTXMEM、RECALL、CTXWIN、TFACE、MBACK、CRON、PGID cancel 與 queue/redelivery 仍通過
    And vet、race test 與 build 通過，沒有無界記憶清單傳輸
```

## Gherkin 核准紀錄

- **核准 commit**: 21aafa8（承載 2026-09-23 已核准 Gherkin 的初次提交）
- **核准來源**: 2026-09-23 本對話使用者「$dev-cycle 核准」，對應當時 [GitHub #21](https://github.com/raybird/Asura/issues/21) 的 WEBMEM-001～010 與本文件的 Gherkin。issue 原文保留具體上限及分頁協定須決議的條件；尚未定案數值見 G-TBD-1～5。

| Scenario | 核准日期 | 狀態 |
|---|---|---|
| WEBMEM-001 | 2026-09-23 | 已核准 |
| WEBMEM-002 | 2026-09-23 | 已核准 |
| WEBMEM-003 | 2026-09-23 | 已核准 |
| WEBMEM-004 | 2026-09-23 | 已核准 |
| WEBMEM-005 | 2026-09-23 | 已核准 |
| WEBMEM-006 | 2026-09-23 | 已核准 |
| WEBMEM-007 | 2026-09-23 | 已核准 |
| WEBMEM-008 | 2026-09-23 | 已核准 |
| WEBMEM-009 | 2026-09-23 | 已核准 |
| WEBMEM-010 | 2026-09-23 | 已核准 |

## 實作步驟概要

權威任務與相依見 [implementation-plan.md](implementation-plan.md)。產品階段須先固定 G-TBD-1～5 的具體政策，再由 decompose 在同檔細化。

## 風險與首要驗證

- **最大風險**：新增 Web 記憶入口若繞過授權或讀取無界清單，可能外洩跨專案內容或耗盡 Daemon／Web 資源。
- **風險等級與理由**：High；涉及授權、安全及可能無界的 SQLite → IPC → HTTP 資料流。
- **首要驗證**：先刻畫現有 session／Host／Origin 與 Daemon canonical 授權，再用隔離 SQLite 資料探測大量記憶、超大內容、索引與有界查詢。核准實作後先建立拒絕與上界的行為紅燈。
- **選擇理由**：授權測試與真實查詢計畫直接驗證資料外洩及無界讀取風險；單看 UI 無法驗證服務端邊界。
- **完成證據**：[初始探測](evidence/probe-results.md)記錄 10k 筆 SQLite keyset 查詢計畫、摘要 SQL 截取及既有邊界測試；新 HTTP 拒絕路徑的 0 寫入／0 backend 呼叫、IPC／HTTP bytes 上限仍須核准後驗證。探測不等同新功能通過驗收。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|---|---|---|---|
| G-TBD-1 | 核准 HTTP endpoint、IPC action、DTO、錯誤映射與 GET／變更方法的權限矩陣 | 待確認 | WEBMEM-001、002、004、005 |
| G-TBD-2 | 核准穩定分頁、hard page-size、摘要／有界 detail／錯誤及傳輸大小上限；初始探測已記錄 | 待確認 | WEBMEM-003、010 |
| G-TBD-3 | 核准 explicit 多選與 bounded 的交互、空選取及 workspace 切換規則 | 待確認 | WEBMEM-005、006、009 |
| G-TBD-4 | 核准回應遺失、重試、表單改動與登入狀態變動時的 request-ID 顯示及找回策略 | 待確認 | WEBMEM-007、009 |
| G-TBD-5 | 核准歷史稽核尺度、超大內容呈現與隱私界線 | 待確認 | WEBMEM-008、009 |
| G-TBD-6 | 核准 WEBMEM-001～010 的最終可觀察結果及必要驗證 | 已解決（2026-09-23 使用者以「$dev-cycle 核准」核准現行 Gherkin；具體上限仍由 G-TBD-1～5 管理） | 全部 Scenario |

## Timeline

| 日期 | 異動 | 負責人 |
|---|---|---|
| 2026-09-23 | GitHub #21 立項，授權規劃與非產品探測，保留設計與驗收核准 gate | Issue 作者 |
| 2026-09-23 | 建立現況分析、技術提案、待核准驗收與權威初始計畫 | Codex |
| 2026-09-23 | 依獨立審查修訂 detail 上界、GET Origin 驗收與 UI 競態，補可重跑探測；規格仍待核准 | Codex |
| 2026-09-23 | 使用者核准 WEBMEM-001～010 外部行為；G-TBD-1～5 的具體數值與協定待定案 | 使用者 |

---
**建立日期**: 2026-09-23
**分級**: Large — Web、Daemon、SQLite 與 Task 稽核跨模組
**風險**: High — 授權外洩、無界傳輸與重送副作用
**狀態**: 驗收已核准，待具體設計定案
