# Issue 1：啟動握手修復覆核

- 審查日期：2026-09-12。
- Reviewed HEAD SHA：`75b74b1b19ccf03f32dbfb7eb64349583abd1f33`。
- 審查基準：`c548a2b6764c5e2c46a8b8780c0690a36d43ff6b`。
- Reviewed patch-id：`de26422c2ae53b55fd7be55ec1a519867c4503f0`。
- 計算命令：`git diff c548a2b 75b74b1 | git patch-id --stable`，範圍為本次修復 commit。
- 獨立 reviewer 識別：本次 Codex 對話主代理，未參與被審查 commit 的程式修復。
- 流程判定：`RETURN TO execute-task`。

## H-01 [P1] 取得啟動鎖失敗時不得繼續執行或宣告握手完成

位置：`cmd/asura/run.go:143-148`，對應取消端 `cmd/asura/cancel.go:58-63`。

兩端都忽略 `os.OpenFile` 失敗，且忽略 `syscall.Flock` 的回傳錯誤；取得互斥鎖失敗時仍繼續啟動或回報取消成功。CLI 允許自訂 Task ID，但直接用 ID 拼成路徑，因此 `--id batch/1` 會要求開啟不存在的 `.task-batch/1.lock`，正常輸入即可令整個握手靜默失效。目錄權限或不支援 flock 等失敗也會走同一路徑。

此時原 G-01 的交錯重新成立：執行端查到 running 後暫停，取消端更新 cancelled，因鎖檔開啟失敗而不等待；PGID 仍為 0 時直接回報成功，執行端恢復後仍可啟動程序並產生副作用。

應使用不受 Task ID 路徑字元影響的鎖檔識別，並檢查開啟／加鎖錯誤；無法建立互斥保證時，執行端應返回明確錯誤，取消端不得宣稱握手與回收完成。

### 實際重現

2026-09-12 使用獨立暫存目錄 `/tmp/asura-review-lock-j8kI5h` 執行：

```bash
go run ./cmd/asura run --db /tmp/asura-review-lock-j8kI5h/asura.db --workspace /tmp/asura-review-lock-j8kI5h --id batch/1 'echo handshake-bypassed'
ls -la /tmp/asura-review-lock-j8kI5h
```

退出碼 0，輸出如下；目錄中只有 `asura.db`，沒有任務鎖檔或 `.task-batch` 子目錄：

```text
Task ID: batch/1
handshake-bypassed
Status:  completed
```

此探針確認「鎖檔建立失敗仍執行」，未宣稱以程序暫停重現完整取消競爭；競爭後果依兩端程式碼時序判定。

## 驗證與已修復項目

2026-09-12 在沙箱外執行 `go test -count=1 -race ./...`，退出碼 0，全部 7 個套件通過，含新的握手整合測試與提前取消持久化測試。

原 G-02 已補上未取消且有界的 Context 保存終態，相關測試通過。原 G-01 在成功取得相同鎖檔時已有跨程序交接，但 H-01 的失敗路徑未受保護，因此尚不能認定完整修復。

本次未修改 production code 或既有測試；只新增本報告，探針資料位於暫存目錄。
