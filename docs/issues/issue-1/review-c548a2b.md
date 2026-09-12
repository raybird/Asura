# Issue 1：最新修復覆核

- 審查日期：2026-09-12。
- Reviewed HEAD SHA：`c548a2b6764c5e2c46a8b8780c0690a36d43ff6b`。
- 審查基準：`79950feb5edb98a9f3019e19c247f62377743a39`。
- Reviewed patch-id：`5e0a7d4450d6e67f068f52662dfa4cc93924987f`。
- 計算命令：`git diff 79950fe c548a2b | git patch-id --stable`；範圍為本次修復 commit。
- 獨立 reviewer 識別：本次 Codex 對話主代理，未參與被審查 commit 的程式修復。
- 流程判定：`RETURN TO execute-task`。

## G-01 [P1] 將取消狀態檢查與程序啟動協調為原子交接

位置：`cmd/asura/run.go:140-148`；原 F-02 尚未完全關閉。

啟動前增加 `GetTask` 可以攔住已經可見的取消，但查詢與 `cmd.Start()` 之間沒有跨程序互斥或啟動握手。合法時序仍是：執行端讀到 running 後被暫停；另一個 CLI 寫入 cancelled，等待 PGID 的 150 ms 用盡後回報取消成功；執行端恢復並啟動程序。事後查詢與 SIGKILL 只能停止已啟動的程序，無法保證它沒有先產生副作用。

應在取消成功返回前，確立執行端不再啟動或已完成資源回收的交接；單純增加事前查詢沒有消除此競爭。本項為程式碼時序分析，未宣稱執行了精確攔在 `GetTask` 與 `cmd.Start` 之間的 CLI 重現測試。新增兩個 F-02 單元測試均在提交前建立已取消的同 ID 任務，未涵蓋上述交錯。

## G-02 [P2] 在提前返回取消結果前保存終態

位置：`internal/engine/engine.go:189-192`。

此新增分支只更新回傳物件便返回，繞過後面的 `persistCtx` 收尾。若父 Context 在資料庫已轉為 running、runner 尚未開始時取消，資料庫沒有外部 CancelTask 幫忙更新，會永久留下 running，雖然 SubmitTask 已回傳 cancelled。應讓此分支也透過未取消且有界的 Context 保存終態並處理保存失敗。

2026-09-12 額外測試已重現：用 Engine 的 `cancelsMu` 暫停取消登記，等待資料庫成為 running 後取消父 Context，再放行。runner 未被執行；實際輸出為 `returned=cancelled saved=running error=context canceled`，測試退出碼 1。

## 驗證與修復狀態

2026-09-12：在沙箱外執行 `go test -count=1 -race ./...`，退出碼 0，全部 7 個套件通過，含 Daemon Socket 與 CLI 測試。

2026-09-12：在暫存副本 `/tmp/asura-review-latest-6HQT9K` 執行 `go test ./internal/engine -run TestLatestReview -count=1 -v`，退出碼 1，G-02 重現。測試檔為該副本的 `internal/engine/latest_review_test.go`；專案 production code 未修改。暫存檔可能被清理，上節已記錄測試時序與結果。

原 F-01 現在先等待 Worker 結束再解鎖，並保留鎖檔；排水期間競爭啟動測試通過。原 F-03 已檢查失敗／取消收尾的寫入錯誤；原 F-04 已納入 ToolStatus=error，相關測試通過。本次另發現 G-02 的提前返回繞過共同持久化流程，不能因此認定所有取消路徑均已涵蓋。

先前 R-04 的 CLI 子程序 race 插樁建議也已採納。上述既有測試通過不涵蓋 G-01 的精確跨程序交錯。
