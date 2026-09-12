# Issue 1：修復後覆核

- 審查日期：2026-09-12。
- Reviewed HEAD SHA：`79950feb5edb98a9f3019e19c247f62377743a39`。
- 審查基準：`86ebf6bb52751c78550e9a650ba2dc3e3f226dec`。
- Reviewed patch-id：`9d49700ad02a85c8b27ac0fc73b9b38e973a7474`。
- 計算命令：`git diff 86ebf6b 79950fe | git patch-id --stable`。範圍為修復與文件更新兩筆 commit，未推定合併目標分支。
- 獨立 reviewer 識別：本次 Codex 對話主代理，未參與 `3b2e067` 的程式修復。
- 流程判定：`RETURN TO execute-task`。
- 風險：High；涉及跨程序取消、執行資源擁有權與資料持久化。

## 結論

2026-09-12：完整既有測試在沙箱外執行 `go test -race ./...` 通過，但額外重現測試確認下列 4 項缺口，尚不能認定 R-01 至 R-10 全部修復完成。GitNexus 未收錄 Asura 索引，本次以 diff、呼叫端閱讀及實際測試覆核。

## F-01 [P1] 等待舊任務結束後才釋放 Daemon 擁有權

位置：`internal/daemon/daemon.go:207-213`；對應原 R-01。

`Close` 在 `pool.Close()` 等待執行中任務之前就解鎖、關閉並刪除鎖檔。只要舊 Daemon 還有未完成的 runner，新 Daemon 就能成功 `Start` 並取得另一份 WorkerPool，突破全域執行上限；若共用資料庫，新 Daemon 的恢復程序還會把舊 Daemon 仍在執行的任務標成 interrupted。應在所有舊工作與收尾完成前維持擁有權。

另需避免刪除 flock 鎖檔：其他啟動者可能已開啟舊 inode，unlink 後再來的啟動者則鎖住新 inode，使互斥鎖不再指向同一個物件。

重現：啟動 Daemon，提交一個以 channel 暫停的工作，並行呼叫 `Close`，在釋放工作前嘗試啟動第二個 Daemon。`TestReviewOwnershipDuringDrain` 失敗：`second daemon started while first daemon still executes a task`。

## F-02 [P1] 取消與 runner 啟動仍缺少原子交接

位置：`cmd/asura/run.go:148-154`、`cmd/asura/cancel.go:55-69`；對應原 R-03。

新增檢查發生在 `cmd.Start()` 之後，因此已取消任務仍可能啟動並先產生副作用，再被 SIGKILL。取消端等待 PGID 最多 150 ms，逾時後仍回報成功；若執行端被排程延遲，取消返回時並沒有證據證明它不會再啟動。Engine 也沒有在外部取消後阻止 runner 呼叫，且 pending 到 running 仍是無條件更新，可覆寫該階段的取消。

重現：在任務成為 running、尚未完成本機取消登記時暫停執行端，以另一個 Engine 呼叫 `CancelTask` 成功後再放行。`TestReviewCancelBeforeRunner` 失敗：`runner executed after external cancellation succeeded before runner registration`。此測試驗證 Engine 層的交接缺口；CLI 的事後強殺無法保證消除啟動後的副作用。

應以跨程序可協調的啟動／取消狀態交接，確認不再啟動或資源確實回收後才回報取消成功。現有 R-03 整合測試先等 PGID 大於 0 才取消，未覆蓋原始啟動空窗。

## F-03 [P2] 回報終態持久化失敗

位置：`internal/engine/engine.go:193-198`，同類分支在 176、186、203 行；對應原 R-05。

獨立 `persistCtx` 修正了父 Context 取消問題，但失敗與取消分支仍以 `_ =` 忽略資料庫錯誤。資料庫關閉、寫入失敗或鎖定超過期限時，回傳 Task 顯示 failed/cancelled，資料庫卻可能維持 running 且遺失部分輸出，呼叫端只收到原始後端錯誤。應檢查保存結果並合併或明確回報持久化錯誤。

重現：runner 返回前關閉 store，再返回 `partial` 與 `backend failed`。`TestReviewPersistError` 確認回傳 `status=failed error=backend failed`，未揭露持久化錯誤，因此失敗。

## F-04 [P2] 將工具失敗事件納入未解決錯誤判定

位置：`internal/engine/engine.go:100-104`；對應原 R-09。

判斷只將 `type=error` 或 rate limit 設為錯誤，雖然已排除 `ToolStatus=error` 作為有效內容，卻沒有把它記入 `hasErrorEvent`。因此只有失敗工具事件且退出碼為 0 時，兩個旗標皆 false，最終仍標成 completed。

`TestReviewToolError` 使用下列輸出，實際回傳 `status=completed error=<nil>`，測試失敗：

```json
{"type":"tool_use","part":{"type":"tool","tool":"apply_patch","state":{"status":"error","output":"permission denied"}}}
```

應把此類已解析出的工具錯誤納入判定，並加入只有失敗工具事件、沒有成功結果的驗收案例。

## 驗證紀錄

2026-09-12 實際執行結果：

| 命令／檢查 | 結果 |
|---|---|
| 專案 `go test ./...`，沙箱內 | 退出碼 1；Socket 測試被 `setsockopt: operation not permitted` 阻擋，其他套件通過 |
| 專案 `go test -race ./...`，沙箱外 | 退出碼 0，全部 7 個套件通過，含 Daemon Socket 測試 |
| 暫存副本 `go test ./internal/daemon -run TestReviewOwnershipDuringDrain -count=1 -v`，沙箱外 | 退出碼 1，F-01 重現 |
| 暫存副本 `go test ./internal/engine -run TestReview -count=1 -v`，沙箱外 | 退出碼 1，F-02、F-03 重現；500 次取消部分輸出檢查通過 |
| 暫存副本 `go test ./internal/engine -run TestReviewToolError -count=1 -v`，沙箱外 | 退出碼 1，F-04 重現 |

暫存副本位於 `/tmp/asura-review-ocWWru`，額外探針為其中的 `internal/daemon/review_test.go` 與 `internal/engine/review_test.go`；它們不修改專案 production code，也不屬於既有測試。暫存檔可能被系統清理，故本報告另記錄重現條件與實際輸出摘要。

原 R-02 的 Worker 退出／入列已改為同鎖交接；R-04 共用 buffer 已加鎖；R-06 取消部分輸出、R-07 提早提供 ID、R-08 保留 IPC Task、R-10 成功終態條件更新，在本次已執行範圍內未發現需另列的阻擋問題。這不代表所有跨程序交錯皆已驗證。

另有測試覆蓋建議：R-04 整合測試以 `go build` 建立 CLI，未加 `-race`，外層 `go test -race` 不會替該子程序插樁；應以 `go build -race` 驗證子程序本身的雙串流共用寫入。
