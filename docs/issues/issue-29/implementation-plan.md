# Issue 29 實作計畫

建立日期：2026-10-01。唯一任務來源。設計提案見 technical-analysis.md；風險與首要驗證引用 README。2026-10-01 使用者已核准；以下 Task 是唯一可執行來源，Phase 表為里程碑摘要。

## 設計與介面

沿用 bounded selector；擴充 sealed Decision 的 RecallID、utility policy/version 及 selection scoring audit。storage 擴充原子 `CreateTaskWithRecall`、`PersistUtility`、scope-bound `GetMemoryUtility`；具體 Go 簽名於 Task 拆解時依既有 store 邊界固定。engine 從 sealed snapshot／terminal trace 觸發 assessment，不由 backend 擁有 ledger。

## 分階段清單

| Phase | 狀態 | 對應驗收 | 產出 | 相依 | 完成判準 |
|---|---|---|---|---|---|
| P0 契約與可信度 probe | ✅ 已完成 | 003、004、005、011 | 正反 fixture、固定版本模型輸出、ID validation／呼叫數證據 | 規格核准、probe backend/model 可用 | technical-analysis K-TBD-10 判準；失敗不進 apply |
| P1 immutable ledger | ✅ 已完成 | 001、002、003、006、009、011 | migration、Task/event 原子寫入、bounded recovery | P0 契約成立 | rollback/restart/redelivery 不產生孤兒或重複；sealed snapshot 不變 |
| P2 assessment 與 aggregate | ✅ 已完成 | 004、005、006、008、009、011 | optional Critic usage、驗證、交易聚合、重建 | P1 | missing/invalid unknown；Direct 0 extra calls；重建 count 與 ledger 相符 |
| P3 audit 與容量 | ✅ 已完成 | 003、010、011 | CLI/IPC/status、10k/100k fixture 與 query plan | P2 | scope fail-closed、limit/cursor/field/response bounds、bounded recovery 證據 |
| P4 opt-in apply | ✅ 已完成 | 001、002、007、008、011 | utility-v1 multiplier 與 sealed score | P0 可信度成立、P3 | neutral/threshold/floor/tie golden fixture，observe 完全相容，deadline 未放寬 |
| P5 交付 | ✅ 已完成（2026-10-21 回填：[review-d01816f.md](review-d01816f.md) PASS，PR #30 於 2026-10-01 合併，merge commit `1b21d58`） | 012 與全部 | 常青文件、固定 BASE/HEAD、PR、獨立 review | P1～P4 | 完整回歸、正式 CI 與持久化 review evidence |

## 使用方式對照

既有 ask/status/memory CRUD 預設行為保留。提案新增 Daemon `ASURA_MEMORY_UTILITY_POLICY` 與 `memory utility` 查詢，observe 預設、apply 明確 opt-in。具體 bounds 見技術分析；核准前不新增可執行入口。

## 測試策略

- UFL-001、007、008：selector golden 單元測試保護 lexical order、explicit 去重、hard bounds、neutral/floor/rounding/tie；Daemon 整合確認封存與設定。
- UFL-002、003、006、009：SQLite transaction／migration／retry 單元測試與 Daemon canonical scope、redelivery、restart、刪除整合測試。兩層有不同失敗面，保留雙迴圈紅燈。
- UFL-004、005、011：Critic schema 與 invalid ID 單元測試，engine success/fail/cancel/partial、DB fault 與呼叫數整合測試。模型一致性另用獨立標註 probe，不以 mock 冒充。
- UFL-010：storage bounded SQL/query plan、大量資料實驗與 CLI/IPC invalid limit/cursor/oversize 整合測試。
- UFL-012：`go vet ./...`、`go test -count=1 -race ./...`、`go build -o /tmp/asura-issue-29 ./cmd/asura`，正式 CI 與回歸驗收標籤完整性查核。
- 行為修改先取得目標紅燈，再最小實作、code-simplify 與最終綠燈；文件以連結、Scenario 集合、核准狀態與 git diff 靜態檢查替代。

## 規劃證據與檢查清單

2026-10-01：以基準程式碼與 GitHub issue 原文比對；建立時 README 所有 UFL-001～012 均待核准；使用者於 2026-10-01 核准後才實作，沒有把規劃授權當 production 授權。四份文件的本地連結、12 個 Scenario／核准列及 fence 靜態檢查通過；git diff --check 通過。基準 `go test ./internal/recall ./internal/pipeline ./internal/storage ./internal/daemon ./cmd/asura` exit 0，五個 package 全綠；此為既有行為基線，非 UFL 驗收。沒有 production 修改，紅綠測試不適用。

- [x] 確認基準、需求來源、模組邊界與 Large／High 分級。
- [x] 盤點 recall、Task admission、Critic schema、storage SQL bounds 與 trace persistence。
- [x] 提出 K-TBD-1～10、比較 ledger/JSON 與 aggregate 策略。
- [x] 2026-10-01 使用者核准具體契約，probe 沿用現有 OpenCode 設定。
- [x] 2026-10-01 在本檔拆解，P0～P4 證據見 evidence/verification.md。

## 核准後責任 Task（2026-10-01）

### Task P0.1 — Critic optional usage 契約與可信度 probe

- 狀態：✅ 已完成
- 產出與最小範圍：pipeline optional raw usage、allowlist 驗證；唯讀 3 fixtures × 3 固定模型 probe
- 相依：無
- 責任驗收：UFL-004
- 完成判準：invalid 區塊全 unknown、legacy review 合法；模型可判定例至少 8/9 一致；保留輸入輸出
- 驗證策略：pipeline stage 單元紅綠、實際模型 probe；Direct 整合在 P2.1
- 證據：2026-10-01 [verification.md](evidence/verification.md) 的紅燈、最小實作、精煉、race／probe 紀錄。

### Task P1.1 — immutable ledger 與原子 admission

- 狀態：✅ 已完成
- 產出與最小範圍：storage migration、ledger、Daemon canonical admission
- 相依：P0.1 契約驗證通過
- 責任驗收：UFL-002、UFL-003
- 完成判準：Task/event 交易原子、redelivery 唯一、sealed 不變、scope 隔離
- 驗證策略：SQLite fault/restart 單元與 Daemon 整合紅綠
- 證據：2026-10-01 [verification.md](evidence/verification.md) 的紅燈、最小實作、精煉、race／probe 紀錄。

### Task P2.1 — assessment／aggregate／recovery

- 狀態：✅ 已完成
- 產出與最小範圍：engine terminal hook、storage 原子 assessment／aggregate、bounded recovery／rebuild
- 相依：P1.1
- 責任驗收：UFL-005、UFL-006、UFL-009、UFL-011
- 完成判準：Direct 額外模型呼叫 0、retry count 一次、取消 unknown、錯誤不改主結果、deleted 不復活
- 驗證策略：storage 單元與 engine/Daemon 整合紅綠、cache 與 ledger 獨立對照
- 證據：2026-10-01 [verification.md](evidence/verification.md) 的紅燈、最小實作、精煉、race／probe 紀錄。

### Task P3.1 — bounded utility audit 與容量

- 狀態：✅ 已完成
- 產出與最小範圍：Daemon IPC、CLI memory utility/status、10k/100k probe
- 相依：P2.1
- 責任驗收：UFL-010；支援 UFL-001 當前 policy/effective weight 的明確稽核。
- 完成判準：授權與 keyset/query/field/64KiB bounds、索引與 bounded recovery
- 驗證策略：storage query plan/容量 probe、IPC/CLI 整合紅綠
- 證據：2026-10-01 [verification.md](evidence/verification.md) 的紅燈、最小實作、精煉、race／probe 紀錄。

### Task P4.1 — observe/apply 與 sealed ranking

- 狀態：✅ 已完成
- 產出與最小範圍：recall multiplier、Daemon 設定入口、sealed score
- 相依：P3.1、P0.1 可信度通過
- 責任驗收：UFL-001、UFL-007、UFL-008
- 完成判準：預設 lexical 相容、threshold 5、floor .5、unknown neutral、explicit 不降權、250ms 不放寬
- 驗證策略：golden selector 單元與 Daemon sealed/redelivery 整合紅綠
- 證據：2026-10-01 [verification.md](evidence/verification.md) 的紅燈、最小實作、精煉、race／probe 紀錄。

### Task P5.1 — 固定版本交付與獨立審查

- 狀態：交付驗證完成；獨立 review 結果以持久化 artifact 判定
- 產出與最小範圍：常青文件、完整回歸、提交、PR、正式 CI、獨立 review
- 相依：P4.1
- 責任驗收：UFL-012
- 完成判準：固定 BASE/HEAD 證據完整；review PASS 或修正再審
- 驗證策略：vet/race/build、CI、持久化 review
- 證據：2026-10-01 [verification.md](evidence/verification.md)、正式 CI 36807429281、PR #30；獨立 review 的固定範圍與判定保存於同目錄 review artifact。PASS 後本 Task 完成並等待合併，未執行 merge。
