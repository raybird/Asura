# Issue 27 — Web Git Memory Capture & Review（Phase 3J）

## 概述

將既有 Git candidate 擷取、檢視、編修核准與捨棄接到 Web Console。來源：[GitHub issue #27](https://github.com/raybird/Asura/issues/27)，2026-09-30 讀取，基準 `231d846e15a1a3aa8e227fa87fde2e1a4c476c73`。

Web 僅作 adapter，Daemon 保持 Git、授權與原子狀態轉移 ownership；沿用 candidate/store/checkpoint/recall。第一版不提供 force、ai_draft、bulk clear、自動 capture、自動審核或 recall 改寫。

## 文件清單

- [需求分析](requirement-analysis.md)
- [技術分析與 J-TBD 提案](technical-analysis.md)
- [權威實作計畫](implementation-plan.md)
- [驗證證據](evidence/verification.md)

## 涉及檔案

```text
internal/
├── web/          # server.go、memory.go 模式、page.go；新增 Git API 與測試
├── daemon/       # daemon.go IPC DTO/dispatch、gitmem_handlers.go；有界入口與測試
├── storage/      # sqlite_gitmem.go/tx.go 契約；新增 bounded query 與測試
└── gitmem/       # 僅必要取消／既有契約測試，不新增 Git 掃描器
tests/integration/ # 新增 WEBGIT 驗收
docs/issues/issue-27/ # 規格、計畫與證據
README.md        # 交付時更新使用方式
docs/superpowers/specs/2026-09-11-asura-architecture-design.md # Web 邊界與資料流
.github/workflows/ci.yml # 新增真實 Chromium UI probe
```

backend、pipeline、recall policy、Cron 與 candidate schema 不在本期修改範圍；若查核證明必要，先說明實際原因與範圍修訂。

## Gherkin 驗收劇本

```gherkin
Feature: Web Git candidate capture and review

  @WEBGIT-001
  Scenario: Authentication / Origin / Host boundary
    Given 未登入、過期 session、非法 Host 或跨 Origin 請求
    When 呼叫 capture、list、detail、approve 或 discard
    Then 全部拒絕，Git subprocess、candidate write、memory write 均為 0

  @WEBGIT-002
  Scenario: Canonical workspace isolation
    Given 有 A/B、父子目錄、symlink alias/escape、撤權及非 Git workspace
    When 讀取或操作 candidate
    Then Daemon 重新授權 canonical workspace；跨 scope 與不存在 ID 回傳相同 not-found，不洩漏內容；非法 capture 不執行 Git

  @WEBGIT-003
  Scenario: Explicit capture truthfulness
    Given 已授權 Git workspace 與固定 range 或 since-last
    When 執行 capture
    Then 只呼叫既有 capture service，HEAD、branch、index、worktree、config 不變，Complete/Partial/Rejected/reused 如實回傳

  @WEBGIT-004
  Scenario: Bounded candidate browsing
    Given 大量候選、相同 created time 與超大欄位
    When 篩選狀態並逐頁讀取或查 detail
    Then SQL 與 IPC 有界；固定資料集翻頁無重複或遺漏；超限明確拒絕，不截短草稿後核准

  @WEBGIT-005
  Scenario: Provenance-safe rendering
    Given subject、path 或 draft 包含惡意 HTML/script
    When 開啟候選詳情
    Then 全部以純文字顯示，Git facts、draft 與正式 memory 狀態分開，沒有 script 執行

  @WEBGIT-006
  Scenario: Edited approval
    Given pending Complete candidate
    When 修改 draft 並確認 approve
    Then 正式 git-approved memory 使用修訂內容，原 Git provenance 不變，顯示正式 memory ID 並可由既有 Memory 區域查詢

  @WEBGIT-007
  Scenario: Partial / invalid approval fail-closed
    Given Partial、discarded、跨 scope、不存在或非法狀態 candidate
    When 直接送 approve API
    Then 服務端於 memory write 前拒絕；不依賴 UI 按鈕隱藏

  @WEBGIT-008
  Scenario: Approve / discard idempotency
    Given 操作已持久化但回應遺失
    When 重送 approve/discard 或並行互斥操作
    Then 只產生一筆 memory、不逆轉終態、不復活 discarded；回傳既有結果或一致 conflict，失敗不顯示成功

  @WEBGIT-009
  Scenario: Session/workspace race isolation
    Given capture/detail/action 回應延遲
    When 切換 workspace、登出或 session 失效
    Then 清除 list/detail/edit/form/pending action；舊 response 不污染目前畫面，不把 A 的 ID 或草稿送往 B

  @WEBGIT-010
  Scenario: Existing recall / sealed context compatibility
    Given 有 pending candidate 與已接受 Task 快照
    When Web approve、discard 或後續變更正式 memory
    Then candidate 永不 recall；approved memory 可 explicit/bounded recall；原 sealed snapshot 不變

  @WEBGIT-011
  Scenario: Disconnect / cancellation
    Given capture 正在執行 Git 子程序且尚未提交交易
    When HTTP 斷線或 deadline 到達
    Then context 傳至 Daemon 並回收子程序，未完成 capture 不推進 checkpoint、不冒充成功；若交易已提交則重新查詢真實持久化狀態

  @WEBGIT-012
  Scenario: Full regression
    Given 完成本期修改
    When 執行 vet、race tests、build 與正式 CI
    Then go vet ./...、go test -count=1 -race ./...、go build ./cmd/asura 全綠，既有 WEB/WEBMEM/GITMEM/MEM/CTXMEM/RECALL/CTXWIN/TFACE/MBACK/BADAPT/Cron/queue/redelivery/cancel 無退化
```

## Gherkin 核准紀錄

- **核准 commit**: 147954b
- **核准來源**: 2026-09-30 使用者回覆「核准」，核准 WEBGIT-001～012 與 J-TBD-1～8。

| Scenario | 核准日期 | 狀態 |
|---|---|---|
| WEBGIT-001 | 2026-09-30 | 已核准 |
| WEBGIT-002 | 2026-09-30 | 已核准 |
| WEBGIT-003 | 2026-09-30 | 已核准 |
| WEBGIT-004 | 2026-09-30 | 已核准 |
| WEBGIT-005 | 2026-09-30 | 已核准 |
| WEBGIT-006 | 2026-09-30 | 已核准 |
| WEBGIT-007 | 2026-09-30 | 已核准 |
| WEBGIT-008 | 2026-09-30 | 已核准 |
| WEBGIT-009 | 2026-09-30 | 已核准 |
| WEBGIT-010 | 2026-09-30 | 已核准 |
| WEBGIT-011 | 2026-09-30 | 已核准 |
| WEBGIT-012 | 2026-09-30 | 已核准 |

## 風險與首要驗證

- **最大風險**：未經 canonical authorization 即讀寫 candidate，造成跨 workspace 洩漏或正式 memory 污染。
- **風險等級與理由**：High；Web 權限、審核交易與子程序取消均涉及安全邊界。
- **首要驗證**：核對既有 Daemon 授權與原子交易，實作前建立非法 session/Host/Origin、跨 scope 與撤權的契約測試；以 subprocess 與 DB write 計數驗證拒絕先於副作用。
- **選擇理由**：授權／狀態契約測試比完整 UI 垂直切片更直接驗證最大風險；資料上限與 disconnect 另以真實 SQLite／Unix IPC／Git probe 驗證。
- **完成證據**：成功對照能擷取並寫 candidate；拒絕組 0 subprocess、0 candidate/memory writes，跨 scope 不洩漏；Partial approve 0 memory writes。現有查核不是新增 Web 路徑的驗收通過。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|---|---|---|---|
| J-TBD-1～8 | 技術分析中的 API、pagination、bytes、timeout、UI、review、race、IPC 提案 | 已解決 | 2026-09-30 使用者核准提案 |
| APPROVAL-1 | 核准本 README 全部劇本及 technical-analysis 的外部行為與限制 | 已解決 | 2026-09-30 使用者回覆「核准」 |

## Timeline

| 日期 | 異動 | 負責人 |
|---|---|---|
| 2026-09-30 | 讀取立項提案，盤點程式碼與既有測試，建立待核准規格及計畫 | Codex |
| 2026-09-30 | 使用者核准全部 WEBGIT 與 J-TBD；進入實作 | 使用者 |

---
**建立日期**: 2026-09-30  
**分級**: Large — 跨 Web、Daemon、storage 與 Git cancellation 整合  
**風險**: High  
**狀態**: 實作與本機驗證完成；待正式 CI、獨立審查與合併
