# Issue 73 需求分析 - Runtime 版本改為「通過驗證即可使用」

## 需求描述

runtime 升級後，Asura 目前只能等程式改版才能使用新版本。使用者要求改成：有一個驗證方式，新版本通過驗證就可以使用（2026-10-21）。未驗證的版本維持在模型呼叫前拒絕，因為版本檢查同時保護唯讀角色的寫入隔離與輸出解析。

## 現況分析（2026-10-21 盤點 main `1826d5f`）

### 版本來源與比對規則

- 共用：`ProbeVersionCommand` 執行 `--version`，只取 stdout 前 256 bytes 並 TrimSpace，字元白名單另外允許 `codex-cli ` 前綴（`internal/backend/probe_process.go:31-65`、`internal/backend/inventory.go:119-134`）。
- OpenCode：前綴比對 `strings.HasPrefix`。adapter 預設 `"1.18"`（`internal/backend/opencode/adapter.go:271-272,350-351,392-393`），daemon 傳入 `"1.18.23"`（`internal/daemon/daemon.go:154-155`），`cmd/asura/inventory.go:34-35` 與 `cmd/asura/run.go:422-423` 用 `1.18`。找執行檔的順序是 `~/.cache/asura-probe-issue-2/...`，再來 PATH（`adapter.go:237-266`）。唯讀與寫入兩個角色的版本必須相同，否則回 `configuration_invalid`（`opencode/inventory.go:67`）。
- Codex：去掉 `codex-cli ` 前綴後精確比對 `0.155.0`（`codex/adapter.go:46,80-97`、`codex/inventory.go:19`、`daemon.go:231`）。
- OMP：不執行 `--version`，比對 cli.js 與 Bun 的 SHA-256，`Version` 為常數 `omp/18.4.4`（`omp/adapter.go:14-17,48-59`、`omp/inventory.go:16-32`）。Run 時另外驗 node_modules 樹的 hash（`omp/adapter.go:93`、`clisandbox/run.go:147-172`），並要求 package layout 正確與 bwrap 存在。程式沒有檢查 Bun 版本字串，只比 hash（文件寫 Bun 1.4.2）。
- Antigravity：比對 cli hash，常數 `1.2.13`（`antigravity/adapter.go:15-17,41-45`）。
- OMP／Antigravity 另外固定模型，`req.Model` 不等於固定模型時回 `FailureUnsupported`（`omp/adapter.go:84`、`antigravity/adapter.go:119`）。hash 不符時 probe 回 `version_failed`（`clisandbox/probe.go:47-48`）。

### 版本檢查的關卡

| 關卡 | 位置 | 不符時的行為 |
|---|---|---|
| 清冊 | `inventory.go:146-176` | `unavailable`，reason `version_failed`；reason 必須在白名單內（`inventory.go:255-261`） |
| admission（explicit） | `requirements.go:192-198` | 只探測指定的 runtime，探測不到回 `InventoryAdmissionError{ErrBackendUnavailable}`，不建立 Task |
| admission（auto） | `selection.go:163-176` | 先排除不可用的 runtime，不 fallback |
| stage 執行前 | `internal/pipeline/stage_binding.go:65-84` | 重新取 inventory，不與封存的版本比較；不可用則拒絕且不改選 |
| adapter Run | `codex/adapter.go:129-138`、`opencode/adapter.go:419-426`、`omp/adapter.go:76-80`、`antigravity/adapter.go:111-115` | `FailureUnavailable`，不啟動行程 |
| doctor | `cmd/asura/doctor.go:250-264` | 版本不符標 warn 並顯示 reason；全部 runtime 都不可用才標 fail |

### 能力矩陣與封存契約

- `GetOpenCode118xxMatrix`、`GetCodex0155xMatrix`（`opencode/matrix.go`、`codex/matrix.go`）只有測試使用；admission 的能力來自寫死的 `Capabilities()`，與版本無關。
- descriptor 記錄 Version（`inventory.go:176`），存在 resolved_context 的 InventorySnapshot（`inventory.go:363-374`）。`InventorySnapshot.Validate` 要求 contract 與 policy 版本完全相等（`inventory.go:262-265`）。
- 重送時回傳原 Task、不重新探測（`daemon.go:792-808`）；daemon 重啟時未完成的 Task 標為 failed（`daemon.go:417`）；版本與封存值不同時不拒絕（架構設計文件 spec:402）。

### 現有 probe 與 live 測試

| 類型 | 測試 | 是否呼叫模型 | 跳過條件 |
|---|---|---|---|
| hermetic | 各 runtime 的 `inventory_test.go`、`codex/adapter_test.go:52-104`、兩個 `*_HermeticFixtures`、`TestCodexProbe_BinaryVersion`、`TestINT001Unit` 的解析部分 | 否 | 無 |
| Codex live | `TestCodexProbe_NonInteractive_JSONL`、`_ReadOnlySandbox`、`_WorkspaceWriteSandbox`、`_ContextCancellation`、`TestAdapter_Run_Live`、`TestCodexProbe_Live` | 是 | 版本以 `strings.Contains` 比對 0.155.0（`adapter_probe_test.go:16-31`） |
| OpenCode live | `TestOpenCodeProbe_Live` | 是 | 只接受 1.18.23 或 1.18.31（`probe_context_test.go:125`） |
| 整合 live | `TestINT001`、`TestINT007`、daemon 的 Hostile 測試 | 是 | 寫死 1.18.23 並要求 `HasLiveCredentials`（`tests/integration/int_test.go:28-34,90-96`、`daemon_test.go:2342-2347`） |
| OMP／Antigravity live | `TestLiveAdapter` | 是 | 需 `ASURA_ISSUE25_LIVE=1`；目前只驗輸出 `PROBE_OK` |

OMP／Antigravity 的唯讀隔離由 Asura 自己的 bwrap 提供（`clisandbox/run.go:179-187`），不依賴 runtime 的旗標。

### 設定與覆寫

目前沒有任何版本覆寫或允許清單：config.toml 沒有相關欄位（`internal/config/config.go:47-59`），也沒有環境變數或 CLI flag。`OpenCodeRuntimeConfig.ExpectedVersion` 欄位存在，但 CLI 沒有接線（`cmd/asura/daemon.go:104` 只設定 Permission）。沒有 config 時資料庫預設為目前目錄的 `asura.db`（`cmd/asura/config.go:72-80`），有 config 時為 `$XDG_DATA_HOME/asura/asura.db`（`internal/config/config.go:80`）。

## 問題點總結

1. 升級 runtime 就無法使用，使用者沒有任何自助的解法。
2. OpenCode 在各路徑的規則不一致：同一台機器上，`runtime inventory` 說 available，daemon 卻拒絕。
3. 文件與程式不一致：README 與 `docs/guides/runtimes.md` 寫 OpenCode 固定 1.18.23、OMP 需 Bun 1.4.2，程式實際是前綴比對與 hash 比對。
4. 能證明安全邊界的 live probe 已經存在，但寫死版本並以 `t.Skip` 跳過，無法拿來驗證新版本。

## 目標

- 使用者能以單一命令驗證已安裝的新版本，通過後所有判定路徑都放行該身分。
- 未驗證的身分在所有判定路徑一致拒絕，並告訴使用者怎麼驗證。
- 驗證抓得到唯讀隔離失效；驗證失敗不放行。
- 內建已驗證的版本行為不變。
