# Issue 1：最近 6 個 commit 審查結果

- 記錄日期：2026-09-12。
- Reviewed HEAD SHA：`86ebf6bb52751c78550e9a650ba2dc3e3f226dec`。
- 審查基準：`2b637d08e5efdd40f1b41e4c98e958a5b3d7ff55`（被審查 HEAD 的 `HEAD~6`）。
- Reviewed patch-id：`4ed8fc79d953912c31d5137907f210ca541e4879`。
- patch-id 計算方式：`git diff 2b637d08e5efdd40f1b41e4c98e958a5b3d7ff55 86ebf6bb52751c78550e9a650ba2dc3e3f226dec | git patch-id --stable`；採本次 6 個 commit 的範圍，未推定合併目標分支。
- 獨立 reviewer 識別：對話 review 功能的 reviewer model；原始輸出未提供模型名稱或執行 ID。
- 流程判定：`RETURN TO execute-task`（依下列 3 項 P1、7 項 P2 記錄；未通過）。
- 來源：2026-09-12 使用者提供的完整審查輸出。本檔保存原報告內容；此次文件整理未重新執行測試或修復程式碼。

## 審查摘要與驗證限制

這 6 個 commit 存在擁有權競爭、佇列停滯、取消與持久化不一致等問題，多項已由額外測試重現。現有非 Socket 測試通過，但 Daemon Socket 測試受沙箱限制，無法完成驗證。

上述測試結果為原 reviewer 的回報；原始輸出未附完整測試命令、測試檔案及執行日誌。本檔不將它們視為此次文件整理另行取得的驗證證據，也不將 Socket 測試標為通過。

## 完整審查意見

### R-01 [P1] 以原子互斥保護 Socket 擁有權取得

位置：`internal/daemon/daemon.go:90-94`。

兩個 Daemon 同時啟動時，可能都先探測到沒有監聽者，接著 A 完成綁定、B 刪除 A 的 Socket 路徑並重新綁定，導致兩者都成功啟動並各自執行任務，突破全域上限。探測、清理與綁定需要受同一個跨程序互斥機制保護，不能將連線失敗直接視為可刪除 Socket 的證據。

### R-02 [P1] 同步 Worker 退出與新任務入列

位置：`internal/daemon/pool.go:99-102`。

最後一個 Worker 判定佇列為空後、釋放 semaphore 前，新 Submit 仍會看到 Worker 已滿而成功入列，但該 Worker 隨即退出，留下無人處理的任務。額外壓力測試已重現 `queue=1, workers=0` 並逾時；Daemon 使用 Background Context，因此請求可能永久卡住。應讓入列與 Worker 退出具有原子交接，或使用持續消費佇列的 Worker。

### R-03 [P1] 在取消成功前同步進程啟動與 PGID 登記

位置：`cmd/asura/cancel.go:54-59`。

若使用者在任務建立後、PGID 寫入前執行 cancel，這裡讀到的 PGID 是 0，因此只更新 cancelled 狀態而不終止進程。取消 CLI 建立的是另一個 Engine，無法取消原執行器的 Context；原任務仍會啟動或繼續產生副作用。取消必須與啟動流程協調，不能把尚未登記 PGID 視為資源已回收。

### R-04 [P2] 同步 stdout 與 stderr 對共用 Buffer 的寫入

位置：`cmd/asura/run.go:114-116`。

子程序同時輸出 stdout 與 stderr 時，os/exec 會透過兩個 goroutine 寫入這兩個不同的 MultiWriter，但它們共用不具執行緒安全性的 bytes.Buffer，可能遺失或破壞保存結果。以交替輸出兩個串流的命令執行 race build 已重現 DATA RACE；應同步共用寫入或分別收集後再合併。

### R-05 [P2] 使用未取消的 Context 持久化終態

位置：`internal/engine/engine.go:140-142`。

當 SubmitTask 的父 Context 被取消時，這裡仍使用同一個已取消的 ctx 更新資料庫，更新會失敗且錯誤被忽略。額外測試已重現回傳 Task 為 cancelled、資料庫卻仍為 running；父 Context 逾時也會影響失敗狀態保存。收尾應使用獨立且有界的 Context，並處理保存失敗。

### R-06 [P2] 保存已取消任務的部分輸出

位置：`internal/engine/engine.go:133-137`。

CancelTask 先以空字串覆寫資料庫 Result，runner 結束後此分支只把 output 放進回傳物件便直接返回，沒有重新保存。額外測試已確認回傳結果為 `partial`，資料庫結果卻是空字串，因此 status 無法揭露取消前的已知進度。應保留 cancelled 狀態並持久化最終取得的部分輸出。

### R-07 [P2] 在任務接受後立即揭露 Task ID

位置：`cmd/asura/run.go:152-157`。

未指定 --id 時，Task ID 只在 SubmitTask 成功完成後才輸出；執行中的任務無法取得 ID 來取消，而失敗任務會在此直接返回，永遠不顯示 ID。已以 `echo partial; exit 42` 重現只有部分輸出與錯誤訊息。應在任務接受後提供 ID，且失敗回應也保留該識別碼。

### R-08 [P2] 在 IPC 失敗回應中保留 Task

位置：`internal/daemon/daemon.go:227-231`。

後端執行失敗時，handleConn 已回傳包含 Task 與 Error 的回應，但 Client 在這裡將 Task 丟棄。呼叫端因此失去伺服器產生的任務 ID、失敗狀態與部分輸出，無法追蹤已接受且可能產生副作用的任務。對這類執行錯誤應同時回傳 resp.Task 與 error。

### R-09 [P2] 辨識結構化錯誤後再判定成功

位置：`internal/engine/engine.go:157-160`。

此判斷只排除空白字串，未處理 SCN-007 要求的未解決結構化錯誤。已透過 CLI 重現：命令只輸出包含 `type:error` 與 429 錯誤的 JSON 並以 0 結束，任務仍被標記 completed。應由後端解析事件並傳遞錯誤狀態，不能只以輸出非空作為成功依據。

### R-10 [P2] 以條件式狀態轉移避免覆寫取消結果

位置：`internal/engine/engine.go:167-169`。

若取消 CLI 在 SubmitTask 查得 running 之後、寫入 completed 之前完成取消，此處的無條件 UPDATE 會把 cancelled 覆寫為 completed。跨程序取消不會取消此 Engine 的 taskCtx，因此前面的 Context 檢查也無法攔截。終態寫入應使用條件式更新或交易，並在狀態已改變時回讀最終結果。

## 文件驗證

2026-09-12：此次為純文件保存，不適用程式行為的紅燈測試。以 `git diff --no-index --check /dev/null docs/issues/issue-1/review-86ebf6b.md` 檢查空白格式，並以 `rg -c '^### R-' docs/issues/issue-1/review-86ebf6b.md` 確認完整保存 10 項意見；實際結果為格式檢查退出碼 0、意見數量 10。
