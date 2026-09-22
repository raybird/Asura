# 實作計畫

2026-09-22。權威任務來源僅本檔。風險與首要驗證見 [README](README.md#風險與首要驗證)。2026-09-22 使用者已核准全部規格，以下細化為唯一權威任務清單。

## 設計方案

建議 `internal/recall.Select(ctx, Input) (Decision, error)`；Input 包含 canonical workspace、原始 prompt、explicit IDs、有限候選與 policy。組合實際 prompt 的預算檢查由服務端串接既有 ComposePrompt／stage preparation。selector 不存取資料庫、不呼叫 AI；storage 獨立負責有界候選讀取。

使用方式：既有 `asura ask --memory-id ID "prompt"` 完全沿用；核准後可加 `--memory-recall bounded`。

## 規劃階段

1. ✅ 現況盤點與邊界刻畫 — RECALL-001、002、006、007；產出 requirement-analysis 及既有隔離測試證據；來源為 issue 已授權的規劃／探測。完成證據見 [probe-results](evidence/probe-results.md)。不是新功能驗收。
2. ✅ 最小詞彙／SQL／預算探測 — RECALL-003、004、010；相依前項；產出 fixture、benchmark、真實 query plan 與限制。完成證據同上。
3. ✅ 固定規格 — RECALL-001～010；產出 D1–D5、Gherkin、核准來源與規格 commit。文件已準備，2026-09-22 使用者已核准。

## 核准後階段概要

| 階段 | 驗收 | 產出 | 相依 | 完成判準 |
|---|---|---|---|---|
| Phase 1 | 001、002、004、005、006 | 外迴圈紅燈、off 字串／指紋刻畫、安全與有界 storage | 完整核准及 decompose | 跨 workspace、oversize、deadline、single connection release 與索引測試紅綠證據 |
| Phase 2 | 003、004、005 | selector、query caps、ComposePrompt 增量及首次 stage budget | Phase 1 | 混合語料、determinism、metadata limits、budget 邊界內外測試與無 AI 證據 |
| Phase 3 | 001、006、007、009 | IPC/ask、seal、fingerprint、status | Phase 2 | 0-added、同 ID 競態、資料變動與重啟、舊 JSON/SQLite fixtures 通過 |
| Phase 4 | 008、010 | stage 整合、完整回歸、常青文件與交付 | Phase 3 | 動態 overrun、保留 trace、壓力測試及全套命令通過，持久化獨立 review |

## 測試策略

- 外迴圈：tests/integration 新增 RECALL-001～010，使用真實 SQLite、Daemon IPC 與 recording backend，觀察輸入、Task、呼叫數；不以 mock selector 取代安全驗收。
- 內迴圈：selector fixture、storage query plan/limits/cancel、budget wrapper、fingerprint、JSON 相容、CLI option forwarding。各自有獨立失敗面，保留雙迴圈。
- 純格式化共用重構：前後相同 characterization tests；產品行為新增先 red，再實作、green、code-simplify/no-op。
- 壓力：固定 10001 筆、1 MiB 單筆、最大 metadata、最大 query；記錄資料量、SQL、bytes、elapsed、配置、環境與結果。deadline 注入可控制取消條件，驗證後續查詢成功；不把極短 wall-time 偶然成功當 timeout 測試。
- 交付命令：`go vet ./...`、`go test -count=1 -race ./...`、`go build -o /tmp/asura-issue19 ./cmd/asura`。涵蓋既有 CTXMEM／CTXWIN／TFACE／MBACK／CRON／WEB、PGID cancel 與 redelivery。
- 2026-09-22：產品紅綠及交付命令證據見下方 Task；獨立 review 尚待完成。

## 檢查清單

- [x] 已依 Large/High 分別評估；四份文件各有實際用途。
- [x] 涉及檔案與風險僅在 README 維護；待核准表覆蓋全部十個 Scenario。
- [x] 探測已持久化命令、環境、結果與限制；無 production code 變更。
- [x] TBD-1～5 於 2026-09-22 核准；基線 aeaa652。
- [ ] decompose、紅綠驗證、精煉、提交、PR 與獨立 review。

## Phase 1 — 有界儲存與安全刻畫
### Task 1.1 — 候選查詢
- 產出與範圍：storage 複合索引、有界候選 API 及 SQL/cancel 測試；外層 opt-in 隔離紅燈。
- 相依：核准基線；前置既有安全刻畫已通過。
- 責任驗收：RECALL-002、004；支援 010。
- 完成判準：同 scope、257 哨兵、metadata/content limits、取消釋放連線與索引計畫證據。
- 驗證：真實 SQLite 單元＋真實 Daemon IPC 外層，兩層失敗面不同。
- 狀態：✅ 已完成
- 證據：2026-09-22 [交付驗證](evidence/verification.md) 與 [命令紀錄](evidence/implementation.log)。

## Phase 2 — 選取與預算
### Task 2.1 — 確定性 selector
- 產出與範圍：recall 純函式、query caps、排名及格式化增量／首次 stage admission。
- 相依：Task 1.1 儲存綠燈；整合外層待 Task 3.1 完成。
- 責任驗收：RECALL-003、005。
- 完成判準：中英、無匹配、tie、大小邊界、explicit 保留、窗口 unknown/observe/reject 測試通過。
- 驗證：selector 單元紅綠與 IPC 外層；Oracle prompt 共用既有 Prepare，不需改動其字串。
- 狀態：✅ 已完成
- 證據：2026-09-22 [交付驗證](evidence/verification.md) 與 [命令紀錄](evidence/implementation.log)。

## Phase 3 — 密封與介面
### Task 3.1 — 接受時召回
- 產出與範圍：Daemon、ExecutionContext、ask/status、舊 JSON 與 queue/restart 整合。
- 相依：Task 1.1、2.1 底層綠燈。
- 責任驗收：RECALL-001、006、007、009；完成 Task 1.1/2.1 外層整合。
- 完成判準：default-off、unknown rejection、0-added、指紋衝突、重送 0 query、競態唯一任務、重啟與 audit 通過。
- 驗證：CLI 單元、Daemon 真實 IPC、SQLite sealed JSON 及 integration 紅綠。
- 狀態：✅ 已完成
- 證據：2026-09-22 [交付驗證](evidence/verification.md) 與 [命令紀錄](evidence/implementation.log)。

## Phase 4 — 交付
### Task 4.1 — 動態 stage、效能與完整回歸
- 產出與範圍：整合測試、壓力證據、README/架構文件、PR 與 review。
- 相依：Task 3.1。
- 責任驗收：RECALL-008、010。
- 完成判準：動態 overrun 保留 trace、無額外呼叫、vet/race/build、code-simplify 與獨立 review。
- 驗證：真實 orchestrator＋recording backend；全套回歸；文件靜態檢查。
- 狀態：⏳ 實作／驗證／文件完成；待 PR 與獨立 review。
- 證據：2026-09-22 [交付驗證](evidence/verification.md) 與 [命令紀錄](evidence/implementation.log)。
