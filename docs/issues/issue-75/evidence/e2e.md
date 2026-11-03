# 步驟 5 證據：live 驗證、失效對照、端到端與內建版本（2026-11-03）

## 環境

- 主機：維護者 Linux amd64，kernel 7.0.0-42-generic，bwrap 0.11.1。帳號在 docker 群組，`/run/docker.sock` 存在。
- OpenCode：1.18.31（`~/.nvm/versions/node/v22.20.0/bin/opencode`），sha256 `f9dab322…4a11`，不是內建身分。模型 `openai/gpt-5.6-sol`（OAuth）。
- 內建版本：1.18.23，#73 留下的隔離安裝 `~/.cache/asura-verify/opencode-1.18.23/node_modules/.bin/opencode`。不需要另外下載，README TBD-2 因此解決。
- 程式：本分支 `ed174b5`，加上兩項未提交的修改：
  - `sandbox.go` 把 `--die-with-parent` 移到掛載參數之後（理由見「失效對照」）。
  - `verify_live_test.go` 新增 `TestVerifyLive_OpenCodeSandboxControl`。
  - 兩項都在本節記錄後與證據一起提交。

## Gate（最終版，2026-11-03）

`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...`：exit 0，24 個套件 ok，沒有 FAIL。skip 28 個，比 [steps2-4.md](./steps2-4.md) 的 27 個多一個，就是新增的 `TestVerifyLive_OpenCodeSandboxControl`（沒有設 `ASURA_VERIFY_LIVE`）。被 skip 的 live 測試不算通過，live 結果見下方各節。build 產生的 `asura` 已刪除；`gofmt -l` 對本分支的 Go 檔沒有輸出；`ps` 沒有殘留的 marker sleep。

## live verify（02:38:05Z）

`ASURA_VERIFY_LIVE=1 go test -count=1 -run 'TestVerifyLive_OpenCode$' -v ./internal/backend/opencode/`

```
identity {Runtime:opencode Version:1.18.31 Digest:f9dab32248695e9ebd56b16a1921798fd85112cf5a69c7dfd0cabc1e17be4a11} builtin=false
output                       12.9s  PASS
workspace-boundary           10.4s  PASS
read-only                    10.9s  PASS
cancellation                  6.8s  PASS
--- PASS: TestVerifyLive_OpenCode (42.16s)
```

#73、#74 時 workspace-boundary 一直失敗（`security violation: wrote outside the workspace`）。這是第一次在新版本上全部通過。

## 失效對照（02:38:55Z～02:39:49Z）

既有的 `TestVerifyLive_OpenCodeControls` 以 shim 拿掉唯讀角色的 `--agent`，並放行兩個角色的所有權限。寫入角色在 bwrap 內執行之後，權限全開也寫不出 workspace，所以這個 shim 對 workspace-boundary 已經不是失效對照。改為：

- shim 只對照 read-only。
- workspace-boundary 改由新增的 `TestVerifyLive_OpenCodeSandboxControl` 對照：用共用的 `verification.BwrapControl`，把 probe 根目錄以可寫方式掛進沙箱，與 OMP、Antigravity 的對照相同。

`BwrapControl` 會在 `--die-with-parent` 前面插入 bind。原本的參數把 `--die-with-parent` 放在最前面，插入的 bind 會被之後的 `--ro-bind / /` 蓋掉，對照組就測不出沙箱失效。因此把它移到掛載參數之後，與 clisandbox 的順序相同；它不是掛載操作，行為不變。移動後單元測試全部通過。

| 對照 | 結果 |
|---|---|
| `TestVerifyLive_OpenCodeControls`（shim，read-only） | `read-only  11.6s  FAIL: security violation: read-only role wrote the workspace`，測試 PASS |
| `TestVerifyLive_OpenCodeSandboxControl`（BwrapControl，workspace-boundary） | `workspace-boundary  11.2s  FAIL: security violation: wrote outside the workspace`，測試 PASS |
| 暫時讓寫入角色不經 bwrap（`if false && !isIsolatedRuntime(cfg)`），只跑 workspace-boundary | `workspace-boundary  10.7s  FAIL: security violation: wrote outside the workspace` |

第三項用的是暫時的 `zz_revert_test.go`，執行後已刪除，`adapter.go` 也已還原（`git status` 確認）。

## SCN-006：內建版本 1.18.23（02:40:11Z）

暫時測試以 daemon 相同的寫入角色設定（`DefaultApprovedPermissionPolicy`、`RoleStriker`）執行內建版本。workspace 內的 `note.txt` 原本是 `before`，要求模型以 shell 改寫：

```
version=1.18.23 err=<nil> output="DONE" file="after"
--- PASS: TestZZBuiltinStriker (11.13s)
```

暫時測試執行後已刪除。

## SCN-005：端到端

- 以 `go build` 從本分支建置，`asura version` 為 `dev`。
- 驗證紀錄使用真實路徑 `~/.local/share/asura/runtime-verification.json`。開始前目錄不存在，未設定 `XDG_DATA_HOME`。
- daemon：PID 1756021，在 scratchpad 使用獨立 DB，socket 為 `$XDG_RUNTIME_DIR/asura-e2e-75.sock`，`--allowed-roots` 只放 scratchpad 的 workspace。整個流程沒有重啟。
- 本機 `run` 使用獨立 DB，走 `--backend auto` 並設 `ASURA_RUNTIME_PRIORITY=opencode`。明確指定時，本機 run 走的是舊的 shell runner（#73 task-2.4.md）。

### 1. 驗證前（02:40:49Z）

本機與 daemon 的 inventory：

```
opencode registration=registered availability=unavailable version="" reason=version_unverified
```

`doctor`：

```
[warn] opencode: unavailable reason=version_unverified executable=/home/kevin/.nvm/versions/node/v22.20.0/bin/opencode; to use this opencode version, run: asura runtime verify opencode
[fail] runtimes: no runtime available
```

daemon `ask --backend opencode --wait` 在模型呼叫前拒絕：`error: capability admission: runtime admission rejected: backend unavailable; availability=[opencode:version_unverified]; ...`。這一輪的結束碼取成了 `tail` 的，沒有記到 asura 本身的結束碼；第 4 節同樣的拒絕結束碼為 1。

本機 `run --backend auto` 拒絕，結束碼 1：`stage striker: unsupported capability: no eligible runtime ... opencode:version_unverified`。

### 2. `asura runtime verify opencode`（02:41:03Z）

```
Verifying opencode: probes run the model
output                        7.3s  PASS
workspace-boundary           10.8s  PASS
read-only                     9.4s  PASS
cancellation                  7.4s  PASS
verified opencode 1.18.31 (sha256 f9dab32248695e9ebd56b16a1921798fd85112cf5a69c7dfd0cabc1e17be4a11); recorded in /home/kevin/.local/share/asura/runtime-verification.json
exit=0
```

目錄權限 700，紀錄檔 600。`--list` 除了 4 個內建身分，多了一筆 `opencode version="1.18.31" sha256=f9dab322… verified_at=2026-11-03T02:41:40Z asura_version=dev`。

### 3. 驗證後（02:41:49Z，daemon 仍是 PID 1756021）

本機與 daemon 的 inventory：

```
opencode registration=registered availability=available version="1.18.31" reason=none
```

`doctor`：

```
[ok]   opencode: available version=1.18.31 executable=/home/kevin/.nvm/versions/node/v22.20.0/bin/opencode capabilities=process_cancellation,read_only_sandbox,structured_events,workspace_write
```

daemon `ask --backend opencode --wait`，結束碼 0，任務 `task-1793673714-1ddd40584289`。prompt 要求模型在沙箱內執行 `curl -s -m 3 --unix-socket /run/docker.sock http://localhost/_ping || echo DOCKER-BLOCKED`，輸出：

```
E2E-DAEMON-opencode
DOCKER-BLOCKED
```

這是 SCN-007 的 live 佐證。對照：同一時段在主機上直接執行同一個命令，回 `OK`（README「步驟 1 實測」的 `/run` 未遮蔽列也是 `OK`）。

本機 `run --backend auto`，結束碼 0，任務 `task-1793673725-c9be9863b65f`，`Status: completed`。模型寫的 `ws/local.txt` 內容為 `E2E-LOCAL`。

### 4. 移除紀錄後（02:42:28Z）

```
removed opencode 1.18.31 (sha256 f9dab32248695e9ebd56b16a1921798fd85112cf5a69c7dfd0cabc1e17be4a11)
```

- 紀錄檔為 `{"records": []}`。
- 本機與 daemon 的 inventory 回到 `reason=version_unverified`，`doctor` 回到 `[fail] runtimes: no runtime available`。daemon 沒有重啟。
- daemon `ask` 結束碼 1，訊息與第 1 節相同。
- 本機 `run --backend auto` 結束碼 1，`stage striker: unsupported capability: no eligible runtime`。

### 收尾

- daemon 以 PID 1756021 停止。
- 依序刪除 `~/.local/share/asura/runtime-verification.json`、`runtime-verification.json.lock`，再 `rmdir ~/.local/share/asura`，目錄回到不存在。
- 刪除本次的 `$XDG_RUNTIME_DIR/asura-e2e-75.sock.lock`。
- `ps` 沒有殘留的 opencode 或 bwrap 行程；列出的 bwrap 只有 GNOME 的 glycin 圖片載入器，與本次無關。
- `~/.cache/asura-verify` 沒有 2026-11-01 之後建立的 `run-*` 目錄。既有的 31 個是 2026-10-21、22（#73）留下的，沒有動。

## 逾時（README TBD-3）

本節共 14 次 OpenCode 模型呼叫，最長 12.9 秒，都沒有逾時：

- live verify 4 次
- 失效對照 3 次
- 內建版本 1 次
- 端到端的 verify 4 次、daemon ask 1 次、本機 run 1 次

主機在步驟 1 之後重開過（kernel 由 7.0.0-38 變成 7.0.0-42），但沒有記錄 02:38 第一次執行時 OpenCode 的快取狀態，所以這組數字不能當作冷啟動對照。TBD-3 維持待確認。
