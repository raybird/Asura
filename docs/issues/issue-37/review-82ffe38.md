# Issue 37 獨立審查報告

- 審查日期：2026-10-04。
- 範圍：完整 PR #38；TARGET=`main`，涵蓋 admission、execution、persistence、audit、測試與文件。
- Reviewed BASE SHA：`d3a40e6ead080d7ab74accf02aadba192aedb48e`
- Reviewed HEAD SHA：`82ffe38fca1ce63e5b0d183d7bab06fc41b4a5c3`
- Reviewed patch-id：`1df7b2593d42201345b5f688e2660b30033681b1`
- 獨立 reviewer：Codex subagent `/root/review_issue37`；未參與 production 實作，只讀查核與執行驗證，本次只新增此報告。
- Review artifact：`docs/issues/issue-37/review-82ffe38.md`
- 風險：High，主要查核不同階段權限與 runtime 身分是否可能繞過封存計畫。

## 問題與風險

- MUST FIX：無。
- SHOULD FIX：無。
- NICE TO HAVE：無需在本次範圍追加修改。

## 固定範圍、核准與證據有效性

2026-10-04 已讀完整異動清單、production diff、新增測試、核准規格、SB-TBD-1～15、實作計畫與證據。核准來源為使用者 2026-10-04「核准」，README 的 STGBIND-001～016 全部已核准。比對 `d27ace3` 與最終 HEAD 的 README：驗收條件、動作、結果與必要檢查未變；僅補核准 SHA、進度與 PR／review 入口。沒有新增未核准行為或 gate 豁免。

最初程式審查版本為 `a0571af6e1a99aff81c4763a5087f61bce2aaebd`。至最終 HEAD 僅 README／implementation-plan 的交付連結及審查入口變動，已逐項覆核；沒有 production、測試、設定或證據變更，因此可重用同組測試。`git diff BASE HEAD | git patch-id --stable` 與上述 patch-id 相同，`git diff --check BASE HEAD` 成功。

2026-10-04 reviewer 執行 `sha256sum -c docs/issues/issue-37/evidence/tested-source.sha256`，exit 0，全部 215 項匹配；另以 Git 讀回被審查提交的每份檔案計算 SHA-256，確認 manifest 集合與 `git ls-files '*.go' go.mod go.sum` 完全相等，且全部 hash 相符。完整 vet／race／build log 均為 exit 0，固定 source 未變；完整 integration race 的 268.688 秒輸出沿用已保存證據，未冒稱 reviewer 重跑全套。

reviewer 於 2026-10-04 獨立執行：

```text
go test ./internal/backend ./internal/pipeline ./internal/daemon ./internal/engine ./cmd/asura ./internal/web -run 'TestStageBinding|TestCapabilitySealedGuard|TestCapabilityThreeFaceBoundRuntime' -count=1 -race
Exit code: 0
ok asura/internal/backend 1.049s
ok asura/internal/pipeline 1.034s
ok asura/internal/daemon 1.605s
ok asura/internal/engine 1.118s
ok asura/cmd/asura 1.130s
ok asura/internal/web 1.029s
```

## 驗收覆蓋

固定規格來源：`82ffe38:docs/issues/issue-37/README.md`；測試名稱與紅綠 log 的完整映射見 [evidence/README.md](evidence/README.md)。以下每项皆經測試斷言及實作查核。

| 驗收 | 觀察與證據 | 結論 |
|---|---|---|
| STGBIND-001 | Direct 單 Striker；既有 shell/local 路徑；legacy fixture 與原 handle freezing 刻畫仍存在；focused／final-race | 通過 |
| STGBIND-002 | 字面 codex/omp/codex 決策、實際 Run 與 trace；真 daemon 三階段分配；admission／execution-red → focused | 通過 |
| STGBIND-003 | OMP 的唯讀階段不 eligible，Striker 可 eligible；DeterminismAndValidation | 通過 |
| STGBIND-004 | 真 IPC／SQLite 拒絕且不存在 Task、Run=0、sentinel 不變；AtomicReject | 通過 |
| STGBIND-005 | explicit 只捕獲指定者、全部 stage 同 ID；省略 opencode 與既有 explicit 回歸 | 通過 |
| STGBIND-006 | explicit OMP 不足唯讀能力即拒絕；不捕獲替代者、不 fallback；AtomicReject／完整回歸 | 通過 |
| STGBIND-007 | 固定 descriptors／captured_at 的完整 JSON 相等與字面 selected IDs | 通過 |
| STGBIND-008 | descriptors 反向順序計畫相同；Registry.Snapshot 既有排序保障 | 通過 |
| STGBIND-009 | 改 priority／新增候選後實際 Run 與 sealed bytes 保持；MixedExecution／Queue | 通過 |
| STGBIND-010 | 初始所有 binding guard；Striker 消失使全部 Run=0，trace 留失敗 stage；MixedExecution／Queue | 通過 |
| STGBIND-011 | 初始與 Oracle 後能力退化拒絕；版本 v2 可執行且快照不改；GuardBetweenStages／MixedExecution | 通過 |
| STGBIND-012 | 相同 intent 回原 Task／plan且 Version 計数不增加；改 intent 拒絕；DaemonMixed／Queue | 通過 |
| STGBIND-013 | SQLite close/reopen及 daemon recovery；pending/queued/running 標 failed，context bytes 相同，不 Run／capture | 通過 |
| STGBIND-014 | 真 admission JSON 的 CLI／JavaScript audit，字面 sealed/actual versions；pipeline guards／trace；audit-red → focused | 通過 |
| STGBIND-015 | 單欄 mutation、plan/version/order/projection、32 candidates／64 KiB decision／192 KiB plan 上限，constructor fallback 拒絕；projection-red → focused | 通過 |
| STGBIND-016 | final-vet、final-race、final-build 均 exit 0；manifest 對應相同 final source | 通過 |

## 已查核維度與重要失敗面

- **需求與核准**：SB-TBD 的共同 snapshot、explicit 單一 ID、Striker 相容投影、legacy 分流、restart failed及非密碼學一致性邊界均與核准內容一致，沒有新增 routing/health/retry/override。
- **權限與唯一來源**：planner 重用 authoritative RequirementsForMode／matcher；Validate 比對精確 stage/order、requirements、共用 candidates/priority、reconstructed decision 與 Striker 投影。engine 查核 persisted Task 投影；orchestrator 與 ExecutePrepared 檢查新契約與正確 BoundBackend，缺 plan／錯 ID 不走 constructor/runner。
- **admission 失敗**：auto 無唯讀候選、explicit write-only、unknown capability、候選 Version failure或超限均 fail-closed。真 IPC 測試驗 Task/Run/sentinel；沒有任何模型呼叫入口位於 admission 拒絕前。explicit 能力先拒絕，auto 一次 registry snapshot/capture 後才做所有階段選擇。
- **execution 失敗**：先檢查所有 sealed runtimes才允許第一個 Run；每 stage 重取 sealed ID 並 guard，stage ExecutePrepared 再檢查 handle identity/capability。不依目前 priority 重選；初始缺失使零 Run，Oracle 後退化只阻止後續 stage，符合不可回滾已完成工作之邊界。
- **版本及契約異常**：unknown contract/policy、missing plan、legacy 夾帶 plan、單欄修改、非 canonical requirements、投影分歧均拒絕；版本更新不要求歷史 equality。Version 查詢錯誤不偽填歷史 actual version，沿核准的能力 guard語意。完整自洽重寫無密碼學保證，已揭露且不宣稱可偵測。
- **queue／redelivery／restart／cancel**：沿已存在 request 的授權/fingerprint lookup 先回 Task；worker／queue 取原 context且保留 auto intent，沒有二次 admission。重啟只 recovery failed並保留 JSON，沒有新增 resume。取消與 workspace queue 原流程未改，完整 race回歸涵蓋。
- **audit 與輸入安全**：CLI Validate 後只讀保存 contract/trace；Web 只讀 JSON 並使用 textContent，沒有 Version/Run或 HTML 注入入口。Web 的結構展示檢查不冒充密碼學或完整 backend Validate；execution仍有完整驗證。缺失/未知資料顯示 unavailable，沒有用目前 registry 回填歷史。
- **架構與品質**：未改 SQL schema、adapter、memory、cron政策；stage routing 留在 orchestrator，backend planner 為純值。完整 decision 在最多三個 stage 重複封存屬已核准取捨且有總量限制。三個既有 stage入口的角色 guard保持局部相同形式，沒有分歧或新增通用 workflow抽象；跨 Task未見需修正的重複/過度設計。
- **測試真偽與持久力**：讀過 admission、execution、audit、projection紅燈；失敗原因來自目標行為而非編譯或環境。literal IDs、版本、Run計數、sentinel與 SQLite bytes斷言可與實作不同，不是同義反覆；沒有 mock matcher/planner/SQLite。既有 union斷言改為核准 Striker投影，legacy測試另固定原契約，未弱化必要失敗面。紅綠分層及同層合併理由已記錄，未發現假綠燈或阻塞性證據缺口。
- **常青文件**：根 README及架構文件反映新契約、mixed例子、上限、queue/restart與限制；最終 issue文件只更新交付狀態，不預填審查 PASS或 merge。

## 豁免、待確認與限制

2026-10-04 無 gate 豁免或未解決的核准項目。本次未呼叫付費外部模型或重跑 opt-in live tests；fixtures配合真 matcher/planner/pipeline/IPC/SQLite及既有 adapter sandbox回歸支持本範圍，不宣稱外部帳號、模型或所有 binary現場實測。Web證據為真 JavaScript renderer執行，不宣稱完整瀏覽器視覺測試。registry查核與 Run之間不跨 Run持锁，沒有承諾原子處理任意並行 registry變更或回滾已完成stage；符合核准方案。

## 流程判定

**PASS**。2026-10-04 完整固定範圍獨立審查完成、全部 16 項驗收有有效證據，無 MUST FIX；報告已保存。此結論綁定上述 Reviewed HEAD；主協調者若新增唯一的報告提交，須依 `docs/agents/review-evidence.md` 與 `verify-artifact.py` 驗證提交例外後才能沿用本 PASS。本報告不代表 PR 已合併。
