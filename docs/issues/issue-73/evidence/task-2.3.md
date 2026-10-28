# Issue 73 Task 2.3 證據：OMP／Antigravity 改為內建或已驗證的身分（2026-10-21）

## 設計重點

- OMP 身分：版本取自 `@oh-my-pi/pi-coding-agent/package.json`（不執行 CLI），雜湊為 `cli.js` 與 Bun 兩個雜湊的合併。`cli.js` 與 Bun 等於內建 hash 時直接沿用內建版本 `18.4.4`，不讀 `package.json`。
- OMP 的 `node_modules`（本機 `~/.bun/install/global/node_modules` 1.2GB、26,855 個項目）不在清冊時計算。執行時以內建樹雜湊，或驗證紀錄的 `tree_sha256`（新增的 `Record.Tree`），固定沙箱快照內容。已驗證版本與內建版本的固定強度相同。
- Antigravity 身分：`agy` 檔案雜湊等於內建 hash 時不執行它；否則以 `agy --version` 取版本，再對照本機紀錄。執行未驗證檔案的 `--version`，與 OpenCode／Codex 的清冊行為一致。
- 執行時固定的檔案雜湊改為「通過判定的那一次計算結果」。沙箱快照仍會比對複製出來的內容，判定之後才被替換的檔案依舊 fail-closed。
- `verification.Admit` 改為回傳相符的紀錄（OMP 需要 `Tree`）；`Allowed` 因此不再被正式程式使用，已移除，測試改為直接測 `Admit`。
- `clisandbox.VerifyProbeFile` 改為只回傳雜湊的 `ProbeFileDigest`；`clisandbox.VerifyFile` 不再被使用，已移除。

## 紅燈

先加入 `VerificationPath` 欄位、`Record.Tree` 與回傳錯誤的 `pins` 骨架，再寫測試。

命令：`go test -count=1 ./internal/backend/omp/ ./internal/backend/antigravity/ -run Identity`

```
--- FAIL: TestOMPIdentityPolicy (0.00s)
    identity_test.go:39: unverified OMP: reason=version_failed
FAIL	asura/internal/backend/omp
--- FAIL: TestAntigravityIdentityPolicy (0.00s)
    identity_test.go:34: unverified antigravity: reason=version_failed
FAIL	asura/internal/backend/antigravity
```

## 綠燈

| 判準（implementation-plan Task 2.3） | 測試 |
|---|---|
| 未驗證：`version_unverified` | `TestOMPIdentityPolicy`、`TestAntigravityIdentityPolicy` |
| 已驗證：available，descriptor 版本為實際版本（`omp/19.0.0`、`1.4.4`） | 同上 |
| 檔案被替換：回到 `version_unverified` | 同上 |
| OMP 執行時固定的檔案為實際雜湊、相依樹為紀錄中的 `tree_sha256` | `TestOMPIdentityPolicy`（`pins`） |
| 內建身分行為不變 | 本機 `runtime inventory`：`omp ... available version="omp/18.4.4"`；`TestRuntimePinnedAcrossSourceUpdate` 等 clisandbox 既有測試通過 |
| `Admit` 的比對規則（含內建只比版本時不計算雜湊、紀錄檔不安全時拒絕） | `TestAdmit` |

本機真實 runtime（`go run ./cmd/asura runtime inventory`）：

```
antigravity registration=registered availability=unavailable version="" reason=version_unverified
codex registration=registered availability=unavailable version="" reason=version_unverified
omp registration=registered availability=available version="omp/18.4.4" reason=none
opencode registration=registered availability=unavailable version="" reason=version_unverified
```

Antigravity 1.4.4 由 `version_failed` 變為 `version_unverified`。

## 因規則改變而更新的既有測試

| 測試 | 變更 | 理由 |
|---|---|---|
| `internal/backend/omp/inventory_test.go` `TestInventoryOMPProbe` | 「內容不明的 cli.js」案例補上 Bun 檔 | 身分要同時算 cli.js 與 Bun 的雜湊；原 fixture 沒有 Bun，現在會先回 `dependency_not_found`。補上 Bun 後回到原本意圖：內容不明的 CLI 回 `version_failed`（讀不到套件版本） |
| `internal/backend/clisandbox/probe_test.go` `TestInventoryPinnedFileProbe` | 改測 `ProbeFileDigest` 回傳的雜湊 | 函式改為只回傳雜湊 |
| `internal/backend/verification/record_test.go` `TestAllowed` | 改寫為 `TestAdmit` | `Allowed` 移除 |

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
