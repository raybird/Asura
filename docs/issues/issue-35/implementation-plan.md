# Issue 35 — 實作計畫

2026-10-03 建立。設計見 [技術提案](technical-analysis.md)，風險與首要驗證只維護在 [README](README.md#風險與首要驗證)。本檔是唯一任務來源。

## 核准與任務來源

2026-10-03 使用者「提案也核准」，完整核准 RTSEL-001～015。

## Phase / Task

| Task | 目標與最小範圍 | 相依 | 責任驗收 | 完成判準與驗證 | 狀態 |
|---|---|---|---|---|---|
| 1.1 | daemon/engine auto admission 與重送負向測試 | 無 | 005、006、010 | 真 IPC/DB 紅燈及 Task/Run/side-effect 斷言；先於主要實作 | ⏳ 進行中 |
| 2.1 | backend contract/snapshot/priority/resolver | 1.1 紅燈 | 002、003、004、008、013、014、015 | matcher 重用、固定預期的排序/版本/上限單元紅綠；設定整合由4.1支援 | 📝 待實作 |
| 3.1 | daemon/engine/pipeline 單 binding 封存及持久化 | 2.1 | 007、009 | 1.1綠燈；三 stage共用、registry drift/重啟/並行重送不改decision | 📝 待實作 |
| 4.1 | CLI auto與Web submission/audit及文件 | 3.1 | 001、011 | 真CLI/IPC/Web 紅綠；explicit shell相容、priority設定/封存與舊資料讀取 | 📝 待實作 |
| 5.1 | 完整回歸與交付 | 4.1 | 012 | vet/race/build、固定BASE/HEAD、PR及獨立review artifact | 📝 待實作 |

同層純函式驗收合併內外紅燈；跨IPC/engine/CLI/持久化保留各自整合保障。證據於各Task完成後在本檔回填。

## 候選 Phase 與完成判準

| Phase | 產出 | 相依 | 驗收 | 完成判準 |
|---|---|---|---|---|
| P1 安全與身分邊界 | daemon/engine admission 與重送負向整合測試 | 規格核准 | 005、006、009、010、011 | 真正目標行為紅燈；以 side-effect sentinel、Run/dispatch 次數與持久化 Task/decision 斷言覆蓋最大風險 |
| P2 contract 與純 selection | request/decision model、membership snapshot、共同 matcher/policy | P1，規格核准 | 002、003、004、008、013、014 | 純函式 table tests：預設 OMP 最後、自訂 priority／設定拒絕與封存、固定字面值預期、註冊排列、snapshot mutation、未知能力、空集合、版本失敗與超限；保存單元紅綠證據 |
| P3 admission 與 execution | daemon/engine Direct/Three-Face selected binding、atomic seal、queue/redelivery | P2 | 001、005、006、007、009、010、011 | P1 全綠；三 stage 記錄同 selected runtime；registry/policy 改變及 DB reopen 不改歷史；並行 submit race 下僅一 Task／execution |
| P4 CLI/Web/audit | ask/run auto、Web backend intent、status/trace audit 與 legacy reads | P3 | 001、009、011、013、014 | 真 CLI + SQLite/IPC/Web 整合紅綠證據；local explicit shell fixture 保持，auto 使用 adapter 且不執行 prompt shell |
| P5 交付驗證 | 常青文件、code-simplify、全回歸、提交、PR、獨立 review artifact | P4 | 012 及全項目 | go vet ./...、go test -count=1 -race ./...、go build ./cmd/asura；記被測版本、命令及 exit code；獨立審查持久化 PASS 才等待合併 |

## 使用方式對照

- 既有 `asura ask --backend codex ...`、`asura run --backend opencode 'shell command'` 保持。
- 提案 `asura ask --backend auto 'prompt'` 由 daemon 選 runtime。
- 提案 `asura run --backend auto 'prompt'` 使用實際 AI runtime；不把自然語言傳給 shell。
- Web API 提案 `{backend:"auto", ...}`，省略維持舊 direct/opencode。

## 測試策略與證據

matcher/policy 是純函式，其同層驗收可合併內外紅燈；daemon/engine/CLI/persistence 有不同失敗面，保留外迴圈整合與底層單元證據。既有相容行為使用 characterization 綠燈，新行為先目標紅燈再最小實作。完成後 code-simplify 一次，no-op 可沿用綠燈並記理由。

不得將 compilation error 或不存在的新 symbol 當目標紅燈；必要時先經既有 API 提交 backend=auto，證明 admission 不支援／錯誤行為。candidate Version 取得只用 fake binary/version fixture，不呼叫真模型驗證選擇。測試真 registry/matcher/seal/dispatch，不 mock 正在驗收的控制平面核心。

## 檢查清單

- [x] Phase 4A 基準、registry、admission、fingerprint、shell run 及 Web submission 已盤點。
- [x] 15 個 Scenario 與核准表一致；RS-TBD-1～12 全部有具體提案。
- [x] 最大風險先由負向 admission／redelivery 測試驗證；任務來源唯一。
- [ ] 核准 TBD-1～4，取得規格基線並 decompose。
- [ ] 各 Task 回填真實紅綠重構／相容性證據與檔案範圍。
- [ ] 固定 BASE/HEAD、PR Proof of Test 與獨立 review artifact。

## 核准前相容性基線（2026-10-03）

被測提交：`37e034b667700a7ced3f22919fb26b2a0d824a6c`，僅有未追蹤 issue 35 規劃文件，production 與測試無改動。

實際命令：`go test -count=1 ./internal/backend/... ./internal/engine ./internal/pipeline ./internal/daemon`，exit code 0；backend 與四個 adapter、clisandbox、engine、pipeline、daemon 均通過。這是既有行為基線，非 auto selection 或 priority 新功能的驗收證據。完整 race／vet／build 尚未執行。
