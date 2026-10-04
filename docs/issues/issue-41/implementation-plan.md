# Issue 41 — 實作計畫

2026-10-04 使用者「核准」；本檔為唯一任務來源。首要驗證先於依賴它的產品實作，狀態與證據在下列 Task 維護。

## 設計與使用方式

模型、失敗政策、API 及 bounds 依 [technical-analysis.md](./technical-analysis.md)。風險與首要驗證唯一來源為 [README](./README.md#風險與首要驗證)。

新增 GetTaskAudit(ctx, taskID) → bounded ExecutionAuditRecord/error；typed observation 的 append 與 Task lifecycle CAS 共享 SQLite transaction。ExecutionContext 只以非持久化欄位傳 observer；binding 仍從原 contract 取得。

| 變更前 | 提案核准後 |
|---|---|
| status 查看 sealed context 與返回後 trace | asura audit --id ID / --json 讀 persisted audit |
| Web task detail 的 trace 摘要 | 增加固定 decision chain、sealed/observed/actual、guard、terminal、completeness |
| admission error 附 inventory | 附安全 normalized rejection 摘要，不新增失敗 Task |

## Phase / Task 與相依

### Task P0.1 — 原子性與 lifecycle 故障實驗
- 產出與範圍：真 SQLite Task/recall rollback、CAS 取消競態與 crash boundary 基線；不改產品行為。
- 相依：無。
- 責任驗收：EXAUD-017（產品證據由 P1.1/P3.1 支援）。
- 完成判準：先確認既有交易與取消單一終態，再以觸發器注入確認 audit 缺失的真實紅燈。
- 驗證：既有 storage/engine cancellation characterization；故障注入對照組，記錄命令/exit code。
- 狀態：⏳ 進行中。

### Task P1.1 — Audit 模型、admission 正規化與 SQLite persistence
- 產出與範圍：internal/audit、storage header/events/migration、Task+recall+audit 同交易、readonly fold/validation。
- 相依：P0.1。
- 責任驗收：EXAUD-003、004、014、015。
- 完成判準：有界 allowlist、immutable/idempotent events、offline provenance、tamper 拒絕；故障全部 rollback。
- 驗證：單元紅綠驗 canonical/transition/security；SQLite 整合紅綠驗交易與 query 不變動。
- 狀態：📝 待實作。

### Task P2.1 — Admission 與實際執行 observations
- 產出與範圍：pipeline guard/stage/Run、engine context recorder、local/daemon/cron admission、rejection projection。
- 相依：P1.1。
- 責任驗收：EXAUD-001、002、005、006、007、008、018、019。
- 完成判準：Direct/mixed fixtures；guard failure Run=0；runtime/processing/terminal 分層；intent 與 confirmed return 不混淆。
- 驗證：計數 backend + 真 SQLite 外層；guard 和 stage 內層紅綠；不 mock recorder。
- 狀態：📝 待實作。

### Task P3.1 — Cancellation、restart、redelivery 與 terminal 原子性
- 產出與範圍：storage CAS/terminal/recovery、engine cancel、daemon startup/redelivery。
- 相依：P2.1。
- 責任驗收：EXAUD-009、010、011、013。
- 完成判準：取消協調器宣告終態、completed stage 不改寫；reopen/crash 保留 prefix；events/header 不重複；legacy 不造歷史。
- 驗證：同步 barrier 競態、SQLite reopen 與 fault injection；回歸真 process-group cancellation。
- 狀態：📝 待實作。

### Task P4.1 — Read-only IPC、CLI、Web audit
- 產出與範圍：GetTaskAudit、task_audit IPC、asura audit、GET task audit 及固定 Web projection。
- 相依：P3.1。
- 責任驗收：EXAUD-012。
- 完成判準：每個 query probe/selection/Run=0、授權不可旁路、response bounded、sentinel 不出現在任何新 surface。
- 驗證：真 IPC/HTTP 與 CLI 紅綠；實際 renderer 檢查；支援 EXAUD-001/002/014/019。
- 狀態：📝 待實作。

### Task P5.1 — 常青文件、精煉與完整回歸
- 產出與範圍：README/架構文件、範圍內 code-simplify、測試與證據映射。
- 相依：P4.1。
- 責任驗收：EXAUD-016。
- 完成判準：go vet ./...、go test -count=1 -race ./...、go build ./cmd/asura 全通過；全部 Scenario 的實際證據集合相等。
- 驗證：四 runtime hermetic 回歸；靜態文件檢查；無真模型呼叫。
- 狀態：📝 待實作。

### Task P5.2 — PR 與獨立審查交付
- 產出與範圍：create-commit、create-pr、固定 BASE/HEAD 的獨立 reviewer 與持久化報告。
- 相依：P5.1。
- 責任：全部驗收的交付證據，不新增驗收編號。
- 完成判準：PR Proof of Test 完整，獨立 review PASS 且 artifact 已保存；等待使用者合併。
- 驗證：review-evidence.md；有 MUST FIX 則修正並重審。
- 狀態：📝 待實作。

## 測試策略

- model 單元層：canonical bytes 的已知字面 fixture、refs/transition/bounds/tamper；不由相同 fold 產生期望值。
- storage 整合層：真 SQLite migration/reopen/故障 trigger/並發 CAS、Task + recall + admission rollback、terminal 唯一。外層 transaction 行為與純 enum/model 驗證分開，保留各自紅燈。
- pipeline/engine 整合層：使用有計數器的 backend fixture，真的走 guard/Run 與 SQLite recorder；golden 為人工定義的預期 stage evidence。不 mock 掉 recorder 或 binding validator。
- daemon/CLI/Web 外層：真 IPC/HTTP、query 權限、唯讀 counter、secret sentinel、projection 節點；實際 browser 或等價可重複 renderer 檢查，不能只比 HTML 字串。
- cancellation/restart：在持久化 guard/run/stage boundary 以同步 barrier 控制順序，再 SQLite reopen + recovery，與真 process-group cancellation 回歸網併驗。
- runtime matrix：OpenCode/Codex/Antigravity/OMP adapter 既有 hermetic tests 與 capability/selection/guard/inventory regressions；不為 audit 新增真實模型調用，runtime invocations 與模型 calls 不混稱。

依 verification.md 保存各 Scenario 紅燈、最小綠燈、範圍內精煉後有效證據；同層保障可合併時記理由。純文件驗證使用靜態檢查，不宣稱程式測試通過。

## 規劃檢核

- [x] 2026-10-04 現況、最小邊界、兩份分析的觸發理由與 EA-TBD 覆蓋已記錄。
- [x] 2026-10-04 所有 Scenario 映射至單一計畫概要，待核准項未列為可執行 Task。
- [x] 2026-10-04 保存使用者核准與規格 baseline commit 6adb11c，decompose 細化七項任務。
- [ ] 依核准提案完成實作、紅綠重構與獨立 review。
