# PR 32 正式 CI blocker 修正

日期：2026-10-01。修正前 HEAD：`b350357c52153c871efe88e469866df6b47c037d`。Task：P5-T1；驗收：MLIFE-012、L-TBD-10。被測 Go 原始碼雜湊見 [tested-source.sha256](../tested-source.sha256)，工作區 issue-31-memory-lifecycle。

## 紅燈與來源
[正式 CI run 254](https://github.com/raybird/Asura/actions/runs/36826294834) conclusion=failure；[摘錄](ci-254-red.log)：100k migration=41.896294367s >30s；QueryDeadline=377.58s，integration package 10m timeout。使用者於 2026-10-01 指出兩個 BLOCKER，允許「把環境敏感的 performance probe 與標準 correctness regression 分離，但性能驗收證據仍要保留」。不是性能門檻豁免。

## 最小修正
- `sqlite_lifecycle_probe_test.go` 加 lifecycleperf build tag。原 10k/100k fixture、migration 30s、query 2s、EXPLAIN 與答案斷言完全保留；CI 新增獨立非 race performance step，含 `/usr/bin/time -v` 的 RSS／耗時。Standard race 保留所有 correctness tests，不再用 instrumentation 後速度代替 production 性能。
- QueryDeadline 使用一列 SQLite view 與固定 400ms 等待的 scalar UDF，實際透過 modernc driver 執行。先用成功查詢的 literal ID/content 驗 fixture，再以 20ms context 驗 query error＋DeadlineExceeded，各段 wall-clock 上限3s；Daemon 250ms deadline、無 backend/Task 副作用與連線恢復斷言保留。取消仍由真實 driver 執行，UDF 沒有偽造取消錯誤。
- 無 production code、依賴或既有業務契約變更；修正範圍僅測試、CI 設定與 issue 證據。gitnexus 無 Asura 索引，改由原始碼直接追蹤 RecallCandidates → modernc driver／Daemon deadline，不建立範圍外索引。

## 本地驗證
使用與正式 CI 相同的 Go 1.26.7 linux/amd64；本機 Linux 6.8.0-142-generic。

| 命令 | 結果／證據 |
|---|---|
| `GOTOOLCHAIN=go1.26.7 go test -race ./tests/integration -run '^TestRECALL_QueryDeadline$' -count=3 -timeout 30s -v` | exit 0，三次約1.4s：[deadline-ci-toolchain.log](deadline-ci-toolchain.log) |
| 相同測試使用本機 Go 1.27.1 | exit 0：[deadline-green.log](deadline-green.log) |
| `/usr/bin/time -v env GOTOOLCHAIN=go1.26.7 go test -tags lifecycleperf -count=1 -timeout 2m -run '^TestLifecycleLargeDataProbe$' -v ./internal/storage` | exit 0，10k/100k 原門檻全通過：[performance-green.log](performance-green.log)；RSS 含 go tool/build，不聲稱是純 Daemon RSS |
| `go test -race -list '^TestLifecycleLargeDataProbe$' ./internal/storage` | exit 0，default suite 無該性能測試：[default-suite-list.log](default-suite-list.log) |
| `GOTOOLCHAIN=go1.26.7 go vet ./...` | exit 0：[vet.log](vet.log)，成功無輸出 |
| `GOTOOLCHAIN=go1.26.7 go build -o /tmp/asura-issue31-ci-fix ./cmd/asura` | exit 0：[build.log](build.log)，成功無輸出 |
| `GOTOOLCHAIN=go1.26.7 go test -count=1 -race ./...` | exit 0，全部套件：[regression.log](regression.log)，integration=304.408s，仍在既有10m上限內 |

2026-10-01 code-simplify 檢查：no-op；單一 fixture 不抽新共用介面，sync.Once 只保障重複 count 的 SQLite 全域 function registration，不改公開 API。沒有精煉後新 diff，沿用上述最終綠燈。紅燈來源為真實正式 CI；本次測試／CI 修正不改產品可觀察行為，依 verification 採 fixture 控制組與實際執行證據，不偽造 production TDD 紅燈。

## 正式放行條件
以上僅本地結果。最新 PR source SHA 的正式 CI 須完整通過 race、獨立性能 probe、兩種 Web browser/UI probe 與 build，才可以進第二輪審查。持久化 CI artifact 與固定 SHA 由 [PR 32 checks](https://github.com/raybird/Asura/pull/32/checks) 及 PR 驗證區塊提供；舊獨立 PASS 不自動沿用新 HEAD。
