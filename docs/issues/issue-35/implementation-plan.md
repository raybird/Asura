# Issue 35 — 實作計畫

2026-10-03 建立。設計見 [技術提案](technical-analysis.md)，風險與首要驗證只維護在 [README](README.md#風險與首要驗證)。本檔是唯一任務來源。

## 核准與任務來源

2026-10-03 使用者「提案也核准」，完整核准 RTSEL-001～015。

## Phase / Task

| Task | 目標與最小範圍 | 相依 | 責任驗收 | 完成判準與驗證 | 狀態 |
|---|---|---|---|---|---|
| 1.1 | daemon/engine auto admission 與重送負向測試 | 無 | 005、006、010 | 真 IPC/DB 紅燈及 Task/Run/side-effect 斷言；先於主要實作 | ✅ 已完成 |
| 2.1 | backend contract/snapshot/priority/resolver | 1.1 紅燈 | 002、003、004、008、013、014、015 | matcher 重用、固定預期的排序/版本/上限單元紅綠；設定整合由4.1支援 | ✅ 已完成 |
| 3.1 | daemon/engine/pipeline 單 binding 封存及持久化 | 2.1 | 007、009 | 1.1綠燈；三 stage共用、registry drift/重啟/並行重送不改decision | ✅ 已完成 |
| 4.1 | CLI auto與Web submission/audit及文件 | 3.1 | 001、011 | 真CLI/IPC/Web 紅綠；explicit shell相容、priority設定/封存與舊資料讀取 | ✅ 已完成 |
| 5.1 | 完整回歸與交付 | 4.1 | 012 | vet/race/build、固定BASE/HEAD、PR及獨立review artifact | ⏳ 等待 PR／獨立審查 |

同層純函式驗收合併內外紅燈；跨IPC/engine/CLI/持久化保留各自整合保障。證據於各Task完成後在本檔回填。

## Phase 概要與完成判準

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
- [x] 核准 TBD-1～4，規格基線 c3381d4，已在本檔 decompose。
- [x] Task 1.1～4.1 回填測試證據；5.1 的 PR／獨立 review 待交付。
- [ ] 固定 BASE/HEAD、PR Proof of Test 與獨立 review artifact。

## 核准前相容性基線（2026-10-03）

被測提交：`37e034b667700a7ced3f22919fb26b2a0d824a6c`，僅有未追蹤 issue 35 規劃文件，production 與測試無改動。

實際命令：`go test -count=1 ./internal/backend/... ./internal/engine ./internal/pipeline ./internal/daemon`，exit code 0；backend 與四個 adapter、clisandbox、engine、pipeline、daemon 均通過。這是既有行為基線，非 auto selection 或 priority 新功能的驗收證據。當時完整 race／vet／build 尚未執行；後續結果見下方 Task 證據。

## Task 證據（2026-10-03）

| Task／驗收 | 真實證據與結果 |
|---|---|
| 1.1；005、006、010 | [admission-red.log](evidence/admission-red.log) 記錄真 IPC/DB 與 backend 純 admission 的目標紅燈；auto 被錯認成 backend ID。005 由既有 explicit 保障及新增零 fallback 斷言驗證；最小實作後全綠見 full-race.log |
| 2.1；002、003、004、008、013、014、015 | backend selection_test.go 與 requirements 測試：固定排序字面值、自訂排序、未註冊偏好、超限、unknown、Version failure、未知policy封存驗證；純函式 matcher/policy 是同層，合併單迴圈。共同 auto 入口的紅燈見 admission-red.log |
| 3.1；007、009、010 | daemon runtime_selection_test.go：真三 stage、queue drift安全拒絕、restart、concurrent winner、正常重送零version/Run調用；queue測試曾因dispatch丟失auto intent逾時，修復後全綠。Task5.1 full-race保障全套 |
| 4.1；001、011、015 | [cli-red.log](evidence/cli-red.log)：CLI不接受auto；[cancel-red.log](evidence/cancel-red.log)：cancel成功後runtime存活，新增metadata handshake修復。CLI/SQLite/真adapter fixture、Web API forwarding與Node audit render最終全綠；explicit shell沿既有characterization |
| 4.1；011 Web boundary | [web-baseline-red.log](evidence/web-baseline-red.log) 為實作後以核准基線server.go重現，原API拒絕backend欄位。此子項沒有實作前獨立Web紅燈；如實揭露順序限制，不將事後重現描述成事前TDD。Web core resolver由1.1真daemon紅綠保障 |
| 5.1；012 | [full-vet.log](evidence/full-vet.log)、[full-race-first.log](evidence/full-race-first.log)、[full-build.log](evidence/full-build.log) 初次 race 因舊 Web 欄位拒絕測試失敗，已按 RTSEL-011 更新契約並重跑；最終結果待保存。[tested-source.sha256](evidence/tested-source.sha256) 固定全部Go/config內容，build輸出/tmp避免污染repo |

2026-10-03 code-simplify 已檢查本次修改：保持既有public/callback契約；registry只列舉，selection只依能力及priority；未引入interface層或抽象factory。精煉移除auto分支先建立但隨即覆寫的adapter。後續核對為no-op，沿同一份最終race綠燈，不重跑相同測試。

2026-10-03 驗證限制：adapter 使用確定性假binary／版本fixture與已存在的單元測試，未呼叫真模型；Web forwarding測試只mock IPC遠端，不mock正在驗收的HTTP boundary，核心selection另由真daemon/matcher/seal覆蓋。既有選用live tests按原環境gate執行，完整go test通過不表示新增實地模型驗收。

2026-10-04：完整 race 初次 exit 1，唯一失敗為既有 TestWEBMEMSubmitExplicitAndBounded 把新核准 backend 欄位視為注入。更新為已知 backend 接受且未知 backend 拒絕；resolved_context/mode 拒絕保障保留。Web baseline 紅燈是事後重現，未冒稱實作前 TDD。最終 race/vet/build 重新執行中。
