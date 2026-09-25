# Issue 23 - Phase 3H：Git-Aware Project Memory Capture（Git 變更感知與審核式記憶擷取）

## 概述

為 Asura 提供**顯式觸發、有界唯讀、隔離且需人工審核**的 Git 變更感知與專案記憶擷取能力。讓 CLI 使用者能針對授權 workspace 的 Git repository，提取固定範圍（commits／diffs）的架構與決策事實，產生待審候選（candidates），經使用者檢視／編修／核准後，以同 scope 的正式專案記憶寫入既有 SQLite，供後續任務召回使用。來源為 [GitHub #23](https://github.com/raybird/Asura/issues/23)。

本 issue 當前授權現況調查、方案分析與不造成新 production 行為的安全 probe；具體 Git 擷取方式、checkpoint、AI 草稿、schema、上限與 GITMEM-001～010 驗收劇本待需求與技術分析確認後由使用者正式核准。

## 文件清單

- [requirement-analysis.md](./requirement-analysis.md)
- [technical-analysis.md](./technical-analysis.md)
- [implementation-plan.md](./implementation-plan.md)

## 關鍵差異

| 項目 | 變更前 | 變更後（待核准目標） |
|---|---|---|
| **專案記憶來源** | 僅能由使用者以 `asura memory add` 手動建立 | 支援手動建立，亦可顯式針對 Git 變更執行 capture 產生待審候選 |
| **Git 感知能力** | 無，完全不與 Git 儲存庫互動 | 透過安全封裝之 Git 子程序唯讀讀取固定 base..head 變更事實 |
| **審核生命週期** | 無審核機制，新增即為正式記憶直接生效 | 擷取後存為 pending 候選草稿，經 CLI 審核／編修／核准後才轉入正式記憶 |
| **自動記憶召回** | 僅檢索 manual 記憶 | 自動召回已核准之 `git-approved` 記憶；未審核草稿 100% 隔離零召回 |

## 涉及檔案

```text
cmd/asura/
├── memory.go                 # 可改：擴充 capture/candidates/approve/discard CLI 子命令
└── main.go                   # 不可觸及：入口分發保持穩定
internal/
├── auth/policy.go            # 可改：輔助檢驗 Git root 與 allowed roots 包含關係
├── daemon/
│   ├── daemon.go             # 可改：新增 Git 記憶擷取與審核之 IPC action 分發
│   └── gitmem_handlers.go    # 可改：實作 Git 記憶 IPC 處理常式
├── gitmem/                   # 可改：新增模組，包含安全 Git CLI 執行、range、hard bounds、facts 封裝
├── storage/
│   ├── sqlite.go             # 可改：新增 memory_candidates 與 git_checkpoints 表
│   ├── sqlite_memory.go      # 可改：支援 git-approved 來源標記寫入
│   └── sqlite_gitmem.go      # 可改：候選與 checkpoint 的 CRUD 及交易支援
├── recall/recall.go          # 不可觸及：保持 Phase 3F lexical-v1 選擇器與額度不變
├── contextwin/               # 不可觸及：保持 Context Window 預估模型不變
├── pipeline/                 # 不可觸及：保持 Three-Face Pipeline 執行邏輯不變
└── web/                      # 不可觸及：本期為 CLI 閉環，不擴充 Web 入口
tests/
└── integration/              # 可改：新增 Git 記憶擷取、邊界負向、候選審核與端到端測試
docs/
├── issues/issue-23/          # 可改：維護本 issue 規格、分析、計畫與探測證據
├── superpowers/specs/        # 交付時更新：系統架構設計與資料流
└── AGENTS.md                 # 不可觸及：上游規則
README.md                     # 交付時更新：使用者 CLI 記憶操作說明
```

## Gherkin 驗收劇本

```gherkin
Feature: Git 變更感知與審核式專案記憶擷取

  @GITMEM-001
  Scenario: Opt-in 與唯讀安全
    Given 任意授權 workspace 的 Git repository
    When 未顯式呼叫 Git 記憶擷取，或呼叫擷取後檢視原 Git repository
    Then 未呼叫時無任何 Git 掃描與記憶寫入
    And 擷取後原 repo 之 branch、HEAD、index、worktree、commit、Git config 與 ref 完全無改動
    And 原 repo 工作樹內未產生任何 .asura 或暫存檔案

  @GITMEM-002
  Scenario: Scope 與 repo identity 嚴格授權
    Given 授權 workspace A/B、父子目錄、symlink 別名、越界路徑、未授權目錄及非 Git 目錄
    When 執行 Git 記憶擷取或候選操作
    Then Daemon 重新授權 exact canonical workspace 且確認 Git 頂層工作樹與 repo identity
    And 拒絕未授權路徑、越界 symlink、非 Git 目錄及撤權 workspace，並維持 0 memory writes 與 0 model calls

  @GITMEM-003
  Scenario: Fixed range 與 branch 增量隔離
    Given 明確指定 base..head、不同分支、merge commits、ref 重寫或 unrelated history
    When 執行變更感知與擷取
    Then 僅在起迄 commits 具備祖先血統關係時進行分析，並將所有候選回溯至固定 Git SHA
    And 分支切換、未關聯歷史或 ref 重寫時拒絕靜默混合，防止跨分支增量混淆

  @GITMEM-004
  Scenario: Bounded capture 硬性邊界防護
    Given 包含大量 commits、巨幅 diff、binary 檔案、submodule、惡意檔名、超大單檔或超時取消的情境
    When 觸發 Git 變更分析與擷取
    Then 嚴格受 hard bounds 限制（commits 數量、diff bytes、檔案數量、執行逾時），超出時明確標示截斷
    And binary、submodule 與超限檔案自動略過或僅記錄變更標記，不得無界讀入或假裝資料完整

  @GITMEM-005
  Scenario: Draft vs approval 候選隔離與人工審核
    Given 已擷取的候選記憶草稿與已存在的正式專案記憶
    When 檢視候選、列出候選或執行 Task 記憶召回
    Then 候選記憶與已核准正式記憶隔離儲存，未經核准之候選不得進入 Task 之 bounded recall
    And 使用者可透過 CLI 檢視、編修或捨棄候選，僅在明確核准後才生成同 scope 之正式記憶

  @GITMEM-006
  Scenario: Provenance 客觀真實性與來源追溯
    Given 候選記憶與核准轉入的正式專案記憶
    When 檢驗其記憶來源與內容
    Then 候選記憶明確區分 Git 原始客觀事實（commits/paths/diff stats）與摘要文字
    And 正式記憶保留可追溯之來源 SHA、workspace、ref 與核准時間，不將推測性文字當作已驗證事實

  @GITMEM-007
  Scenario: 崩潰恢復、交易保證與重送冪等
    Given 擷取、儲存候選、checkpoint 更新與核准各階段注入崩潰或失敗
    When 系統重啟並重新執行相同要求
    Then 任何部分失敗不冒充成功，不前進未完成之 checkpoint
    And 相同範圍與政策之重複請求維持冪等，不產生重複候選或重複正式記憶

  @GITMEM-008
  Scenario: Sealed context 密封相容性
    Given 經 Git 變更感知核准之正式記憶，以及已接受或執行中的任務
    When 提交新任務或後續召回，以及變更／刪除 Git-sourced 記憶
    Then 核准的記憶可供後續 explicit 或 bounded 召回使用，且相容既有五欄位 API 與 Phase 3F 選擇器
    And 記憶的變更或重新擷取不改寫歷史已接受任務之密封快照與 fingerprint

  @GITMEM-009
  Scenario: 唯讀模型邊界（若本期核准 AI 草稿）
    Given 含惡意 commit message、注入指令之 diff 或不受信任之 Git 內容
    When 啟用 AI 輔助生成候選草稿摘要
    Then 模型在唯讀權限邊界（deny mutating capabilities）與固定 token 預算下執行
    And 模型無法直接寫入資料庫或外送資料，任何生成失敗或超限均如實揭露並退回手動編修模式

  @GITMEM-010
  Scenario: 完整回歸與系統穩定性
    Given 既有 CLI、Daemon、SQLite、Web、Context Window、Cron 與 Multi-Backend 系統
    When 加入 Git 變更感知與審核式記憶擷取後執行完整回歸
    Then MEM、CTXMEM、RECALL、WEBMEM、CTXWIN、TFACE、MBACK、Cron 與 queue/redelivery 回歸測試維持通過
    And go vet ./...、go test -count=1 -race ./... 與 go build ./cmd/asura 均全綠通過
```

## Gherkin 核准紀錄

- **核准 commit**: 待提交
- **核准來源**: 使用者於 2026-09-25 提交 GitHub Issue #23，授權現況調查、方案分析與安全 probe；GITMEM-001～010 驗收行為與 H-TBD-1～6 待需求與技術分析確認後由使用者正式核准。

| Scenario | 核准日期 | 狀態 |
|---|---|---|
| GITMEM-001 | - | 待核准 |
| GITMEM-002 | - | 待核准 |
| GITMEM-003 | - | 待核准 |
| GITMEM-004 | - | 待核准 |
| GITMEM-005 | - | 待核准 |
| GITMEM-006 | - | 待核准 |
| GITMEM-007 | - | 待核准 |
| GITMEM-008 | - | 待核准 |
| GITMEM-009 | - | 待核准 |
| GITMEM-010 | - | 待核准 |

## 實作步驟概要

權威任務與相依見 [implementation-plan.md](implementation-plan.md)。本期已授權 Phase 1 現況調查與安全 probe；Phase 2～6 待 H-TBD-1～6 與 Gherkin 驗收核准後推進實作。

## 風險與首要驗證

- **最大風險**：Git 差異讀取若未嚴格設限或存在參數/路徑逃逸，可能造成非預期的檔案修改、任意指令注入、巨量資料耗盡 Daemon 記憶體，或因未審核草稿提前外洩進入召回 prompt。
- **風險等級與理由**：High；涉及未受信任之外部 Git 儲存庫輸入、子程序安全邊界、檔案系統路徑授權及潛在的記憶污染。
- **首要驗證**：在實作生產程式碼前，先於測試倉庫構建包含 symlink 逃逸、submodule、二進位檔案、巨大 diff、無關歷史（unrelated history）與注入參數的負向樣本集，探測 Git CLI 安全唯讀旗標與 SQLite 候選儲存隔離能力，產出安全性探測報告。
- **選擇理由**：先以隔離探測直接檢驗 Git 子程序呼叫與資料邊界，能以最低成本排除原 repo 被意外修改及資料逃逸的致命風險；單看單元測試無法保證外部 Git 子程序的真實行為。
- **完成證據**：探測報告證實原 repo 之 HEAD、branch、worktree、index 與 Git config 零變更，且所有異常邊界均能被有效攔截與揭露截斷，無未經審核之草稿寫入 `memories`。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|---|---|---|---|
| H-TBD-1 | 第一版 Git range／ref／checkpoint 策略；分支、merge、force-push、uncommitted 狀態 | 待確認（技術分析已提供 1A/1B 比較） | 影響 GITMEM-003、GITMEM-007、Task 2.2、4.1 |
| H-TBD-2 | Repo identity、路徑授權與 Git 子程序安全、固定 SHA 防 TOCTOU 策略 | 待確認（技術分析已提供防護矩陣） | 影響 GITMEM-001、GITMEM-002、Task 2.1 |
| H-TBD-3 | 擷取數量／bytes／時間上限與 binary、symlink、submodule、secret policy | 待確認（技術分析已提供保守數值） | 影響 GITMEM-004、Task 2.3 |
| H-TBD-4 | 候選 schema、provenance、審核命令／修改方式、保留與刪除；正式 memory source 向後相容 | 待確認（技術分析已提供獨立表設計） | 影響 GITMEM-005、GITMEM-006、Task 3.1、3.2 |
| H-TBD-5 | AI 草稿是否納入本期、模型執行預算與唯讀／prompt-injection 邊界；未核准時的無模型最小交付 | 待確認（技術分析已提供無模型最小交付） | 影響 GITMEM-009、Task 5.2 |
| H-TBD-6 | 候選去重、核准冪等、checkpoint 交易／崩潰恢復，及 GITMEM-001～010 的最終驗收 | 待確認（技術分析已提供交易方案） | 影響 GITMEM-007、GITMEM-010、Task 4.1、4.2 |

## Timeline

| 日期 | 異動 | 負責人 |
|---|---|---|
| 2026-09-25 | 建立 Issue 23，完成現況調查、技術分析、初始實作計畫與驗收規格草案 | - |

---
**建立日期**: 2026-09-25  
**分級**: Large  
**風險**: High  
**狀態**: 立項／待需求與驗收核准
