# Issue 37 — 初始實作計畫

2026-10-04 使用者「核准」完整提案與驗收。唯一任務來源為本檔，以下為核准後細化的責任 Tasks。

## 設計方案

採 technical-analysis SB-TBD-1～15。入口保留 `AdmitRuntime(ctx, registry, mode, requested, priority...)` 回傳 ExecutionContract；新版本含 stage plan，planner 為 requirements/descriptors/intent/priority 的純值運算。execution 按 role 讀 binding，BoundBackend 僅為當前 stage 的執行 handle，Task.Backend 是 Striker 投影。風險與首要驗證唯一來源見 [README](README.md#風險與首要驗證)。

## 實作階段

| Phase | 狀態 | 產出與順序 | 相依 | 驗收 | 完成判準 |
|---|---|---|---|---|---|
| P1 安全邊界基線 | 📝 待實作 | daemon/pipeline 外迴圈負向測試；existing contract 刻畫 | 完整規格核准 | 001/004/006/010/015 | 取得直接關聯缺少 stage 行為的紅燈，且 existing 行為刻畫為綠；不接受 compile error 作紅燈 |
| P2 Contract 與純 planner | 📝 待實作 | bounded plan、一次 snapshot、stage selector、version 分流及完整 validation | P1 | 001/002/003/005/006/007/008/015 | 單元紅燈後全綠；固定 OMP/Codex fixture、序列化 round trip、上限/未知 stage/版本與 mutation 覆蓋 |
| P3 Admission 與持久化 | 📝 待實作 | engine/daemon/local 的 atomic plan 封存、Striker 投影、intent identity | P2 | 001/004/005/006/009/012/013 | 實際 SQLite/IPC 驗證 admission 拒絕零 Task/dispatch/Run/sentinel，重送 plan 不變且無額外 Version capture，重啟保留 plan 並 failed |
| P4 Pipeline execution | 📝 待實作 | 全 plan 初始 guard、逐 stage guard、stage 局部 context、legacy branch、trace | P3 | 001/002/009/010/011/014/015 | 實際 Run recording 依 codex/omp/codex；constructor 衝突、runtime 缺失/退化不 fallback；先前 stage 間變動在後續 Run 前拒絕；context/cancel 回歸 |
| P5 Audit 與交付 | 📝 待實作 | CLI/Web stage audit、使用者文件、架構文件、有效測試證據、PR/獨立 review | P4 | 014/016 | status/Web 不探測 runtime；vet/race/build 成功，每個 Scenario 有版本一致證據，獨立 review 保存；不自動 merge |

## 使用方式對照

`ask --mode three-face --backend auto` 仍是原入口，現在提案改為各 stage eligibility 選擇；daemon 的 `ASURA_RUNTIME_PRIORITY=omp,codex` 可形成 Codex/OMP/Codex（前提是全部已註冊候選能捕獲版本）。不設定新 stage 排序。explicit `--backend opencode` 仍三階段同 runtime；Direct auto 僅 Striker；既有 local shell run 不改為三階段 AI。

## 驗收與測試層級

| 驗收 | 單元層 | 外迴圈與獨立斷言 |
|---|---|---|
| 001/005/006 | mode/intent 與 stage plan、legacy Validate | CLI/daemon/pipeline 既有 direct/explicit 行為及四 runtime 回歸 |
| 002/003/007/008 | 用真實能力組合的固定 descriptors；字面預期 runtime IDs、排序/上限 | mixed pipeline 的實際 backend.Run role recording；純 determinism 屬同層可合併迴圈，無另一整合責任 |
| 004 | 無 eligible stage 整次 planner 拒絕 | daemon 真實 Task 數、dispatch、Run、workspace sentinel；不只驗錯誤訊息 |
| 009/010/011/015 | plan validation 與 scalar mutation 表、未知 capability、超限 | queued/pipeline registry mutation、constructor 衝突、stage 間能力退化；Run 與 trace 斷言 |
| 012/013 | intent fingerprint/JSON round trip | SQLite reopen、daemon recovery、IPC 重送；Version/Run 計數及原 plan bytes |
| 014 | stage trace/projection 輸出 | CLI status/Web audit 讀真實 persisted Task，對照 sealed/actual/guard，Version 計數零 |
| 016 | 既有全套 | `go vet ./...`、`go test -count=1 -race ./...`、`go build -o /tmp/asura-issue37 ./cmd/asura`；不需外部付費模型調用 |

新增行為記外迴圈紅燈、單元紅燈、最小實作綠燈及 code-simplify 後綠燈；不同層級不合併。code-simplify no-op 時記理由並重用有效綠燈。pure docs 用靜態連結、ID 集合、diff 檢查，不宣稱程式測試。證據需實際命令、exit code、版本／工作區內容識別及結果，保存於本 issue；固定 BASE/HEAD 後才建立 PR 與獨立 review。

## 檢查清單

- [x] 2026-10-04 完成現況盤點、最小邊界與首要驗證設計。
- [x] 2026-10-04 16 個驗收 ID 與 SB-TBD-1～15 均有提案。
- [ ] 完整核准與規格提交基線。
- [ ] decompose 在本檔建立可執行 Tasks。
- [ ] 紅綠證據、精煉、常青文件與完整回歸。
- [ ] 提交、PR、獨立 review artifact；PASS 後等待合併。

## 本次文件檢查證據

2026-10-04 針對本目錄 4 份未提交提案執行 Python 靜態檢查，exit code 0：README 的 16 個唯一 Scenario ID 與待核准表格集合完全一致；technical-analysis 的 SB-TBD 編號集合為 1～15；所有本機 Markdown 連結存在、code fence 成對且無範本註解。`git diff --check` exit code 0；此命令不涵蓋未追蹤文件，因此另讀取四份新檔檢查空白。

可重複檢查：以 `re.findall(r"@STGBIND-(\d{3})", README文字)` 及核准表 `re.findall(r"^\| STGBIND-(\d{3}) \| - \| 待核准 \|$", README文字, re.M)` 建立集合，兩者必須均等於 001～016；以 `re.findall(r"^\| SB-TBD-(\d+) \|", 技術提案文字, re.M)` 比對 1～15；逐檔檢查本機連結目標、偶數個 code fence、無 `<!--` 與行尾空白。

本階段為純文件，紅綠行為測試不適用；尚未執行 Phase 4C 測試、提交或獨立審查。

## 責任任務

### Task 1.1 — 安全邊界紅燈

- 產出與最小範圍：daemon/pipeline/backend 測試與紅燈記錄
- 相依：無
- 驗收編號：STGBIND-支援 001～015
- 完成判準：同一既有入口可重現未封存 stage plan/mixed 不可 admission；舊契約刻畫保持綠
- 驗證：依上方測試層級，真實紅綠或文件等價證據；證據列出命令、exit code 與被測內容。
- 狀態：📝 待實作
- 證據：執行時填入。

### Task 2.1 — Stage contract 與純 planner

- 產出與最小範圍：internal/backend 模型、planner、validation 與單元測試
- 相依：1.1
- 驗收編號：STGBIND-003/005/006/007/008/015
- 完成判準：已知字面 fixture、mutation/limit/version round trip 均通過
- 驗證：依上方測試層級，真實紅綠或文件等價證據；證據列出命令、exit code 與被測內容。
- 狀態：📝 待實作
- 證據：執行時填入。

### Task 3.1 — 原子封存與重送／重啟

- 產出與最小範圍：engine/daemon/local admission 整合與 SQLite/IPC 測試
- 相依：2.1
- 驗收編號：STGBIND-004/012/013
- 完成判準：拒絕無 Task/Run 副作用；原 plan 重送不探測，SQLite reopen/recovery 保留快照
- 驗證：依上方測試層級，真實紅綠或文件等價證據；證據列出命令、exit code 與被測內容。
- 狀態：📝 待實作
- 證據：執行時填入。

### Task 4.1 — 唯一 stage 執行與 guards

- 產出與最小範圍：pipeline stage binding、legacy 分流、trace 及整合測試
- 相依：3.1
- 驗收編號：STGBIND-001/002/009/010/011
- 完成判準：codex/omp/codex 實際 Run；缺失/退化/constructor 衝突不 fallback，版本升級保持相容
- 驗證：依上方測試層級，真實紅綠或文件等價證據；證據列出命令、exit code 與被測內容。
- 狀態：📝 待實作
- 證據：執行時填入。

### Task 5.1 — Audit 與交付

- 產出與最小範圍：CLI/Web audit、常青文件、完整驗證、PR/獨立報告
- 相依：4.1
- 驗收編號：STGBIND-014/016
- 完成判準：查詢不探測；vet/race/build 通過，固定範圍 PR 與獨立 review 保存
- 驗證：依上方測試層級，真實紅綠或文件等價證據；證據列出命令、exit code 與被測內容。
- 狀態：📝 待實作
- 證據：執行時填入。
