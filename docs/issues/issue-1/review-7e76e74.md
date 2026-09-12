# Issue 1：程序身分修復覆核

- 審查日期：2026-09-12。
- Reviewed HEAD SHA：`7e76e745d1136b30d34a7a0909c6243fe0c7e35f`。
- 審查基準：`716416a8a4b6290ddfe33d081dc6e090ed7a37b8`。
- Reviewed patch-id：`a2ed42d36f7becdfd5dbdb02c3ef6b443c4e4e7d`。
- 計算命令：`git diff 716416a 7e76e74 | git patch-id --stable`。
- 獨立 reviewer 識別：本次 Codex 對話主代理，未參與被審查 commit 的程式修復。
- 流程判定：`RETURN TO execute-task`。

## K-01 [P1] 身分無法確認時不得將仍存活的任務視為已回收

位置：`cmd/asura/cancel.go:91-99`；身分來源為 `internal/backend/opencode/process.go` 的 `IsTaskProcessGroup`。

環境變數不是不可變的程序身分。合法命令 `exec env -i ...` 會保持同一個程序／進程組，卻移除 ASURA_TASK_ID，導致 IsTaskProcessGroup 返回 false。取消端因此略過 SIGKILL，但仍無條件清除 PGID 並返回取消成功，原程序繼續執行，之後重試也因 PGID=0 而無法回收。讀取 `/proc` 失敗也被折疊成相同的 false，不能視為資源已消失的證據。

應使用不依賴受控命令保留環境變數的程序身分／監督機制，並區分「確認已結束或不屬於此任務」與「無法確認」。僅在確認完成回收後清除回收資訊並回報成功；不得為恢復回收而重新改為不驗身分的盲目強殺。

### 實際重現

2026-09-12：在 `/tmp/asura-review-identity-BT6bYT` 編譯最新 CLI，使用獨立暫存資料庫，以 Task ID `clean-env` 執行：

```sh
exec env -i PATH=/usr/bin:/bin sh -c "echo STARTED; sleep 3; echo STILL_EXECUTED"
```

啟動後執行 cancel，實際輸出順序如下：

```text
Task ID: clean-env
STARTED
Task clean-env cancelled successfully
STILL_EXECUTED
error: context canceled
```

cancel 成功返回，原程序仍繼續執行，run 最終退出碼 1。測試只執行文字輸出與有限等待，程序已自然結束，未操作使用者既有工作。

## 驗證結果

2026-09-12 在沙箱外執行 `go test -count=1 -race ./...`，退出碼 0，全部 7 個套件通過。

原 J-01 所列「舊 PGID 指向未帶任務環境變數的不相關程序」已有測試通過；但這不足以證明所有程序身分判斷可靠。新增身分機制引入 K-01，既有測試未覆蓋命令自行清除環境的情況。本次未修改 production code 或既有測試。
