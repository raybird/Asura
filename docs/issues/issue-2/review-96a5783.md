# 提交 96a5783 審查報告

建立日期：2026-09-13。

## 審查版本與範圍

- Reviewed HEAD SHA：`96a57837903d5c0bfb6f5a041ae2cedf2399eed0`
- Reviewed patch-id：`9842108f7ebd56bccce926d3d36e02d86686f634`（`git diff main...HEAD | git patch-id --stable`）
- 單一提交 patch-id：`0afbbc0830f97f06eaa6d1eaab7431ada092029d`（`git diff HEAD^ HEAD | git patch-id --stable`）
- 獨立 reviewer：本次 Codex 審查工作階段，未參與受審提交實作。
- 範圍：父提交至本提交的四個變更檔案，並交叉檢查 Storage、Daemon、Engine 與 CLI 的開庫及 Request ID 查詢路徑；不代表整個分支已受審。
- 方法：使用 gitnexus-pr-review 流程；GitNexus 回報 Asura 未建立索引，因此改以原始碼搜尋、diff 與測試確認，未宣稱取得圖譜證據。

## 結論

**patch is correct；程式碼審查 PASS。** 未發現達到回報門檻的新缺陷。

去重 SQL 在建立唯一索引前執行，只清除重複任務的 Request ID，保留任務本體。最早建立的任務保留映射，同時間以 ID 排序決定；這是提交明確選定的遷移行為，不將其誤列為缺陷。新增測試覆蓋真實 Go driver 開庫、保留結果、Request ID 查詢與唯一約束。

## 驗證證據

2026-09-13 實際執行：

```text
$ GOCACHE=/tmp/asura-review-gocache go test -count=1 -race ./internal/storage
ok  asura/internal/storage  1.222s
退出碼：0

$ GOCACHE=/tmp/asura-review-gocache go test -count=1 ./cmd/asura ./internal/engine
ok  asura/cmd/asura       0.008s
ok  asura/internal/engine 0.274s
退出碼：0
```

2026-09-13 另以 Python SQLite 記憶體資料庫擷取受審程式的 `createTable`、`dedupQuery` 與 `indices` SQL，插入六筆固定資料：三筆共用 Request ID（其中兩筆建立時間相同）、一筆獨立 Request ID、兩筆空 Request ID。確認字典序較小的同時間任務保留映射、其他重複映射清空、獨立及空映射不變、任務內容未變，再執行一次遷移確認結果相同。

```text
PASS: tie-break, three duplicates, unique/empty IDs, payload preservation, repeated migration
退出碼：0
```

此補充檢查是 SQL 層驗證，不冒稱為 Go driver 的額外永久回歸測試。可重複的主要驗證入口為上述儲存層測試命令，其中包含 `TestSQLiteStore_UpgradeFromSchemaWithDuplicateRequestIDs`。

## 限制與流程判定

- 未執行完整 `go test ./...`、完整 race 或真實 OpenCode／Daemon 端到端驗證；本報告不確認 README 所述完整回歸歷程。
- 未重建實作者的核准及紅綠重構歷程；程式碼 PASS 不等同整個 Issue 的流程 gate 全數通過。
- 本次僅新增審查文件，不修改 production code 或測試，因此文件產出不適用紅燈要求；以檔案存在、審查 SHA／patch-id 與必要章節檢查替代。
- 審查文件已持久化於本機，未建立 commit。
