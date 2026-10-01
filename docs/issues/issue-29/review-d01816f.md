# Issue 29 獨立審查報告

審查日期：2026-10-01。

- 範圍：完整 PR；79 個異動檔案，包含所有 production/test diff、常青文件、規格與持久化 probe/log artifacts。
- Reviewed BASE SHA：7785a204dc0de486938ee223342ae2af17eb6c2a
- Reviewed HEAD SHA：d01816fb7c8fc3045dfb7394e6cec45d9a97bd7e
- Reviewed patch-id：b1a2d956c5cb6a566c86ab87f9a51b926ceba023
- 獨立 reviewer：Codex 隔離 subagent `/root/review_issue29`，未參與實作；未修改 production/tests、未操作遠端。
- Review artifact：`docs/issues/issue-29/review-d01816f.md`
- 核准來源：2026-10-01 使用者「核准」，核准規格 commit `95ac2347e2857aa9b7a6d7f8b7ad11e1b1165805`。與 reviewed HEAD 比對，驗收語意未變，僅回填 SHA、證據及進度。

## 問題與風險

- MUST FIX：無。
- NICE TO HAVE／證據持久力：`internal/engine/utility_test.go:64` 使用固定 pipeline trace fixture；建議另加真實 PipelineRunner → Engine → SQLite 的 Three-Face usage 串接案例，防止日後 trace JSON 欄位或 stage name 改動時分層測試各自通過。目前真實 Critic stage parser、Engine+SQLite、既有 pipeline 整合及完整回歸共同提供有效證據，未 mock 正在驗收的 ledger/aggregate，也未構成假綠燈。
- 限制：recovery 單批 100、高水位固定，但遇第一筆錯誤即停止；5 秒啟動期限可能留 pending/error，後續重啟才再嘗試。此行為保留真實狀態，沒有宣稱已完成 recovery。無 GC、重建占用單一 SQLite 連線、busy lock 的實際取消延遲及模型 estimated 訊號限制均已揭露。

## 已查核維度

### 驗收與證據

以下規格均定位於 reviewed HEAD 的 `docs/issues/issue-29/README.md`；共同持久化命令、exit code、fixture/probe 限制在 `docs/issues/issue-29/evidence/verification.md`。已核對全部 12 項核准集合與唯一計畫責任 Task，未新增未授權行為。

| 驗收 | 具體查核與證據 | 結論 |
|---|---|---|
| UFL-001 | observe 不讀 aggregate；原 lexical score/ID ordering、候選/budget 路徑保持；`TestUtilityObserveAndExplicit` 與完整 RECALL 回歸 | 通過 |
| UFL-002 | Task/event 同交易；request 去重先於 recall；`TestUtilityLedgerAdmission`、`TestUtilityAdmissionRollback`、`TestUtilityRecallIdentity`、真實 IPC `TestUtilitySealedRedelivery` | 通過 |
| UFL-003 | canonical authorization 先於 ledger lookup、exact scope SQL；alias/授權內子 workspace/foreign scope；`TestUtilityIPCIsolation`、scope-bound PersistUtility、既有 auth/MEM 回歸 | 通過 |
| UFL-004 | optional raw 欄位與核心 review 分離，8192-byte/4-item/auto-ID allowlist；missing/type/duplicate/fabricated/explicit/state 異常全 unknown；9 個固定模型原始結果與最終 parser 重播 | 通過；僅 estimated |
| UFL-005 | Engine terminal hook 不呼叫模型；`TestDirectUtilityUnknown` success/failed 均 calls=1、unknown=1；取消與 partial output 的既有 integration 回歸 | 通過 |
| UFL-006 | assessment 唯一鍵，payload conflict、aggregate 同交易；三次 retry 一次 count；trigger rollback、重啟二次與 cache 污損後 ledger 重建 | 通過 |
| UFL-007 | threshold 5、floor .5、整數 rounding/final tie；explicit 不進 aggregate/read weighting；selected lexical rank/weight/final score 封存；250ms context 涵蓋候選/aggregate/selector | 通過 |
| UFL-008 | 低樣本與全 unknown neutral；unknown 不進 used+unused 分母；最大 int64 計數公式使用 big.Int 避免乘法溢位；壞 count 拒絕 | 通過 |
| UFL-009 | 無 memory cascade；刪除後不再候選，tombstone query 同 scope；redelivery 保留 context；新批准 ID 不繼承 aggregate，既有 Git approved/delete 回歸 | 通過 |
| UFL-010 | keyset DESC time/recall_id、scope+memory cursor、SQL sentinel field projection、server/client 64KiB、limit/cursor/ID bounds；10k/100k 容量與 query plan 證據 | 通過 |
| UFL-011 | admission/apply read fail-closed；terminal telemetry fail-open、不改主結果；`TestUtilityFailureStatus`/policy failure、cancelled unknown、critic stage failure unknown、failed verdict 可 estimated | 通過 |
| UFL-012 | 本機最終 vet/race/build、183 檔 hash manifest、正式 CI 36807429281 全部步驟 success；CI source cbbed925 與 reviewed HEAD 的差異僅交付文件 | 通過 |

### 重要失敗面與邊界

- atomic admission：event INSERT trigger 中止，Task rollback；重送 request 不留第二 event。交易 executor 保留既有 duplicate request 判別，Daemon 失敗釋放容量。
- retry/conflict/crash：相同 state/provenance 不更新 observation time/count；不同 payload conflict；assessment INSERT 與 aggregate UPDATE 在同交易；高水位 keyset rebuild 不整批載入，SQLite 交易／單一連線避免並行新增被遺漏。
- authorization：拒絕 workspace 在 query 前結束；SQL 同時使用 workspace+ID/recall ID，foreign/not-found 同錯誤；查詢無寫入。撤權與 symlink escape 沿用同一已驗證 canonical policy 邊界。
- Critic：未知欄位型別不影響合法核心 verdict；invalid usage whole-block unknown；模型提供 provenance 不受信，重新標 estimated；核心 invalid review 維持既有 pipeline failure。
- terminal truthfulness：僅 terminal Task 可完成 utility；取消強制 unknown；完成 Critic stage 與合法 verdict 才使用 trace signal；trace 缺失/超界 unknown；寫入失敗保留 pending/error，不假冒計數。
- apply：只有 positive lexical score 能入選，score 上限由 128 query terms 得出，乘 weight 不溢位；weight counts 合法性在 store 與公式邊界驗證；explicit 不受降權，沒有 boost/額外 memory/background AI。
- history/delete：封存的 memory content、RecallID、utility policy/rank 不以現況重算；legacy 無 RecallID 不補造事件；tombstone 身分不產生正式 memory 寫入。
- bounded audit：limit 1..100，最多 limit+1 rows；cursor 512 bytes、ID 256、time/reason sentinel；超量 JSON 明確 error，client 限制 reader；capacity 查核命中索引而非只斷言查詢成功。

### 架構、品質與測試真偽

- storage 擁有 ledger/aggregate；Daemon 授權與 policy，Engine terminal hook，backend 僅供 bounded estimated signal。沒有新增 Web UI、額外模型 worker 或不相關重構。
- 已查核跨 Task 重複：online/rebuild 共用 `incrementUtilityAggregate`；pipeline/storage 在各自信任邊界再次驗證有必要，沒有因兩份邏輯分歧造成已知錯誤。
- golden 字面期望、trigger fault、ledger/cache 對照與真實 IPC/SQLite 斷言可與實作不一致；未削弱既有測試。容量 seed 直接寫入是容量資料建構，沒有宣稱該部分驗 admission。真實模型 fixture 由人工規則標註，未把模型自評當排序正確性的真相。
- 紅燈證據記錄具體命令/exit/目標缺失；P0、P2、P3、P4 合併或分層理由已說明。沒有把語法錯誤、純文件檢查或 skip live probe 當模型驗收。
- 2026-10-01 reviewer 獨立執行 `sha256sum -c docs/issues/issue-29/evidence/tested-files.sha256`：exit 0，初輪 182 檔吻合；最終 reviewed HEAD 更新為 183 檔，再次檢查 exit 0。
- 2026-10-01 reviewer 獨立執行 `go test -race ./internal/storage ./internal/recall ./internal/pipeline ./internal/engine ./internal/daemon ./cmd/asura -run 'Utility|StatusRecall' -count=1`：exit 0，6 個 package 全綠。沒有重跑 opt-in live 模型；核對 archived prompt/JSONL/review 與最終 replay。
- 豁免：無。待確認：兩項已按真實核准/現有設定解決；模型 probe 已取得，不推論其他模型可信度。

### 最終版本與交付查核

- 2026-10-01 完整增量覆核 `b618f23..06b6cfe`：utility audit 明列 current policy，observe 的有效 weight 固定 1000000，apply 保留 derived weight；設定於 server response size 檢查前。原回應缺 policy 的 SHOULD FIX 已修正。`TestUtilityCurrentPolicyAudit` 以真實 IPC、相同 5-unused aggregate，獨立字面斷言 observe/apply 為 1.0/0.5；reviewer 補跑 `go test -race ./internal/daemon -run '^TestUtilityCurrentPolicyAudit$' -count=1` exit 0。
- 2026-10-01 完整增量覆核 `06b6cfe..cbbed925`：正式 CI 發現原有 migration marker 的 SELECT/INSERT 競態；真實 CLI 與 16 連線開庫紅燈已保存。修復以隔離交易第一個操作 `INSERT ... ON CONFLICT(name) DO NOTHING` 取得 claim，僅 RowsAffected=1 者執行隔離，未取得者提交略過。marker 與隔離更新同交易，trigger 故障可 rollback/retry；保留已完成 marker fast path，未改隔離範圍。reviewer 補跑 `go test -race ./internal/storage -run 'TestLegacyMigration|TestGit' -count=1` exit 0。
- 2026-10-01 完整增量覆核 `cbbed9250ef809416961480e2321b2a58e99112f..d01816fb7c8fc3045dfb7394e6cec45d9a97bd7e`：六份交付文件／log，沒有 production/test/config 變更；183 檔 source manifest 再次全數一致。固定核准規格與 UFL-001～012 語意未變，文件更新沒有被冒充程式測試。
- 最終完整本機 `go test -race ./... -count=1` exit 0，integration 278.995s，見 `evidence/final-race.log`。正式 [CI 36807429281](https://github.com/raybird/Asura/actions/runs/36807429281) 被測 source 為 `cbbed9250ef809416961480e2321b2a58e99112f`，`evidence/final-ci.json` 保存全部步驟 success：vet、完整 race、Web memory UI、Web Git browser、CLI build。與 reviewed HEAD 的差異僅上述六份文件，故可重用 source 證據。
- 首輪 CI 的 Git WaitDelay 100ms 失敗本機重跑 30 次未重現；沒有改等待門檻或弱化測試。最新正式全套已成功，不以「未重現」取代正式回歸結果。migration 原錯誤已修復，故不存在尚未處理的已知必要工作。
- 已核對 PR #30 的最終 Proof of Test 內容（協調者保存於 `/tmp/asura-29-pr.md` 並更新平台）：12 列恰等於核准集合，各列規格與證據均固定在 reviewed HEAD；source CI 與文件 HEAD 對應明列。後續報告提交 SHA 尚未產生，由協調者追加真實 SHA，不能預填不存在的提交。
- README/P5 目前採「交付驗證完成、review PASS 後完成並等待合併」的條件式紀錄，沒有假冒尚未取得的報告或 merge。本報告 PASS 是 P5 獨立審查完成依據；PR 尚未合併。

## 流程判定

**PASS**。

完整固定範圍獨立審查已完成，本報告已持久化，必要 12 項驗收與重要失敗面具有效證據，沒有 MUST FIX。保留上述 NICE TO HAVE 與已揭露限制；可等待合併，不代表已合併或 issue 已結案。

本報告綁定 reviewed HEAD `d01816fb7c8fc3045dfb7394e6cec45d9a97bd7e`。協調者可新增直接後繼、唯一非 merge 且只加入本報告的提交，再執行 `verify-artifact.py` 驗證 artifact-only 例外；其餘新交付變更須重新查核。
