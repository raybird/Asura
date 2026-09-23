# 初始實作計畫

2026-09-23。Large issue 的唯一權威任務來源為本檔；風險與首要驗證見 [README](README.md#風險與首要驗證)。WEBMEM-001～010 已核准，G-TBD-1～5 的具體協定與上限仍待決議。決議後由 decompose 在本檔細化產品 Phase／Task；下列產品階段概要不是可執行 Task。

## 設計方案

提案見 [technical-analysis.md](technical-analysis.md)：Web 的獨立 DTO 經現有 session／Host 及變更方法的 Origin 入口送至 Daemon；Daemon 每次 canonical 授權；SQLite 以 keyset 及 SQL 欄位上界提供 `PageMemories` 與 Web 專用的有界 detail 查詢；Task submit 只轉送核准的 explicit IDs 與 recall mode；稽核只讀已保存 Task。具體函式簽名、cursor 編碼、錯誤映射、hard limits 在探測及 G-TBD-1～5 核准後固定。

使用方式對照：目前 Web 僅能選 workspace 並送 prompt，CLI 可操作記憶及 explicit／bounded；待核准後 Web 才增加記憶管理、選取及稽核操作。原 Web 未使用記憶的 request 保持 off。

## 規劃與探測步驟（已授權）

1. ✅ **P0-1：現況與安全邊界盤點** — 對應 WEBMEM-001、002、005、007、008；產出 [需求與現況](requirement-analysis.md)及[能力矩陣](technical-analysis.md#技術可行性與安全能力矩陣)。相依：無。完成判準：指出 Web 欄位丟失、Daemon 授權／密封順序、儲存讀取與前端 request-ID 現況；2026-09-23 已以原始碼靜態核對。此步不代表新功能驗收。
2. ✅ **P0-2：隔離資料庫及現有邊界探測** — 對應 WEBMEM-001～003、010；相依 P0-1。產出不觸及產品資料的 10k／大內容 SQL、index plan、讀取列數與 bytes、現有 session／變更方法 Origin／workspace 拒絕基線。完成判準：記錄 fixture、查詢原文、環境、命令、exit code、實際數值與限制；以已知跨 scope／無 session 對照組驗證拒絕判準。2026-09-23 [初始探測](evidence/probe-results.md)及[可重跑 SQL](evidence/probe.sql)已記錄；新 HTTP 路徑、IPC bytes 與 Go driver 的新索引計畫列於 Phase 1 驗收，未被此步宣稱通過。
3. ⏳ **P0-3：固定提案並取得決策** — 對應 G-TBD-1～5、WEBMEM-001～010；相依 P0-2。已核准 Gherkin 並提出具體 URL／DTO／錯誤碼、page/cursor/bytes、Web detail 上界、GET／變更方法權限矩陣、前端交互、request-ID 找回方式與稽核界線；尚需決議 G-TBD-1～5 並記錄核准來源。完成判準：README 的待確認項目有真實結論，核准表與所有現存 Scenario 一致；未核准決策不得轉為產品 Task。

## 核准後階段概要

| 階段 | 對應驗收 | 產出 | 相依 | 可檢查完成判準 |
|---|---|---|---|---|
| Phase 1：安全與有界儲存 | 001～004、010 | 目標失敗理由的 HTTP／IPC 拒絕紅燈、paged 與有界 detail SQLite／IPC、索引 | P0-3 核准及 decompose | 未授權 0 寫入／0 backend；10k 資料查詢與傳輸上界、穩定分頁；超大 detail 在 IPC 前拒絕，Go 查詢結果與 IPC 無全量內容 |
| Phase 2：記憶 HTTP API | 001～004 | list/detail/add/delete 路由、錯誤映射 | Phase 1 | 真實 HTTP → IPC → SQLite、撤權、父子、symlink、重啟與失敗結果通過 |
| Phase 3：Web submit | 005～007、009 | explicit／bounded DTO、UI、request-ID 顯示與重送狀態 | Phase 2 | 預設 off、跨 scope 0 backend、密封快照、同 ID 原 payload 重送與衝突、未知結果依 ID 查詢通過 |
| Phase 4：稽核與交付 | 004、008～010 | 歷史稽核 UI、隱私檢查、文件與回歸證據 | Phase 3 | legacy／off／0 auto 區分、純文字與狀態清除、全套 vet／race／build、獨立 review |

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
- [x] 產品階段均有驗收、產出、相依與完成判準；未核准前不細化為可執行產品 Task。
- [x] P0-2 初始探測有可重跑的 fixture、查詢與既有邊界測試證據。
- [ ] P0-3 決策完成，記錄核准來源；新功能驗證留給 Phase 1～4。
- [ ] 核准後 decompose、紅綠驗證、常青文件更新及交付審查。
