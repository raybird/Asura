# Issue 73 Task 3.2 證據：verify 命令的成功與失敗（2026-10-22）

## 變更

- `cmd/asura/inventory.go`：
  - 新增 `asura runtime verify <opencode|codex|omp|antigravity>`：經 `verification.Verify` 依序執行該 runtime 的 probe，逐項印出結果。
    - 全部通過：以 `verification.Add` 寫入紀錄，內容為 Task 3.1 算出的身分，加上 `verified_at`（UTC）與 `asura_version`（build 注入的版本）。
    - 任一失敗：結束碼 1，不寫紀錄。
    - 收到 SIGINT／SIGTERM 時取消 probe，行程樹由 adapter 回收，不寫紀錄。
  - 抽出 `localBackend`：`newLocalRegistry` 與 verify 共用同一份 adapter 設定（與 daemon 相同），verify 額外接上 `OnProcessStarted`。
  - `asura runtime` 的 usage 與總說明加上 verify。
- `internal/backend/verification/spec.go`：`Verify` 遇到內建身分直接回傳，不執行 probe、不呼叫模型。CLI 印出「built-in verified identity; nothing to verify」並以 0 結束。
- 四個 live 測試：`Verify` 對內建身分不會回傳結果，改為 skip，避免空結果被當成通過；`ASURA_VERIFY_*_BIN` 的說明同步修改。

## 紅燈

| 測試 | 紅燈 | 綠燈 |
|---|---|---|
| `cmd/asura/runtime_verify_test.go` `TestRuntimeVerifyCommand` | verify 為只印 usage 的 stub：`not logged in: output lacks "output"`、`... lacks "authentication failed"`、`workspace escape: output lacks "FAIL: security violation: wrote outside the workspace"`、`bwrap unavailable: output lacks "bwrap"`、`all pass: exit 1` | PASS |
| `internal/backend/verification/spec_test.go` `TestVerify` | 內建身分仍執行 probe：`builtin: spec = {...Builtin:true...}, results = [...]`（results 非空、runs > 0） | PASS |

`TestVerify` 的另一半（runtime 卡住時每個 probe 都以逾時失敗）在變更前就成立，屬於現況刻畫，不算紅燈。

命令：`go test -count=1 ./cmd/asura/ -run TestRuntimeVerifyCommand -v`、`go test -count=1 ./internal/backend/verification/ -run 'TestVerify$' -v`

## 完成判準對照

`TestRuntimeVerifyCommand` 以 CLI 執行檔搭配 fake runtime 執行（PATH 只含 fake runtime 目錄與 `/usr/bin:/bin`，HOME／XDG 都在暫存目錄）。驗證紀錄預先放入一筆無關的 OpenCode 紀錄，每個失敗情境之後都逐 byte 比對紀錄檔。

| 情境 | fake runtime | 結果 |
|---|---|---|
| 未登入 | codex 0.163.0，每次執行都回 `401 Unauthorized: login required` | 4 個 probe 都 FAIL，原因為 `authentication_failed`；結束碼非 0；印出 `no record written`；紀錄不變；inventory 仍為 `version_unverified` |
| 單一 probe 失敗 | codex 0.163.0，workspace-write 不限制寫入位置 | output、read-only、cancellation PASS；workspace-boundary `FAIL: security violation: wrote outside the workspace`；結束碼非 0；紀錄不變；仍為 `version_unverified` |
| bwrap 不可用 | agy 1.4.4，PATH 沒有 bwrap | output、workspace-boundary、credential-boundary、cancellation 都 FAIL：`exec: "bwrap": executable file not found in $PATH`；結束碼非 0；紀錄不變；仍為 `version_unverified` |
| 未安裝 | 移除 codex | `error: verify codex: ...`，結束碼非 0 |
| 逾時 | `TestVerify`：每次執行都卡到 context 結束 | 4 個 probe 都 FAIL，`Summary` 不通過 |
| 全部通過 | codex 0.163.0，模擬 workspace-write 沙箱 | 4 個 probe PASS，結束碼 0；紀錄變成兩筆：原紀錄不變，加上 `{codex 0.163.0 <FileDigest>}`，`asura_version=dev`，`verified_at` 在執行期間內 |
| 通過後的判定 | 同上 | 有 config 時 inventory 為 `codex ... availability=available version="codex-cli 0.163.0" reason=none`；以兩個不同的 `--db` 執行本機 run 都會執行 codex，沒有 `codex:version_unverified`；刪除 config 後 inventory 仍為 available |
| 內建身分 | codex 0.155.0 | `codex 0.155.0 is a built-in verified identity; nothing to verify`，結束碼 0，沒有呼叫 `exec`，紀錄不變 |
| 未知 runtime | `verify nope` | 印出 usage，結束碼非 0 |

逾時放在 verification 層驗證：每個 probe 的上限是 4 分鐘，在 CLI 層讓 runtime 卡住要等 16 分鐘。CLI 對逾時與其他失敗原因走同一條路徑（任一 `Result.Err` 非 nil 就不寫紀錄），這條路徑已由上表的三種失敗情境覆蓋。

## 突變檢查（暫時修改後還原）

| 突變 | 結果 |
|---|---|
| 失敗時仍寫紀錄（`if !ok && false`） | `not logged in: exit code 0`、`records changed`，另外兩個失敗情境同樣報錯，測試 FAIL |
| `Verify` 對內建身分仍執行 probe | `built-in identity: exit 0, 4 model calls`，測試 FAIL |

## 未執行

- 沒有用真實 runtime 執行 `asura runtime verify`：會把紀錄寫進維護者的 `~/.local/share/asura/`，屬於 Task 3.5 的端到端範圍。真實 runtime 走同一個 `verification.Verify`，Task 3.1 的 live 實跑已涵蓋。

## 專案 gate

命令：`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...`

結果：exit 0，24 個套件 `ok`，無 FAIL。被跳過的測試（不計為通過）：

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
