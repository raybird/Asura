# Issue 73 Task 3.3 證據：`--list` 與 `--remove`（2026-10-22）

## 變更

- `cmd/asura/runtime_verify.go`（新增）：
  - verify 命令從 `inventory.go` 移到這個檔案，內容不變。
  - `asura runtime verify --list`：先列出四個內建身分，標示 `built-in`；只比版本的身分 sha256 顯示 `any`。接著依檔案順序列出每筆紀錄的 runtime、版本、sha256、`verified_at`、`asura_version`。格式沿用 `runtime inventory` 的 `key=value`。
  - `asura runtime verify --remove <runtime> <version> [<sha256>]`：
    - 省略 sha256 時必須剛好有一筆相符的紀錄；多筆時要求指定 sha256。
    - 內建身分不能移除。省略 sha256 時，只要版本與內建身分相同就拒絕；否則 OMP／Antigravity 這類綁 hash 的內建身分會被回報成「沒有紀錄」，讓人誤以為內建身分可以移除。
  - usage 列出三種用法。
- 各 adapter 匯出 `Builtin` 身分（`opencode`、`codex` 只比版本；`omp`、`antigravity` 綁 hash），供 `--list`／`--remove` 使用。OMP 與 Antigravity 的 admit 改用同一個值，行為不變。
- `internal/backend/opencode/identity.go`：清掉 Task 3.1 留下、多餘的 import 空行。

## 紅燈

`cmd/asura/runtime_verify_test.go` `TestRuntimeVerifyListRemove`，未實作前：

```
--list: exit 1, stderr "flag provided but not defined: -list\n..."
--remove [codex 0.155.0]: exit 1, stdout "", stderr "flag provided but not defined: -remove\n...", want "error: codex 0.155.0 is a built-in ...
after remove: codex registration=registered availability=available version="codex-cli 0.163.0" reason=none
```

實作後 PASS。命令：`go test -count=1 ./cmd/asura/ -run TestRuntimeVerifyListRemove -v`

## 完成判準對照（字面輸出）

測試環境：HOME 與 XDG 目錄都在暫存目錄；假 codex 回報 0.163.0。紀錄預先寫入三筆：codex 0.163.0（假 codex 的 sha256）、omp 19.0.0 的 `e1` 與 `f2`。

| 情況 | 斷言 |
|---|---|
| `--list` | stdout 全文相等：四行 `<runtime> version="<v>" sha256=<any 或 hash> built-in`，接著三筆紀錄 `<runtime> version="<v>" sha256=<hash> verified_at=2026-10-20T01:02:03Z asura_version=<v>`；stderr 為空，結束碼 0 |
| 移除內建身分 | `codex 0.155.0`、`omp 18.4.4`、`omp 18.4.4 <內建 hash>` 的 stderr 全文都是 `error: <runtime> <version> is a built-in verified identity: built-in verified identities cannot be removed`，結束碼非 0 |
| 沒有紀錄 | `codex 9.9.9` → `error: codex 9.9.9: no verification record for this identity` |
| 多筆相符 | `omp 19.0.0` → `error: omp 19.0.0 has 2 verification records; specify the sha256` |
| 失敗後 | 紀錄檔與失敗前逐 byte 相同 |
| 移除一筆 | `--remove codex 0.163.0` → `removed codex 0.163.0 (sha256 <hash>)`；`--remove omp 19.0.0 f2` → `removed omp 19.0.0 (sha256 f2)`；之後 `--list` 只剩四個內建身分與 `e1` |
| 移除後 | 移除前 inventory 的 codex 為 `availability=available`；移除後為 `availability=unavailable ... reason=version_unverified` |
| 用法錯誤 | `--list codex`、`--remove codex`、`--remove nope 1.0`、`--list --remove codex 1.0` 都印出 usage，結束碼非 0 |

## 突變檢查（暫時修改後還原）

| 突變 | 結果 |
|---|---|
| 拿掉內建身分的前置檢查 | `omp 18.4.4` 變成 `no verification record for this identity`；另外兩個情境的訊息也不符；測試 FAIL |

## 專案 gate

命令：`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...`

結果：exit 0，24 個套件 `ok`，無 FAIL，`TestRuntimeVerifyCommand`、`TestRuntimeVerifyListRemove` PASS。被跳過的測試（不計為通過）：

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
