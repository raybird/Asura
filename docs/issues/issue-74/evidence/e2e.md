# Issue 74 步驟 6 證據：Codex 0.163.0 真實端到端（2026-10-28）

## 環境

- 主機：維護者 Linux amd64。以 `go build` 從本分支建置，含未提交的步驟 1～5 修改（`ff40b8c` 之後），`asura version` 顯示 `dev`。
- Codex：`codex-cli 0.163.0`，sha256 `40f51e0a…5e80`。不是內建身分（內建為 0.155.0）。
- 驗證紀錄：真實路徑 `~/.local/share/asura/runtime-verification.json`。開始前目錄不存在，未設定 `XDG_DATA_HOME`。
- daemon：PID 58101，在 scratchpad 使用獨立的 DB，socket 為 `$XDG_RUNTIME_DIR/asura-e2e-74.sock`，`--allowed-roots` 只放 scratchpad 的 workspace。整個流程都沒有重啟。
- 本機 `run` 使用 scratchpad 的獨立 DB，走 `--backend auto` 並設 `ASURA_RUNTIME_PRIORITY=codex`。明確指定 codex 時，本機 run 走的是舊的 shell runner，不經過 runtime（#73 task-2.4.md）。

## 1. 驗證前（2026-10-28T02:52:21Z）

本機與 daemon 的 inventory 相同：

```
codex registration=registered availability=unavailable version="" reason=version_unverified
```

（另外三個 runtime 也都是 `version_unverified`。）

`doctor`：

```
[warn] codex: unavailable reason=version_unverified executable=/home/kevin/.nvm/versions/node/v22.20.0/bin/codex; to use this codex version, run: asura runtime verify codex
[fail] runtimes: no runtime available
```

daemon `ask --backend codex --wait` 在模型呼叫前拒絕，結束碼 1：

```
error: capability admission: runtime admission rejected: backend unavailable; availability=[codex:version_unverified]; to use this codex version, run: asura runtime verify codex
```

本機 `run --backend auto` 也拒絕，結束碼 1：`stage striker: unsupported capability: no eligible runtime ... availability=[... codex:version_unverified ...]`。

## 2. `asura runtime verify codex`（2026-10-28T02:52:49Z）

```
Verifying codex: probes run the model
output                        6.7s  PASS
workspace-boundary           11.3s  PASS
read-only                    14.5s  PASS
read-only-sandbox             0.1s  PASS
cancellation                  6.9s  PASS
verified codex 0.163.0 (sha256 40f51e0a186f83e4e26e8173c059d683ee633eb5eaad02efc13716211dd55e80); recorded in /home/kevin/.local/share/asura/runtime-verification.json
exit=0
```

紀錄檔權限為 0600、目錄為 0700。紀錄欄位為 `asura_version, runtime, sha256, verified_at, version`。`--list` 除了 4 個內建身分，多了一筆 `codex version="0.163.0" sha256=40f51e0a… verified_at=2026-10-28T02:53:29Z asura_version=dev`。

## 3. 驗證後（2026-10-28T02:53:38Z，daemon 仍是 PID 58101）

本機與 daemon 的 inventory：

```
codex registration=registered availability=available version="codex-cli 0.163.0" reason=none
```

`doctor`：

```
[ok]   codex: available version=codex-cli 0.163.0 executable=/home/kevin/.nvm/versions/node/v22.20.0/bin/codex capabilities=process_cancellation,read_only_sandbox,structured_events,workspace_write
```

daemon `ask --backend codex --wait`：任務 `task-1793156021-21d553897757`，輸出 `E2E-DAEMON-codex`，結束碼 0。

本機 `run --backend auto`：**失敗**，`error: create task in storage: audit_invalid`，結束碼 1。原因見下方「發現：auto 路徑的稽核驗證」。

## 4. 移除紀錄後（2026-10-28T02:55:38Z）

```
removed codex 0.163.0 (sha256 40f51e0a186f83e4e26e8173c059d683ee633eb5eaad02efc13716211dd55e80)
```

- 紀錄檔內容為 `{"records": []}`，`--list` 只剩 4 個內建身分。
- 本機與 daemon 的 inventory 回到 `reason=version_unverified`，`doctor` 回到 `[fail] runtimes: no runtime available`。daemon 沒有重啟。
- daemon `ask` 以結束碼 1 拒絕，訊息與第 1 節相同。

第一次執行移除時，誤用了 `runtime verify codex --remove` 的參數順序：用法錯誤，結束碼 1，紀錄不變。改用正確格式 `runtime verify --remove codex 0.163.0` 重做。

## 發現：auto 路徑的稽核驗證（#73 帶進來的缺陷）

本機與 daemon 的 `--backend auto` 都在建立任務時失敗：

- 本機：`create task in storage: audit_invalid`
- daemon：`failed to persist initial task: audit_invalid`

- **不是本 issue 造成的**：用 `origin/main`（`a5e2e58`）建置的 binary，以全新的 DB 執行，得到相同的錯誤。
- **定位**：用暫時插樁的建置（worktree，用完已移除），把每個 `ErrInvalid` 標上行號。錯誤來自 `internal/audit/types.go:296`：admission 快照裡有 `Unavailable` 的 runtime，而它的 reason 不在 `probeReason()`（`types.go:361`）的白名單裡。
- **原因**：白名單沒有 #73 新增的 `backend.VersionUnverified`。auto 的 admission 快照包含所有 runtime；只要有任何一個是 `version_unverified`，稽核 header 就驗證不過。
- **為什麼 #73 的端到端沒有發現**：當時用的是 `--backend omp|antigravity`，快照只含指定的 runtime。
- **影響**：#73 合併後，主機上只要有一個未驗證的 runtime，本機與 daemon 的 `--backend auto` 就完全不能用。SCN-005 的「本機 run」這條路徑也因此無法通過。

## 收尾

- 用確認過的 PID（58101）以 SIGTERM 停止 daemon，socket 已移除。
- 沒有殘留的 probe 行程。
- `~/.local/share/asura/` 是這次建立的：已刪除紀錄檔與 lock 檔並移除目錄，回到開始前的狀態。

## 2026-10-28 補跑：本機 run（#79 合併後）

#79（PR #80，merge commit `01ce169`）修正了 auto 路徑的稽核缺陷。本分支在 `69e2a5a` 併入 main 之後重新建置，補跑本機 run（`--backend auto`、`ASURA_RUNTIME_PRIORITY=codex`）。環境同上：真實紀錄路徑，開始前目錄不存在。

| 階段 | 時間（UTC） | 結果 |
|---|---|---|
| 1. 驗證前 | 04:24:55 | inventory `reason=version_unverified`；run 結束碼 1：`stage striker: unsupported capability: no eligible runtime ... codex:version_unverified`。stderr 附上 `Admission rejection:` 投影；#79 之前，這種情況下不會印出投影 |
| 2. verify | 04:24:56 | output、workspace-boundary、read-only、read-only-sandbox、cancellation 全部 PASS，`verified codex 0.163.0`，結束碼 0 |
| 3. 驗證後 | 04:25:36 | inventory `available version="codex-cli 0.163.0"`；run 結束碼 0，任務 `task-1793161537-3512e05f627e` 的結果為 `E2E-LOCAL-codex`，`Backend version (striker): codex-cli 0.163.0` |
| 4. 移除後 | 04:25:55 | `removed codex 0.163.0`，紀錄為 `{"records": []}`；inventory 回到 `version_unverified`；run 結束碼 1，訊息與第 1 階段相同 |

收尾：刪除紀錄檔與 lock 檔並移除 `~/.local/share/asura/`，回到開始前的狀態。沒有殘留的 probe 行程。

## 判定

SCN-005 逐項成立：
- 驗證前，daemon、inventory、doctor、本機 run 都是 `version_unverified`。
- `asura runtime verify codex` 全部 probe 通過。
- 驗證後四條路徑都放行，任務成功（daemon 見第 3 節，本機 run 見本節）。
- 移除紀錄後回到 `version_unverified`。

daemon 整個流程沒有重啟。

probe 版本（2026-10-28 PR #82 審查補註）：本文件兩次端到端（02:52Z、04:24Z）都早於 `9feed5a`，`runtime verify codex` 跑的是舊版 cancellation 命令（先寫標記再 `setsid`，有假綠燈競態）。最終命令在 Codex 0.163.0 上的 probe 結果，見 [step4.md](./step4.md)「新命令重跑」（05:01Z，`TestVerifyLive_Codex` 五個 probe 全部 PASS）。這兩次 verify 走的是同一個 `verification.Verify`、同一份安裝（sha256 `40f51e0a…5e80`），差別只在 cancellation 的命令字串。SCN-005 驗的是驗證紀錄在四條路徑上的放行與撤銷，那一部分不受 probe 命令影響。
