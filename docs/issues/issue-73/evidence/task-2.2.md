# Issue 73 Task 2.2 證據：OpenCode／Codex 改為身分比對（2026-10-21）

## 設計重點

- 共用判定 `verification.Admit`：內建身分沒有雜湊時只比版本，且先比版本、不計算雜湊，內建版本的行為與成本與變更前相同（SCN-001）；其他版本以版本 + SHA-256 對照本機紀錄。
- OpenCode 身分：`opencode` 解析 symlink 後的原生執行檔（本機 185MB，雜湊約 0.18 秒）。內建版本由前綴 `1.18` 改為精確的 `BuiltinVersion = "1.18.23"`（Q5）。
- Codex 身分：npm 安裝時 `codex` 是 Node 包裝腳本，實際執行的是平台套件 `@openai/codex-linux-x64` 的原生檔，同套件還附帶 Codex 沙箱用的 `bwrap` 與 `rg`。因此身分涵蓋整個 `@openai/codex` 套件樹，平台套件不在其中時（npm 本地安裝會 hoist）另外合併其樹的雜湊；平台套件依 `codex.js` 的 `require.resolve` 規則尋找。本機 411MB、48 個檔案，雜湊約 0.47 秒。非 npm 安裝（原生執行檔）只算該檔。
- 新 reason `version_unverified` 加入清冊白名單；`version_failed` 只留給版本取不到、格式錯或無法計算雜湊。policy 維持 `inventory-v1`（Q13）。
- `AdapterConfig.VerificationPath` 為空時只認內建身分；CLI 與 daemon 的接線在 Task 2.4。

## 紅燈

先加入 `VersionUnverified` 常數、`VerificationPath` 欄位，以及只算包裝腳本雜湊的 `codexDigest` 骨架，再寫測試。

命令：`go test ./internal/backend/codex/ ./internal/backend/opencode/ -run Identity`

```
--- FAIL: TestCodexIdentityPolicy (0.00s)
    identity_test.go:43: unverified version: reason=version_failed
--- FAIL: TestCodexIdentityCoversNpmPlatformPackage (0.00s)
    identity_test.go:106: replacing the native binary did not change the codex identity
FAIL	asura/internal/backend/codex
--- FAIL: TestOpenCodeIdentityPolicy (0.00s)
    identity_test.go:52: unverified 1.18.x: reason=none
FAIL	asura/internal/backend/opencode
```

`TestAllowed` 新增的「只釘版本的內建身分」案例是和實作一起加的，另以暫時把比對改回整個身分相等取得紅燈：

```
--- FAIL: TestAllowed (0.00s)
    record_test.go:116: version-only builtin did not admit its version
```

## 綠燈

| 判準（implementation-plan Task 2.2） | 測試 |
|---|---|
| 內建版本免驗證 | `TestCodexIdentityPolicy`（0.155.0）、`TestOpenCodeIdentityPolicy`（1.18.23） |
| 未驗證：清冊為 `version_unverified`；Run 回 `backend_unavailable` 且不執行該檔 | 同上（以 marker 檔確認沒有執行） |
| 已驗證：清冊 available、`ValidateVersion` 通過 | 同上 |
| 版本字串相同但執行檔內容不同：回到 `version_unverified` | 同上 |
| Codex 的原生檔被換掉時身分改變 | `TestCodexIdentityCoversNpmPlatformPackage` |
| OpenCode 1.18.x 不再以前綴放行（Q5） | `TestOpenCodeIdentityPolicy` |

本機真實 runtime（`go run ./cmd/asura runtime inventory`）：

```
codex registration=registered availability=unavailable version="" reason=version_unverified
opencode registration=registered availability=unavailable version="" reason=version_unverified
```

把 `~/.cache/asura-verify/opencode-1.18.23/node_modules/.bin` 放到 PATH 最前面時：

```
opencode registration=registered availability=available version="1.18.23" reason=none
```

## 因規則改變而更新的既有測試

| 測試 | 變更 | 理由 |
|---|---|---|
| `internal/backend/opencode/inventory_test.go` `TestInventoryOpenCodeProbe` | fixture 版本 1.18.31 → 1.18.23 | 測試目的是兩個角色的設定有效；1.18.31 依 Q5 不再免驗證 |
| `internal/backend/opencode/adapter_test.go` `TestINT001Unit` | 找不到內建版本 OpenCode 時跳過 live 段 | 原本只看憑證，靠前綴放行 1.18.31 而在 `go test ./...` 中實際呼叫模型；比照 `tests/integration` 的 `TestINT001` |
| `cmd/asura/doctor_test.go` `TestDoctorDegradedWithOneRuntime` | OpenCode `0.0.1` 的預期 reason `version_failed` → `version_unverified` | TBD-1：格式正確但未驗證的版本屬於 `version_unverified` |

新測試 `identity_test.go` 把紀錄放在 `Add` 建立的 0700 子目錄：umask 002 下 `t.TempDir()` 為 0775，紀錄檔依設計拒絕可被 group 寫入的目錄。Asura 自己建立的資料目錄為 0755／0700，不受影響。

## 專案 gate

命令：`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...`

結果：exit 0，24 個套件 `ok`，無 FAIL。

另：第一次跑 `go test -count=1 ./...`（無 `-race`）時，`internal/backend` 的 `TestInventoryVersionProcessCancellation` 失敗一次；單獨跑 30 次在改動前（`git stash`）與改動後都全過，之後 3 次完整執行與上列 gate 皆未重現。本 Task 在該套件只新增常數與白名單項目，未觸及此測試或 `ProbeVersionCommand`，判定為既有的時序不穩定，未修改。

被跳過的測試（不計為通過）：

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
