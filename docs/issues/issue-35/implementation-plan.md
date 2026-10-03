# Issue 35 — 實作計畫

2026-10-03 建立。設計見 [技術提案](technical-analysis.md)，風險與首要驗證只維護在 [README](README.md#風險與首要驗證)。本檔是唯一任務來源。

## 核准前狀態

2026-10-03 分批核准 RTSEL-003、015（排序及設定入口）；其餘 Scenario 待確認。核心 admission 主要成功／失敗路徑尚未完整核准，依 acceptance.md 分批門檻先保存基線，不啟動 production 實作。核准後依 decompose 在本檔細化穩定 Task ID、scenario mapping、相依與證據欄位，不建立第二份任務清單。

## 已核准部分的責任 Task

### Task 2.1 — 可調 deterministic priority

- 產出與範圍：共同 priority 解析及排序純函式；預設 opencode 優先、其他 ID 字典序、OMP 最後；ASURA_RUNTIME_PRIORITY 同一解析入口、非法設定拒絕。不新增 Web 編輯 API。
- 相依：核心 admission 主要成功與重要失敗路徑核准；P1 首要風險驗證完成。設定入口整合另依賴 P3/P4 的 auto 接線。
- 驗收編號：RTSEL-003、RTSEL-015；有效 priority 封存為 RTSEL-009 的支援工作，009 仍待核准。
- 完成判準：已知候選下預設／自訂順序符合字面值預期；空項、重複、未知設定 ID 拒絕；未列者補齊；只選 eligible；daemon client 不覆蓋設定；歷史排序不被修改。
- 驗證：priority 純函式單層紅綠；環境入口、能力限制與封存另保留整合外迴圈。
- 狀態：📝 待實作（核准相依尚未滿足）。
- 證據：2026-10-03 設定功能尚未實作；下方基線不是本 Task 綠燈。

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
