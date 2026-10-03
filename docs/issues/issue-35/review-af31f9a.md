# Issue 35 獨立審查報告

- 審查日期：2026-10-04。
- 範圍：完整 PR 變更，固定 BASE..HEAD；包含規格、production、測試、常青文件與證據。
- Reviewed BASE SHA：`37e034b667700a7ced3f22919fb26b2a0d824a6c`。
- Reviewed HEAD SHA：`af31f9acfb204508e0ffa6787dfd4934eea6f783`。
- Reviewed patch-id：`55b17fe9870007787f9268901efd24e07a0ecd8c`；reviewer 以 `git diff BASE HEAD | git patch-id --stable` 獨立核對。
- 獨立 reviewer：Codex 隔離 subagent `/root/review_issue35`；原實作者為協調者 `/root`。reviewer 未修改 production 或測試，僅新增本報告。
- Review artifact：`docs/issues/issue-35/review-af31f9a.md`。
- 交付：[PR #36](https://github.com/raybird/Asura/pull/36)。審查時遠端 source 尚為 `6a5bf2d`；本結論綁定上述本機固定 HEAD，協調者需推送並核對 source，不能直接宣稱舊遠端 source 已通過此版本審查。

## 問題與風險

MUST FIX：最終被審查版本無未解決項目。初版曾發現以下兩項，均已取得真正紅燈、修復並納入最終完整 race 綠燈：

1. local auto 選 OMP／Antigravity 時，`BeforeStart` 持有 flock，而 `OnProcessStarted` 再以另一個 file handle 取得相同 flock，造成同步死鎖。`clisandbox.Run` 要等 callback 返回才釋鎖，因此啟動與外部取消都可能停住。最終 `cmd/asura/run.go:336` 保存已持有的 handle，`cmd/asura/runtime_process.go:14` 重用該 handle 登記身分。[start-lock-red.log](evidence/start-lock-red.log) 為真子進程與真 flock 紅燈，`TestRuntimeSelectionHeldStartLock` 驗證 callback 完成及完整身分 metadata。
2. local auto OpenCode／Codex 在版本探測期間已被成功外部取消，仍可能先啟動 prompt 程序產生 side effect，再於啟動 callback 被殺。最終兩 adapter 在準備完成且 `cmd.Start` 之前呼叫 additive `BeforeStart`；local composition root 將取消檢查與啟動身分登記包在同一鎖區間。[cancel-probe-red.log](evidence/cancel-probe-red.log) 保存成功取消後仍執行的紅燈，`TestRuntimeSelectionCancelDuringVersionProbe` 使用真 CLI、DB、外部取消與 sentinel 驗證修復。

SHOULD FIX（證據持久力改善，不阻塞）：

- `cmd/asura/runtime_selection_test.go:195` 的已持鎖測試直接呼叫登記 helper，未跑完整 OMP／Antigravity sandbox 啟動接線。已查核兩 adapter 共用 `clisandbox` 的 BeforeStart→OnProcessStarted→release 順序及 composition root callback；後續可補整條接線 fixture，讓 callback 被錯誤接回另一個 handle 時也能自動阻止回歸。
- daemon 使用 `Config.RuntimePriority`、ask 不傳 client priority 的實作邊界正確；現有新增測試主要驗證純 policy 與 local 環境變數。可補 daemon 自訂排序加 client 相反環境、並在設定變動後重送的整合案例。

NICE TO HAVE：`cmd/asura/runtime_selection_test.go:181` 的 sentinel 在釋鎖前取樣；可在 runner 完成後再確認一次 sentinel 不存在，加強延遲啟動的回歸保障。本次紅燈與修復邏輯有效，不將持久力改善稱為假綠燈。

## 已查核維度

### 核准、範圍與測試版本

2026-10-03 使用者「提案也核准」涵蓋 RTSEL-001～015 與全部技術提案；規格基線為 `c3381d4`。已以 `git diff c3381d4 af31f9a -- docs/issues/issue-35/README.md` 核對：驗收劇本與核准表未實質改變，差異是核准 SHA 回填、必要取消修復的檔案清單及交付狀態。

最終證據：[final-race.log](evidence/final-race.log)、[final-vet.log](evidence/final-vet.log)、[final-build.log](evidence/final-build.log)。實際命令為 `go test -count=1 -race ./...`、`go vet ./...`、`go build -o /tmp/asura-issue35 ./cmd/asura`，全部 exit 0，race 包含 `tests/integration`。reviewer 執行 `sha256sum -c docs/issues/issue-35/evidence/tested-source.sha256`，207 項全部符合、exit 0；被測來源與固定 HEAD 相同。`6a5bf2d..af31f9a` 僅有 issue 交付文件差異，不改被測 production、測試或 Go 設定。

### 驗收覆蓋

規格定位：`af31f9acfb204508e0ffa6787dfd4934eea6f783:docs/issues/issue-35/README.md`。下列全部使用同一份最終完整綠燈；個別測試及紅燈來源另列。

| 驗收 | 查核結果與證據 |
|---|---|
| RTSEL-001 | PASS；CLI explicit／省略仍維持既有 shell 分支，auto 另建真 adapter registry；既有 run 回歸及 `TestRuntimeSelectionLocalAuto` 保護。 |
| RTSEL-002 | PASS；共同 matcher 產生每個 candidate required／available／missing；`TestRuntimeSelectionDefaultAndOrder` 與真 daemon admission／redelivery 測試。 |
| RTSEL-003 | PASS；預設 OpenCode 優先、其他 ID 字典序、OMP 最後；policy 與 Registry.Default、prompt、health 無耦合，priority 測試使用固定字面值期望。 |
| RTSEL-004 | PASS；Registry.Snapshot 在同一讀鎖取得集合並排序，capture 在鎖外；反向註冊順序測試結果相同。 |
| RTSEL-005 | PASS；explicit 只查指定 backend，能力不足直接拒絕；真 daemon 測試斷言 A／B Run 均零，無 fallback。 |
| RTSEL-006 | PASS；無 eligible 時提供 inspected candidates 與缺少能力，拒絕持久化與 Run；`TestRuntimeSelectionNoEligible` 及 admission 紅燈。 |
| RTSEL-007 | PASS；先以 Three-Face requirements union admission，再將 selected backend 封存／綁定；真三 stage 測試斷言三次同 ID。 |
| RTSEL-008 | PASS；OMP／Antigravity 既有 capabilities 未宣告 read-only；共同 matcher 排除，backend／daemon union 測試及四 adapter 既有測試保護。 |
| RTSEL-009 | PASS；ExecutionContract additive decision 與 Task 同次 INSERT 保存；Validate 核對要求、候選唯一性、match、priority、policy 與 selected；restart／queue drift／legacy audit 測試保護，安全 guard 不重選。 |
| RTSEL-010 | PASS；fingerprint 使用 intent，不含 selected 或版本；先查已保存請求，正常重送零 Version／Run；真 DB concurrent winner 僅一 Task／一次執行，auto→explicit 重用 ID 拒絕。 |
| RTSEL-011 | PASS；CLI、IPC 支援 auto，Web 新增已知 backend 白名單並保留注入拒絕；local 真 CLI／DB／adapter fixture、Web forwarding／integration 與取消兩項新增紅綠保障。 |
| RTSEL-012 | PASS；上述完整 vet／race／build 全綠，所有模組含既有 Memory／Recall／UFL／Lifecycle／Cron／Git memory／Web／取消測試均包含於完整 suite。 |
| RTSEL-013 | PASS；任一候選 CaptureRuntime Version 錯誤、空值或 timeout 直接拒絕，不移除候選再選；新 version failure 與既有 CaptureRuntime 錯誤測試保護；空值與 timeout 為既有 CaptureRuntime guard 的程式查核，未宣稱新增各自的 auto 整合測試。 |
| RTSEL-014 | PASS；候選 33 個、JSON 超過 64 KiB、未知 capability 均拒絕；純 admission／matcher 測試用真值與 errors.Is，沒有截斷後繼續路徑。 |
| RTSEL-015 | PASS；env parser 拒絕空項／重複／未知 ID，daemon 啟動與 local auto 使用同 parser；未列者補齊、自訂 OMP 第一及未註冊偏好測試，能力 matcher 仍先於排序。 |

### 重要失敗面

| 輸入／狀態 | 預期與覆蓋結論 |
|---|---|
| explicit 不相容、其他 backend 相容 | 拒絕且零 fallback；真 IPC／DB／Run 次數 PASS。 |
| auto 無候選或全部不相容 | fail-closed、零執行，不建立 Task；resolver 空集合邏輯與無 eligible 整合 PASS。 |
| 某候選 version 錯誤／空值／逾時、unknown capability、超限 | 完整 snapshot 失敗，不能減少候選後選擇；Version 錯誤及 selection bounds 測試 PASS；空值／timeout 另查核既有 CaptureRuntime 明確拒絕路徑。 |
| Three-Face 選擇時或排隊期間缺 read-only | admission 或執行 guard 拒絕，不能換 OMP／降低 requirements；真 stage／queue drift PASS。 |
| sealed decision 的 policy／match／selected／candidate 不一致 | Validate 由封存 descriptor 及封存 priority 重建，deep equality 拒絕不一致；unknown policy 測試 PASS，其餘欄位另查核重建與 deep equality 拒絕路徑，未宣稱逐欄位都有新 tamper 測試。 |
| registry 更新、daemon 重啟、相同 ID 正常重送 | 回傳原封存 Task，無重新枚舉／版本探測／執行；真 IPC／SQLite reopen PASS。 |
| 同 ID 不同 intent、並行首次提交 | intent fingerprint 拒絕碰撞，唯一 INSERT winner 派送一次；真 concurrent race PASS。 |
| local auto 已持鎖 callback、版本探測時成功取消 | 共用啟動鎖且 Start 前拒絕；兩项已發現缺陷紅燈與最終綠燈 PASS。 |
| Web 未知 backend、resolved_context／mode 注入 | HTTP boundary 拒絕；新契約僅放行已知 backend，既有 Web integration 保障仍存在，PASS。 |
| 歷史資料缺 decision、未知 decision policy | 顯示 legacy／unsupported unavailable，不重新封存；status、Node audit 及既有 legacy reads PASS。 |

### 架構、安全與品質

registry 僅管理集合，policy 保持純資料選擇，adapter 不承擔 routing；Task.Backend 保存 selected，而原 intent 留在契約與 fingerprint。沒有新增 schema、fallback、模型品質或可用性排序。local registry binary 存在檢查是 composition root 建立集合的既有型態延伸，所有已註冊候選仍完整捕獲版本；daemon 與 local 的集合差異已公開文件化。

workspace authorization、memory/context admission、CreateTask 原子保存、queue ownership 與 execution guard 邊界保留。Web audit 使用 textContent，新增候選文字不形成 HTML 注入。兩 adapter additive startup hooks 不設定時保持既有行為；重複的少量 hook release 邏輯屬各自 process 邊界，沒有已分歧的 routing 邏輯或需要擴大抽象的問題。整份 diff 未見跨 Task 正確性分歧或過度設計。

### 證據誠實、豁免與限制

沒有 gate 豁免或未核准 Scenario。初次完整 race 的 Web 失敗保存在 [full-race-first.log](evidence/full-race-first.log)，修改舊測試是因 RTSEL-011 已核准 additive backend；resolved_context、mode 與未知 backend 拒絕未弱化。

Task 的共同 admission／CLI 入口先有目標紅燈；純 matcher／policy 同層使用固定字面值與真 admission，沒有以同一實作重算期望值。priority／bounds／部分封存斷言未逐分支保存事前獨立紅燈，Web HTTP boundary 的 baseline 紅燈是事後重現，已在 [implementation-plan.md](implementation-plan.md) 揭露；本報告不宣稱所有 assertion 都經獨立事前 TDD。共同 admission 核心有事前紅綠，Web boundary 有獨立最終測試與 baseline 行為對照，能判斷本次行為及完整回歸；公開順序限制不列為假綠燈。兩項安全修復另有本次缺陷的事前紅燈。

fake binary、fake descriptor 僅取代外部模型／CLI 回應，真 matcher、registry、封存、SQLite、IPC、取消與 stage 調度均受測。沒有新增真模型或完整 OMP／Antigravity live sandbox 呼叫；既有選用 live gate 不等於本次實地模型驗收。Node audit 測試依既有環境可跳過 Node 不可用情形；最終提供的完整 suite 是該受測環境結果。

## 流程判定

**PASS**：固定本機 BASE..HEAD 的獨立完整審查完成，報告已持久化，兩個 MUST FIX 均修復，最終來源雜湊與完整回歸證據一致，沒有未解決 MUST FIX。

本 PASS 不表示 PR 已合併，也不代替遠端 CI。協調者新增只含本報告的直接後繼提交後，需依 review-evidence.md 的 report-only successor 規則與 verify-artifact.py 核對有效性，再推送並確認 PR source。
