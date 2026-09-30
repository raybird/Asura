# 實作計畫

建立日期：2026-09-30。唯一任務來源為本檔；風險與首要驗證引用 [README](README.md#風險與首要驗證)。設計以 [technical-analysis](technical-analysis.md) 提案為準；2026-09-30 使用者已核准全部驗收與設計；以 decompose 技能在本檔細化 Tasks。

## 設計與使用方式

變更前 Web 只管理正式 memory；變更後由授權 workspace 的 Git Memory 區域 capture → 分頁 → detail/edit → approve/discard，approved ID 可在原 Memory 區域查詢。UI/API → bounded Daemon IPC → 既有 capture/store transaction。

## 分階段步驟

| Phase | 狀態 | 驗收 | 相依 | 產出與完成判準 |
|---|---|---|---|---|
| P0 授權與容量／取消契約 | 📝 待實作 | 001、002、004、007、011 | 使用者核准 | 建立拒絕組與成功對照，0 subprocess/writes；大量 SQLite 候選、超大欄位、Unix disconnect/真實 Git process 回收 probe，保存實際結果；未知未解除則不進 P1 |
| P1 Bounded storage/IPC | 📝 待實作 | 002、004、007、008 | P0 | page/detail bounded SQL、cursor、reader/encoder limits；交易內容限制與冪等；紅綠測試證明不全量讀取、不跨 scope、不雙寫 |
| P2 Web API/capture cancellation | 📝 待實作 | 001～004、006～008、011 | P1 | strict DTO、permission/error mapping，真實 HTTP→Unix IPC→Git/SQLite 取消，交易前取消無 checkpoint 變更 |
| P3 Web UI/審核與隔離 | 📝 待實作 | 003、005、006、008、009 | P2 | 純文字來源與草稿、確認流程、generation/abort；可控制延遲的 browser 測試證明切換/登出隔離 |
| P4 整合與交付 | 📝 待實作 | 010、012及全部 | P3 | real repo+HTTP+IPC+SQLite 驗收、recall/sealed context、正式 CI；更新 README，commit、PR 與持久化獨立 review |

## 測試策略

- Storage 單元：同時間戳 keyset、空頁/邊界、超大 legacy 欄位、JSON escaping、Partial/終態/重送與交易競態。
- Daemon 契約：exact canonical scope、撤權、ID 不存在不洩漏、bounded envelope、capture ctx 與 subprocess 回收。
- HTTP 整合：WEBGIT-001～004、006～008、011；既有 core 用真實 service/store，不 mock 掉授權或原子交易。
- Browser：WEBGIT-005、009，惡意字串不得執行、delayed response 不污染 DOM；手動替代須列出可重複步驟與限制。
- 端到端：WEBGIT-010，候選永不 recall、approve 後可 recall，已接受 Task sealed snapshot 不變。
- WEBGIT-012：`go vet ./...`、`go test -count=1 -race ./...`、`go build ./cmd/asura` 與 CI。

外迴圈整合與底層 SQL/IPC 單元存在不同失敗面，保留各自紅燈；若某項同層可合併，實作時記明理由。每個 Task 保存命令、exit code、工作區/提交及紅綠重構證據；code-simplify no-op 可重用最終綠燈，不把未執行測試標成通過。

## 規劃查核

2026-09-30 已完成程式碼盤點與既有相關測試查核，結果見 requirement-analysis。純文件不改變可執行行為，文件連結、Scenario/核准表集合與 metadata 以靜態檢查；新 Web 功能尚未實作或驗收。

- [x] Large/High 分開評估，必要分析文件有實際調查與方案取捨。
- [x] Scenario IDs 與待核准紀錄一一對應；未知保留，未冒充核准。
- [x] 首要驗證對應授權最大風險；容量與取消另列 probe。
- [x] 使用者核准 WEBGIT-001～012 與 J-TBD-1～8。
- [ ] decompose 細化 Tasks，取得核准規格 commit baseline。
- [ ] 實作、驗證、PR 與獨立 review；不自行 merge。

## 權威 Task 清單（2026-09-30）

### Task 0.1 — 授權、容量與取消前置查核

- 產出與最小範圍：現有 scope/Partial 契約、超大 legacy 欄位與 IPC capture disconnect probe；確認必要修改點，保留真實結果。
- 相依：無
- 責任驗收：WEBGIT-002、007、011
- 完成判準：上述產出可重複驗證、對應必要檢查通過並保存 artifact。
- 驗證：Daemon/storage/gitmem 真實契約測試；成功對照與拒絕組。
- 狀態：📝 待實作
- 證據：待執行。

### Task 1.1 — Bounded candidate storage 與交易

- 產出與最小範圍：SQL 投影、keyset、metadata/content bounds；同 scope 原子批准、重送不雙寫。
- 相依：0.1
- 責任驗收：WEBGIT-004、008
- 完成判準：上述產出可重複驗證、對應必要檢查通過並保存 artifact。
- 驗證：storage 單元紅綠測試含大量資料、同時間戳、超大 metadata/facts/draft。
- 狀態：📝 待實作
- 證據：待執行。

### Task 1.2 — Web bounded Daemon IPC

- 產出與最小範圍：新增五個 bounded IPC，scope/DTO/response 上限、錯誤分類，不改 CLI contract。
- 相依：1.1
- 責任驗收：WEBGIT-002、007
- 完成判準：上述產出可重複驗證、對應必要檢查通過並保存 artifact。
- 驗證：真實 Unix IPC 與 store 單元／整合紅綠。
- 狀態：📝 待實作
- 證據：待執行。

### Task 2.1 — Web Git API 與取消

- 產出與最小範圍：路由、strict DTO/error matrix、reader limits；固定 range/since-last capture 與 HTTP disconnect 回收。
- 相依：1.2
- 責任驗收：WEBGIT-001、003、006、011
- 完成判準：上述產出可重複驗證、對應必要檢查通過並保存 artifact。
- 驗證：HTTP 外迴圈、Daemon/Git 底層紅綠；不 mock 授權/原子 store。
- 狀態：📝 待實作
- 證據：待執行。

### Task 3.1 — Web 候選審核 UI

- 產出與最小範圍：表單、狀態篩選/翻頁、來源/draft/狀態純文字、確認與 generation/abort。
- 相依：2.1
- 責任驗收：WEBGIT-005、009
- 完成判準：上述產出可重複驗證、對應必要檢查通過並保存 artifact。
- 驗證：browser 惡意字串與 controlled delayed response；API approve/discard 對照。
- 狀態：📝 待實作
- 證據：待執行。

### Task 4.1 — Recall 與完整回歸交付

- 產出與最小範圍：real HTTP/IPC/SQLite recall/sealed snapshot；文件、全套測試、CI、commit/PR/review。
- 相依：3.1
- 責任驗收：WEBGIT-010、012
- 完成判準：上述產出可重複驗證、對應必要檢查通過並保存 artifact。
- 驗證：go vet、race tests、build、端到端驗收及獨立 review。
- 狀態：📝 待實作
- 證據：待執行。
