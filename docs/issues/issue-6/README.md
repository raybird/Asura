# Issue 6 - Phase 2B 容量政策與動態 Cron 排程管理

建立日期：2026-09-13。追蹤：[GitHub Issue #6](https://github.com/raybird/Asura/issues/6)。承接 [Phase 2A](../issue-4/README.md) 與 [階段規劃](../../plans/post-core-roadmap.md)。

## 概述

本議題旨在落實 Asura 核心之 Phase 2B：容量政策、同專案寫入互斥與動態 Cron 定時排程管理。
透過在常駐中樞（Daemon）內建容量調度器與排程引擎，解決多任務寫入衝突並賦予系統無人值守之自主定時巡檢能力。

## 文件清單

- [現況與需求分析](./requirement-analysis.md)
- [技術方案取捨](./technical-analysis.md)
- [實作計畫](./implementation-plan.md)
- [任務拆解細化](./implementation-plan-decomposition.md)

## 快速導覽

### 當前流程
- 任務無專案互斥：CLI 與 Web 可同時向 Daemon 提交相同 Workspace 的任務，造成並行 AI 修改同一目錄下的程式碼檔案。
- WorkerPool 採純 FIFO：所有任務無差別爭搶 Worker，無保留互動通道。
- 缺乏定時機制：僅能由人類手動在 CLI 或 Web 即時下指令，無法自主定期執行測試、健康檢查或維護任務。

### 重構後流程
- **同專案互斥排隊**：同專案已有任務在執行時，後續任務進入 `queued` 狀態依序執行，杜絕寫入衝突。
- **保留互動通道**：背景 Cron 任務最多佔用 1 個 Worker，隨時為人類互動任務（CLI/Web）預留可用 Worker。
- **動態 Cron 排程**：排程設定持久化於 SQLite，透過 `asura cron` 系列指令動態管理，由 Daemon 依主機本地時區自動定時觸發，支援重疊略過（Skip）與重啟不補跑（No Catchup）。

## 關鍵差異

| 項目 | 變更前 | 變更後 |
|---|---|---|
| 同專案並行性 | 同目錄多任務可並行執行，存在檔案衝突風險 | 同專案嚴格互斥序列化，後續任務排隊等待（`queued`） |
| 容量調度 | 所有任務無差別競爭所有 Workers | 保留互動通道，限制背景 Cron 上限，保障人類指令即時響應 |
| 定時排程 | 無 | 支援標準 5 欄位 Cron、本地時區、重疊略過、重啟不補跑歷史 |
| 排程管理介面 | 無 | CLI 子命令 `asura cron`（add, list, pause, resume, remove） |
| 授權防逃逸 | 僅於任務提交當下檢查 | 排程觸發時複查授權目錄，若已移出白名單則嚴格拒絕並自動暫停 |

## 涉及檔案

```text
cmd/asura/
├── cron.go                  # 預定新增：asura cron 子命令與參數解析
├── run.go                   # 路由分派整合
└── run_test.go              # CLI 參數與子命令單元測試

internal/daemon/
├── daemon.go                # Daemon 中樞整合排程器與同專案互斥管理
├── pool.go                  # 擴充保留互動通道與優先權額度控制
├── scheduler.go             # 預定新增：內嵌 Cron 排程引擎與定時器
└── scheduler_test.go        # 排程引擎核心單元測試

internal/storage/
├── sqlite.go                # 擴充 Schema：cron_schedules 與 cron_runs 資料表
└── sqlite_cron_test.go      # 排程與觸發歷史儲存單元測試

tests/integration/
└── cron_test.go             # 預定新增：CRON-001～CRON-008 端到端整合測試

# === 不可觸及區域 ===
internal/backend/            # 沿用既有 OpenCode Adapter，本期不更動
internal/web/                # 沿用既有 Web 控制台，本期不新增排程介面
```

## Gherkin 驗收劇本

```gherkin
Feature: 常駐中樞容量政策與動態 Cron 排程管理

  @CRON-001
  Scenario: 同專案互斥排隊與依序執行
    Given 授權專案 A 已有執行中的任務 1
    When 使用者或排程提交同專案 A 的任務 2
    Then 任務 2 被成功接受且初始狀態為 queued 排隊等待
    And 任務 1 完成後任務 2 自動啟動執行且兩者不並行修改檔案

  @CRON-002
  Scenario: 保留互動通道與背景容量上限
    Given 背景 Cron 任務已佔用 1 個 Worker 且達到背景並行上限
    When 人類透過 CLI 或 Web 提交即時互動任務
    Then 互動任務立即分配至保留的 Worker 啟動執行而不被背景工作阻塞

  @CRON-003
  Scenario: 動態建立與管理排程
    Given 使用者操作 CLI
    When 執行 asura cron add 指定名稱、授權專案、5 欄位表達式與自然語言提示詞
    Then 排程成功持久化至資料庫且狀態為 active 啟用
    And 可透過 asura cron list 查看排程、下次觸發時刻與狀態
    And 支援透過 asura cron pause/resume 暫停與恢復，及 asura cron remove 刪除排程

  @CRON-004
  Scenario: 排程週期觸發產生可追蹤任務
    Given 已啟用每分鐘執行的有效排程
    When 預定時間點抵達
    Then 自動建立關聯該排程的標準 Task 實體並交由中樞排程執行
    And 使用者可透過既有的 asura status 或 Web 介面查得該任務之執行狀態與結果

  @CRON-005
  Scenario: 排程執行重疊時略過不重複堆疊
    Given 排程 A 的上一次觸發任務仍在執行中
    When 排程 A 的下一個預定觸發時間點抵達
    Then 系統略過本次觸發且不產生新任務
    And 記錄該次觸發因前次執行中而略過（skipped）

  @CRON-006
  Scenario: 重啟或中斷後不補跑過期排程
    Given 排程在服務離線期間錯過了多次預定觸發時間點
    When Daemon 服務重新啟動
    Then 系統不補跑過去錯過的任務
    And 自動以當前重啟時間計算下一個未來觸發點並等待

  @CRON-007
  Scenario: 觸發時非授權目錄嚴格拒絕並自動暫停
    Given 排程已建立但其目錄在觸發時已不在 Daemon 授權清單內
    When 該排程預定時間點抵達
    Then 系統拒絕啟動後端且觸發紀錄標示為 unauthorized 失敗
    And 該排程自動被標記為 paused 暫停狀態且記錄拒絕原因

  @CRON-008
  Scenario: 排程與觸發紀錄於服務重啟後完整持久化
    Given 系統已累積排程定義與多次歷史觸發記錄
    When Daemon 重新啟動
    Then 所有排程定義、啟用狀態、下次觸發時間及歷史觸發記錄完整還原
```

## Gherkin 核准紀錄

- **核准 commit**: `df5b48a`
- **核准來源**: 使用者於 2026-09-13 對話明確核准

| Scenario | 核准日期 | 狀態 |
|----------|---------|------|
| CRON-001 | 2026-09-13 | 已核准 |
| CRON-002 | 2026-09-13 | 已核准 |
| CRON-003 | 2026-09-13 | 已核准 |
| CRON-004 | 2026-09-13 | 已核准 |
| CRON-005 | 2026-09-13 | 已核准 |
| CRON-006 | 2026-09-13 | 已核准 |
| CRON-007 | 2026-09-13 | 已核准 |
| CRON-008 | 2026-09-13 | 已核准 |

## 實作步驟概要

1. ✅ **步驟 1**：儲存層 Schema 擴充（新增 `cron_schedules` 與 `cron_runs`）與 SQLite 遷移
2. ✅ **步驟 2**：容量調度器升級（同專案排隊互斥鎖與保留互動通道，CRON-001, CRON-002）
3. ✅ **步驟 3**：內嵌 Cron 排程引擎實作（標準 5 欄位、時鐘計算、重疊略過與漏跑不補，CRON-003, CRON-004, CRON-005）
4. ✅ **步驟 4**：CLI 子命令 `asura cron` 接線與 Daemon IPC 擴充（CRON-006, CRON-007）
5. ⏳ **步驟 5**：雙迴圈整合測試、服務重啟驗收與文件更新

## 風險與首要驗證

- **最大風險**：排程設定之目錄不在授權清單內卻觸發本機執行；或同專案多任務並行造成原始碼毀損。
- **風險等級與理由**：High。涉及定時自主觸發本機 AI 與檔案寫入副作用。
- **首要驗證**：建立「同專案互斥排隊」與「未授權目錄排程觸發即刻拒絕並自動暫停」的外迴圈整合測試紅燈。
- **選擇理由**：在實作排程計時器之前，先確保安全防護與專案寫入鎖有效，最直接降低最大風險。
- **完成證據**：同專案並行請求確實回傳 `queued` 並序列化執行；未授權排程觸發日誌為 `unauthorized`、後端零啟動、排程自動轉為 `paused`。

## Timeline

| 日期 | 異動 |
|------|------|
| 2026-09-13 | 建立 Issue #6 規格說明與 BDD 驗收條件，使用者核准（commit `df5b48a`） |
| 2026-09-13 | 完成 Phase 1 儲存層擴充與 CRON-008 持久化驗證（commit `1ec1a48`） |
| 2026-09-13 | 完成 Phase 2 容量調度器升級（CRON-001 同專案互斥排隊、CRON-002 保留互動通道，commit `2c8855d`） |
| 2026-09-13 | 完成 Phase 3 內嵌 Cron 排程引擎核心（CRON-003 5 欄位解析、CRON-004 重疊略過與漏跑不補、CRON-005 未授權自動暫停，commit `f05dd94`） |
| 2026-09-13 | 完成 Phase 4 CLI 子命令 `asura cron` 接線與 Daemon IPC 擴充（CRON-006, CRON-007） |

---

**建立日期**: 2026-09-13  
**分級**: Large（跨 Daemon 容量與排程、Storage Schema 擴充、CLI 子命令與整合測試）  
**風險**: High（定時自主觸發與檔案寫入互斥）  
**狀態**: 已核准，待實作
