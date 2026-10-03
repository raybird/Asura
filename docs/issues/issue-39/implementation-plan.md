# Issue 39 — 初始實作計畫

2026-10-04 建立。唯一任務來源；本計畫的 Phase 全部待核准，尚無可實作 Task。核准後依 decompose 細化原檔並標註驗收／責任映射。

## 設計方案

新增 inventory model 與 CaptureInventory(ctx, registeredSnapshot)；RuntimeProber 為可選 adapter probe 介面。AdmitRuntime 對 auto 使用一次全註冊 inventory、explicit 使用一次指定 runtime inventory，傳 available descriptors 給原 planner。新 ExecutionContract 包含 InventorySnapshot；新 validator 以封存資料核對，不以當前 Registry 改寫。

模型與限制以 technical-analysis RI-TBD-1～15 為提案來源；風險與首要驗證只引用 [README](./README.md#風險與首要驗證)。

## 初始 Phase（待核准，非可實作 Task）

| Phase | 對應驗收 | 產出 | 相依 | 完成判準 |
|---|---|---|---|---|
| P1：首要風險驗證 | 003/004/007/008/010/013 | daemon admission／pipeline 負向測試與同層 inventory 單元紅燈 | 規格核准與 Task 拆解 | 真實 failure isolation、timeout、cancel、sealed disappearance 紅燈；Task/dispatch/Run 計數與 workspace sentinel |
| P2：inventory 與四 adapter probe | 001/002/003/008/009/012/013 | typed states/reasons、Registry identity snapshot、bounded context-aware probe/output | P1 | enum、sorting、identity、panic、deadline、huge output、secret sentinel、版本/依賴與 read-only config 單元及 adapter 測試全綠 |
| P3：admission、封存、execution | 004/005/006/007/010/011/012 | auto/explicit integration、runtime-inventory-v1 validate、sealed guard、trace | P2 | auto 繼續可用候選；explicit 無 fallback；每個 admission runtime probe 一次；Run 前拒絕不可用 sealed runtime；全契約一致性與上限通過 |
| P4：composition、persistence、audit | 002/007/011/012/014/015 | local/daemon 固定登錄、inventory CLI/IPC/Web、task audit、legacy/queue/restart tests、常青文件 | P3 | local/daemon semantics 一致；status 不 probe；legacy 不回填；queue/redelivery 不重新 admission；restart 維持既有政策 |
| P5：交付與審查 | 015 及全部驗收 | 全套回歸證據、code-simplify、commit/PR、獨立 review artifact | P4 | vet/race/build 全綠、全部驗收證據、固定 BASE/HEAD、獨立 review PASS 或完成修正 |

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

- [ ] RI-TBD／Scenario 核准與可追溯基線齊備
- [ ] decompose 在本檔完成 Phase/Task 映射
- [ ] 全部 Scenario 紅綠重構或適用等價證據保存
- [ ] 常青文件功能／架構與驗證日期更新
- [ ] go vet ./...
- [ ] go test -count=1 -race ./...
- [ ] go build ./cmd/asura（產物置於工作區外或清除本次產物）
- [ ] 固定交付 BASE/HEAD 與 PR 範圍一致
- [ ] 獨立 review 保存有效報告；不自行 merge
