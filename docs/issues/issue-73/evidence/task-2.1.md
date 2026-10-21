# Issue 73 Task 2.1 證據：驗證紀錄與身分計算（2026-10-21）

## 紅燈

先放入只有簽名、不做事的骨架（`Load` 回 nil、`Add`／`Remove` 不寫檔、`Allowed` 回 false、`FileDigest` 回空字串，`Paths.Verification` 未設定），再寫測試。

命令：`go test ./internal/backend/verification/ ./internal/config/`

```
--- FAIL: TestAddWritesPrivateFileAndReplacesSameIdentity (0.00s)
    record_test.go:43: records=[]
--- FAIL: TestRemove (0.00s)
--- FAIL: TestLoadRejectsCorruptedOrUnsafeFile (0.00s)
--- FAIL: TestAllowed (0.00s)
    record_test.go:116: builtin: Allowed=false want true
    record_test.go:116: verified: Allowed=false want true
--- FAIL: TestFileDigest (0.00s)
    record_test.go:128: digest="" err=<nil>
FAIL	asura/internal/backend/verification
--- FAIL: TestDefaultPaths (0.00s)
FAIL	asura/internal/config
```

## 綠燈

命令：`go test -count=1 -race -v ./internal/backend/verification/ ./internal/config/`

```
--- PASS: TestLoadMissingFileHasNoRecords
--- PASS: TestAddWritesPrivateFileAndReplacesSameIdentity
--- PASS: TestRemove
--- PASS: TestLoadRejectsCorruptedOrUnsafeFile
--- PASS: TestAllowed
--- PASS: TestFileDigest
ok  	asura/internal/backend/verification
--- PASS: TestDefaultPaths
ok  	asura/internal/config
```

完成判準對照：

| 判準 | 測試 |
|---|---|
| 新增、重複新增取代同一身分、與其他身分並存 | `TestAddWritesPrivateFileAndReplacesSameIdentity` |
| 檔案 `0600`、目錄 `0700` | 同上 |
| 移除；內建身分不能移除；不存在的身分回錯誤；失敗時檔案逐 byte 不變 | `TestRemove` |
| 檔案損毀、可被 group／others 寫入時拒絕讀取 | `TestLoadRejectsCorruptedOrUnsafeFile` |
| 版本或 SHA-256 任一不同即不放行 | `TestAllowed` |
| 路徑固定為 `$XDG_DATA_HOME/asura/runtime-verification.json`（預設 `~/.local/share/asura/`），與 DB 路徑同樣由 `DefaultPaths` 決定，不讀 `--db` | `TestDefaultPaths` |

精煉：`probe.go` 的 `digests` 改用 `FileDigest`，移除重複的 SHA-256 計算；重跑上列測試仍為綠燈。

## 專案 gate

命令（docs/agents/project.md）：`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...`

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
| `TestVerifyLive_OpenCode` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestVerifyLive_OpenCodeControls` | set ASURA_VERIFY_LIVE=1 to run model-calling probes |
| `TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly` | skipping live hostile opencode test: valid opencode binary not found: opencode found in PATH but version invalid: unsupported opencode version: expected 1.18.23, got 1.18.31 |
| `TestUtilityLiveProbe` | set ASURA_UFL_PROBE_DIR to preserve live probe evidence |
| `TestUtilityCapacityProbe` | opt-in 10k/100k SQLite probe |
| `TestINT001` | opencode 1.18.23 not available in this environment: opencode found in PATH but version invalid: unsupported opencode version: expected 1.18.23, got 1.18.31; skipping contract test |
| `TestINT007` | opencode 1.18.23 not available in this environment: opencode found in PATH but version invalid: unsupported opencode version: expected 1.18.23, got 1.18.31; skipping contract test |
