# Issue 39 獨立審查報告

2026-10-04。

- 範圍：完整 PR [#40](https://github.com/raybird/Asura/pull/40)，固定 main merge-base 至 source HEAD 的全部 diff，含程式、測試、規格、常青文件與證據。
- Reviewed BASE SHA：625e62d6e07feb57ddf55daf458ff7e207301074
- Reviewed HEAD SHA：5bf635195a3616bc9316b8821924eeee6d03e18e
- Reviewed patch-id：14b5d2adce465819df1c20456fc8bd6205d66a55
- 獨立 reviewer：Codex 子代理 `/root/issue39_review`；未參與產品實作，僅新增並清理獨立負向測試 fixture，及撰寫本報告。
- Review artifact：`docs/issues/issue-39/review-5bf6351.md`
- 風險：High；probe 失敗污染 admission、敏感輸出持久化，及 sealed runtime 失效後發生錯誤派送／工作區副作用。

## 問題與風險

- MUST FIX：無未解決項目。
- 已修復 MUST FIX：原 `internal/backend/opencode/inventory.go:27` 與 `:41` 只以版本前綴驗證，成功 stdout `1.18.31 token=secret-sentinel` 會成為 available descriptor.version 並進入稽核資料。2026-10-04 reviewer 獨立負向測試 exit 1 確認此路徑；實作者另保存永久 `TestInventoryOpenCodeVersionSecret` 紅燈與修復。`internal/backend/inventory.go:119` 共用版本 token 驗證現已拒絕附帶空白、等號與控制字元，subprocess、generic descriptor capture 及 sealed validator 共用此界線；四內建 adapter 的既有版本格式相容，永久回歸全綠。
- SHOULD FIX：追蹤既有 `cmd/asura/runtime_selection_test.go:81` 取消測試的偶發失敗。修復後一輪全套在 PGID 已存在時得到 Task 已 failed，原因未證實；原斷言及取消流程均未變更，單獨 `-count=20 -race` 全綠、後續無重疊全套全綠。現有證據不足以判定產品回歸，本次不阻塞；重疊執行僅為可能因素，不能視為已確認原因。
- NICE TO HAVE：stage contract 版本辨識及 CLI composition root 登錄清單有局部重複，但未分歧；本次不要求擴大抽象或重構。

## 已查核維度

### 核准、驗收與測試真偽

核准來源為 2026-10-04 使用者「核准」，規格基線 `a88fe79`；README 現存 RTINV-001～015 與核准集合完全相等，RI-TBD-1～15／TBD-1～5 均有已解決紀錄。最終文件未新增未核准的產品行為。責任 Task 與驗收映射完整。

下表引用固定 HEAD 的 `docs/issues/issue-39/verification-evidence.md`，可用 `git show 5bf635195a3616bc9316b8821924eeee6d03e18e:docs/issues/issue-39/verification-evidence.md` 取回。測試 fixture 僅代替外部 runtime，不 mock inventory、planner、admission、持久化或 guard；期待值來自規格、固定狀態、獨立 sentinel、呼叫計數或原封存 byte equality，未發現假綠燈。

| 驗收 | 已查核證據與行為 | 結論 |
|---|---|---|
| RTINV-001 | PolicyStatesAndOrdering／OpenCodeProbe／PinnedFileProbe：available descriptor、無 Run callback、檔案 sentinel 不變 | PASS |
| RTINV-002 | 四 adapter probe／CLI：缺 binary、必要 dependency、pin 或版本不符為固定 unavailable reason，排除 auto 候選 | PASS |
| RTINV-003 | PolicyStatesAndOrdering／AdmissionFailureIsolation：一般 error 與 panic 隔離，健康候選仍可 admission | PASS |
| RTINV-004 | ContractSnapshot／daemon 真 IPC 與 SQLite：OMP version error／timeout 不阻擋 Codex，OMP Run=0 | PASS |
| RTINV-005 | ExplicitRecoveryAndCoherence／IPCRejectionAndCancellation：指定 unavailable 回傳安全 reason，無 Task／fallback | PASS |
| RTINV-006 | ExplicitRecoveryAndCoherence 與既有 unknown backend 測試：未登錄 ID 拒絕，未動態建 adapter | PASS |
| RTINV-007 | ExplicitRecoveryAndCoherence／StageBindingMixed／QueueRedelivery：每 admission 每 ID 一次 probe、所有 stage 共用 candidate snapshot，缺必要 stage 拒絕 | PASS |
| RTINV-008 | CancellationAndWholeBudget／UncooperativeObserverBound／daemon timeout：3 秒與 5 秒政策、unknown reasons、健康結果保留、observer 有界 | PASS |
| RTINV-009 | DeterministicCompletion／PolicyStatesAndOrdering：固定時戳下插入及完成順序不同，序列化相同；ID／能力 canonical 排序 | PASS |
| RTINV-010 | SealedRuntimeDisappears／StageBindingGuardBetweenStages／MixedExecution：封存 ID guard、0 非預期 Run／model、失敗 trace，不重選 | PASS |
| RTINV-011 | ExplicitRecoveryAndCoherence／QueueRedelivery：fresh recovery 只改新 admission，舊 snapshot／plan bytes 不變 | PASS |
| RTINV-012 | OpenCodeVersionSecret／BoundsAndTampering／ContractSnapshot／VersionProcessBounds：成功 stdout、error、panic／stderr 安全界線，大小與篡改資料拒絕 | PASS |
| RTINV-013 | CancellationAndWholeBudget／VersionProcessCancellation／IPCRejectionAndCancellation：context 取消、合作 probe 與程序 leader 回收、無 Task／Run 副作用 | PASS |
| RTINV-014 | CLI／HTTPBoundary／WebRender／inventory IPC 與既有 status audit：認證、fresh inventory、sealed task audit、legacy／未知 policy；status 不 probe | PASS |
| RTINV-015 | LegacyContracts／RestartRecovery／QueueRedelivery 與最終完整 race：Direct／Three-Face／explicit／auto、Memory／Recall／UFL／Lifecycle／Git／Web／Cron 等既有回歸 | PASS |

紅燈與修復記錄涵蓋 admission failure isolation、sealed runtime disappearance、Codex 版本政策、CLI／HTTP 入口及成功 stdout 洩漏。inventory API 同層單迴圈與其他整合層的區分合理；後補 bounds／ordering／cancel／legacy 只列額外回歸，未冒稱全部先紅。既有 selection error assertion 的修改直接對應核准的新失敗隔離政策；legacy fixture 保留原契約 assertions。未刪除、弱化或跳過原取消測試。

### 相關失敗面、架構與安全

- 註冊身分錯誤、重複／超長 ID、未知 enum、無效 version／capability／候選、超限 inventory／contract：capture／validator fail-closed；Registry snapshot 保留 authoritative map key，不以 adapter ID 改名。
- 單 ID error／panic、失敗 executable／dependency／pin、版本錯誤：只影響該 entry；auto 僅用 available descriptors，再走既有 capability filter／priority。explicit 僅 probe 指定 registered ID，不因其他 ID 健康而 fallback。
- Probe timeout／whole budget／上游取消：bounded worker／buffered result 保護 observer；全域未完成 entries 記 unknown，上游取消拒絕 admission。內建 subprocess 使用進程組取消、有界 wait 與 bounded stdout，stderr 丟棄；檔案 hash 每 chunk 檢查 context，不新增 workspace 寫入或 execution callbacks。
- 成功 version 附帶資料、error／panic sentinel、大量輸出：版本 token 驗證、固定 diagnostics、大小限制共同防止 raw diagnostics 持久化；修復後成功 stdout 負向測試有效。允許保存合法 version 是 descriptor 契約，非任意程序輸出稽核。
- Sealed runtime 註銷、版本 probe 失敗或能力退化：guard 只查封存 ID／handle，必要 stage 執行前拒絕並留下 trace；不重新全 registry 選擇、不使用 constructor 補位、不改寫原 snapshot。
- Contract candidate 集合／projection／stage order／requirements 不一致：新 validator 以 sealed inventory 與原 planner 核對，不讀當前 Registry；legacy capability-v1／stage-binding-v1 各沿原 validator，未 backfill。
- HTTP 未認證／跨 origin、future policy／legacy task：server 認證與 origin gate、snapshot.Validate 及前端 textContent 稽核；task status 只讀持久化值。新增 GET 不修改 Task 或 runtime 設定。
- 架構：無 schema migration、背景 scheduler、TTL cache、retry 或模型 prompt probe；局部責任清楚。完整 diff 跨 Task 重複邏輯未形成正確性分歧；新增抽象限於 inventory/probe 與必要共用安全界線。

### 被測版本與最終證據有效性

2026-10-04 reviewer 自行重算固定 BASE..HEAD patch-id，與本報告一致。安全修復 source `6681b0c0db89f3769cf945069b5884cead12c0a3` 至 Reviewed HEAD 只增加 issue 文件與證據，無產品、測試、設定或依賴異動；reviewer 執行 `sha256sum -c docs/issues/issue-39/evidence/source-sha256.txt` exit 0，237 項全 OK。

- 實作者最終 `go test -count=1 -race ./...` exit 0，所有套件 PASS，integration 251.356 秒；持久原始輸出 `docs/issues/issue-39/evidence/race-final.log` 與 environment／source manifest 已查核。
- 實作者 `go vet ./...`、`go build -o /tmp/asura-issue39 ./cmd/asura` 均 exit 0；命令與版本記錄於 verification-evidence。
- Reviewer 獨立完整定向 race 命令 `go test -count=1 -race ./internal/backend/... ./internal/daemon ./internal/pipeline ./internal/engine ./internal/web ./cmd/asura -run 'Test(Inventory|StageBinding|Capability|RuntimeSelection|ExpandedBackendsRegistered)'` exit 0，11 套件 PASS；當時 source 為 ce45cc9，故只支持已審原實作。
- Reviewer 修復覆核另執行 `go test -count=1 -race ./internal/backend/... -run '^TestInventory'` exit 0，6 套件 PASS；最終 source manifest 與該修復一致。最終全套實際使用修復後 source，未沿用 ce45cc9 綠燈取代修復驗收。

### 豁免、待確認與限制

無 gate 豁免或尚待核准的驗收。2026-10-04 PR CI Test & Build 仍在執行，本報告不聲稱遠端 CI 已通過。

離線 fixture 未證明四個 provider 的遠端 auth／quota 或真實模型成功，符合核准 available 本機證據邊界；OMP／Antigravity 固定 pin 成功案例的離線限制已揭露。任意不合作的第三方 in-process probe 不能被 Go 強制終止；observer 有界不等同第三方資源回收，此限制符合核准提案，內建合作 probe 另有回收證據。一次取消測試失敗、後續 count=20／完整無重疊全綠與原因未確認均已保留，未偽裝為所有歷次執行皆綠。未使用 production 觀測判準或尚未到期窗口。

## 流程判定

**PASS**。完整固定範圍獨立審查、修復覆核與必要驗收證據已完成，無未解決 MUST FIX；implementation-plan 的 Task 5 實作、證據與獨立審查均已完成，本報告為其結案 artifact。README 的等待審查／合併狀態依本 artifact 確認審查完成，不代表已合併。報告綁定 Reviewed HEAD，不自行冒用尚未存在的報告提交 SHA；後續只新增本報告的單一提交，仍須依 review-evidence.md 的 artifact 有效性規則另行驗證。
