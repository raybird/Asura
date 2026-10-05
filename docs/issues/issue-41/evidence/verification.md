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
| EXAUD-005 | 立即有界 rejection，無 Task | `TestExecutionAuditIPC、TestExecutionAuditCLIAdmissionRejection、TestExecutionAuditLocalRejection` | execution-red.log（新 observation/query 尚缺） | audit-matrix-green.log、review-green.log、review-local-green.log |
| EXAUD-006 | missing guard 零 Run、不 fallback | `TestExecutionAuditFailures/missing` | execution-red.log | execution-green.log |
| EXAUD-007 | 身分漂移零 Run、保留 sealed | `TestExecutionAuditIdentityMismatch` | execution-red.log（guard provenance 尚缺） | integration-final.log |
| EXAUD-008 | runtime failure 分層與敏感 error 排除 | `TestExecutionAuditFailures/runtime` | execution-red.log | execution-green.log |
| EXAUD-009 | 三個取消 barrier 與單一終態 | `TestExecutionAuditCancellation` | recovery-red.log（lifecycle provenance 尚缺） | audit-matrix-green.log |
| EXAUD-010 | reopen 中斷保留 Oracle、Striker interrupted | `TestExecutionAuditTerminalAndRecovery` | recovery-red.log | query-final-race.log |
| EXAUD-011 | 重送無 probe/Run，append 冪等／conflict | `TestExecutionAuditIPC、TestExecutionAuditTerminalAndRecovery` | recovery-red.log | audit-matrix-green.log、query-final-race.log |
| EXAUD-012 | Store/IPC/CLI/HTTP 唯讀與授權 | `TestExecutionAuditCompleted、IPC、CLI、HTTPBoundary、WebRender` | cli-red.log、http-red.log | query-green.log、query-final-race.log |
| EXAUD-013 | 三版歷史 contract 無 events/terminal/backfill | `TestExecutionAuditLegacyContracts、LegacyAuto、TestExecutionAuditCLI` | cli-red.log（新增 legacy query 入口不存在） | legacy-contracts.log、query-final-race.log、review-auto-red.log、review-green.log |
| EXAUD-014 | allowlist、DB bytes、秘密與 response bounds | `TestAuditRejectForeignFields、TestExecutionAuditEventOrderAndBounds、Failures、HTTPResponseBound、CLI、WebRender` | model-red.log、admission-red.log、cli-red.log、http-red.log | model-storage-green.log、query-final-race.log |
| EXAUD-015 | seq/key/order/header/contract 篡改拒絕 | `TestExecutionAuditEventOrderAndBounds、NormalizationAndTamper、MissingHeaderIsNotLegacy、LegacyInvalidEnums` | model-red.log、admission-red.log | query-final-race.log |
| EXAUD-016 | 全 repo vet/race/build | `全專案既有與新增測試` | 不適用：回歸與文件保障不引入預期失敗 | final-delivery-race.log、wal-full-vet.log、wal-full-build.log |
| EXAUD-017 | admission/observation/terminal/recovery 原子性 | `AdmissionAtomic、ObservationFailureStopsExecution、TerminalRollback、RecoveryStartupFailure、ConcurrentWriter` | admission-red.log、recovery-red.log | observation-fault.log、integration-final.log、query-final-race.log |
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


## 獨立審查退回與修正（2026-10-04）

第一輪固定 HEAD `30cc401` 的獨立報告 [review-30cc401.md](../review-30cc401.md) 為 **RETURN TO execute-task**，不是 PASS。原 MF-1/MF-2/MF-3 由真測試重現於 [review-red.log](review-red.log)，命令：`go test -count=1 ./internal/storage ./cmd/asura -run 'TestExecutionAuditLegacyAuto|TestExecutionAuditLegacyInvalidEnums|TestExecutionAuditCLIAdmissionRejection' -v`，exit 1。

`review-red.log` 的 Three-Face auto fixture 最初沒有把新 stage-only Match 轉成歷史 task-wide Match，該子項先被既有 contract validator 拒絕，不能算它的產品紅燈。修正 fixture 的全 mode Match 後，`go test -count=1 ./internal/storage -run TestExecutionAuditLegacyAuto -v` 仍在 direct/three-face 兩個合法舊契約讀取時回 audit_invalid；見 [review-auto-red.log](review-auto-red.log)，exit 1。

- MF-1：capability-v1 保留原先全 Task 的 SelectionDecision／candidate match／priority／policy；三個 binding 共用原 selection，validator 依全 mode requirements 驗證，不改寫成新 per-stage 決策。direct 與 three-face old-row query 皆 legacy、無 events/backfill。覆蓋 EXAUD-013。
- MF-2：無 contract 的 legacy 須是 exact minimal safe header、合法 schema／identity／mode／state；有 sealed contract 的 legacy stage projection 必須等於無事件 prefix，不能偷帶 execution history。Store 也先 Validate 才返回。非法 mode/state 回固定 invalid，不輸出 sentinel。覆蓋 EXAUD-014/015。
- MF-3：Client.Submit 以 typed RejectionError 保留既有 Cause/Error 契約，另外傳遞 validated immediate projection；ask 與 local run 顯示有界 rejection JSON/digest。空 RequestID 的 daemon rejection 產生即時 correlation ID，不建立 Task。覆蓋 EXAUD-005/012/014。

最小綠燈命令：`go test -count=1 ./internal/audit ./internal/storage ./internal/daemon ./cmd/asura -run 'TestExecutionAudit|TestAuditRejectForeignFields|TestRuntimeSelection' -v`，exit 0；見 [review-green.log](review-green.log)。`go test -count=1 ./cmd/asura -run TestExecutionAuditLocalRejection -v`，exit 0；真 local runtime unavailable／SQLite 零 Task 證據見 [review-local-green.log](review-local-green.log)。精煉檢查 no-op：跨模組 typed error、legacy validation 與原 scope 差異是必要邊界，保留各自契約；同樣測試與完整回歸確認最終 bytes。

修正後完整 gate：`go vet ./...` → [review-full-vet.log](review-full-vet.log)，exit 0；`go build -o /tmp/asura-issue41-reviewed ./cmd/asura` → [review-full-build.log](review-full-build.log)，exit 0；`go test -count=1 -race ./...` → [review-full-race.log](review-full-race.log)，exit 0（已含 tests/integration）；後續 CI 競態修正的最終 gate 另見下節。原 full-race-final 為修正前證據，不能代替修正後完整 gate。tested-source.sha256 將在修正後最終交付重新固定。


## CI WAL 並行 writer 修正（2026-10-04）

PR #42 原 HEAD 30cc401 的 [GitHub run 37198088965](https://github.com/raybird/Asura/actions/runs/37198088965) 在既有 `TestRuntimeSelectionLocalCancel` 出現 runtime did not start；原始失敗摘錄見 [ci-platform-excerpt.log](ci-platform-excerpt.log)。本機修正前 `go test -count=40 -race ./cmd/asura -run '^TestRuntimeSelectionLocalCancel$'` 亦有三次相同失敗（[ci-cancel-red.log](ci-cancel-red.log)，exit 1）；/tmp overlay 在 handleRun 完成後讀取 stderr，確認 Task failed、guard_error=audit_persistence_failed，Run 尚未開始。main BASE 對照的相同 40 次測試另有一次「Task 已 failed，不能 cancel」的既有間歇失敗（[base-cancel-characterization.log](base-cancel-characterization.log)，exit 1）；不把兩種失敗混稱本次 regression，也不把該對照寫成 PASS。

真 WAL reader→writer 升級重現：預先建立 40 個 Task，第二 SQLite connection 持續變更非 audit 的 updated_at，主連線 append guard_started。`go test -count=1 ./internal/storage -run TestExecutionAuditConcurrentWriter -v` exit 1，observation 回 audit_persistence_failed；見 [wal-writer-red.log](wal-writer-red.log)。首個探索版本只作無變化的 request_id UPDATE，未重現，因此未作紅燈證據；最終測試使用實際 updated_at 切換。

修正：AppendTaskAudit 與 recovery 原本先讀取 deferred transaction；現在在首次讀取前執行 `UPDATE task_execution_audits SET digest=digest WHERE 0` 取得 SQLite WAL writer lock，**沒有 header row 被改動**。SQLite busy_timeout 可在 snapshot 建立前等待其他 writer，避免 read snapshot 過期後升級立即失敗。CreateTask 與 lifecycle CAS 已先寫入，不額外加鎖。這不是重新 Run／admission 的 retry，也不改 fail-closed 或 query 的唯讀語意。

`go test -count=5 -race ./internal/storage -run 'TestExecutionAuditConcurrentWriter|TestExecutionAuditTerminalAndRecovery|TestExecutionAuditAdmissionAtomic' -v` exit 0，見 [wal-writer-green.log](wal-writer-green.log)。`go test -count=50 -race ./cmd/asura -run '^TestRuntimeSelectionLocalCancel$'` exit 0，見 [ci-cancel-green.log](ci-cancel-green.log)。既有取消測試沒有被改動、略過或加長 timeout。

最終 WAL 修正 bytes 的 gate：`go vet ./...` → [wal-full-vet.log](wal-full-vet.log)，exit 0；`go build -o /tmp/asura-issue41-final ./cmd/asura` → [wal-full-build.log](wal-full-build.log)，exit 0；`go test -count=1 -race ./...` → [wal-full-race.log](wal-full-race.log)，exit 0（使用尚未加 writer 間隔的壓力 fixture）。EXAUD-016/017 以此新版本 gate 為準；既有 logs 保留各階段的實際版本與限制。最後 source hashes 由 tested-source.sha256 確認。


2026-10-04：`wal-writer-first.log` 保留第一輪不間斷 writer 壓力中一次 11 秒後失敗：等待鎖超過既有 10 秒 busy_timeout，這不是 snapshot 升級立即失敗，也不能宣稱 PASS。最終 writer fixture 在每次實際 metadata 更新之間間隔 1 ms，使測試不要求無限壓力下突破既有 timeout；assertion 仍要求全部 observation 成功，沒有略過任何 iteration。

以相同最終 fixture 與 `30cc401` 原 append/recovery source 的 `/tmp` overlay 比較：`go test -overlay=/tmp/asura-issue41-wal-overlay.json -count=1 -race ./internal/storage -run '^TestExecutionAuditConcurrentWriter$' -v` exit 1，0.79 秒就回 audit_persistence_failed；[wal-writer-final-red.log](wal-writer-final-red.log)。同 fixture 新版 `go test -count=5 -race ./internal/storage -run 'TestExecutionAuditConcurrentWriter|TestExecutionAuditTerminalAndRecovery|TestExecutionAuditAdmissionAtomic' -v` exit 0，[wal-writer-green.log](wal-writer-green.log)。這證明更改測試輸入沒有掩蓋 snapshot 競態；失敗與通過仍由交易實作決定。

最終 delivery bytes 的 `go test -count=1 -race ./...` 保存於 [final-delivery-race.log](final-delivery-race.log)，exit 0（所有套件及 tests/integration，log 明列 exit_code=0）。它包含最終 writer fixture；不把 wal-full-race 的不同 fixture 版本冒充本次最終 gate。產品 Go 檔案在兩輪 full gate 間相同，vet/build 的產品內容未改變，故重用 wal-full-vet／wal-full-build 的 exit 0。
