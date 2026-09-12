# Issue 1：StartTime 身分與程序回收覆核

- 審查日期：2026-09-12。
- Reviewed HEAD SHA：`6683a0f61333b9b0f9d7f4f4ec608fa6c95b846b`。
- 審查基準：`7e76e745d1136b30d34a7a0909c6243fe0c7e35f`。
- Reviewed patch-id：`8a5f0ffb23d288c8fe6b109179597bd379f60c16`。
- 計算命令：`git diff 7e76e74 6683a0f | git patch-id --stable`。
- 獨立 reviewer 識別：本次 Codex 對話主代理，未參與被審查 commit 的程式修復。
- 流程判定：`RETURN TO execute-task`。

## L-01 [P1] 不要將進程組主程序不存在視為整組已回收

位置：`internal/backend/opencode/process.go:104-107`，呼叫端為 `cmd/asura/cancel.go` 的 `safeKillTaskProcessGroup`。

VerifyTaskProcess 只查詢 `/proc/<PGID>/stat`，此處的 PID 是進程組主程序。主程序退出並被回收後，其子程序仍可保有同一個 PGID 並繼續執行。此分支卻返回 `(false, nil)`；取消端因而略過整組強殺、清除 PGID，並宣稱取消成功。這違反 SCN-004 的整個程序樹回收要求，也令後續重試失去回收目標。

應區分單一主程序退出與整個進程組結束，並以能持續確認任務所屬程序樹的身分／監督方式完成回收；不能因主程序不存在就推定剩餘成員不存在，也不能直接恢復無身分核驗的盲目強殺。

### 實際重現

2026-09-12 在 `/tmp/asura-review-group-lBhQYS` 編譯最新 CLI，以獨立暫存資料庫及 ID `orphan-group` 執行：

```sh
(sleep 3; echo CHILD_STILL_EXECUTED) & echo LEADER_EXIT; exit 0
```

主程序退出後執行 cancel，實際輸出順序如下：

```text
Task ID: orphan-group
LEADER_EXIT
Task orphan-group cancelled successfully
CHILD_STILL_EXECUTED
error: context canceled
```

取消端成功返回，子程序仍繼續執行並輸出，run 最後退出碼 1。此時 run 的 cmd.Wait 仍等待子程序持有的輸出管線，程序組尚有工作，並非任務已全部完成。探針僅輸出文字並有限等待，測試程序已自然結束。

## 驗證結果

2026-09-12 在沙箱外執行 `go test -count=1 -race ./...`，退出碼 0，全部 7 個套件通過。

原 K-01 的乾淨環境情境已有 StartTime 核驗與對應測試，但既有測試尚未涵蓋主程序先退出、其他組員仍在執行的情境。本次未修改 production code 或既有測試；只新增本報告。
