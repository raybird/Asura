# Issue 73 Task 4.1 證據：版本政策與命令文件（2026-10-27）

## 變更

只改文件：

| 檔案 | 修改 |
|---|---|
| `docs/guides/runtimes.md` | 新增「版本驗證」一節，涵蓋身分、內建身分、雜湊範圍、判定路徑、probe 表、失敗語意、紀錄位置、list/remove、已知限制。另改寫「Runtime adapter」開頭的固定版本敘述，在 2026-09-30 的 OMP／Antigravity 段落補上指向新一節的說明，並把「可用性清冊」的「固定版本／hash」改為內建或已驗證的身分 |
| `docs/guides/cli-reference.md` | 新增 `runtime verify` 一節，說明命令、結束碼與中止行為 |
| `README.md` | runtime 表改為「內建版本」，說明其他版本先 verify 才能用。指令一覽新增 `runtime verify` |
| `CHANGELOG.md` | `[Unreleased]` 新增：功能、相容性（`version_unverified` 與 OpenCode 規則統一）、已知限制（#74、#75） |

requirement-analysis「問題點總結」第 3 點的不一致（README 與 runtimes.md 寫固定版本，程式用前綴與 hash）已修正：上述文件現在描述的就是程式的身分規則。requirement-analysis 本身是當時的分析紀錄，保留原文。

## 人工核對：文件與程式

| 文件敘述 | 程式依據 |
|---|---|
| 內建身分：OpenCode 1.18.23、Codex 0.155.0 只比對版本；OMP、Antigravity 比對雜湊 | `cmd/asura/runtime_verify.go` 的 `builtinIdentities`。`--list` 實際輸出見 [e2e.md](./e2e.md) 第 1、4 節（`sha256=any` 與固定雜湊） |
| 雜湊範圍：OpenCode 為 EvalSymlinks 後的執行檔；Codex 為 npm 套件加平台套件；OMP 為 CLI 加 Bun，另記樹雜湊；Antigravity 為 CLI | 各 adapter 的 `identity.go`（`codexDigest`、OMP `VerificationSpec` 的 `clisandbox.TreeHash`）。紀錄欄位見 e2e.md 第 2 節 |
| probe 表：output、workspace-boundary 全部適用；read-only 只有 opencode／codex；credential-boundary 只有 omp／antigravity；cancellation 全部適用；protected-files-unchanged 只比對檔案，只有 omp／antigravity | `verification.Probes`（`ReadOnlyRoles`、`CredentialDir`）、`verification.Run`（`protected` 非空才加 `protected-files-unchanged`）。e2e.md 第 2 節的 OMP 與 Antigravity 輸出相符 |
| 單一 probe 最長 4 分鐘 | `probeTimeout` |
| credential-boundary 測「寫不到」憑證目錄 | `credentialProbe`：要求覆寫哨兵檔，哨兵必須維持原內容 |
| 失敗時 exit 1、紀錄不變；內建身分 exit 0 且不跑 probe；用法錯誤 exit 1 | `handleRuntimeVerify` 的各個 return。Task 3.2、3.3 的測試 |
| `--remove` 省略 sha256 時須剛好一筆；內建身分不能移除 | `removeVerification`。e2e.md 第 4 節 |
| 紀錄在 `$XDG_DATA_HOME/asura/runtime-verification.json`，不跟 `--db` 走 | `verificationPath()`。Task 3.2 的 `run --db` 測試、e2e.md |
| 不自動驗證 | Task 3.4 `TestRuntimeVerificationNotAutomatic` |
| 各判定路徑同一規則，daemon 不必重啟 | Task 2.4 測試。e2e.md 第 3、4 節（daemon 沒有重啟） |
| OpenCode 原本在本機 run、doctor、inventory 接受 1.18.x，daemon 只接受 1.18.23 | requirement-analysis 現況調查 |

靜態檢查：
- 新增的錨點 `cli-reference.md#runtime-verify` 與 `runtimes.md#版本驗證` 都存在。
- 三份 guide／README 的相對連結都指向存在的檔案（以腳本逐一 `test -e`）。

## Gate

`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...`：exit 0，24 個套件 ok，沒有 FAIL。build 在 repo 根目錄產生的 `asura` 執行檔已刪除。

skip 共 26 個。其中 25 個與 [task-3.4.md](./task-3.4.md) 列出的相同（live opt-in 或版本不是內建身分）。多出的 1 個：

| 測試 | 原因 |
|---|---|
| `TestOpenCodeProbe_Live` | `live opencode run failed (credential/network missing): signal: killed`。這是既有測試，以真實 OpenCode 1.18.31 執行；這次執行失敗，測試自己改為 skip |

從 `7f2ab50` 之後沒有程式差異，所以這個 skip 不是本 Task 造成的；它也不屬於 #73 的驗收證據。
