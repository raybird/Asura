# 初始實作計畫

2026-09-23。Large issue 的唯一權威任務來源為本檔；風險與首要驗證見 [README](README.md#風險與首要驗證)。WEBMEM-001～010 與 G-TBD-1～5 已核准；下列 Phase／Task 為實作與證據的唯一狀態清單。

## 設計方案

核准設計見 [technical-analysis.md](technical-analysis.md)：Web 的獨立 DTO 經現有 session／Host 及變更方法的 Origin 入口送至 Daemon；Daemon 每次 canonical 授權；SQLite 以 keyset 及 SQL 欄位上界提供 `PageMemories` 與 Web 專用的有界 detail 查詢；Task submit 只轉送 explicit IDs 與 recall mode；稽核只讀已保存 Task。

使用方式對照：目前 Web 僅能選 workspace 並送 prompt，CLI 可操作記憶及 explicit／bounded；實作後 Web 增加記憶管理、選取及稽核操作。原 Web 未使用記憶的 request 保持 off。

## 規劃與探測步驟（已授權）

1. ✅ **P0-1：現況與安全邊界盤點** — 對應 WEBMEM-001、002、005、007、008；產出 [需求與現況](requirement-analysis.md)及[能力矩陣](technical-analysis.md#技術可行性與安全能力矩陣)。相依：無。完成判準：指出 Web 欄位丟失、Daemon 授權／密封順序、儲存讀取與前端 request-ID 現況；2026-09-23 已以原始碼靜態核對。此步不代表新功能驗收。
2. ✅ **P0-2：隔離資料庫及現有邊界探測** — 對應 WEBMEM-001～003、010；相依 P0-1。產出不觸及產品資料的 10k／大內容 SQL、index plan、讀取列數與 bytes、現有 session／變更方法 Origin／workspace 拒絕基線。完成判準：記錄 fixture、查詢原文、環境、命令、exit code、實際數值與限制；以已知跨 scope／無 session 對照組驗證拒絕判準。2026-09-23 [初始探測](evidence/probe-results.md)及[可重跑 SQL](evidence/probe.sql)已記錄；新 HTTP 路徑、IPC bytes 與 Go driver 的新索引計畫列於 Phase 1 驗收，未被此步宣稱通過。
3. ✅ **P0-3：固定提案並取得決策** — 對應 G-TBD-1～5、WEBMEM-001～010；相依 P0-2。產出具體 URL／DTO／錯誤碼、page/cursor/bytes、Web detail 上界、GET／變更方法權限矩陣、前端交互、request-ID 找回方式與稽核界線。完成判準：2026-09-23 使用者核准十個 Scenario 與 G-TBD-1～5，來源與結論見 [README](README.md#待確認事項)。此步是規格證據，非產品測試。

## 產品階段概要

| 階段 | 對應驗收 | 產出 | 相依 | 可檢查完成判準 |
|---|---|---|---|---|
| Phase 1：安全與有界儲存 | 支援 001～004、010 | 有界 SQLite／IPC、索引與授權拒絕證據 | P0-3 | 10k 查詢與傳輸上界、穩定分頁；超大 detail 在 IPC 前拒絕，Go 查詢結果與 IPC 無全量內容 |
| Phase 2：記憶 HTTP API | 001～004 | list/detail/add/delete 路由、錯誤映射 | Phase 1 | 真實 HTTP → IPC → SQLite、撤權、父子、symlink、重啟與失敗結果通過 |
| Phase 3：Web submit | 005～007、009 | explicit／bounded DTO、UI、request-ID 顯示與重送狀態 | Phase 2 | 預設 off、跨 scope 0 backend、密封快照、同 ID 原 payload 重送與衝突、未知結果依 ID 查詢通過 |
| Phase 4：稽核與交付 | 008～010 | 歷史稽核 UI、隱私檢查、文件與回歸證據 | Phase 3 | legacy／off／0 auto 區分、純文字與狀態清除、全套 vet／race／build、獨立 review |

## Phase 1 — 有界儲存與 Daemon 邊界

### Task 1.1 — 有界 SQLite 記憶頁與 detail
- 產出與最小範圍：在 `internal/storage` 加入 `(workspace,created_at,id)` 索引、keyset page 與 Web detail 專用查詢；保持既有 CLI `ListMemories`／`GetMemory` 契約。頁面與 detail 遵守 G2 的 bytes、列數、游標及 legacy 錯誤界線。
- 相依：P0-3。
- 驗收編號：WEBMEM-003、010（支援；責任 Task 為 2.2、4.3）。
- 完成判準：10k／超大內容 fixture 在相同 timestamp 下完整、穩定分頁；Go 結果內容有界，超大 detail 不先回傳完整 BLOB；索引 query plan、cursor 錯誤與跨 scope 查詢可重複檢查。
- 驗證：先以真實 SQLite fixture 取得目標行為紅燈；儲存測試檢查固定預期資料與 bytes，記錄 query plan。這一層沒有 HTTP 整合責任，外／內迴圈在儲存層合併，理由與證據於執行時記錄。
- 狀態：✅ 已完成（2026-09-23，Task 1.1）。證據：在 `fa41524` 後的工作區加入測試，`go test ./internal/storage -run '^TestMemoryPageBoundaries$' -count=1` exit 1，直接失敗於 `WEBMEM-003: bounded page/detail contract unavailable`；加入實作後 `go test ./internal/storage -run '^TestMemoryPage' -count=1 -timeout=60s` exit 0、`go test ./internal/storage -count=1` exit 0。測試以 10,000 筆真實 SQLite 資料確認 50+1 keyset、同時間戳的順序與索引計畫無 temp sort；另涵蓋 512 bytes UTF-8 截斷、65,536/65,537 bytes detail、跨 scope、過量頁與 legacy metadata。外／內迴圈合併於真實 SQLite 層，因本 Task 只交付 storage 查詢，HTTP／IPC 整合由後續 Task 各自負責。精煉檢查限於本次 storage diff：目前函式與 DTO 各有明確用途，無可證明安全的簡化，no-op，沿用同一綠燈。限制：此層只證明 Go 結果有界，不證明 SQLite 內部 BLOB I/O 或後續 IPC／HTTP bytes。

### Task 1.2 — Daemon 有界 IPC 與 canonical 授權
- 產出與最小範圍：在 `internal/daemon` 增加 `memory_page`／`memory_detail_bounded`，重用每次 canonical workspace 授權；保留 CLI 全量 get/list；區分授權、缺 ID、超限與 legacy 錯誤。
- 相依：Task 1.1。
- 驗收編號：WEBMEM-002（責任）、WEBMEM-003（支援）。
- 完成判準：A/B、父子、symlink、越界與撤權的真實 IPC 請求均只見精確 scope；跨 scope ID 與不存在 ID 同樣不可識別；page/detail IPC 回應遵守 G2 上界，拒絕路徑零寫入／零 backend 呼叫。
- 驗證：先建立 IPC → Daemon → SQLite 的拒絕與容量紅燈，另用 Daemon 層測試辨識 canonical 授權失敗面；修復後比對實際 response bytes 與儲存狀態。
- 狀態：✅ 已完成（2026-09-23，Task 1.2）。證據：在 `c59b8ee` 後的工作區，`go test ./internal/daemon -run '^TestDaemonBoundedMemoryIPC$' -count=1` exit 1，直接失敗於 `WEBMEM-002: bounded IPC contract unavailable`；加入新動作後相同命令 exit 0，`go test ./internal/daemon -count=1` exit 0。真實 Unix socket → Daemon → SQLite fixture 覆蓋 A/B、父 scope、symlink 別名與改指向越界後的撤權、跨 scope／不存在 ID 同結果、長內容 detail 拒絕；原始 IPC 行長度小於 512 KiB 且不含全文。新 action 編碼前量測完整 JSON，大於 512 KiB 回固定錯誤；既有 CLI 全量 action 保留。外層 IPC 與底層 Daemon 測試在同一真實 IPC fixture 合併，沒有 mock 授權或儲存。精煉檢查限本次新 action／client 方法，沿用既有 IPC 風格，無可證明安全的額外簡化，no-op；綠燈證據仍有效。HTTP session／Origin 與其副作用計數留在 Task 2.1。

## Phase 2 — Web 記憶 HTTP API

### Task 2.1 — Session 與同站邊界
- 產出與最小範圍：在 `internal/web` 建立四個 G1 路由的入口防護與錯誤映射，沿用 session／Host／變更方法 Origin；GET 由既有瀏覽器同源機制保護，不額外要求 Origin。
- 相依：Task 1.2。
- 驗收編號：WEBMEM-001（責任）、WEBMEM-002（支援）。
- 完成判準：未登入／登出請求、錯誤 Host、跨 Origin 變更均服務端拒絕且零記憶寫入／零 backend 呼叫；GET 不帶 Origin 可讀授權資料，跨站頁面無法讀取回應。
- 驗證：先以真實 HTTP → IPC → SQLite 取得拒絕路徑與合法 GET 紅燈；Web 單元測試分辨 session／Host／Origin 規則。404 缺路由不得充當授權紅燈。
- 狀態：✅ 已完成（2026-09-23，Task 2.1）。證據：`c7ca120` 後工作區先執行 `go test ./tests/integration -run '^TestWEBMEMHTTPBoundary$' -count=1` exit 1，合法 GET 不帶 Origin 得 404，證明新路由缺失；同測試的未登入讀寫及跨 Origin 變更先保持拒絕。加入路由後相同測試 exit 0；`go test ./internal/web -count=1` exit 0、`go test ./tests/integration -count=1` exit 0。真實 HTTP → IPC → SQLite 證明 session／Host／Origin 共用入口、未登入及跨站變更不寫入且 backend 計數 0；已登入 GET 不帶 Origin 可讀。既有 cookie `SameSite=Strict` 與無 cookie GET 401 作瀏覽器跨站不可讀的服務端證據；直接帶有效 cookie 的不同 Origin GET 仍可讀，符合核准的服務端矩陣。外層整合與 Web 單元有不同失敗面，沿用既有 WEB-001／010 單元回歸。精煉檢查：新路由只在既有全域邊界後分派，無額外可安全簡化，no-op。

### Task 2.2 — 記憶 list/detail/add/delete
- 產出與最小範圍：完成 G1 DTO、四個 Web 路由和 `page`／bounded detail IPC 呼叫，保持輸入上限、錯誤碼與顯示資訊一致；不建立第二套記憶儲存。
- 相依：Task 2.1。
- 驗收編號：WEBMEM-003、004（責任）。
- 完成判準：真實 HTTP → IPC → SQLite 的分頁與明確 detail 在 10k／長內容 fixture 下符合 G2，超大 detail 於 IPC 前拒絕；手動新增／確認刪除／重啟持久化結果正確，舊 Task 快照未改寫。
- 驗證：先取得 HTTP 外層紅燈與 DTO／IPC 錯誤映射內層紅燈；記錄實際 HTTP／IPC bytes、狀態碼、SQLite 值及 Task 快照。
- 狀態：✅ 已完成（2026-09-23）。紅燈：在 `c7ca120` 基線執行 `go test ./tests/integration -run ^TestWEBMEMCRUDAndLimits$ -count=1` exit 1，新增回 404。綠燈：現行 `go test ./tests/integration -run ^TestWEBMEM -count=1` exit 0，10k HTTP 頁、預設 20／上限 50、detail 413、legacy metadata 422、Web body 413、CRUD 與跨 scope 拒絕；`node docs/issues/issue-21/evidence/ui-probe.cjs` exit 0，刪除前 confirm 且顯示結果。`go test ./internal/storage -run TestSQLiteStore_MemoryCRUD -count=1` 與 `go test ./internal/daemon -run TestDaemon_TaskContextSealedAndImmutable -count=1` 均 exit 0，分別覆蓋 SQLite 重啟與刪除後舊 Task 快照。真實 HTTP 整合與底層持久化各有獨立失敗面；精煉檢查無額外可安全簡化，no-op。

## Phase 3 — Web 召回與重送

### Task 3.1 — explicit IDs 提交契約
- 產出與最小範圍：Web submit DTO 只轉送 G3 的 explicit IDs；由 Daemon 既有流程解析完整快照與指紋，不接收 client sealed context。ID 數及字串長度遵守 G2。
- 相依：Task 2.2。
- 驗收編號：WEBMEM-005（責任）、WEBMEM-007（支援）。
- 完成判準：同 scope IDs 的完整快照進入保存 context；跨 scope、已刪除、超量或自行注入 context 在 backend 前拒絕，backend 呼叫數為零。
- 驗證：真實 Web submit → Daemon → SQLite → recording backend 先紅後綠，內層 DTO allowlist／Daemon 解析測試分辨拒絕原因。
- 狀態：✅ 已完成（2026-09-23）。紅燈：`go test ./tests/integration -run ^TestWEBMEMSubmitExplicitAndBounded$ -count=1` exit 1，舊 Web 遺失 `memory_ids`，保存 Task refs/context 為空。綠燈：同測試 exit 0，同 scope 完整快照保存；跨 scope、缺失、已刪除 ID 回 404 且 Task 不存在、backend 計數不增加；33 IDs 與 client 注入 `resolved_context/backend/mode` 回 400。沿用 Daemon 解析與密封；HTTP 保存 Task 及 backend 計數是外層真相，DTO allowlist 是獨立入口失敗面。精煉檢查 DTO 僅五欄，no-op。

### Task 3.2 — bounded 召回與預設 off
- 產出與最小範圍：Web submit 只增加 G3 的 recall mode 選項；不改 Daemon selector、額度、provenance 或 stage admission。
- 相依：Task 3.1。
- 驗收編號：WEBMEM-006（責任）。
- 完成判準：舊請求與空選取維持 off；明確 bounded 的保存 context／trace 與同條件 CLI 的 Daemon 流程一致，無隱式召回。
- 驗證：真實提交的 off／bounded 對照先紅後綠，另測 DTO 預設與模式驗證；以保存 Task 與 recording backend 為真相來源。
- 狀態：✅ 已完成（2026-09-23）。紅燈：固定基線 `db6c847` 執行 `go test ./tests/integration -run ^TestWEBMEMBoundedOnly$ -count=1` exit 1，Task 無 recall context。綠燈：現行同命令 exit 0，保存 `bounded` 與 `lexical-v1`；`TestWEBMEMSubmitExplicitAndBounded` 比對原請求 off 的空 refs 與明確 bounded context。selector、額度、provenance 與 stage admission 沿用原 Daemon 路徑，未修改。外／內層均以真實 HTTP → Daemon 保存 context 觀察，合併紅燈；精煉檢查 no-op。

### Task 3.3 — UI 選取與同 ID 重送
- 產出與最小範圍：Web UI 增加記憶多選、bounded 開關、提交前 Request ID、鎖定送出 payload、同 ID 原 payload 重試及依 ID 查詢未知結果；API 不替 client 重建 ID。
- 相依：Task 3.2。
- 驗收編號：WEBMEM-007（責任）、WEBMEM-009（支援）。
- 完成判準：回應遺失後原 payload 一次執行且可找回同 Task，改 ID 順序仍按既有 fingerprint 語意，同 ID 改內容／模式為衝突；切換 workspace 或重新登入後仍可憑預先保存的 ID 查詢。
- 驗證：記錄真實提交／中斷回應／重送的 backend 次數、保存 Task ID 與衝突；以 UI 互動測試檢查顯示與 payload 鎖定。
- 狀態：✅ 已完成（2026-09-23）。紅燈：固定基線 `db6c847` 執行 `node docs/issues/issue-21/evidence/ui-probe.cjs retry` exit 1，網路前未顯示 Request ID；獨立審查在 `682193c` 找出 HTTP 503 後重試按鈕仍隱藏，補上 `node docs/issues/issue-21/evidence/ui-probe.cjs retry503` 先得 exit 1（`true !== false`）。綠燈：修正後 retry503、retry 與完整 probe 均 exit 0，503 與回應遺失均顯示原請求重試，表單改動後仍送完全相同 JSON，新提交產生新 ID；`go test ./tests/integration -run ^TestWEBMEMRedelivery$ -count=1` exit 0，刪除後同 ID 原 payload／改順序回同 Task、保存快照不變，模式改動回 409，backend 計數僅 1。既有 `TestWEB006` 覆蓋重新登入依 ID 查詢。UI payload 與 Daemon 指紋是不同層級；精煉後移除重複清理呼叫，probe 再通過。

## Phase 4 — 稽核、隱私與交付

### Task 4.1 — 保存 Task 的記憶稽核
- 產出與最小範圍：Web 依既有 status 路徑呈現保存的 context／trace，依 G5 顯示 compact metadata；不重新查即時記憶，保留已核准的既有 status 大小限制。
- 相依：Task 3.3。
- 驗收編號：WEBMEM-008（責任）。
- 完成判準：刪除記憶後歷史 explicit／auto IDs、policy、score、skip reason、stage trace 仍與保存快照相同；legacy、off、0 auto、unknown／estimated 清楚區分。
- 驗證：先用舊 JSON／trace 與刪除後 Task fixture 取得 UI 行為紅燈；完成後比對持久化 Task，不從現存記憶推導預期值。
- 狀態：✅ 已完成（2026-09-23）。紅燈：固定基線 `db6c847` 執行 `node docs/issues/issue-21/evidence/ui-probe.cjs audit` exit 1，legacy 稽核欄空白。綠燈：現行 audit 與完整 probe exit 0，檢查 legacy/off/explicit-only/bounded 0 auto、policy、score、skip、stage admission 與 estimated；`TestWEBMEMRedelivery` 對照刪除後持久化 Task 的快照。介面只讀 Task status 保存資料，不查即時 memory；保留 G5 已核准的既有 status 大小限制。UI 顯示與儲存快照分層驗證；精煉檢查單一 render 函式已足夠，no-op。
- 2026-09-24 審查補強：queued Task 的已保存 `recall.budget.first_stage` 與執行後 `pipeline_trace` 分開顯示。`node docs/issues/issue-21/evidence/ui-probe.cjs audit` 先因缺少「接受時預估」斷言失敗，最小修正後 exit 0；沒有重新查即時記憶。

### Task 4.2 — 純文字顯示與切換清除
- 產出與最小範圍：UI 對記憶及稽核內容使用純文字節點；以 session／workspace generation 丟棄遲到回應，切換或失效時清除內容、選取與待送 payload。
- 相依：Task 4.1。
- 驗收編號：WEBMEM-009（責任）。
- 完成判準：腳本樣式內容不執行；A 回應晚於 B 切換或登出到達仍不回填；下一任務送出的 ID 僅屬當前 workspace。
- 驗證：先建立可控制回應順序的 UI 互動紅燈，完成後檢查 DOM、選取與真實 submit payload；session 失效分支單獨覆蓋。
- 狀態：✅ 已完成（2026-09-23）。紅燈：固定基線 `db6c847` 執行 `node docs/issues/issue-21/evidence/ui-probe.cjs race` exit 1，A Task 遲到回應在切換 B 後寫回狀態。綠燈：現行 race 與完整 probe exit 0，script/img 樣式只作文字，A detail／Task 遲到回應不回填；切換清除選取、recall、Task 與待重送 payload，session 401 清空並隱藏 app。`TestWEBMEMHTTPBoundary` 另保障服務端 session 拒絕；UI 競態與 HTTP 授權不同層。精煉檢查移除重複清理並重跑 probe。
- 2026-09-24 審查補強：同 workspace 查詢 A 後查 B，A 遲到覆寫 B 的 status、result 與稽核。`node docs/issues/issue-21/evidence/ui-probe.cjs taskRace` 先得 exit 1（實際 `failed`，預期 `completed`）；加入 Task 顯示世代後 exit 0，並覆蓋 A 長結果分頁及提交後 A 輪詢晚於 B；新提交立即清除前一任務狀態。此 UI 探測與 workspace `race`、HTTP 授權為不同失敗面；精煉檢查沿用單一世代變數與既有 generation guard。

### Task 4.3 — 全面回歸與交付文件
- 產出與最小範圍：更新根 `README.md` 與既有架構文件的 Web 記憶資料流，整理各 Task 證據與資源邊界，準備 PR。
- 相依：Task 4.2。
- 驗收編號：WEBMEM-010（責任）。
- 完成判準：WEB／MEM／CTXMEM／RECALL／CTXWIN／TFACE／MBACK／CRON／PGID cancel／queue-redelivery 回歸及 `go vet ./...`、`go test -count=1 -race ./...`、`go build ./cmd/asura` 全綠；記錄傳輸上界與 query plan；文件與實作一致。PR 與獨立 review 由 dev-cycle 的交付 gate 處理。
- 驗證：重用版本與環境一致的有效 Task 證據，執行完整回歸命令；文件以靜態對照程式與手動閱讀檢查。完成 Task 後固定 BASE／HEAD 供 PR 與獨立 reviewer 使用。
- 狀態：✅ 已完成（2026-09-23）。證據：對固定提交 `0869a60`，`go vet ./...` exit 0；`go test -count=1 -race ./...` exit 0，涵蓋 `cmd/asura`、`internal/auth/backend/contextwin/cron/daemon/engine/pipeline/recall/storage/web` 與 `tests/integration`；`go build -o /tmp/asura_issue21_build ./cmd/asura` exit 0；`node docs/issues/issue-21/evidence/ui-probe.cjs` exit 0；`git diff --check` exit 0。獨立審查修正 HTTP 503 重試入口後，同組 vet、race 全套、build、完整 UI probe、`retry` 與 `retry503` probe、`git diff --check` 均再得 exit 0。根 README 與架構文件對照新 Web 路由、上限及資料流更新；UI probe 採最小 DOM 模擬，非真實瀏覽器驗證。query plan 與傳輸 bytes 的真實 fixture 證據分別在 Task 1.1、1.2 與 2.2。文件任務無產品紅燈，依程式對照與可重跑命令作等價證據；精煉檢查只修正受本功能影響的說明，no-op。
- 2026-09-24 審查補強回歸：`go vet ./...`、`go test -count=1 -race ./...`、`go build -o /tmp/asura_issue21_task_race_build ./cmd/asura`、完整／`audit`／`taskRace`／`retry503` UI 探測及 `git diff --check` 均 exit 0。CI 新增四種 UI 探測命令，讓同 Task 競態、首次 Stage 預估及 5xx 重試持續受檢。精煉檢查未見可證明安全且改善清晰度的修改，no-op；Daemon SQLite 查詢取消傳遞屬建議改善，現有分頁／欄位界線與 Web IPC 期限仍保留，未在本次競態修復中改動 IPC 契約。

## 測試策略與驗收對應

| 驗收 | 外部可觀察層級 | 底層獨立失敗面 |
|---|---|---|
| 001、002 | 真實 Web HTTP → Unix IPC → Daemon，檢查拒絕與副作用計數；瀏覽器跨站 GET 無法讀取回應 | session／Host、變更方法 Origin 單元、canonical scope 與精確 storage 查詢 |
| 003、010 | 10k 與超大內容的分頁及 detail HTTP／IPC 回應大小、排序與限制 | SQLite query plan、列數／欄位 sentinel、detail 超限在 IPC 前拒絕、cursor 同值及變動資料 |
| 004 | HTTP CRUD、SQLite 重啟及舊 Task 快照 | storage Get/Delete 與 Daemon 錯誤映射 |
| 005～007 | 真實 submit、recording backend 呼叫數、保存 context 與重送；回應遺失後保存 ID 再登入查詢 | DTO allowlist、Daemon 指紋與 explicit 解析、前端 payload 狀態 |
| 008、009 | Web 查詢 Task／切換／登出及腳本樣式內容 | 舊 JSON／trace fixture、DOM 純文字節點；控制 A 回應晚於切換 B／登出抵達，確認不回填或誤送 |

產品行為新增時先取得與目標行為相關的外層及底層紅燈，再最小實作、回歸與精煉；同層可合併並記理由。以保存的 Task、SQLite、recording backend 及真實 IPC 作獨立真相來源，避免 mock 核心授權。純文件與非產品探測以靜態核對及可重複 probe 紀錄作等價證據，不稱作產品測試。驗證入口依現有 `go test` 套件及 #21 要求的 `go vet ./...`、`go test -count=1 -race ./...`、`go build ./cmd/asura` 選擇；執行時記錄真實命令與結果。

## 檢查清單

- [x] Large 與 High 分開判定；需求分析由程式現況觸發，技術分析由有界排序及重送方案取捨觸發。
- [x] 最大風險先用授權刻畫與真實 SQL 探測，不預設 UI 切片能驗證資料邊界。
- [x] 已核准 Scenario 均由唯一 Task 負責；各 Task 有產出、最小範圍、相依、完成判準及驗證。
- [x] P0-2 初始探測有可重跑的 fixture、查詢與既有邊界測試證據。
- [x] P0-3 決策完成，記錄核准來源；新功能驗證留給 Phase 1～4。
- [x] 核准後 decompose；無待核准 Scenario。
- [x] 依 Task 紅綠驗證及常青文件更新。
- [ ] 建立 PR 並完成獨立審查。
