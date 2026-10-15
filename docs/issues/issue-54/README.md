# Issue 54 - Flaky test：TestRuntimeSelectionThreeFaceAndQueueGuard 偶發 queued 任務狀態為 pending (Small)

## 概述

來源：[GitHub #54](https://github.com/raybird/Asura/issues/54)。`internal/daemon/runtime_selection_test.go` 的 `TestRuntimeSelectionThreeFaceAndQueueGuard` 在 CI 偶發失敗，重跑後通過。目標是證實原因並修正，不以加長等待或重試掩蓋。

| 日期 | CI run | 失敗位置 | 訊息 |
|---|---|---|---|
| 2026-10-13 | [38370089073](https://github.com/raybird/Asura/actions/runs/38370089073) attempt 1（PR #53，只改 README；attempt 2 通過） | `runtime_selection_test.go:218` | `queued-auto` 預期 `queued`，實際 `pending` |

### 現況分析（2026-10-13，已由步驟 1 的紅燈證實）

測試先等 three-face 任務 `three` 變成 `completed`，再在第 215 行以 `d.wsQueue.TryAcquire(root, "blocker")` 佔住 workspace（**不檢查回傳值**），接著送出 `queued-auto`，斷言回傳 `queued`。

daemon 的實際順序（`internal/daemon/daemon.go` 的 `dispatchTaskWithMode`）：engine 在 worker 內就寫入 `completed`，workspace 要等 WorkerPool 槽位歸還後，才在 done callback 呼叫 `releaseWorkspaceAndWakeNext` 釋放。所以存在「已是 `completed`、workspace 仍被 `three` 佔著」的時間窗。測試落在這個時間窗時：

1. `TryAcquire(root, "blocker")` 沒有取得 workspace，而是把 `blocker` 排進等待佇列，回傳 `(false, 1)`
2. `three` 的 done callback 執行 `Release`，彈出 `blocker` 並呼叫 `wakeQueuedTask("blocker")`
3. store 裡沒有 `blocker` 這個任務，`wakeQueuedTask` 直接釋放 workspace
4. `queued-auto` 取得 workspace 立即派工，回傳 `pending`

產品行為判斷：client 在這個時間窗送出同 workspace 的新任務，會先排隊，`three` 釋放後立即被喚醒執行，結果正確，不是缺陷。問題在於測試把「`completed`」當成「workspace 已釋放」。

## 涉及檔案

- `internal/daemon/runtime_selection_test.go` — 等 workspace 實際釋放後再佔用，並斷言 `TryAcquire` 成功

## 驗收條件

1. **AC-1**：用可重複的手段（注入延遲或 CI 等效負載）在修正前重現 CI 症狀（`queued-auto` 回傳 `pending`），並證實原因與「現況分析」一致：佔用 workspace 時 `TryAcquire` 回傳 `false` — 檢查方式：在修正前的程式上執行重現命令，紀錄失敗次數與 `TryAcquire` 回傳值
   - 失敗路徑：無法重現、或失敗時 `TryAcquire` 其實回傳 `true`（原因另有他處），則不依本分析修正；回報已嘗試的手段與結果，交由使用者決定
2. **AC-2**：只修改測試：佔用 workspace 前，先等到 workspace 確實釋放（可觀察的條件，不是固定時間），並斷言 `TryAcquire` 取得成功；daemon 的 `completed` 與釋放順序不變。同一重現手段轉綠，本機最小回歸（`go vet ./...`、`go test -count=1 -race -v ./...`、`go build ./cmd/asura`）全數通過 — 檢查方式：重跑 AC-1 命令與回歸命令
   - 失敗路徑：修正只是加長固定等待或加上重試，或改動了 production 程式，都判定未通過
3. **AC-3**：在 CI 等效條件（限制 CPU、加 `-race`）下以 `-count=200` 執行本測試，修正前至少失敗 1 次作為對照組、修正後 0 失敗，前後條件相同；PR CI 一次全綠作為補充 — 檢查方式：記錄壓力命令原文、CPU 限制方式與修正前後的失敗次數
   - 失敗路徑：修正後仍有任何失敗即未通過；修正前同條件 0 失敗時，判準沒有鑑別力，不得當作通過，回報後重新決定條件

- **核准日期**: 2026-10-13
- **核准來源**: 2026-10-13 Q1（TBD-1）回答「核准，只修測試 (Recommended)」，接受建議：AC-1～AC-3 照草稿核准，只修測試、不改 daemon 的 `completed` 與釋放順序；重現結果推翻分析時依 AC-1 失敗路徑停止回報。GitHub #54 由使用者於 2026-10-13 要求開立（「好，開一個 issue 追蹤」），完成判準沿用 #48；同日以 `/dev-cycle issue:54` 要求推進
- **核准 commit**: 0fbc00b

## 風險與首要驗證

- **最大風險**：根因只由程式碼推得；若實際原因不同，修測試會掩蓋真正的問題
- **風險等級與理由**：Medium — 有一項重要未知（推得的根因尚未以紅燈證實）；修正範圍只在測試、容易回復
- **首要驗證**：在修正前重現症狀，並記錄失敗時 `TryAcquire` 的回傳值
- **選擇理由**：`TryAcquire` 回傳 `false` 是推論鏈的第一環，能直接證實或推翻整條分析
- **完成證據**：修正前的紅燈與 CI 症狀一致，且失敗輪次的 `TryAcquire` 皆回傳 `false`

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|------|------|------|------|
| TBD-1 | AC-1～AC-3 是否核准；特別是 AC-2「只修測試、不改 daemon 的 `completed` 與釋放順序」的範圍 | 已解決 | 2026-10-13 使用者核准，只修測試：daemon 先寫 completed 再釋放 workspace 是既有設計，時間窗內的新任務只會短暫排隊，結果正確 |

## 實作與驗證步驟

1. ✅ **AC-1：重現與證實原因**— 驗證方式：暫時記錄 `TryAcquire` 回傳值，以 CI 等效負載跑 `-race -count=N` 重現，紀錄修正前的失敗次數與回傳值
   - 產出：重現命令與結果（寫回本節）
   - 相依：無
   - 完成判準：紅燈與 CI 症狀一致，失敗輪次的 `TryAcquire` 皆為 `false`；不符時依 AC-1 失敗路徑停止並回報
   - 完成證據（2026-10-13，go1.27.1 linux/amd64，12 核）：測試暫時改為記錄 `TryAcquire` 回傳值（診斷碼已移除，未進入交付），在步驟 3 的壓力條件下跑 `-count=100`：FAIL 30/100，30 次全為 `runtime_selection_test.go:218: DIAG54 tryAcquire=false pos=1 status=pending`，與 CI 症狀相同，且證實推論鏈第一環
2. ✅ **AC-2：修正測試**— 驗證方式：重跑步驟 1 的重現命令轉綠，並跑本機最小回歸
   - 產出：測試的最小修正
   - 相依：步驟 1
   - 完成判準：同條件綠燈；diff 只在測試，沒有固定等待或重試
   - 完成證據（2026-10-13）：
     - 修正：佔用前輪詢 `d.wsQueue.IsLocked(root)` 直到 workspace 釋放（3 秒上限只是防卡死，沿用同檔案 `waitExpandedTask` 的寫法），再斷言 `TryAcquire` 取得成功；daemon 程式未修改
     - `go test -count=1 -race -run 'TestRuntimeSelection' -v ./internal/daemon` 5 項全部 PASS
     - `go vet ./...` 通過；`go test -count=1 -race -v ./...` 23 個 package 全部 `ok`；`go build ./cmd/asura` 成功
     - 被 SKIP 的 14 項不計為通過，都是需要固定版本 runtime 或登入憑證的 live／contract 測試，與本次修改無關：TestLiveAdapter（2 處）、TestCodexProbe_BinaryVersion、TestCodexProbe_NonInteractive_JSONL、TestCodexProbe_ReadOnlySandbox、TestCodexProbe_WorkspaceWriteSandbox、TestCodexProbe_ContextCancellation、TestAdapter_Run_Live、TestCodexProbe_Live、TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly、TestUtilityLiveProbe、TestUtilityCapacityProbe、TestINT001、TestINT007
     - 重構 no-op：修正只有等待條件與一個斷言，沿用既有輪詢寫法，沒有可精煉的內容；沿用上述綠燈
3. ✅ **AC-3：壓力對照**— 驗證方式：同一 CPU 限制與 `-race` 條件下跑 `-count=200`，修正前後各一次；PR CI 一次全綠
   - 產出：壓力命令原文、條件與修正前後的失敗次數（寫回本節）
   - 相依：步驟 2
   - 完成判準：修正前至少失敗 1 次，修正後 0 失敗
   - 完成證據（2026-10-13）：
     - 執行檔：修正前（原始測試）、修正後各以 `go test -race -c ./internal/daemon` 建立
     - 條件：背景 6 個 `taskset -c 0,1 yes > /dev/null`，測試以 `taskset -c 0,1 ./daemon.test -test.run '^TestRuntimeSelectionThreeFaceAndQueueGuard$' -test.count=200 -test.v` 執行
     - 結果：修正前 PASS 138／FAIL 62（全為 `queued-auto` 狀態 `pending`），修正後 PASS 200／FAIL 0
     - PR CI 一次全綠：PR #55 在 `e923898` 的 [run 38380272760](https://github.com/raybird/Asura/actions/runs/38380272760) 為 success，`TestRuntimeSelectionThreeFaceAndQueueGuard`（0.52s）PASS。同一 HEAD 的 push run 38380243311 被 workflow `concurrency.cancel-in-progress` 取消（conclusion=cancelled），不是測試失敗

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-10-13 | 建立；Q1 回答後核准 AC-1～AC-3（只修測試） | Raybird |

---
**建立日期**: 2026-10-13  
**分級**: Small — 只修改單一測試檔  
**風險**: Medium — 建立時根因由程式碼推得，尚未以紅燈證實（2026-10-13 步驟 1 已證實）\
**狀態**: 審查中
