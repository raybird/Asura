# Issue 33 驗證紀錄

驗證日期：2026-10-03。核准基線 `665e409`，production 工作區的精確檔案雜湊另存 `tested-source.sha256`。尚未提交的工作區不以核准 SHA 冒充被測 source HEAD。

## 紅綠與精煉

- P1-T1／P3-T1：`admission-red.log` 為真 IPC／SQLite 紅燈；最小實作後 `TestCapabilityContractAdmissionRejects` 與 `TestCapabilityContractSealedRedelivery` 通過。額外 queue、restart、Cron、duplicate winner 測試支持相同風險判準。
- P2-T1：`matcher-red.log` 取得純契約內外合併紅燈；`go test -count=1 ./internal/backend -run '^TestCapability'` exit 0，排序、去重、缺少能力、未知 role/access/capability 與版本取得失敗可辨識。
- P4-T1：`guard-red.log` 為 baseline 舊 engine/pipeline 下固定 JSON fixture 的行為紅燈；新 guard 下所有 mutation／upgrade case 通過。固定 JSON 不以 builder 重算 expected；Three-Face 另以實際 stage backend Run 驗三個 stage 綁同 runtime。
- P5-T1：`audit-red.log` 為 CLI 與 Web 呈現紅燈；新 status fixture 真 SQLite 前後 bytes 不變。Web 測試透過 Node 執行 indexHTML 實際 renderer，textContent DOM seam 只替代瀏覽器 DOM，不替代被驗收的 render 邏輯；未涵蓋真人瀏覽器互動操作。
- 最小實作綠燈命令：`go test -count=1 ./internal/daemon ./internal/engine ./internal/pipeline ./cmd/asura ./internal/web -run '^TestCapability|^TestExpandedBackendsRegisteredFailClosed'`，exit 0，六個相關 package 通過（backend 純契約另述）。
- code-simplify：2026-10-03 檢查本次契約、DI 與 JSON wire shape；移除 daemon 記憶分支原有序列化與新增共同序列化的重複。其餘保留最小介面與 JSON 欄位，沒有可證明安全且實質改善的額外重構。精煉後完整 race／vet／build 結果見同目錄 log。

## 回歸失敗與限制

- 首次 `go test -count=1 ./...` 的失敗是兩項舊測試仍預期接受後 Failed Task；依已核准 RTCAP-003／009／014 更新為接受前錯誤，所有 backend 零呼叫斷言保留。變更位置：daemon/backend_expansion_test.go、tests/integration/multi_backend_test.go。
- 首次新並行 fixture 未設定 queue（預設只有兩個容量），六個並行請求因既有容量 gate 被拒絕；fixture 改 MaxQueue=16 後 `go test -count=1 -race ./internal/daemon -run '^TestCapabilityContractConcurrentWinner'` exit 0。這是 fixture 容量選擇，不修改 capacity gate 或忽略失敗請求。既有容量測試仍在全 suite。
- 本期不呼叫真實模型。現有 live binary／sandbox probes 的可用條件與 skip 行為沿既有 suite；測試使用固定能力、真 storage 與真 IPC，不能據此宣稱 production runtime 即時認證可用。
- 本機 opencode/codex run 原有 shell runner 行為尚待相容性決策；沒有偽造它的 RuntimeDescriptor，正式 runtime 路徑已封存。對應 README 的 RC-TBD-11 與 RTCAP-016。

## 已取得完整檢查

- `go vet ./...`：exit 0，`full-vet.log` 無診斷。
- `go build -o /tmp/asura-issue33 ./cmd/asura`：exit 0，`full-build.log` 無診斷；與未指定輸出檔名的 build 等價，避免新增 repo 二進位產物。
- `go test -count=1 -race ./...`：exit 0，19 個 package 通過；`full-race.log`。

## 獨立審查前修復版本期限（2026-10-03）

- 原工作區在 a9ba9b9 增加 opencode/capability_version_test.go，命令 `go test -count=1 ./internal/backend/opencode -run '^TestCapabilityVersionProbeDeadline'` exit 1；caller 50ms deadline 仍等待 2.00s，真實紅燈見 version-deadline-red.log。
- 原因：OpenCode Version 內先走不帶 context 的 Resolve/Validate helper；外部 CaptureRuntime timeout 無法中斷它。只新增私有 context-aware helpers，原公開 helper 簽章與既有行為保留，Version 傳遞 caller context。未變更 adapter 能力宣告與隔離規則。
- `go test -count=1 -race ./internal/backend/opencode -run 'TestCapabilityVersionProbeDeadline|Test.*Version|Test.*Resolve'` exit 0，version-deadline-green.log。沒有獨立 IPC 層失敗面，真版本 subprocess 可直接驗版本期限；同層合併紅燈。
- code-simplify：只增加必要 context 傳遞，保留既有公開 helper；沒有額外可證明安全的精煉，no-op。後續完整檢查即重構後全綠證據。
- 最終 `go vet ./...` exit 0，final-vet.log；`go build -o /tmp/asura-issue33 ./cmd/asura` exit 0，final-build.log；`go test -count=1 -race ./...` exit 0，19 package 通過，final-race.log（integration 262.324s）。版本對應 final-tested-source.sha256，未冒用 a9ba9b9 作為修復後被測版本。

## RC-TBD-11 定案（2026-10-03）

使用者回覆「保留」，核准 shell 相容方案與 RTCAP-016 修訂。沒有 production 變更；原 status／Web 缺契約分支已提供 legacy/unavailable。新增 TestCapabilityShellRunCompatibility 真 shell／SQLite／status 刻畫測試，opencode/codex fake binary sentinel 證明零 runtime 呼叫。外內迴圈為同一相容路徑，紅燈不適用（維持既有行為）；綠燈見 shell-compatibility-green.log。code-simplify 已查核，no-op：短小 table case 沿用現有測試風格，無需抽象或更動 production。最終全回歸的 production source 完全未變，既有 final race／vet／build 證據仍有效；新增測試另以 race 驗證。
