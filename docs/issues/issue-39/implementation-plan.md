# Issue 39 — 初始實作計畫

2026-10-04 建立。唯一任務來源；2026-10-04 使用者核准後，依 decompose 細化以下責任 Task。

## 設計方案

新增 inventory model 與 CaptureInventory(ctx, registeredSnapshot)；RuntimeProber 為可選 adapter probe 介面。AdmitRuntime 對 auto 使用一次全註冊 inventory、explicit 使用一次指定 runtime inventory，傳 available descriptors 給原 planner。新 ExecutionContract 包含 InventorySnapshot；新 validator 以封存資料核對，不以當前 Registry 改寫。

模型與限制以 technical-analysis RI-TBD-1～15 為提案來源；風險與首要驗證只引用 [README](./README.md#風險與首要驗證)。

## Phase 與責任 Task

### Task 1 — 首要風險紅燈（Phase 1）
- 產出與最小範圍：daemon admission／pipeline failure isolation、timeout/cancel、sealed disappearance 整合測試；不改 production。
- 相依：無；核准基線 a88fe79。
- 驗收：支援 RTINV-003/004/007/008/010/013；最終責任分別在 Task 2／3。
- 完成判準：取得目標行為的失敗，記錄 Task/Run 零副作用斷言與失敗原因。
- 驗證：daemon 外迴圈及 pipeline 執行邊界；後續 inventory 單元紅燈保留另一失敗面。
- 狀態：✅ 已完成
- 證據：[驗證紀錄](./verification-evidence.md#紅燈與修復)；daemon error/timeout 阻擋 Codex、pipeline 版本錯誤仍 Run 的真實紅燈。

### Task 2 — Inventory 與 adapter probe（Phase 2）
- 產出與最小範圍：backend inventory、authoritative snapshot、四 adapter context-aware bounded probe。
- 相依：Task 1 紅燈。
- 責任驗收：RTINV-001/002/003/008/009/012/013。
- 完成判準：state/reason/identity、timeout/panic/cancel、deterministic serialization、secret/output/size bounds 與 adapter fixture 全綠。
- 驗證：inventory 單元與 adapter 真實 subprocess/file fixture；同層 API 行為可合併迴圈，跨 subprocess 資源回收另驗。
- 狀態：✅ 已完成
- 證據：[驗證紀錄](./verification-evidence.md) 對應驗收與定向 race 全綠；code-simplify 已檢查本次 API/序列化契約，保留必要 probe 邊界。

### Task 3 — Admission、sealed execution（Phase 3）
- 產出與最小範圍：auto/explicit integration、新 contract validator、engine/pipeline 辨識、guard 與 trace。
- 相依：Task 2。
- 責任驗收：RTINV-004/005/006/007/010/011。
- 完成判準：single failure 不污染 auto；explicit 不 fallback；Three-Face 共用 snapshot；sealed disappearance 與 recovery 不改綁。
- 驗證：daemon/local Task/dispatch/Run 與 workspace sentinel 整合、planner/validator 單元。
- 狀態：✅ 已完成
- 證據：[驗證紀錄](./verification-evidence.md) 對應驗收與定向 race 全綠；code-simplify 已檢查本次 API/序列化契約，保留必要 probe 邊界。

### Task 4 — Audit 與 persistence（Phase 4）
- 產出與最小範圍：local/daemon composition、inventory CLI/IPC/Web、sealed task status、legacy/queue/restart fixtures、README/架構文件。
- 相依：Task 3。
- 責任驗收：RTINV-014；支援 RTINV-002/007/011/012/015。
- 完成判準：四個固定 registered ID；CLI/Web state/reason 可讀；status 不 reprobe；legacy 不回填；queue/redelivery/restart 保留封存契約。
- 驗證：CLI/HTTP/IPC 外迴圈、SQLite reopen/queue fixtures，常青文件靜態檢查。
- 狀態：✅ 已完成
- 證據：[驗證紀錄](./verification-evidence.md) 對應驗收與定向 race 全綠；code-simplify 已檢查本次 API/序列化契約，保留必要 probe 邊界。

### Task 5 — 回歸交付（Phase 5）
- 產出與最小範圍：完整測試、code-simplify、交付 commit/PR、獨立 review artifact。
- 相依：Task 4。
- 責任驗收：RTINV-015。
- 完成判準：vet/race/build 全綠；全部驗收證據；固定 BASE/HEAD；獨立審查 PASS 或修正後再審。
- 驗證：全 repo 回歸與範圍一致性；審查遵循 review-evidence。
- 狀態：✅ 已完成；獨立審查 [review-5bf6351.md](review-5bf6351.md) PASS，PR #40 於 2026-10-04 合併（merge commit `2f583c7`），PR CI [run 37162742387](https://github.com/raybird/Asura/actions/runs/37162742387) success（2026-10-21 回填）
- 證據：[驗證紀錄](./verification-evidence.md)；2026-10-04 vet／全套 race／build 全綠。最後的獨立審查以同次 `review-{HEAD7}.md` 的固定版本與 PASS 判定結案 Task 5；不自行 merge。

## 使用方式對照

- 既有 run/ask/cron 的 explicit/auto 參數沿用；auto 對 unavailable runtime 排除，explicit unavailable 拒絕。
- 提案新增 `asura runtime inventory` 查詢 fresh inventory；Task `status` 則只展示該任務封存 snapshot。
- Web 增加最小唯讀 inventory／task audit，沿用認證與授權，不加入修改 runtime 的 API。

## 測試策略

| 驗收 | 外迴圈 | 內迴圈 |
|---|---|---|
| 001/002/003/008/009/012/013 | registry → inventory 真實 orchestrator＋fake runtime，adapter subprocess/file fixture | 型別、分類、排序、邊界；同可觀察層級可合併，但需記理由 |
| 004/005/006/007 | daemon/local admission 至 Task persistence 與 dispatch 計數；標記 Scenario ID | inventory filter 與 planner/validator |
| 010/011 | sealed queue/pipeline execution、runtime 消失／恢復、Run 與 workspace sentinel | sealed guard 與 contract validate |
| 014 | CLI 輸出、IPC、Web HTTP 與前端 detail；assert registration/state/reason/legacy | audit presenter／error mapping |
| 015 | legacy persisted fixtures、restart、redelivery、Cron/context/cancellation 及全套 race 回歸 | 舊 validators 與封存一致性 |

成功與失敗預期來自規格字面值，不重算被測演算法。對時間測試使用同步 barrier 而非只靠睡眠；snapshot ordering 固定 clock。output 邊界用真正 fixture process 驗證讀取上限，不能只驗持久化截斷。secret sentinel 必須確認不存在於 error、JSON、trace 與 UI。

會改行為的 Task 先取得對應行為紅燈，再最小實作與綠燈；no-op 精煉明列理由。文件及規劃以靜態檢查為證據，不冒充程式測試。四內建 adapter 使用隔離 fixture，不發送模型請求或讀取真實登入憑證。

## 規劃文件檢查證據

2026-10-04 執行 Python 靜態檢查（exit 0）：四份文件存在；15 個唯一 Scenario ID 與 15 列待核准紀錄逐項相符；本地文件連結存在；code fence 成對；無範本 HTML 註解。人工對照 GitHub #39 R1～R16 與 RI-TBD-1～15，均有提案／驗收或明確界線；未核准項目無可實作 Task。純規劃未改產品行為，因此不適用程式紅綠測試；RTINV-015 的 vet/race/build 尚未執行。

重現方式：對 README 擷取 Scenario 標籤與核准表格首欄 RTINV ID，比對集合、數量與順序；逐文件驗證相對連結存在、code fence 成對、無範本 HTML 註解。提交前另外執行 `git diff --cached --check`。

## 交付檢查清單

- [x] RI-TBD／Scenario 核准與可追溯基線齊備
- [x] decompose 在本檔完成 Phase/Task 映射
- [x] 全部 Scenario 紅綠重構或適用等價證據保存
- [x] 常青文件功能／架構與驗證日期更新
- [x] go vet ./...
- [x] go test -count=1 -race ./...
- [x] go build ./cmd/asura（產物置於工作區外或清除本次產物）
- [ ] 固定交付 BASE/HEAD 與 PR 範圍一致
- [ ] 獨立 review 保存有效報告；不自行 merge
