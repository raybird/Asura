# Issue 73 Task 2.4 證據：所有判定路徑一致（2026-10-21）

## 變更

- 驗證紀錄路徑 `$XDG_DATA_HOME/asura/runtime-verification.json` 接到所有建立 adapter 的地方：`asura daemon`（`daemon.Config.VerificationPath`）、`asura runtime inventory` 與 `doctor`（共用 `newLocalRegistry`）、本機 `run`（`--backend auto` 與明確指定 omp／antigravity）。路徑由 `config.DefaultPaths` 決定，不讀 `--db` 與 config。
- 移除 `internal/daemon/daemon.go` 寫死的 Codex `0.155.0`。daemon 對外的 `OpenCodeConfig().ExpectedVersion` 預設仍為 1.18.23（`TestDaemon_INT007_PermissionPolicy` 檢查此值），改用 `opencode.BuiltinVersion`；Codex 預設改用新的 `codex.BuiltinVersion`。
- 未驗證時的提示：`InventoryAdmissionError.Error()`（daemon `ask` 與本機 `run` 的拒絕訊息都來自這裡），以及 doctor 的 runtime 行，都加上 `to use this <runtime> version, run: asura runtime verify <runtime>`。

## 紅燈

整合測試 `cmd/asura/runtime_verification_test.go` `TestRuntimeVerificationAcrossPaths`：建置真的 CLI 執行檔、以子行程啟動 daemon，PATH 只放假的 codex 0.163.0（加上 `/usr/bin:/bin`，已確認其中沒有 codex／opencode），依序檢查五個階段。

2026-10-27 更正：這支測試當時只涵蓋 Codex。SCN-008 要求的 OpenCode「驗證後四條路徑都 available」沒有跨路徑覆蓋，是 PR #77 審查發現的缺口；已改為 codex、opencode 兩個子測試，見 [phase5.md](./phase5.md) Task 5.1。

命令：`go test -count=1 ./cmd/asura/ -run TestRuntimeVerificationAcrossPaths -v`

```
runtime_verification_test.go:108: before verification: doctor:
runtime_verification_test.go:108: before verification: local run err=exit status 1:
runtime_verification_test.go:108: before verification: daemon ask:
runtime_verification_test.go:119: after verification: inventory (socket ""):
runtime_verification_test.go:119: after verification: inventory (socket ".../asura.sock"):
runtime_verification_test.go:119: after verification: doctor:
runtime_verification_test.go:119: after verification: local run did not execute the verified codex
runtime_verification_test.go:119: after verification: daemon ask:
runtime_verification_test.go:119: after verification: daemon ask did not execute the verified codex
（binary restored、after the record was removed 階段同樣失敗）
```

驗證前，兩個 inventory 的 reason 已是 `version_unverified`（Task 2.2 的結果），失敗的是缺少提示；寫入紀錄後，沒有任何路徑讀取它。daemon ask 紅燈時的訊息：

```
error: capability admission: runtime admission rejected: backend unavailable; availability=[codex:version_unverified]
```

## 綠燈

```
--- PASS: TestRuntimeVerificationAcrossPaths (0.79s)
ok  	asura/cmd/asura
```

| 階段 | 本機 inventory | daemon inventory（`--socket`） | doctor | 本機 run `--backend auto --mode three-face` | daemon `ask --backend codex` |
|---|---|---|---|---|---|
| 驗證前 | `version_unverified` | `version_unverified` | warn + 提示 | 拒絕 + 提示，未執行 codex | 拒絕 + 提示，未執行 codex |
| 寫入紀錄後（daemon 執行中） | available | available | ok | 執行 codex | 執行 codex |
| 執行檔換掉、版本相同（SCN-006） | `version_unverified` | `version_unverified` | warn + 提示 | 拒絕，未執行 | 拒絕，未執行 |
| 執行檔還原 | available | available | ok | 執行 | 執行 |
| 紀錄移除後 | `version_unverified` | `version_unverified` | warn + 提示 | 拒絕，未執行 | 拒絕，未執行 |

「未執行」以假 codex 的呼叫紀錄確認：只出現 `--version`，沒有 `exec`。

本機真實 runtime（`go run ./cmd/asura doctor`）：

```
[warn] antigravity: unavailable reason=version_unverified executable=/home/kevin/.local/bin/agy; to use this antigravity version, run: asura runtime verify antigravity
[warn] codex: unavailable reason=version_unverified executable=/home/kevin/.nvm/versions/node/v22.20.0/bin/codex; to use this codex version, run: asura runtime verify codex
[ok]   omp: available version=omp/18.4.4 ...
[warn] opencode: unavailable reason=version_unverified executable=/home/kevin/.nvm/versions/node/v22.20.0/bin/opencode; to use this opencode version, run: asura runtime verify opencode
```

殘留檢查：`grep -rn '"1\.18\.23"\|"0\.155\.0"\|"1\.18"' --include=*.go cmd internal`（排除測試與只供測試使用的 matrix.go）只剩 `codex.BuiltinVersion` 與 `opencode.BuiltinVersion` 兩個常數。

## 觀察（未修改）

本機 `asura run --backend codex`（以及 `opencode`）不經過 runtime：`cmd/asura/run.go` 只在 `omp`、`antigravity`、`auto` 時接上 adapter，明確指定 codex／opencode 時沿用舊的 shell runner，把 prompt 當 shell 命令執行。這條路徑不會呼叫任何 runtime，因此不影響版本判定；規格 Background 列的本機 run 判定路徑即為 `--backend auto`。

## 專案 gate

命令：`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...`

第一次執行：`TestDaemon_INT007_PermissionPolicy` 失敗（`expected OpenCode ExpectedVersion '1.18.23', got ""`），原因是移除了 daemon 對外的預設值；改為以 `opencode.BuiltinVersion` 保留後重跑。

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
