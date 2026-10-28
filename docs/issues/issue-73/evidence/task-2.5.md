# Issue 73 Task 2.5 證據：紀錄移除後的 stage 執行前檢查與舊封存相容（2026-10-21）

本 Task 沒有修改產品程式。stage guard 在 stage 執行前重新取清冊，Task 2.2 與 2.4 接上驗證紀錄後就已涵蓋 SCN-009。本 Task 補上兩支測試鎖住這個行為，並以反向對照證明測試抓得到失效。

## 1. 紀錄在 stage 執行前被移除（SCN-009）

`cmd/asura/runtime_verification_test.go` `TestRuntimeVerificationRemovedBeforeStage` 的設定：

- 用真的 CLI 執行檔以子行程啟動 daemon，`runtime_priority = ["codex", "opencode"]`。
- 假 codex 0.163.0 已寫入驗證紀錄；假 opencode 1.18.23 是內建版本，隨時可用，作為「改選」的誘餌。
- `ask --backend auto --mode three-face --wait`，daemon 以已驗證的 codex 接受任務。
- 假 codex 在第一次執行任務時（oracle）刪掉驗證紀錄，模擬使用者在 oracle 與 striker 之間執行 `--remove`。

斷言：codex 只執行 1 次（只有 oracle）、opencode 執行 0 次（不改選），任務以 `codex:version_unverified` 失敗。

```
task failed: runtime admission rejected: backend unavailable; availability=[codex:version_unverified]; to use this codex version, run: asura runtime verify codex
--- PASS: TestRuntimeVerificationRemovedBeforeStage (0.49s)
```

反向對照。命令：`go test -count=1 ./cmd/asura/ -run TestRuntimeVerificationRemovedBeforeStage -v`；每次跑完都以 `git checkout` 還原，並確認 `git diff --quiet internal/`：

| 對照 | 暫時改動 | 結果 |
|---|---|---|
| 紀錄沒有被移除 | 假 codex 的 `rm -f` 註解掉 | FAIL：`codex ran 3 times, want 1 (oracle only)`、任務不是以 `version_unverified` 失敗 |
| 只拿掉 stage guard | `stage_binding.go` 的清冊重新判定改成 `if false && ...` | FAIL：任務改以 adapter 自己的版本檢查失敗（`backend codex failed (backend_unavailable): ... runtime version is not verified`），codex 仍只執行 1 次。adapter 的 `ValidateVersion` 是第二層防線 |
| 拿掉 stage guard 與 adapter 檢查 | 再把 codex `ValidateVersion` 的 `a.admit` 換成回傳 nil | FAIL：`codex ran 3 times, want 1 (oracle only)`，任務跑完三個 stage（`critic review missing verdict`） |

## 2. 升級前封存的清冊快照（SCN-009、Q13）

fixture `internal/backend/testdata/inventory-v1-pre-issue-73.json` 的取得方式：在 main（`1826d5f`，尚未包含 #73）的暫時 worktree 裡，用 `newLocalRegistry` + `CaptureInventory` 對本機真實 runtime 抓取，`json.MarshalIndent` 後原樣存檔，抓完即移除該 worktree。內容是 policy `inventory-v1`；antigravity 與 codex 為 `version_failed`；omp 18.4.4 可用；opencode 1.18.31 當時依 `1.18` 前綴規則判為可用。快照不含執行檔路徑。

`internal/backend/inventory_test.go` `TestInventorySnapshotBeforeVersionVerificationStillValid` 讀入 fixture，要求 `Validate()` 通過：

```
--- PASS: TestInventorySnapshotBeforeVersionVerificationStillValid (0.00s)
```

反向對照：暫時把 `InventoryPolicyVersion` 改成 `inventory-v2`，測試 FAIL，還原後確認無差異。這證明若調升 policy 版本，升級前封存的任務會被拒絕；Q13 決定不調升。

封存的快照只用於驗證契約，stage 執行前仍會重新取清冊。因此封存時可用的 opencode 1.18.31，升級後會在 stage 執行前以 `version_unverified` 被拒絕，與第 1 節的語意相同。

## 專案 gate

命令：`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...`

結果：exit 0，24 個套件 `ok`，無 FAIL。被跳過的測試（不計為通過）：

| 測試 | 原因 |
|---|---|
| `TestLiveAdapter` | live OAuth model call opt-in |
| `TestVerifyLive_Antigravity` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestVerifyLive_AntigravityControl` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestCodexProbe_BinaryVersion` | codex version mismatch (expected 0.155.0, got codex-cli 0.163.0); skipping live contract probe |
| `TestCodexProbe_NonInteractive_JSONL` | codex version mismatch (expected 0.155.0, got codex-cli 0.163.0); skipping live contract probe |
| `TestCodexProbe_ReadOnlySandbox` | codex version mismatch (expected 0.155.0, got codex-cli 0.163.0); skipping live contract probe |
| `TestCodexProbe_WorkspaceWriteSandbox` | codex version mismatch (expected 0.155.0, got codex-cli 0.163.0); skipping live contract probe |
| `TestCodexProbe_ContextCancellation` | codex version mismatch (expected 0.155.0, got codex-cli 0.163.0); skipping live contract probe |
| `TestAdapter_Run_Live` | codex version mismatch (expected 0.155.0, got codex-cli 0.163.0); skipping live contract probe |
| `TestCodexProbe_Live` | installed version "codex-cli 0.163.0" is neither 0.155.0 nor 0.155.1; skipping live assertion |
| `TestVerifyLive_Codex` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestVerifyLive_CodexControls` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestLiveAdapter` | live OAuth model call opt-in |
| `TestVerifyLive_Omp` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestVerifyLive_OmpControl` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestINT001Unit` | skipping live adapter.Run test: built-in opencode 1.18.23 not available: opencode found in PATH but version invalid: unsupported opencode version 1.18.31: runtime version is not verified: opencode 1.18.31; run asura runtime verify opencode |
| `TestVerifyLive_OpenCode` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestVerifyLive_OpenCodeControls` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly` | skipping live hostile opencode test: valid opencode binary not found: opencode found in PATH but version invalid: unsupported opencode version 1.18.31: runtime version is not verified: opencode 1.18.31; run asura runtime verify opencode |
| `TestUtilityLiveProbe` | set ASURA_UFL_PROBE_DIR to preserve live probe evidence |
| `TestUtilityCapacityProbe` | opt-in 10k/100k SQLite probe |
| `TestINT001` | opencode 1.18.23 not available in this environment: opencode found in PATH but version invalid: unsupported opencode version 1.18.31: runtime version is not verified: opencode 1.18.31; run asura runtime verify opencode; skipping contract test |
| `TestINT007` | opencode 1.18.23 not available in this environment: opencode found in PATH but version invalid: unsupported opencode version 1.18.31: runtime version is not verified: opencode 1.18.31; run asura runtime verify opencode; skipping contract test |
