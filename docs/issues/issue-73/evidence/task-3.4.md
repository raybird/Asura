# Issue 73 Task 3.4 證據：驗證不自動觸發（2026-10-22）

## 變更

只新增測試，產品程式不變：`cmd/asura/runtime_verify_test.go` 的 `TestRuntimeVerificationNotAutomatic`（SCN-010）。

- 假的 codex（0.163.0）與 opencode（1.18.31）都沒有驗證紀錄。每次被呼叫都把參數寫進同一份呼叫紀錄；只回應 `--version`。
- 依序執行：
  1. 啟動 daemon
  2. 本機 `runtime inventory`
  3. 經 daemon 的 `runtime inventory`
  4. `doctor`
  5. 本機 `run --backend auto --mode three-face`
  6. daemon `ask --backend auto --mode three-face`
- 斷言：
  - 呼叫紀錄每一行都只有 `--version`。probe 一定要讓 runtime 執行模型回合（Codex 是 `exec`，OpenCode 是 `run`），只要任何路徑跑了 probe，這裡就會出現其他參數。
  - 正向對照：兩個假 runtime 都至少被查過一次版本，證明各路徑確實看到了它們。
  - 兩個 inventory 都顯示 `codex`、`opencode` 為 `availability=unavailable version="" reason=version_unverified`。doctor、本機 run、daemon ask 的輸出含 `version_unverified`，以及 `asura runtime verify codex`、`asura runtime verify opencode` 提示。

與 Task 2.4 的 `TestRuntimeVerificationAcrossPaths` 不重複：Task 2.4 在 daemon 啟動之後才開始計數，只數 Codex 的 `exec`。這裡從 daemon 啟動前就開始記錄，涵蓋兩個 runtime 的所有非版本呼叫。

## 紅燈：反向檢查（暫時修改後還原）

這是現況的刻畫測試，第一次執行就 PASS。依完成判準以反向檢查證明它抓得到自動驗證：新增一個暫時檔，以 `verification.Verify` 對 codex 執行 output probe，接到某條路徑的開頭。

| 接上 probe 的路徑 | 結果 |
|---|---|
| `handleDoctor` | `unverified runtime was executed beyond the version check: codex exec --json --ephemeral --skip-git-repo-check -C /tmp/.../workspace ...`，測試 FAIL |
| `handleDaemon`（daemon 啟動） | 同上，測試 FAIL |

還原後 `git status` 只剩測試檔，測試 PASS。

命令：`go test -count=1 ./cmd/asura/ -run TestRuntimeVerificationNotAutomatic -v`

## 專案 gate

命令：`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...`

結果：exit 0，24 個套件 `ok`，無 FAIL，`TestRuntimeVerificationNotAutomatic` PASS。被跳過的測試（不計為通過）：

| 測試 | 原因 |
|---|---|
| `TestLiveAdapter` | live OAuth model call opt-in |
| `TestVerifyLive_Antigravity` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestVerifyLive_AntigravityControl` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestVerifyLive_AntigravityCredentialControl` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
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
| `TestVerifyLive_OmpCredentialControl` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestINT001Unit` | skipping live adapter.Run test: built-in opencode 1.18.23 not available: opencode found in PATH but version invalid: unsupported opencode version 1.18.31: runtime version is not verified: opencode 1.18.31; run asura runtime verify opencode |
| `TestVerifyLive_OpenCode` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestVerifyLive_OpenCodeControls` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly` | skipping live hostile opencode test: valid opencode binary not found: opencode found in PATH but version invalid: unsupported opencode version 1.18.31: runtime version is not verified: opencode 1.18.31; run asura runtime verify opencode |
| `TestUtilityLiveProbe` | set ASURA_UFL_PROBE_DIR to preserve live probe evidence |
| `TestUtilityCapacityProbe` | opt-in 10k/100k SQLite probe |
| `TestINT001` | opencode 1.18.23 not available in this environment: opencode found in PATH but version invalid: unsupported opencode version 1.18.31: runtime version is not verified: opencode 1.18.31; run asura runtime verify opencode; skipping contract test |
| `TestINT007` | opencode 1.18.23 not available in this environment: opencode found in PATH but version invalid: unsupported opencode version 1.18.31: runtime version is not verified: opencode 1.18.31; run asura runtime verify opencode; skipping contract test |
