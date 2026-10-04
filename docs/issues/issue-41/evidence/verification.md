# Issue 41 驗證證據

2026-10-04：核准規格基線 `6adb11c:docs/issues/issue-41/README.md`；核准記錄 `a97fe1b`。本檔與 logs 均屬開發工作區的實際證據；最終被測 Go／module bytes 由 [tested-source.sha256](tested-source.sha256) 固定，交付 commit 必須保持該內容。環境見 [environment.log](environment.log)。Go 1.27.1、Node v22.20.0、Linux amd64；fixtures 不呼叫真模型。

## 紅燈與最小綠燈

紅燈是在功能尚未接線的工作區執行，不是 commit 完成後回頭造出的失敗；log 保留原始測試行號，因此不等於最終檔案行號；cli-red.log 只移除空白行行尾空格，未更改結果或診斷。

| 實際命令 | 結果與原因 | 原始輸出 |
|---|---|---|
| `go test -count=1 ./internal/storage -run TestExecutionAuditAdmissionAtomic -v` | exit 1，既有 CreateTask 成功後不存在 admission audit table；產品行為紅燈，不是 compile error | [admission-red.log](admission-red.log) |
| `go test -count=1 ./internal/audit -run TestAuditRejectForeignFields -v` | exit 1，terminal 接受非 allowlist Scope | [model-red.log](model-red.log) |
| `go test -count=1 ./internal/engine -run 'TestExecutionAuditCompleted\|TestExecutionAuditFailures' -v` | exit 1，真 pipeline/Task 已執行，但缺 stage/Run/terminal provenance | [execution-red.log](execution-red.log) |
| `go test -count=1 ./internal/storage -run 'TestExecutionAuditTerminalAndRecovery\|TestExecutionAuditTerminalRollback' -v` | exit 1，crash prefix 無 recovery terminal；同次 terminal rollback 已通過，不能算它的紅燈 | [recovery-red.log](recovery-red.log) |
| `go test -count=1 ./cmd/asura -run TestExecutionAuditCLI -v` | exit 1，未知 audit command | [cli-red.log](cli-red.log) |
| `go test -count=1 ./internal/web -run TestExecutionAuditHTTPBoundary -v` | exit 1，authenticated GET audit 為 404 | [http-red.log](http-red.log) |
| `ASURA_AUDIT_BASELINE_FILE=/tmp/asura-issue41-before-page.go go test -count=1 ./internal/web -run TestExecutionAuditWebRender -v` | exit 1，`git show 6adb11c:internal/web/page.go` 的原頁面沒有 persisted audit renderer；真 SQLite fixture 及 Node assertion 保持相同 | [renderer-red.log](renderer-red.log) |

跨 Scenario 共用 red baseline 的範圍明列於下表：它證明整條新 provenance 路徑缺失，**不是聲稱每個後補 boundary 都曾個別 fail**。model allowlist 是獨立內層紅燈；SQLite rollback／reopen／CAS 本身就是整合層，不另外 mock 交易造一層 unit。guard/Run/outcome 真 pipeline + SQLite 斷言取固定 runtime ID、字面 enum 與計數，不以 fold 重算答案。CLI/HTTP 的內層重用 model/storage/pipeline 證據，外層保留各自 route/renderer 紅燈，未遺漏授權或 serialization 層。

最小實作後綠燈：admission-green、model-storage-green、execution-green、recovery-final-green、query-green、audit-matrix-green、integration-final logs。`core-first.log`、`integration-first.log` 與 `recovery-green.log` 保留真失敗，不當作通過：分別修正 corrupt Task projection 的失敗宣告、JSON empty slice 的 canonical 比較、未知歷史 contract、recovery+terminal 同交易的中間 fold。沒有改弱既有 regression assertions。

`terminal-layers.log` 的初次失敗源自新 fixture 使用 `fail` 而非既有 `failed` verdict；它不能當作產品紅燈。修正 fixture 後 [terminal-layers-green.log](terminal-layers-green.log) 顯示 critic verdict 與空輸出 Task validation 都通過。

## 驗收映射

| 編號 | 真實可觀察結果 | 測試 | 共用紅燈 | 綠燈／最終證據 |
|---|---|---|---|---|
| EXAUD-001 | Direct 全鏈與獨立 Task terminal | `TestExecutionAuditCompleted/direct` | execution-red.log | execution-green.log |
| EXAUD-002 | Codex/OpenCode/Codex 固定順序與 UI | `TestExecutionAuditCompleted/three-face、TestExecutionAuditWebRender` | execution-red.log、renderer-red.log | audit-matrix-green.log、query-final-race.log |
| EXAUD-003 | OMP availability 排除及 policy/priority | `TestExecutionAuditNormalizationAndTamper` | admission-red.log | query-final-race.log |
| EXAUD-004 | 可用但缺能力的候選 refs 與 missing enum | `TestExecutionAuditNormalizationAndTamper` | admission-red.log | query-final-race.log |
| EXAUD-005 | 立即有界 rejection，無 Task | `TestExecutionAuditIPC` | execution-red.log（新 observation/query 尚缺） | audit-matrix-green.log |
| EXAUD-006 | missing guard 零 Run、不 fallback | `TestExecutionAuditFailures/missing` | execution-red.log | execution-green.log |
| EXAUD-007 | 身分漂移零 Run、保留 sealed | `TestExecutionAuditIdentityMismatch` | execution-red.log（guard provenance 尚缺） | integration-final.log |
| EXAUD-008 | runtime failure 分層與敏感 error 排除 | `TestExecutionAuditFailures/runtime` | execution-red.log | execution-green.log |
| EXAUD-009 | 三個取消 barrier 與單一終態 | `TestExecutionAuditCancellation` | recovery-red.log（lifecycle provenance 尚缺） | audit-matrix-green.log |
| EXAUD-010 | reopen 中斷保留 Oracle、Striker interrupted | `TestExecutionAuditTerminalAndRecovery` | recovery-red.log | query-final-race.log |
| EXAUD-011 | 重送無 probe/Run，append 冪等／conflict | `TestExecutionAuditIPC、TestExecutionAuditTerminalAndRecovery` | recovery-red.log | audit-matrix-green.log、query-final-race.log |
| EXAUD-012 | Store/IPC/CLI/HTTP 唯讀與授權 | `TestExecutionAuditCompleted、IPC、CLI、HTTPBoundary、WebRender` | cli-red.log、http-red.log | query-green.log、query-final-race.log |
| EXAUD-013 | 三版歷史 contract 無 events/terminal/backfill | `TestExecutionAuditLegacyContracts、TestExecutionAuditCLI` | cli-red.log（新增 legacy query 入口不存在） | legacy-contracts.log、query-final-race.log |
| EXAUD-014 | allowlist、DB bytes、秘密與 response bounds | `TestAuditRejectForeignFields、TestExecutionAuditEventOrderAndBounds、Failures、HTTPResponseBound、CLI、WebRender` | model-red.log、admission-red.log、cli-red.log、http-red.log | model-storage-green.log、query-final-race.log |
| EXAUD-015 | seq/key/order/header/contract 篡改拒絕 | `TestExecutionAuditEventOrderAndBounds、NormalizationAndTamper、MissingHeaderIsNotLegacy` | model-red.log、admission-red.log | query-final-race.log |
| EXAUD-016 | 全 repo vet/race/build | `全專案既有與新增測試` | 不適用：回歸與文件保障不引入預期失敗 | full-race-final.log、full-vet.log、full-build.log |
| EXAUD-017 | admission/observation/terminal/recovery 原子性 | `AdmissionAtomic、ObservationFailureStopsExecution、TerminalRollback、RecoveryStartupFailure` | admission-red.log、recovery-red.log | observation-fault.log、integration-final.log、query-final-race.log |
| EXAUD-018 | runtime/processing/critic/task terminal 分層 | `TestExecutionAuditFailures、TestExecutionAuditTaskTerminalLayers` | execution-red.log | execution-green.log、terminal-layers-green.log |
| EXAUD-019 | intent/confirmed return 分開，model unknown | `TestExecutionAuditCompleted、TerminalAndRecovery、WebRender` | execution-red.log、recovery-red.log | execution-green.log、query-final-race.log |

## 精煉與最終驗證

2026-10-04：執行 code-simplify，檢查新增 model、Store tx、observer、IPC／CLI／HTTP 的跨模組與 JSON 契約。no-op：保留單一 fold 與各 surface 的必要邊界驗證，未發現可證明不改行為且實質提升清晰度的抽象移除；不為產生 diff 重構。gofmt 僅套用本次 touched Go 檔案。上述綠燈之後未修改產品行為，後補 legacy/HTTP tests 在 query-final-race 與 full-race-final 再驗證。

| 實際命令 | 結果 | 證據 |
|---|---|---|
| `go test -count=1 ./internal/engine -run TestExecutionAuditObservationFailureStopsExecution -v` | exit 0；run intent 寫入故障零 Run、Oracle outcome 故障停止 Striker | [observation-fault.log](observation-fault.log) |
| `go test -count=1 ./internal/engine -run TestExecutionAuditTaskTerminalLayers -v` | exit 0；completed stage 不被 failed Task 改寫 | [terminal-layers-green.log](terminal-layers-green.log) |
| `go test -count=1 ./internal/storage -run TestExecutionAuditLegacyContracts -v` | exit 0；三版真 SQLite old rows 不回填 | [legacy-contracts.log](legacy-contracts.log) |
| `go test -count=1 -race ./internal/web ./internal/storage -run TestExecutionAudit -v` | exit 0；新增真 SQLite／Unix IPC／session HTTP、過大 response、Node renderer | [query-final-race.log](query-final-race.log) |
| `go vet ./...` | exit 0，無輸出 | [full-vet.log](full-vet.log) |
| `go test -count=1 -race ./...` | exit 0；所有套件與 tests/integration 通過（log 明記 exit_code=0） | [full-race-final.log](full-race-final.log) |
| `go build -o /tmp/asura-issue41 ./cmd/asura` | exit 0，無輸出；僅改 artifact 位置，與 `go build ./cmd/asura` 同一 package | [full-build.log](full-build.log) |

既有 opt-in capacity／真模型探針依既有環境條件 skip；不是這次 audit 的必要測試，也不宣稱已執行真 provider 模型。Web 使用實際頁面函式的 Node DOM 等價 renderer，未宣稱瀏覽器端到端人工測試。當前沒有 runtime retry／fallback、歷史回填或 rejected Task persistence。provider model calls unknown、hash chain 非不可否認性，均為已核准限制。

純文件檢查：`python3 docs/issues/issue-41/evidence/check_planning.py`、`git diff --check`，並將 Gherkin block 與 `6adb11c` 原文逐字比較。2026-10-04：三者均 PASS，核准 Gherkin 逐字相同。根 README CLI／安全／完整度與架構 schema／truth／failure model 已更新（2026-10-04）。
