# 步驟 2～4 證據：寫入角色在 bwrap 內執行（單元與 CLI 層）

- 日期：2026-11-03
- 環境：bwrap 0.11.1；kernel 7.0.0-42-generic；主機有 `/run/docker.sock`（帳號在 docker 群組）與 `/run/user/1000`
- 新測試：`internal/backend/opencode/sandbox_test.go`、`cmd/asura/opencode_sandbox_test.go`。需要 bwrap 的測試在主機無法建立 namespace 時以 `t.Skip` 跳過；本機都有執行，沒有 skip。

## 步驟 2 — SCN-001、SCN-002

`TestStrikerSandbox_OnlyWorkspaceAndOpenCodeDataReachHost`：假 opencode 以寫入角色執行，HOME 指向測試目錄（放在 `~/.cache` 底下，不在 `/tmp`，與真實的 HOME overlay 掛載方式相同）。

修改前（`go test -count=1 -run TestStrikerSandbox -v ./internal/backend/opencode/`）：

```
sandbox_test.go:99: wrote outside the workspace: .../sibling/outside.txt
sandbox_test.go:99: wrote outside the workspace: .../home/home.txt
sandbox_test.go:99: wrote outside the workspace: .../home/.config/opencode/config.txt
sandbox_test.go:99: wrote outside the workspace: .../home/.cache/opencode/cache.txt
sandbox_test.go:99: wrote outside the workspace: .../outside-home.txt
sandbox_test.go:99: wrote outside the workspace: /tmp/asura-opencode-sandbox-388328712.txt
--- FAIL
```

修改後 PASS：workspace、`~/.local/share/opencode`、`~/.local/state/opencode` 的檔案留在主機上，其他六個都不存在。

### 既有測試的調整

全套測試在修改後只有 `TestRuntimeVerificationAcrossPaths/opencode` 失敗：`daemon ask did not execute the verified opencode`。原因是假 runtime 把呼叫紀錄寫在 workspace 外，寫入角色在沙箱內寫的紀錄被丟棄了，這正是本 issue 要的行為。紀錄改寫在 workspace 內之後通過。

另外有兩個測試同樣把紀錄寫在 workspace 外，而且斷言的是「沒有執行」。它們在修改後仍然通過，但已經變成假綠燈：OpenCode 寫入角色就算真的執行了，紀錄也留不下來。

- `TestRuntimeVerificationRemovedBeforeStage`：斷言 `opencodeRuns == 0`
- `TestRuntimeVerificationNotAutomatic`：斷言除了 `--version` 之外沒有其他呼叫

兩者的紀錄都改寫到 workspace 內。其他使用假 opencode 標記檔的測試都逐一確認過，標記本來就在 workspace 內，或者 OpenCode 根本不以寫入角色執行：`capability_status_test.go`、`daemon/backend_expansion_test.go`、`opencode/identity_test.go`，以及 `tests/integration/cli_test.go` 的 shell run。

## 步驟 2b — SCN-007

`TestStrikerSandbox_HidesHostRunSockets`：假 opencode 在沙箱內檢查 `/run/docker.sock`、`/var/run/docker.sock`、`/run/user/1000` 是否存在，以及 `/etc/resolv.conf` 是否可讀，結果寫進 workspace。

| 版本 | 結果 |
|---|---|
| 最終版 | PASS |
| 拿掉 `--tmpfs /run` | FAIL：`visible /run/docker.sock`、`visible /var/run/docker.sock`、`visible /run/user/1000` |
| 拿掉 resolv.conf 所在目錄的唯讀掛回 | FAIL：`resolv.conf unreadable` |

## 步驟 3 — SCN-003

`TestStrikerSandbox_CancelReapsCommandsDetachedBeforeCancel`：假 opencode 執行 `( setsid sleep <marker> </dev/null >/dev/null 2>&1 & ) ; echo ok > <ws>/started ; exec sleep 300`。子 shell 結束後，該 sleep 已不是 opencode 的子孫。兩個子測試：`in-process`（取消 context）與 `external`（`KillProcessGroup(pgid)`，`asura cancel` 走的路徑）。

| 版本 | 結果 |
|---|---|
| 最終版 | 兩個子測試都 PASS（0.08 秒） |
| 暫時讓寫入角色不經 bwrap | 兩個都 FAIL：`command detached before cancellation survived: map[477717:[1654792]]`、`map[477718:[1655399]]` |

第一次做對照時，假命令沒有把 stdio 導走。脫離的 sleep 持有 opencode 的 stdout，`cmd.Wait` 等不到 EOF，測試停在 `Run did not return after cancellation`，沒有走到殘留的判定。改成導向 `/dev/null`（與 bash 工具啟動的命令相同，不持有 opencode 的 stdout）後，紅燈的理由落在殘留。測試結束時以 marker 清掉殘留的 sleep，之後以 `ps` 確認沒有留下行程。

## 步驟 4 — SCN-004

`TestStrikerSandbox_FailsClosedWithoutBwrap`（opencode 套件）：

- PATH 只有假 opencode 的目錄時，`CaptureInventory` 回 `dependency_not_found`；寫入角色的 Run 回 `FailureUnavailable`，假 opencode 沒有被執行。
- PATH 改成一個永遠失敗的假 bwrap 時，寫入角色的 Run 同樣回 `FailureUnavailable`，假 opencode 沒有被執行。

| 版本 | 結果 |
|---|---|
| 最終版 | PASS |
| inventory 不探測 bwrap | FAIL：inventory 判為 `available`、`ReasonCode:none` |
| 找不到 bwrap 時退回沙箱外執行 | FAIL：`opencode was executed outside the sandbox` |
| 拿掉 namespace 檢查 | FAIL：`FailureClass:execution_failed`（`bwrap: setting up uid map: Permission denied`） |

最初版本以 `touch` 寫標記，但這個測試的 PATH 沒有 `/usr/bin`，`touch` 根本找不到，「沒有被執行」永遠成立（第二個突變的輸出顯示 `touch: not found`）。改用 shell 內建的 `: >` 之後，兩個判定都能獨立抓到突變。

`TestOpenCodeWithoutBwrapFailsClosed`（`cmd/asura`，PATH 只有假 opencode）：

- 本機與 daemon 的 inventory：`opencode registration=registered availability=unavailable version="" reason=dependency_not_found`
- doctor：`[warn] opencode: unavailable reason=dependency_not_found`
- 本機 `run --backend auto`：`opencode:dependency_not_found`
- daemon `ask --backend opencode`：`opencode:dependency_not_found`
- 假 opencode 只被查過版本

拿掉 inventory 的 bwrap 探測時，五項都 FAIL，而且假 opencode 以唯讀角色被執行。本機 `run --backend opencode` 走的是不經 admission 的既有路徑，在沒有 `sh` 的 PATH 下以 `exec: "sh": executable file not found` 失敗，與本 issue 無關，所以明確指定改以 daemon `ask` 驗證。

## Gate（2026-11-03）

`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...`

第一次 exit 1，有三個失敗，都是同一個原因：測試把 PATH 設成只有假 runtime 的暫存目錄，加上 inventory 的 bwrap 探測之後，OpenCode 照 Q3 判為 `dependency_not_found`。

- `TestProbeExecutableReportsProbedBinary`：`executable_test.go:30: dependency_not_found`
- `TestOpenCodeVerificationSpec`：`spec_test.go:50: candidate with temporary records: reason=dependency_not_found`
- `TestConfigRuntimePriorityAndContext`：`config_precedence_test.go:270: env priority did not win: codex`（OpenCode 不可用，auto 改選 codex）

這三個測試驗的不是沙箱，所以新增 `linkBwrap`，把主機的 bwrap symlink 進假 runtime 目錄；主機沒有 bwrap 時 skip，與專案「需要 bwrap」的前提一致。

第二次：exit 0，24 個套件 ok，沒有 FAIL。skip 27 個，數量與 #74 收尾時相同；新加的 `requireBwrap`、`linkBwrap` 在本機都沒有觸發 skip。被 skip 的 live 測試不算通過，live 結果見 [e2e.md](./e2e.md)。build 產生的 `asura` 已刪除；`ps` 沒有殘留的 marker sleep。`gofmt -l` 對本分支新增或修改的 Go 檔沒有輸出。
