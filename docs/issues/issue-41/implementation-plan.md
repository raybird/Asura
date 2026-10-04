# Issue 41 — 實作計畫

2026-10-04 草案；所有階段待 README 的驗收與 EA-TBD 提案核准。以下為不可執行的規劃概要；核准後由 decompose 在本檔細化 Phase / Task，不建立第二份清單。

## 設計與使用方式

模型、失敗政策、API 及 bounds 依 [technical-analysis.md](./technical-analysis.md)。風險與首要驗證唯一來源為 [README](./README.md#風險與首要驗證)。

新增 GetTaskAudit(ctx, taskID) → bounded ExecutionAuditRecord/error；typed observation 的 append 與 Task lifecycle CAS 共享 SQLite transaction。ExecutionContext 只以非持久化欄位傳 observer；binding 仍從原 contract 取得。

| 變更前 | 提案核准後 |
|---|---|
| status 查看 sealed context 與返回後 trace | asura audit --id ID / --json 讀 persisted audit |
| Web task detail 的 trace 摘要 | 增加固定 decision chain、sealed/observed/actual、guard、terminal、completeness |
| admission error 附 inventory | 附安全 normalized rejection 摘要，不新增失敗 Task |

## 階段概要與相依

| Phase | 狀態 | 產出與相依 | 驗收 | 完成判準 |
|---|---|---|---|---|
| P0 | 待核准，不可實作 | 原子性／故障／recovery characterization 與最小持久化實驗；依提案核准 | 009～011、015、017 | 故障注入證明 Task/header/event/recall 同交易 rollback；CAS 取消競態不雙終態；crash prefix 可辨完整度；確認各 boundary 可持久化而不改 selection |
| P1 | 待核准，不可實作 | audit-v1 value model、canonical digest、normalized admission、SQLite header/events 與 migration；依 P0 | 001～005、014、015、017、019 | 新舊 DB 均可讀；固定 goldens；32 candidates 及 size 邊界；typed validation 拒絕錯序／錯 digest |
| P2 | 待核准，不可實作 | local/daemon/cron admission 與 typed pipeline/backend Run observation；依 P1 | 001～008、018 | Direct/mixed goldens；guard Run=0；runtime failure、postprocessing failure 分層；persisted Oracle completion 不依 terminal trace |
| P3 | 待核准，不可實作 | engine CAS/CancelTask/recovery/redelivery；依 P2 | 009～011、013、017 | reopen DB crash fixture；no phantom execution；同 request/header/event 唯一；完整故障矩陣與回歸 |
| P4 | 待核准，不可實作 | GetTaskAudit、IPC、CLI、Web 與常青文件；依 P3 | 012～015、019 | 所有讀取 probe/selection/Run=0；authorization 負向；各 surface secret/bounds assertion；Web 實際 renderer 驗證 |
| P5 | 待核准，不可實作 | 最終回歸、code-simplify、commit、PR、獨立審查；依 P4 | 016 與全部 | go vet ./...、go test -count=1 -race ./...、go build ./cmd/asura；固定交付 BASE/HEAD，持久化 review artifact |

編號欄的短號皆指 EXAUD- 前綴；完整 Scenario 集合只有 README 維護。

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
- [ ] 保存使用者核准與規格 baseline commit，decompose 細化任務。
- [ ] 依核准提案完成實作、紅綠重構與獨立 review。
