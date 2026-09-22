# 規劃探測紀錄

日期：2026-09-22。基線：`8576dd0323750a1aa8f2c550c0f69e8edd30e55d`；工作區僅新增 issue-19 規劃文件與 probe。

## 首要風險：既有隔離與冪等邊界

命令：

```sh
go test -count=1 ./tests/integration -run 'TestCTXMEM(002|003|004|005|_Queued|_Symlink)'
```

退出碼 0；原始結果：

```text
ok  asura/tests/integration  0.090s
```

覆蓋跨專案拒絕、撤權／清單外拒絕、重送快照、刪除／不存在記憶、queued snapshot 與 symlink retarget。映射 RECALL-001、002、006、007 的既有行為基線，不代表新功能已驗收。Go 的 Snap 啟動在沙箱內失敗，獲准沙箱外執行後取得此結果；環境失敗不算功能紅燈。

## 詞彙、SQL 與 benchmark

可重現原始碼：[recall_probe_test.go](recall_probe_test.go)。fixture 使用實際 Memory 的五欄位形狀建立隔離 SQLite 表，不接觸使用者資料。

```sh
go test -v -count=1 ./docs/issues/issue-19/evidence -bench . -benchtime=10x
```

退出碼 0；2026-09-22 原始輸出：

```text
=== RUN   TestRecallProbe
query="SQLITE workspace 隔離" selected="a,d,b"
query="transaction rollback" selected="b"
query="部署 Docker" selected="c"
query="香蕉果汁" selected=""
query="資料庫" selected=""
query="隔" selected=""
formatted_bytes=76 estimated_delta=33 raw_content_estimate=25
--- PASS: TestRecallProbe (0.00s)
=== RUN   TestBoundedSQLProbe
SEARCH memories USING INDEX recall_scope_id (workspace=?)
database_rows=10001 returned=257 returned_bytes=14081 elapsed=477.073µs
--- PASS: TestBoundedSQLProbe (0.04s)
goos: linux
goarch: amd64
pkg: asura/docs/issues/issue-19/evidence
cpu: 11th Gen Intel(R) Core(TM) i5-11400 @ 2.60GHz
BenchmarkBoundedRanking-12    10    46292473 ns/op
PASS
ok  asura/docs/issues/issue-19/evidence  0.559s
```

上列去除 Go 行號前綴，保留實際數值。排序每個案例重跑 100 次；expected IDs 為獨立手選 fixture，非以同一排序公式重算。benchmark 使用 256 筆各 300 次重複混合文字，約 46.3ms/op；此資料不是最大不同詞數的 worst case。

SQL 原文：`SELECT id, substr(CAST(content AS BLOB),1,8193) FROM memories WHERE workspace=? ORDER BY id LIMIT 257`，參數 `/allowed`。資料：同 scope 10000 筆（第一筆 1 MiB），另一 scope 1 筆 SECRET；證據包含超大內容 sentinel、257 列與 foreign ID 排除。查詢 deadline 100ms 下正常完成；沒有驗證 timeout 真正發生時取消成功。

## 判讀與未完成驗證

- 此探測支持可重現文字比對，不支持語意召回品質或生產延遲保證。
- 單筆超大內容未全量返回；索引 plan 沒有 TEMP B-TREE。metadata 完整上限 SQL 為後續提案，尚未由此 probe 覆蓋。
- wrapper overhead 多 8 estimated tokens，不能只估 raw content。
- 固定 ID 候選前綴、同義詞與單字查詢有漏召回限制，需使用者核准。
- 未執行新功能安全／重啟／取消／完整回歸；新 RECALL 驗收不是 PASS。

## 文件檢查

2026-09-22：以 Python 檢查四份必要文件存在、Markdown 相對連結目標存在，並以正規表示式比對十個唯一 `@RECALL-NNN` 與十列待核准紀錄完全一致，退出碼 0。`git diff --check` 退出碼 0；新增文件尚未追蹤，另以逐行檢查確認新增檔案無不預期尾端空白及衝突標記。純規劃文件不適用產品紅綠測試。
