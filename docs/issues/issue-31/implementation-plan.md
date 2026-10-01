# Issue 31 — 初始實作計畫

建立日期：2026-10-01。唯一任務來源為本檔；2026-10-01 使用者核准全部驗收與提案，依 decompose 在本檔維護唯一 Task 清單。

## 設計方案
參照 [technical-analysis.md](technical-analysis.md) 的 L-TBD 提案；storage transaction helper 接受 Memory 與 CreateMemoryOptions（Supersedes、Durable、OperationID、Reason），供 manual 與 Git approval 共用。Daemon 在呼叫前完成 canonical authorization。具體函式簽名在核准後細化；不先實作猜測 API。

風險與首要驗證以 [README](README.md#風險與首要驗證) 為唯一來源。

## Phase / Task

### P1-T1 — 既有隔離與歷史刻畫
- 產出與最小範圍：真實 SQLite 與 Daemon 既有測試基線、failure fixtures。
- 相依：無。
- 驗收責任：支援 MLIFE-001/009/010。
- 完成判準：現況測試成功且新行為測試可揭露缺失。
- 驗證：依下方測試策略取得對應紅綠證據；P1 純刻畫、P5 文件使用等價靜態證據。
- 狀態：✅ 已完成
- 證據：[verification.md](evidence/verification.md)，依本 Task 對應章節查核。

### P2-T1 — 持久化 lifecycle 原子契約
- 產出與最小範圍：storage migration、relations、operation、durable 與測試。
- 相依：P1-T1。
- 驗收責任：MLIFE-001/002/005/006/008。
- 完成判準：legacy 相容、rollback/concurrency/retry/restart/chain/delete 通過。
- 驗證：依下方測試策略取得對應紅綠證據；P1 純刻畫、P5 文件使用等價靜態證據。
- 狀態：✅ 已完成
- 證據：[verification.md](evidence/verification.md)，依本 Task 對應章節查核。

### P3-T1 — Recall 與封存 audit
- 產出與最小範圍：storage active scan、recall audit 與 engine snapshot。
- 相依：P2-T1。
- 驗收責任：MLIFE-003/004/007/010/011。
- 完成判準：超過 256 筆 superseded 不佔候選；explicit audit、UFL neutral 與 sealed 不變。
- 驗證：依下方測試策略取得對應紅綠證據；P1 純刻畫、P5 文件使用等價靜態證據。
- 狀態：✅ 已完成
- 證據：[verification.md](evidence/verification.md)，依本 Task 對應章節查核。

### P4-T1 — Daemon 與 CLI lifecycle
- 產出與最小範圍：IPC、CLI、bounded detail/permission tests。
- 相依：P3-T1。
- 驗收責任：MLIFE-009；L7。
- 完成判準：跨 scope existence hiding、CLI 真實路徑與 bounds 通過。
- 驗證：依下方測試策略取得對應紅綠證據；P1 純刻畫、P5 文件使用等價靜態證據。
- 狀態：✅ 已完成
- 證據：[verification.md](evidence/verification.md)，依本 Task 對應章節查核。

### P4-T2 — Git approval lifecycle
- 產出與最小範圍：ApproveOptions、共用 tx helper 與 Git tests。
- 相依：P2-T1。
- 驗收責任：L6。
- 完成判準：partial 拒絕、provenance 同交易與重試衝突通過。
- 驗證：依下方測試策略取得對應紅綠證據；P1 純刻畫、P5 文件使用等價靜態證據。
- 狀態：✅ 已完成
- 證據：[verification.md](evidence/verification.md)，依本 Task 對應章節查核。

### P5-T1 — 性能、回歸與交付
- 產出與最小範圍：10k/100k probes、常青文件；PR/review 由 dev-cycle 交付階段處理。
- 相依：P4-T1/P4-T2。
- 驗收責任：MLIFE-012；L-TBD-10。
- 完成判準：核准門檻、完整 regression 與常青文件檢查通過。
- 驗證：依下方測試策略取得對應紅綠證據；P1 純刻畫、P5 文件使用等價靜態證據。
- 狀態：⏳ 修正與本地驗證完成；正式 CI／第二輪審查依 PR 最新結果判定
- 證據：[verification.md](evidence/verification.md)；2026-10-01 正式 CI 紅燈，最新修正與證據見 [ci-fix](evidence/ci-fix/README.md)。

## 使用方式對照
現有 memory add/list/delete 保持預設語意；新增 add --supersedes/--operation-id/--durable、durable、detail 的提案參見 technical-analysis。2026-10-01 核准後已更新產品 README。

## 測試策略
- Storage 單元／真實 SQLite 整合：MLIFE-001/002/005/006/008/009/011，migration、回滾、CAS、重試與刪除歷史；沒有 mock 核心 transaction。
- Recall 單元與 storage candidate 整合：MLIFE-003/007；固定 literal score/ID/tie-breaker，superseded 堆積 fixture 驗 LIMIT 前過濾。
- Daemon/CLI 外迴圈：MLIFE-004/009/010 與 L6/L7；驗授權、explicit audit、IPC 邊界、Task status/redelivery/restart；單元測試另保護 snapshot copy 與 metadata validation。
- P2/P4：storage 失敗注入和外部 IPC 紅燈為不同失敗面，保留雙迴圈；只有同層且沒有額外整合責任時才記理由合併。
- 每個 production Task 保存驗收 ID、命令、exit code、失敗原因、最小實作綠燈與 code-simplify 後綠燈。沒有重構時明列 no-op。
- MLIFE-012：`go vet ./...`、`go test -count=1 -race ./...`、`go build ./cmd/asura`；保留來源列出的全部既有功能 regression，不刪測試。
- 2026-10-01 依使用者 CI review 允許，10k/100k probe 以 `go test -tags lifecycleperf -count=1 -timeout 2m -run '^TestLifecycleLargeDataProbe$' -v ./internal/storage` 獨立非 race 執行，CI 保留相同門檻與結果／EXPLAIN／RSS；一般 correctness race 不執行環境敏感性能門檻。
- Probe 使用獨立 fixture 預期答案；記 fixture 數量、查詢計畫、實際耗時與資源。這是本地性能測試，不假稱生產觀測。

## 建檔檢查與證據
2026-10-01：已讀流程契約 2.0、project/acceptance/verification/document-types/readme-templates/issue-checklist；來源 ID、main 基準與候選範圍明確。需求分析因現況需調查而建立，技術分析因 delete/retry 有實質方案取捨而建立。初建時全部 MLIFE 待核准，此為初建時狀態；2026-10-01 核准後建立 issue-31-memory-lifecycle 並完成下列證據。

文件驗證：本地 Markdown 連結存在、MLIFE-001～012 與核准表相等、metadata 齊備、git diff --check；2026-10-01 實際執行：Python 靜態檢查 exit 0，12 個 Scenario 與核准表完全一致，本地連結及 metadata 全數有效；初次未追蹤文件未被 `git diff --check` 涵蓋；2026-10-01 staged check 發現來源 Markdown 行尾空白，已移除並重新檢查。純文件不適用 production 紅綠燈或完整 regression。
