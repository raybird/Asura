# Issue 1：孤兒進程組修復覆核

- 審查日期：2026-09-12。
- Reviewed HEAD SHA：`6c8895b0cd9c5fb7b4de02963a46274d3c6f98ef`。
- 審查基準：`6683a0f61333b9b0f9d7f4f4ec608fa6c95b846b`。
- Reviewed patch-id：`f0b85f47f2403dfca7f39bad9608d8e70b332a9a`。
- 計算命令：`git diff 6683a0f 6c8895b | git patch-id --stable`。
- 獨立 reviewer 識別：本次 Codex 對話主代理，未參與被審查 commit 的程式修復。
- 流程判定：`RETURN TO execute-task`。

## M-01 [P1] 孤兒進程組身分無法確認時須保留未知狀態

位置：`internal/backend/opencode/process.go:247-258`。

此路徑已確認進程組仍存活且 Leader 不存在。若 Runner 也已中斷，剩餘成員沒有 ASURA_TASK_ID 並不能證明它們不屬於原任務，因為合法的 `exec env -i` 可以移除此標記。但函式最終返回 `(false, nil)`，呼叫端將其當成身分明確不符／已回收，略過 SIGKILL、清除 PGID 並宣稱取消成功。

應將「沒有找到足夠身分證據」回報為無法確認，保留回收資訊並揭露錯誤；若要支援此情境的自動回收，必須保存不依賴 Runner 存活或子程序環境變數的程序樹擁有權證據。不能直接對未知進程組盲目強殺。

### 實際重現

2026-09-12：於 `/tmp/asura-review-orphan-8iKroX` 編譯最新 CLI，使用獨立暫存資料庫 `tasks2.db` 與 Task ID `clean-orphan` 執行：

```sh
exec env -i PATH=/usr/bin:/bin sh -c "sleep 4 & echo LEADER_EXIT; exit 0"
```

Leader 退出後，中止本次測試自己啟動的 CLI Runner，模擬執行端崩潰，再執行 cancel。取消端回報：

```text
Task clean-orphan cancelled successfully
```

緊接著依鎖檔 PGID 查詢 `ps -eo pid,pgid,stat,args`，仍有非 zombie 的存活成員：

```text
  22056   22055 S    sleep 4
```

這確認取消成功後程序仍存活。所有受操作程序均由探針建立；sleep 已自然結束，沒有中止使用者既有工作。

## 驗證結果

2026-09-12 在沙箱外執行 `go test -count=1 -race ./...`，退出碼 0，全部 7 個套件通過。原 L-01 的 Runner 仍存活／孤兒保留 Task ID 標記情境已有測試涵蓋，但未涵蓋本次 Runner 中斷且子程序清除環境變數的組合。

本次未修改 production code 或既有測試；只新增本報告。
