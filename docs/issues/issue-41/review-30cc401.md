# Issue 41 獨立審查報告

- 日期：2026-10-04
- 範圍：完整 PR，包含規劃、核准、實作、測試、證據與常青文件；PR [#42](https://github.com/raybird/Asura/pull/42)
- TARGET：origin/main
- Reviewed BASE SHA：2f583c7f257fa9fd3b974ff94f5bbd82c787e4b5
- Reviewed HEAD SHA：30cc401ff0040cb8ae0f8824f0d114d93ba55b12
- Reviewed patch-id：a86264cd4dae4943b169b36bdb71b6b58d7c45af
- 獨立 reviewer：Codex 隔離 reviewer `/root/review_issue41`；未參與實作，只建立本報告，實驗限 `/tmp` overlay／測試 DB。
- Review artifact：docs/issues/issue-41/review-30cc401.md
- 風險：High；新增跨模組持久化、取消／重啟生命週期與安全投影。

## 問題與風險

### MUST FIX — MF-1：合法 capability-v1 auto 歷史契約無法查詢

位置：`internal/audit/types.go:234–239`（無 StageBindingPlan 分支），`internal/storage/sqlite_execution_audit.go:126`（legacy 呼叫 NewHeader）。

無 StageBindingPlan 時，NewHeader 把所有 binding 固定投影成 `policy=explicit`、只保留 selected runtime。歷史 capability-v1 支援 `requested_backend=auto` 與 top-level SelectionDecision；該契約本身 Validate 成功，但 Header.Validate 要求 auto 使用原選擇政策，因此 query 回 `audit_invalid`。既有三版 legacy 測試只使用 explicit codex，未覆蓋 Phase 4B 的 auto 形狀。這使 EXAUD-013 未完整成立，亦遺失歷史可驗證 decision chain。

2026-10-04 獨立重現（Go overlay 在 `/tmp`，不改 repo）：沿用 storage 的 auditRuntime fixture，呼叫 `backend.AdmitRuntime(ctx, reg, "direct", "auto")`；移除 InventorySnapshot、StageBindingPlan，設 ContractVersion=CapabilityContractVersion；保留原 SelectionDecision，`c.Validate("direct", "codex")` 成功。以舊 row 直接 INSERT（audit marker 空字串、沒有新 header），再 GetTaskAudit。

實際命令：`go test -overlay=/tmp/review41-overlay.json -count=1 ./internal/storage -run TestReview41LegacyAuto -v`，exit 1：

```text
=== RUN   TestReview41LegacyAuto
    review41_probe_test.go:9: valid capability-v1 auto task rejected: audit_invalid
--- FAIL: TestReview41LegacyAuto (0.01s)
FAIL asura/internal/storage
```

修正方向：以契約中的歷史 SelectionDecision 正規化 candidates、priority、policy 與 selected；只有真正無 selection 的 explicit 舊契約使用 explicit 投影。補合法 capability-v1 auto old-row 測試，確認無 events/backfill/probe。

### MUST FIX — MF-2：無契約 legacy 值未驗證，自由文字穿透安全投影

位置：`internal/storage/sqlite_execution_audit.go:145–152`、`internal/audit/fold.go:210–222`。

沒有 contract 的 legacy row 直接把 Task.execution_mode 與 status 放進 Record。legacy Validate 在 ContractDigest 空字串時不驗 schema、mode、state、identity 的合法性，也沒有針對這些來源欄位的 allowlist；因此非法 mode（包括換行與任意敏感文字）仍是成功 query，會透過 IPC／HTTP／CLI／Web 輸出。這違反 EA-TBD-15 的非法 legacy 值回 bounded unavailable，以及 EXAUD-014 的新 query 安全投影；它不是正常 Task.prompt/result 診斷的既有保存例外。

2026-10-04 獨立重現：以已建置的 `/tmp/asura-issue41` 建立 `/tmp/asura-review41-legacy.db`；用 `/usr/bin/python3` 的 sqlite3 插入沒有 contract/header 的舊 row：

```sql
INSERT INTO tasks(id,prompt,workspace,status,execution_mode,created_at,updated_at)
VALUES('legacy','private','/tmp','completed','secret-sentinel' || char(10) || 'forged history',
       '2026-10-04T00:00:00Z','2026-10-04T00:00:00Z');
```

`/tmp/asura-issue41 audit --db /tmp/asura-review41-legacy.db --id legacy --json` exit 0，實際 JSON 包含：

```json
{"header":{"schema":"execution-audit-v1","task_id":"legacy","mode":"secret-sentinel\nforged history"},"task_state":"completed","completeness":"legacy","integrity":"unavailable"}
```

上例僅節錄回應欄位；實際回應還含 created_at、空 admission、空 events/stages。`Record.Validate()` 已由 CLI 成功呼叫，未擋下此非法值。修正方向：讓所有 legacy 路徑使用共同的 safe identity、mode/state enum 與 bounded header 驗證，非法值返回固定 unavailable/invalid；不要將自由文字截斷後當成有效 mode。補無契約 legacy 的非法 mode/state/identity 邊界。

### MUST FIX — MF-3：CLI submission 丟棄新拒絕摘要及 correlation digest

位置：`internal/daemon/daemon.go:1506–1510`、`cmd/asura/ask.go:122`、`cmd/asura/run.go:431–439`。

daemon submit IPC 已包含 AdmissionRejection，但 Client.Submit 把它丟棄，只建立 `errors.New(resp.Error)`；ask 因而無法顯示新的安全摘要與 request correlation digest。local run 的 admission error 同樣只印原 error，沒有新 projection。EA-TBD-7 已核准「CLI/Web submission 能顯示即時拒絕」，EXAUD-005 要求即時 bounded rejection 摘要與 correlation digest。Web 會顯示 HTTP JSON，IPC 已涵蓋；CLI 面仍缺此行為，audit 查詢也不能補救，因 rejection 正確地不建立 Task。

可重現路徑：向 daemon 用 `asura ask --backend codex` 提交一個 explicit unavailable 的請求；IPC response 有 AdmissionRejection.RequestDigest，但 CLI 只收到 Error 字串，無 digest。local `asura run --backend codex` 在同樣不可用環境只印 admission error。這是靜態完整呼叫鏈可確認的缺口；本次未呼叫真 provider，不把該手動路徑記為已實際執行。

修正方向：保留 validated typed rejection 供 CLI 印 bounded 摘要；local admission 也建立相同安全 projection。追加 CLI rejected submission assertions，要求 digest／safe enum 出現、秘密與 Task／Run 不出現。

### SHOULD FIX / NICE TO HAVE

無額外阻塞外的程式改善建議。event 與跨 surface 驗證重複各有 producer／trust boundary 用途，未發現已分歧的跨 Task 重複邏輯；orchestrator 各 stage 接線沿既有結構，不建議為此增加泛用 framework。

## 驗收與證據

規格固定來源：`6adb11c:docs/issues/issue-41/README.md`；核准紀錄 `a97fe1b`。2026-10-04 比對核准基線至 Reviewed HEAD，Gherkin 內容未改，核准表 EXAUD-001～019 全部已核准，TBD-1～4 已有使用者「核准」來源；無新行為豁免。下表證據路徑以 `docs/issues/issue-41/evidence/` 為前綴。

| 驗收 | 獨立查核結論 | 證據與查核位置 |
|---|---|---|
| EXAUD-001 | 通過既有覆蓋：Direct 全鏈、Task terminal 獨立 | execution-green.log；TestExecutionAuditCompleted/direct |
| EXAUD-002 | 通過既有覆蓋：固定 mixed bindings/stage 順序與 renderer | audit-matrix-green.log、query-final-race.log；Completed/three-face、WebRender |
| EXAUD-003 | 通過：OMP exclusion 與已封存 priority/policy 可離線解釋 | NormalizationAndTamper；query-final-race.log |
| EXAUD-004 | 通過：availability 與 missing capability 分欄，refs 重新驗證 | NormalizationAndTamper；query-final-race.log |
| EXAUD-005 | 部分通過；IPC/Web bounded rejection、零 Task/Run 有證據；CLI 缺口 MF-3 | IPC；audit-matrix-green.log；Client.Submit 與 CLI 路徑 |
| EXAUD-006 | 通過 missing guard／零 Run；unavailable 分支靜態採相同 fail-closed 路徑 | Failures/missing；execution-green.log；guardStage |
| EXAUD-007 | 通過：sealed 未改寫、mismatch digest 觀測、零 Run | IdentityMismatch；integration-final.log |
| EXAUD-008 | 通過：typed runtime layer，error/output 不進 audit | Failures/runtime；execution-green.log |
| EXAUD-009 | 通過：三 phase barrier、單一取消 terminal，CAS 保留既有完成 | Cancellation；audit-matrix-green.log、lifecycle-baseline.log |
| EXAUD-010 | 通過：reopen 保留 Oracle，Striker interrupted，Critic not reached | TerminalAndRecovery；query-final-race.log |
| EXAUD-011 | 通過：accepted redelivery 不 probe/select/Run；append 冪等與不同 payload conflict | IPC、TerminalAndRecovery；audit-matrix-green.log |
| EXAUD-012 | 通過正常 query 覆蓋與授權：Store／IPC／CLI／HTTP 無 execution；legacy 非法值另見 MF-2 | Completed、IPC、CLI、HTTPBoundary、WebRender；query-final-race.log |
| EXAUD-013 | 未完整通過：三版 explicit fixture 通過，capability-v1 auto 重現失敗 MF-1 | legacy-contracts.log；本報告獨立 overlay probe |
| EXAUD-014 | 新 header/events 與 response bounds、正常秘密排除通過；legacy allowlist 缺口 MF-2 | RejectForeignFields、EventOrderAndBounds、HTTPResponseBound；query-final-race.log；本報告 CLI probe |
| EXAUD-015 | 正常新 audit 篡改拒絕通過：seq/key/order/header/contract/marker | NormalizationAndTamper、EventOrderAndBounds、MissingHeaderIsNotLegacy；query-final-race.log |
| EXAUD-016 | 現有全 repo vet/race/build 證據有效；通過不消除新增 probe 發現 | full-race-final.log exit_code=0（含 tests/integration）；full-vet.log、full-build.log 與 verification.md 命令/exit 0 |
| EXAUD-017 | 通過現有真 SQLite 故障注入：admission/recall/header、observation、terminal、recovery | AdmissionAtomic、ObservationFailureStopsExecution、TerminalRollback、RecoveryStartupFailure；observation-fault.log、integration-final.log、query-final-race.log |
| EXAUD-018 | 通過：Run 返回、stage parsing、critic verdict 與空輸出 Task 分層 | Failures、TaskTerminalLayers；execution-green.log、terminal-layers-green.log |
| EXAUD-019 | 通過：intent/returned/actual 分開、crash incomplete、model unknown | Completed、TerminalAndRecovery、WebRender；execution-green.log、query-final-race.log |

## 重要失敗面

| 輸入／狀態 | 預期與覆蓋結論 |
|---|---|
| audit header INSERT 故障且有 recall ledger | 同交易全 rollback；AdmissionAtomic 真 trigger 與對照 row 通過，Run 不啟動 |
| run_intent／Oracle outcome append 故障 | 零首次 Run／不進 Striker；ObservationFailureStopsExecution 通過 |
| terminal INSERT 故障 | CAS update rollback，pending/running durable state 不假終態；TerminalRollback 通過 |
| recovery INSERT 故障 | 整批 rollback，daemon listener/lock 釋放且不派送；RecoveryStartupFailure 通過 |
| cancel 與 completion 競態、已有 completed stages | 既有 CAS winner 終態權；取消 barrier 與既有 lifecycle regression 通過；stage_outcome 已完成不回寫 |
| Run 前／Run 返回前 crash | intent 非確定呼叫；returned 才設 actual，recovery partial；TerminalAndRecovery 通過 |
| runtime missing/unavailable/identity drift/capabilities changed | sealed ID guard，無 fallback，append 成功才 Run；missing/mismatch fixtures 通過，availability/capability 分支靜態核對 |
| huge runtime error/output、非法 descriptor/version/任意 fields | 正常新 model 與 producer 不讀 Error/output，自由 Scope、超界 version/DB payload 拒絕；legacy 例外 MF-2 |
| seq/event key/order/header refs/contract digest/schema 竄改 | bounded decode、DisallowUnknownFields、chain/Fold/source contract 比對拒絕；已核准完整合法重算／prefix truncation 不可否認性限制保留 |
| repeated append 與 accepted RequestID 重送 | 相同語意沿用時間/digest；不同 payload conflict；不重新 preflight/Run，現有 IPC/Store 覆蓋通過 |
| unauthenticated HTTP、foreign Origin、outside workspace IPC | session/origin 與 daemon workspace policy 在取得 audit 前拒絕；HTTPBoundary／IPC 覆蓋通過；CLI local 依 OS DB 權限，daemon 已回錯不 fallback |
| IPC response 超界 | LimitedReader 與 HTTP 固定錯誤回應；HTTPResponseBound 通過，不輸出 sentinel |
| 三版 legacy contract 與無 contract row | 不造 events/backfill，explicit 三版通過；auto 與無契約非法欄位分別 MF-1／MF-2 |
| stage returned success 但 parse/critic/Task validation 失敗 | RunOutcome、stage outcome 與 terminal layer 各有證據；TaskTerminalLayers 通過 |
| rejected admission 查詢／CLI 展示 | 不建 rejected Task 正確；IPC/Web 即時摘要存在，CLI MF-3 |

## 架構、品質與證據有效性

2026-10-04 核對 root AGENTS.md、docs/AGENTS.md 及 project/acceptance/verification/review-evidence；流程契約 2.0 相容。audit 依 backend 安全值型別，storage 不依 pipeline/engine，typed observer 在 ExecutionContext 的非 JSON 欄位；ExecutionContract 與原 Task CAS 持續是 authority。未加入 selection/retry/fallback/memory 新策略。README 與架構文件已更新，但需修正上述未涵蓋行為後再交付。

2026-10-04 `git diff BASE HEAD | git patch-id --stable` 與指定 patch-id 一致；`sha256sum -c docs/issues/issue-41/evidence/tested-source.sha256` exit 0，253 個 Go/module 檔案一致。完整 race log 含全部套件與 tests/integration、exit_code=0；vet/build 空 log 的成功狀態由 verification.md 的實際命令與 exit 0 明列。重用此 evidence，未重跑整組昂貴 regression；獨立新增 probe 的紅燈不改寫既有綠燈。

已讀全部 evidence logs，admission/model/execution/recovery/CLI/HTTP/renderer 紅燈均對應產品缺行為，與共用範圍揭露一致。core-first、integration-first、recovery-green、terminal-layers 的失敗均保存且非當作綠燈；`fail` fixture 誤用修成 `failed` 是有契約依據的 fixture 修正，非產品紅燈。現有 assertion 以字面 stage/runtime/enum/count、真 DB rollback 與不同來源結果判定，未發現 tautological assertion 或弱化既有測試。

Node 執行實際頁面的 renderer 搭配真 SQLite/IPC/session HTTP 是已揭露的等價 renderer 證據；未宣稱完整瀏覽器操作。既有 opt-in 大容量與真 provider probes skip 不屬本次 audit 必要驗收，未視為假綠燈。provider model calls unknown、hash 非不可否認性、原 trace/result 自由文字維持既有保存邊界，均有已核准限制。

本報告只綁固定 Reviewed HEAD；修復提交改變產品或測試後必須重新審查，不能沿用本報告當作 PASS。完整 probe 用 `/tmp` Go overlay，snap Go 以 require_escalated 執行；沒有修改 repo 的測試或產品，也沒有 commit/push。

## 流程判定

**RETURN TO execute-task**。MF-1、MF-2、MF-3 涉必要驗收及安全／legacy／CLI 行為缺口。先修復並保存相應紅綠證據，再以新的固定 BASE／HEAD／patch-id 做獨立覆審。
