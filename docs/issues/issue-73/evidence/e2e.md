# Issue 73 Task 3.5 證據：本機真實 runtime 端到端（2026-10-27）

## 範圍

依 2026-10-27 Q16，端到端改用 OMP 18.12.6 與 Antigravity 1.4.4。兩者都不是內建身分（內建為 OMP 18.4.4、Antigravity 1.2.13），而且是目前驗證得過的版本。Codex 0.163.0 與 OpenCode 1.18.31 的端到端留到 #74、#75 驗收時補做。

## 環境

- 主機：維護者 Linux amd64。用 `go build ./cmd/asura` 從 `7f2ab50` 建置，加上未提交的文件修改，沒有程式差異。`asura version` 顯示 `dev`。
- 驗證紀錄：真實路徑 `~/.local/share/asura/runtime-verification.json`。開始前目錄不存在，未設定 `XDG_DATA_HOME`。
- daemon 在 scratchpad 使用獨立的 DB，socket 為 `$XDG_RUNTIME_DIR/asura-e2e-73.sock`，`--allowed-roots` 只放 scratchpad 的 workspace。整個流程 daemon 都沒有重啟。
- 本機 `run` 使用 scratchpad 的獨立 DB。主機上沒有執行其他 Asura daemon。

## 1. 驗證前：四條判定路徑都是 `version_unverified`

本機 `runtime inventory --socket ""` 與經 daemon 的 `runtime inventory`，兩者輸出相同（2026-10-27T09:14Z）：

```
antigravity registration=registered availability=unavailable version="" reason=version_unverified
codex registration=registered availability=unavailable version="" reason=version_unverified
omp registration=registered availability=unavailable version="" reason=version_unverified
opencode registration=registered availability=unavailable version="" reason=version_unverified
```

`doctor`：

```
[warn] antigravity: unavailable reason=version_unverified executable=/home/kevin/.local/bin/agy; to use this antigravity version, run: asura runtime verify antigravity
[warn] omp: unavailable reason=version_unverified executable=/home/kevin/.bun/install/global/node_modules/@oh-my-pi/pi-coding-agent/dist/cli.js; to use this omp version, run: asura runtime verify omp
[fail] runtimes: no runtime available
```

本機 `run --backend omp|antigravity` 與 daemon `ask --backend omp|antigravity --wait` 都在模型呼叫前拒絕。daemon ask 的結束碼為 1：

```
error: capability admission: runtime admission rejected: backend unavailable; availability=[omp:version_unverified]; to use this omp version, run: asura runtime verify omp
error: capability admission: runtime admission rejected: backend unavailable; availability=[antigravity:version_unverified]; to use this antigravity version, run: asura runtime verify antigravity
```

## 2. `asura runtime verify`

```
== verify omp (2026-10-27T09:14:57Z)
Verifying omp: each probe runs the model once
output                        6.1s  PASS
workspace-boundary            8.9s  PASS
credential-boundary          15.2s  PASS
cancellation                 10.8s  PASS
protected-files-unchanged     0.0s  PASS
verified omp 18.12.6 (sha256 cc10c11c6694c59798e1b467211be7a20537887a98d34acc561ca26bb7a5a4cd); recorded in /home/kevin/.local/share/asura/runtime-verification.json
exit=0
== verify antigravity (2026-10-27T09:15:42Z)
Verifying antigravity: each probe runs the model once
output                        6.2s  PASS
workspace-boundary           12.4s  PASS
credential-boundary          13.9s  PASS
cancellation                  7.7s  PASS
protected-files-unchanged     0.0s  PASS
verified antigravity 1.4.4 (sha256 337dcde668441a2828968644d7982829c2d2a5e0c4144f346e1f3fc6abb3cdc7); recorded in /home/kevin/.local/share/asura/runtime-verification.json
exit=0
```

紀錄檔權限為 0600、目錄為 0700。紀錄欄位：OMP 有 `runtime, version, sha256, tree_sha256, verified_at, asura_version`；Antigravity 少了 `tree_sha256`。`--list`：

```
omp version="18.12.6" sha256=cc10c11c… verified_at=2026-10-27T09:15:42Z asura_version=dev
antigravity version="1.4.4" sha256=337dcde6… verified_at=2026-10-27T09:16:22Z asura_version=dev
```

## 3. 驗證後：四條判定路徑都放行，任務成功

本機與 daemon 的 inventory（daemon 沒有重啟，2026-10-27T09:16:43Z）：

```
antigravity registration=registered availability=available version="1.4.4" reason=none
codex registration=registered availability=unavailable version="" reason=version_unverified
omp registration=registered availability=available version="omp/18.12.6" reason=none
opencode registration=registered availability=unavailable version="" reason=version_unverified
```

`doctor`：

```
[ok]   antigravity: available version=1.4.4 executable=/home/kevin/.local/bin/agy capabilities=process_cancellation,structured_events,workspace_write
[ok]   omp: available version=omp/18.12.6 executable=/home/kevin/.bun/install/global/node_modules/@oh-my-pi/pi-coding-agent/dist/cli.js capabilities=process_cancellation,structured_events,workspace_write
```

本機 `run` 結束碼為 0，`status` 顯示結果：

| 任務 | Backend version (striker) | Result |
|---|---|---|
| task-1793092604-0bac1fd08495 | omp/18.12.6 | `E2E-LOCAL-omp` |
| task-1793092611-b03a05f3a924 | 1.4.4 | `E2E-LOCAL-antigravity` |

daemon `ask --wait`，結束碼為 0：

| 任務 | 輸出 |
|---|---|
| task-1793092618-10c723348823 | `E2E-DAEMON-omp` |
| task-1793092627-06c4f7fbc9c3 | `E2E-DAEMON-antigravity` |

## 4. 移除紀錄後回到 `version_unverified`

```
removed omp 18.12.6 (sha256 cc10c11c6694c59798e1b467211be7a20537887a98d34acc561ca26bb7a5a4cd)
removed antigravity 1.4.4 (sha256 337dcde668441a2828968644d7982829c2d2a5e0c4144f346e1f3fc6abb3cdc7)
```

省略 sha256 時，每個 runtime 剛好只有一筆紀錄，所以可以直接移除。之後：

- `--list` 只剩 4 個內建身分，紀錄檔內容為 `{"records": []}`。
- 本機與 daemon 的 inventory 回到四個 runtime 都是 `version_unverified`，`doctor` 回到 `[fail] runtimes: no runtime available`。daemon 仍然沒有重啟。
- 本機 `run` 與 daemon `ask` 都以結束碼 1 拒絕，訊息與第 1 節相同。

## 收尾

- 用確認過的 PID（798235）以 SIGTERM 停止 daemon，socket 已移除。
- 沒有殘留的 probe 行程：搜尋 `e2e/ws` 與 6 位數以上的 `sleep`，都沒有結果。
- `~/.local/share/asura/` 是這次建立的，已刪除紀錄檔與 lock 檔並移除目錄，回到開始前的狀態。

## 判定

完成判準（Q16 修訂後）逐項成立：
- 驗證前四條路徑都是 `version_unverified`。
- 驗證後都是 available，本機 run 與 daemon 任務成功。
- 移除紀錄後回到 `version_unverified`。

跨路徑一致（SCN-003）。daemon 每次判定都重新讀取紀錄，不需要重啟。SCN-008 指的是 OpenCode，這次沒有涵蓋到它的真實 runtime。SCN-008 由 Task 2.4 的 hermetic 測試負責；OpenCode 的跨路徑子測試是 2026-10-27 PR #77 審查後才補上的（[phase5.md](./phase5.md) Task 5.1）。OpenCode 1.18.31 的真實端到端留到 #75 驗收時補做。

觀察（不影響判準）：開頭訊息「each probe runs the model once」，對只比對檔案的 `protected-files-unchanged` 不精確。

## 2026-10-27 重跑：Antigravity（Phase 5 之後）

Phase 5 Task 5.3 改了 Antigravity 的判定：只比雜湊，不再執行 agy。上面第 1～4 節的 Antigravity 證據對應的是 `7f2ab50`，所以用 `f321df7` 建置後重跑同一流程。方法跟之前相同：獨立的 daemon（socket `$XDG_RUNTIME_DIR/asura-e2e-73b.sock`）與 DB，全程不重啟 daemon。OMP 的判定在 Phase 5 沒有改變，沒有重跑。

| 階段 | 本機 inventory | daemon inventory | doctor | 任務 |
|---|---|---|---|---|
| 驗證前 | `version_unverified` | `version_unverified` | `[warn] … version_unverified`，提示 verify | daemon ask 被拒，`availability=[antigravity:version_unverified]` |
| `runtime verify antigravity` | — | — | — | 開頭提示 `probes run the model`；output、workspace-boundary、credential-boundary、cancellation、protected-files-unchanged 全部 PASS，exit 0，sha256 `337dcde6…`（與第 2 節相同） |
| 驗證後 | `available version="1.4.4"` | `available version="1.4.4"` | `[ok]   antigravity: available version=1.4.4` | 本機 run 為 `completed`，Result `E2E2-LOCAL`；daemon ask 輸出 `E2E2-DAEMON`，exit 0 |
| `--remove antigravity 1.4.4` 之後 | `version_unverified` | `version_unverified` | `[warn] … version_unverified` | daemon ask 被拒 |

收尾：
- 停止 daemon 時，我用 `pgrep -f` 找 PID，結果也比對到執行這個命令的 shell 自己，所以那個 shell 也收到了 SIGTERM。daemon（PID 1160221）正常結束，socket 已移除，沒有影響其他行程。
- 沒有殘留的 probe 行程。
- `~/.local/share/asura/` 已刪除，回到開始前的狀態。
