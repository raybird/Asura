# Issue 29 驗證證據

驗證日期：2026-10-01。核准規格：`95ac2347e2857aa9b7a6d7f8b7ad11e1b1165805`；工作區的核准 SHA 回填及執行證據更新不改驗收語意。被測 Go 檔案與 go.mod/go.sum 以 [tested-files.sha256](tested-files.sha256) 固定，`sha256sum -c docs/issues/issue-29/evidence/tested-files.sha256` 可重驗；交付提交由 Git 保存完整內容。文件更新不等同程式測試。

## 紅燈與最小實作

| Task | 真實紅燈命令 | exit／目標失敗原因 | 綠燈與映射 |
|---|---|---|---|
| P0.1 | `go test ./internal/pipeline -run 'TestCriticUtility' -count=1` | 1；7 個 usage 案例皆沒有 memory_usage，prompt 缺 schema/allowlist | pipeline race／archived replay；UFL-004 |
| P1.1 內層 | `go test ./internal/storage -run '^TestUtilityLedgerAdmission$' -count=1` | 1；SQL no such table: recall_events，缺持久化 event | storage race；UFL-002 |
| P1.1 外層 | `go test ./internal/daemon -run '^TestUtilityRecallIdentity$' -count=1` | 1；sealed 決策沒有 recall_id、utility policy/version/weight | daemon race；UFL-002、003 |
| P2.1 | `go test ./internal/engine -run '^TestDirectUtilityUnknown$' -count=1` | 1；success/failed 均 calls=1、aggregate=map[]，缺 utility observation | engine/storage race；UFL-005、006、009、011 |
| P3.1 | `go test ./internal/daemon -run '^TestUtilityIPCIsolation$' -count=1` | 1；真實 IPC 回傳 unknown action: memory_utility；另揭露 empty trace SQL NULL，已修正 coalesce | daemon/CLI/storage race；UFL-003、010 |
| P4.1 | `go test ./internal/recall -run '^TestUtilityApplyRanking$' -count=1` | 1；應 a,z 實際 z,a，weight 仍 1.0 | recall race；UFL-001、007、008 |

P0 的 Stage 單元測試直接執行 Prepare/Execute 與 optional parser；同一層檢核不重複紅燈，真實模型 probe 另驗估計一致性。P1 有 admission 與儲存不同失敗面，保留兩層紅燈。P2 以真實 Engine+SQLite 驗 Direct observation 缺失；再補 storage retry/rollback/rebuild 與 Three-Face/terminal fixture 查核各失敗面。P3 的真實 IPC+SQLite 覆蓋外部 page 契約，storage/CLI 測試補 bounds 與 decoder 失敗，沒有另宣稱未執行的單元紅燈。P4 selector golden 紅燈與 storage 數值案例分別驗排序、整數公式；Daemon redelivery 整合保護 sealed policy，不以模型自評當排序正確性的來源。

## 具體失敗面

- `TestCriticUtilityContract`：missing、wrong type、fabricated、explicit-only、duplicate、invalid state 全 unknown；合法模型訊號 estimated，核心 verdict 保留。
- `TestUtilityAdmissionRollback`：event INSERT trigger failure 不能留下 Task；duplicate RequestID 不能增 event。
- `TestUtilityAtomicRetryRebuild`：重送三次只算一次、conflict 拒絕、跨 scope 不計數、aggregate trigger failure 全交易 rollback；刻意損毀 cache 後由 ledger 重建相同 counts/time。
- `TestUtilityRecoveryAndDeletion`／`TestUtilityTerminalOutcomes`：重啟兩次一次計數、tombstone audit、cancelled unknown、有效 Critic verdict failed 可 estimated、Critic stage failed unknown。
- `TestUtilityFailureStatus`：UFL 寫入錯誤後主 Task 仍 completed，audit 為 error。
- `TestDirectUtilityUnknown`：success/failed 皆單一模型呼叫、unknown=1；原取消、partial output 與 queue 回歸在完整 integration 套件。
- `TestUtilityPageBounds`／`TestUtilityIPCIsolation`／`TestUtilityClientRejectsOversize`／`TestMemoryUtilityCLI`：keyset、錯 scope cursor、limit、legacy 超大 timestamp、canonical alias、授權範圍內跨 scope 相同不存在錯誤、unauthorized、daemon unavailable、client 64KiB cap、CLI 非零錯誤。
- `TestUtilitySealedRedelivery`：真實 IPC Task 執行後刪 memory、重啟改 apply，同 RequestID 回傳相同 Task/context/observe policy/content，模型 calls=1、observed=1。
- `TestUtilityWeight`／`TestUtilityApplyRanking`／`TestUtilityObserveAndExplicit`：threshold 5、floor、rounding、最大 int64 不溢位、unknown neutral、observe lexical、explicit 排除權重、final tie 與原 lexical rank。

## Critic 真實模型 probe

命令：`ASURA_UFL_PROBE_DIR="$PWD/docs/issues/issue-29/evidence/critic-probe" go test ./internal/pipeline -run '^TestUtilityLiveProbe$' -count=1 -v`。exit 0，112.534s，OpenCode 1.18.31／openai/gpt-5.6-sol、pure、所有工具 deny、臨時 workspace。3 個人工明確 fixture 各跑 3 次，used/unused/unknown 9/9 一致；原始 prompt/JSONL/review 保存在 [critic-probe/](critic-probe/)，執行輸出 [critic-probe.log](critic-probe.log)。

模型 probe 於 P0 執行；後續新增 optional 欄位 8192-byte cap 不改 probe prompt 或 allowlist。`go test -race ./internal/pipeline -count=1` exit 0（[critic-replay.log](critic-replay.log)），9 份原始回應再由最終 parser 重播。這是 fixture 一致性與 parser 相容證據，不能證明實際因果效用，也不能代表其他模型已通過。

## 容量與對照組

命令：`ASURA_UFL_CAPACITY_PROBE=1 go test ./internal/storage -run '^TestUtilityCapacityProbe$' -count=1 -v`。exit 0，16.244s；[capacity.log](capacity.log) 保存實際兩組數值。100k events 下 query20=273.023µs、aggregate256=5.755877ms、rebuild=3.57353013s、recovery100=348.797296ms，DB+WAL+SHM=163691664 bytes。

固定 fixture 一個 event 一個 auto observation，4 筆 ledger/task 邏輯寫入再加 aggregate；對照組以整數已知 ceil(N/256) 驗單筆 observed count，刻意刪 100 assessments、重建 cache 後 recovery 恢復總數 N。EXPLAIN 必須命中 utility_scope_memory_time 且不能 SCAN utility_assessments。這些判準在少計／漏 recovery／全表 query 時會紅；並非僅看到 query 成功就宣稱通過。recovery100 並非 recall admission，因此不套 250ms admission 判準；同一 probe 明確要求 aggregate256<=250ms。

容量 fixture 不承諾最壞 contention latency、四 auto 的實際 DB 總量或任意數量的 explicit；既有 SQLite busy_timeout=10000 與單一連線仍可能延後 lock cancellation。應用 query 使用 context deadline；不把它宣稱成 OS 級即時中斷保證。retention 無 GC，DB 持續增長。

## 精煉與最終驗證

2026-10-01 使用 code-simplify：把 online aggregation／ledger rebuild 共用的 count 更新收斂為 `incrementUtilityAggregate`，避免兩份 SQL 分歧；reason 使用直接 if，保留跨模組、JSON、store API 契約。保留 pipeline／storage 在各自信任邊界再次驗證 usage，這是防禦查核，不合併為模型可信度來源。精煉後 storage/race 與容量 probe 通過；未擴大無關範圍。

- `go vet ./...`：exit 0，空錯誤輸出見 [vet.log](vet.log)。
- `go build -o /tmp/asura-issue-29 ./cmd/asura`：exit 0。
- `go test -race ./internal/daemon ./cmd/asura -run 'Utility|StatusRecall' -count=1`：exit 0，[client-cli-race.log](client-cli-race.log)。
- `go test -race ./internal/daemon -run Utility -count=1`：exit 0，[daemon-race.log](daemon-race.log)，含最後新增 sealed redelivery 整合。
- 精煉後完整 `go test -count=1 -race ./...`：exit 0，所有 package 全綠，integration 263.278s；[race.log](race.log)。之後只新增 pipeline archived/oversize fixture 與 Daemon sealed redelivery 測試，兩個 package 最終增量 race 亦全綠；production code 沒有後續修改。
- 正式 CI 與獨立 review：P5 尚待執行，不冒充通過。
