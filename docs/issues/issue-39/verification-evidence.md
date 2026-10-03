# Issue 39 — 驗證紀錄

2026-10-04。規格基線 a88fe79，使用者於 2026-10-04「核准」RI-TBD-1～15、RTINV-001～015 與 TBD-1～5。檔案以交付 commit 的版本與下方 source manifest 固定實際被測內容；本紀錄不是核准來源。

## 紅燈與修復

1. 2026-10-04，production 未改前執行 `go test -count=1 ./internal/daemon ./internal/pipeline -run '^TestInventory'`，exit 1。daemon 的 error 與 timeout 子案例均回報 available Codex blocked by OMP；timeout 用 5.01 秒且整次拒絕。pipeline TestInventorySealedRuntimeDisappears 的 err 為 nil、sealed runtime calls=1、ModelCalls=1，證明 Version error 被忽略。Task/Run/workspace sentinel 為外部斷言，不 mock admission。
2. 2026-10-04，inventory admission integration 未改前執行 `go test -count=1 ./internal/backend -run '^TestInventoryContractSnapshot$'`，exit 1。OMP raw version error 阻擋正常 Codex，未形成封存 inventory。此內迴圈檢查封存契約；daemon/pipeline 外迴圈另外保護持久化與 Run 邊界。
3. 2026-10-04，Codex 專用 probe 未實作前執行 `go test -count=1 ./internal/backend/codex -run '^TestInventoryCodexVersionPolicy$'`，exit 1。ExpectedVersion 0.155.0，但 --version fixture 回報 0.155.1，原 admission 為 available。修復後為 unavailable/version_failed。
4. 2026-10-04，audit 入口未改前執行 `go test -count=1 ./cmd/asura ./internal/web -run '^TestInventory(CLI|HTTPBoundary)$'`，exit 1。CLI unknown command runtime；Web 已認證查詢為 404。修復後有 fixed registered inventory 與受認證的唯讀路徑。
5. 2026-10-04，最小修復後 `go test -count=1 ./internal/backend ./internal/daemon ./internal/pipeline -run '^TestInventory'` exit 0，原首要風險測試均綠；daemon 用 3.044 秒完成含 timeout 的測試集合。
6. 2026-10-04，範圍內精煉後 `go test -count=1 -race ./internal/backend/... ./internal/daemon ./internal/pipeline ./internal/engine ./internal/web ./cmd/asura -run 'Test(Inventory|StageBinding|Capability|RuntimeSelection|ExpandedBackendsRegistered)'` exit 0，10 個套件全綠。移除 daemon 多餘 block、explicit 重複 requirements 解析及本次造成的 unused import；保留 Backend/Probe 與 JSON 契約；無其他重構。

Inventory state/ordering API 的單元與外部行為為同一可觀察層級，採單迴圈；subprocess cancellation／daemon IPC／sealed pipeline／HTTP/CLI 各保留獨立整合保障。fixture 只取代外部 runtime，不取代 inventory、selection、validator 或 Task persistence。後補的 bounds/ordering/cancel/legacy tests 沒有聲稱先取得紅燈；它們是同一修復的額外回歸保障。

## 既有測試調整理由

2026-10-04，核准 RTINV-004 取代 Phase 4B「一個版本失敗即全 auto 拒絕」政策；selection 舊測試改為確認健康 runtime admission 與 unavailable entry，未刪測試。backend expansion 的 raw version unavailable 字串改為 availability reason，但仍要求已註冊、拒絕及零模型呼叫。legacy capability-v1 fixture 移除新增 inventory 欄位，避免把新契約資料偽裝成 legacy；原 legacy assertions 保留。

## 驗收映射

| 驗收 | 實際測試與判準 | 結果 |
|---|---|---|
| RTINV-001 | TestInventoryPolicyStatesAndOrdering、TestInventoryOpenCodeProbe、TestInventoryPinnedFileProbe：available descriptor、無 Run callback、檔案不變 | 定向 race PASS |
| RTINV-002 | TestInventoryOMPProbe／AntigravityProbe／OpenCodeProbe／CodexVersionPolicy、CLI：binary/pin/config/version failure 排除候選 | 定向 race PASS |
| RTINV-003 | TestInventoryAdmissionFailureIsolation、TestInventoryPolicyStatesAndOrdering：錯誤／panic 隔離並保留其他候選 | 定向 race PASS |
| RTINV-004 | TestInventoryContractSnapshot、TestInventoryAdmissionFailureIsolation：OMP 失敗仍由 Codex admission，OMP Run=0 | 定向 race PASS |
| RTINV-005 | TestInventoryExplicitRecoveryAndCoherence、TestInventoryIPCRejectionAndCancellation：explicit reason、無 fallback/Task/secret leak | 定向 race PASS |
| RTINV-006 | TestInventoryExplicitRecoveryAndCoherence、既有 unknown backend 測試：未註冊拒絕 | 定向 race PASS |
| RTINV-007 | TestInventoryExplicitRecoveryAndCoherence、StageBindingMixed/QueueRedelivery：每 admission 每 runtime probe 一次，共用 snapshot／所有 stages 成功才 admission | 定向 race PASS |
| RTINV-008 | TestInventoryCancellationAndWholeBudget、TestInventoryUncooperativeObserverBound、daemon timeout：3/5 秒 policy、局部/全域 timeout、observer 有界 | 定向 race PASS |
| RTINV-009 | TestInventoryDeterministicCompletion／PolicyStatesAndOrdering：固定 clock、插入及完成順序不同而 JSON 相同 | 定向 race PASS |
| RTINV-010 | TestInventorySealedRuntimeDisappears、StageBindingGuardBetweenStages／MixedExecution：0 Run/model、trace guard、constructor 不補位 | 定向 race PASS |
| RTINV-011 | TestInventoryExplicitRecoveryAndCoherence、StageBindingQueueRedelivery：fresh recovery 僅改新 admission，舊 snapshot/plan byte equality | 定向 race PASS |
| RTINV-012 | TestInventoryBoundsAndTampering／ContractSnapshot／VersionProcessBounds、RuntimeSelectionPriorityAndBounds：ID/version/count/JSON/diagnostic 範圍、secret sentinel、tamper 拒絕 | 定向 race PASS |
| RTINV-013 | TestInventoryCancellationAndWholeBudget／VersionProcessCancellation／IPCRejectionAndCancellation：合作 probe 結束、leader 回收、無 Task/Run | 定向 race PASS |
| RTINV-014 | TestInventoryCLI／HTTPBoundary／WebRender、IPC Inventory、既有 status/stage audit：registration/state/reason、legacy／unknown policy、task status 不 probe | 定向 race PASS |
| RTINV-015 | TestInventoryLegacyContracts、既有 StageBindingRestartRecovery／QueueRedelivery；全部 Memory/Recall/Lifecycle/Git/Web/Cron/CLI 回歸 | 完整回歸進行中 |

## 全套驗證

2026-10-04：`go vet ./...` exit 0；`go build -o /tmp/asura-issue39 ./cmd/asura` exit 0，等價於指定 build 但產物不進 repo。首次 `go test -count=1 -race ./...` exit 1，僅 daemon 舊錯誤字串預期失敗，其餘套件含 tests/integration（254.069 秒）PASS；已依核准政策調整原斷言。

2026-10-04：最終 `go test -count=1 -race ./...` 正在收尾，完整結果將在本檔回填。/tmp/issue39-final-race.log 是執行中的原始日誌，尚未當作完成證據。

## 限制

- 2026-10-04 內建 adapter 使用離線檔案／程序 fixture；未發送新模型請求。既有 full suite 的 live test 依其原條件執行或 Skip，不將其當成四 runtime 的 real-provider 成功驗收。
- available 不保證遠端 auth/quota。OMP／Antigravity 成功的固定 binary hash 無法用任意離線 fixture冒充；缺檔／pin 不符／取消及共用 hash helper 有測試，真實固定版本執行回歸保留既有測試。
- 任意不合作的第三方 in-process Probe 無法強制終止；observer 可返回，測試自行釋放不合作 fixture。內建合作 probe 與 process group cancellation 有單獨回收斷言。
- 2026-10-04 未執行 production 觀測驗收，無外部驗收窗口／gate 豁免。
