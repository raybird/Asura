# Issue 1：取消重試修復覆核

- 審查日期：2026-09-12。
- Reviewed HEAD SHA：`716416a8a4b6290ddfe33d081dc6e090ed7a37b8`。
- 審查基準：`8696817565146e500ba8f148862effb50d743af9`。
- Reviewed patch-id：`897aaa6e7a06f025b829e155144c5d47dd88c3b0`。
- 計算命令：`git diff 8696817 716416a | git patch-id --stable`。
- 獨立 reviewer 識別：本次 Codex 對話主代理，未參與被審查 commit 的程式修復。
- 流程判定：`RETURN TO execute-task`。

## J-01 [P1] 重試取消前確認儲存的 PGID 仍屬於該任務

位置：`internal/engine/cancel.go:36-38`；呼叫端為 `cmd/asura/cancel.go` 的 PGID 強殺流程。

新增分支對所有 cancelled 任務返回成功，讓 CLI 可以不受時間限制地再次對舊 PGID 發送 SIGKILL。執行端收尾沒有清除 PGID，資料庫也沒有記錄程序啟動身分或資源回收完成狀態。原任務結束後，系統若將相同 PID／PGID 配給另一個同使用者的工作，再次取消舊任務就會殺掉不相關程序，違反 SCN-004 的其他工作隔離要求。此次變更前，已取消且不在活躍清單的任務會被拒絕，新增的冪等放行擴大了此風險。

應區分「取消已請求、回收未完成」與「回收已完成」，並在發送信號前確認程序身分仍屬於該次執行；僅檢查 PGID 存活不足以辨識編號重用。已回收的取消重試應為無副作用的成功。

### 重現證據

2026-09-12 在暫存副本 `/tmp/asura-review-pgid-w8eSjZ` 新增 `cmd/asura/pgid_review_test.go`。測試自行啟動一個獨立進程組的 `sleep`，將其 PGID 寫入代表舊 cancelled 任務的資料列，模擬編號已重用的狀態，再透過 `RunCLI` 取消該舊任務。

```bash
go test ./cmd/asura -run TestReviewCancelledTaskReusedPGID -count=1 -v
```

實際退出碼 1，輸出：

```text
unrelated test process killed: signal: killed; cancel exit=0 stdout="Task old-cancelled cancelled successfully\n" stderr=""
```

這是以已知測試程序建構 PGID 重用狀態的驗證，未等待核心實際重新分配同一個 PID；所有被操作的程序均由該測試建立，沒有接觸使用者既有工作。測試程序已結束。

## 驗證結果

2026-09-12 在沙箱外執行 `go test -count=1 -race ./...`，退出碼 0，全部 7 個套件通過，包含新增的 I-01 取消重試整合測試。

原 I-01 的鎖失敗後重試與盡力回收已補上，但新增測試只涵蓋原程序仍屬於任務的情況，未涵蓋回收完成後的重複取消與 PGID 重用，因此未攔住 J-01。本次未修改 production code 或既有測試。
