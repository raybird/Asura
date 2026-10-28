# Issue 74 步驟 2～5 證據（2026-10-28）

## 步驟 2：共用實作回收整棵子孫樹

- 實作：`internal/backend/process_tree.go` 的 `KillProcessTree`。
  - 以 `/proc/<pid>/stat` 找出 group 成員，再沿 PPID 找出所有子孫。
  - 先以 `SIGSTOP` 凍結並重新掃描，直到集合不再變大；之後對整個集合送 `SIGKILL`，最後送 `kill(-pgid)`。
  - `opencode.KillProcessGroup` 改呼叫它，Codex adapter 的行程內取消也改呼叫它。
- `TestKillProcessGroupReapsDetachedDescendants`：步驟 1 的紅燈轉綠（0.10s），無關行程保留。
- `TestKillProcessGroupReapsForkingDescendants`：每 10ms fork 一個另開 session 子行程的 shell，取消後全部回收。
  - 最初版本沒有節流：突變（拿掉凍結，單次掃描直接 `SIGKILL`）3 次中有 2 次失敗（1095、4372 個子行程殘留）。但這個版本幾秒內就會累積數千個行程（實測峰值 6248，平常約 590），併入 main 後的 gate 裡，同時執行的 `TestRuntimeSelectionLocalAuto` 因此版本探測逾時而失敗；單獨重跑則通過。
  - 改為節流（`sleep 0.01`）之後，峰值為 614。同一個突變 5 次中 2 次失敗（各殘留 1 個子行程），3 次通過。
  - 結論：這個測試抓得到「沒有凍結與重新掃描」，但不保證每次都抓到，只能當回歸網，不能當確定性的紅燈。凍結與重新掃描的正確性主要靠程式本身的推理：凍結的行程不能 fork，集合不再變大才送 SIGKILL。
- 突變（整個函式退回只送 `kill(-pgid)`）時，上面兩個測試都失敗，步驟 3 的測試也失敗。

## 步驟 3：外部取消路徑

`cmd/asura/runtime_selection_test.go` 的 `TestRuntimeSelectionLocalCancelReapsDetachedCommand`，結構同既有的 `TestRuntimeSelectionLocalCancel`：

- 假 codex 在背景留下 `setsid /bin/sleep <marker>` 後持續執行；`asura run` 在 goroutine 裡跑。
- 看到 PGID 與 sleep 之後，執行 `handleCancel`（外部 `asura cancel`）。
- 修正後：通過（0.12s），sleep 被回收，任務狀態為 cancelled。
- 退回只送 `kill(-pgid)`：失敗，`runtime survived cancel`。留下來的 setsid sleep 握著 runtime 的輸出 pipe，`asura run` 無法結束；這也是 #74 的症狀。

## 步驟 4：加強 cancellation probe

- 命令從 `echo started > F; sleep N` 改為 `sleep N & setsid sh -c 'echo started > F && exec sleep N+2000' & wait`。判定改為兩個 sleep 都消失，而且 group 已不存在。
  - 最初版本是 `echo started > F; sleep N & setsid sleep N+2000 & wait`。併入 main 後的第二次完整 gate（2026-10-28）中，`TestCancelProbe` 的 groupOnly 情況失敗（`detached background command: err = <nil>, want process tree survived`）：標記先寫出，取消落在 `setsid` 之前，背景 sleep 根本還沒產生，只回收 group 的 runtime 因此通過。這是 probe 本身的假綠燈，不只是測試不穩。
  - 改由另開 session 的子行程寫標記：probe 看到標記時，背景 sleep 已經離開 runtime 的 group。修正後 `go test -count=1 -race ./internal/backend/verification/ -run TestCancelProbe` 連跑 8 次都通過。
- 單元紅燈：`TestCancelProbe` 新增 `inGroup` 的假 runtime：命令跑在 runtime 的 group 裡，group 被殺時前景命令一起結束。
  - 舊命令：probe 通過（`err = <nil>, want process tree survived`）。
  - 新命令：probe 失敗。
- 測試工具修正：
  - 假 runtime 的 shell 改為結束時立即回收。否則殭屍行程留在 group 裡，`kill(-pgid, 0)` 會成功，probe 因為錯的理由失敗。
  - 清除殘留的 helper 改成只掃描 `/proc` 一次。

### Live（2026-10-28）

下表與失效對照是最初版本命令的結果。改成由背景子行程寫標記之後的重跑結果，見本節最後的「新命令重跑」。

修正後，以真實 runtime 執行 `TestVerifyLive_*`（全部 probe）：

| Runtime | 版本 | 結果 |
|---|---|---|
| Codex | 0.163.0 | output、workspace-boundary、read-only、read-only-sandbox、cancellation 全部 PASS |
| OpenCode | 1.18.31 | cancellation PASS；workspace-boundary FAIL（`wrote outside the workspace`，#75，預期中） |
| OMP | 18.12.6 | 全部 PASS（含 cancellation） |
| Antigravity | 1.4.4 | 全部 PASS（含 cancellation） |

失效對照：暫時把 `KillProcessTree` 退回只送 `kill(-pgid)`，只跑 cancellation probe（以暫時的測試檔呼叫 `liveCodex`／`liveOpenCode`，執行後已刪除，原始碼已還原）：

```
codex     cancellation 11.3s FAIL: process tree survived cancellation: pgid 29485, sleep pids [30347], detached sleep pids [30348]
opencode  cancellation 13.2s FAIL: process tree survived cancellation: pgid 37388, sleep pids [38143], detached sleep pids [38144]
```

對照留下的行程都先以 `ps -o pid,ppid,pgid,sid,args` 確認是本次 probe 啟動的，再以 PID 精準清除：

- Codex：namespace init `codex-linux-sandbox` 30333，以及它底下的 bash、兩個 sleep。
- OpenCode：bash 38142 與兩個 sleep。

最後以 `ps` 確認沒有任何 probe 留下的 sleep。

#### 新命令重跑（2026-10-28）

標記改由背景子行程寫出之後，命令又調整過一次：第一版用 `;` 串接（`sh -c 'echo started > F; exec sleep M'`），`cmd/asura` 的 `TestRuntimeVerifyCommand` 因此失敗。那個測試的假 codex 用 `;` 切開命令來模擬模型執行，引號裡的 `;` 把命令切壞了（`sh: 1: Syntax error: Unterminated quoted string`）。最終版改用 `&&` 串接，引號裡不再有 `;`；`TestCancelProbe` 模擬 OMP 內建 shell 的 fake 改成取 `> ` 之後的第一個欄位當標記路徑。

最終命令（`&&` 版）的 live 結果（2026-10-28T05:01Z～05:02Z）：`ASURA_VERIFY_LIVE=1 go test -count=1 -v -timeout 30m -run 'TestVerifyLive_(Codex|OpenCode|Omp|Antigravity)$' ./internal/backend/{codex,opencode,omp,antigravity}/`

| Runtime | 版本 | 結果 |
|---|---|---|
| Codex | 0.163.0 | 五個 probe 全部 PASS（cancellation 8.4s） |
| OpenCode | 1.18.31 | cancellation PASS（7.2s）；workspace-boundary FAIL（`wrote outside the workspace`，#75，預期中） |
| OMP | 18.12.6 | 全部 PASS（cancellation 7.0s） |
| Antigravity | 1.4.4 | **未執行**：所有需要模型的 probe 都回 `authentication_failed: Antigravity login expired; update original CLI login`，沒有任何 probe 真的跑起來，不算通過。`;` 版命令在 04:4x 的重跑全部 PASS（cancellation 6.6s），兩版的差別只有 `;` 換成 `&&` |

`;` 版命令（04:40Z～04:48Z）的結果：Codex、OMP、Antigravity 全部 PASS，OpenCode 只有 workspace-boundary FAIL（#75）。

失效對照（最終命令，05:02Z）：方式同上，暫時把 `KillProcessTree` 退回只送 `kill(-pgid)`，以暫時的測試檔只跑 cancellation（執行後已刪除，原始碼已還原，`git status` 確認）：

```
codex     cancellation 11.4s FAIL: process tree survived cancellation: pgid 890167, sleep pids [891004], detached sleep pids [891005]
opencode  cancellation 12.8s FAIL: process tree survived cancellation: pgid 890609, sleep pids [891152], detached sleep pids [891153]
```

`;` 版命令的對照也都失敗（Codex pgid 807970、OpenCode pgid 804977）。

清除對照留下的行程：先確認命令列就是 probe 的命令、工作目錄在 `asura-verify/run-*` 底下，再以 PID 精準清除。

- Codex：namespace init `codex-linux-sandbox`，以及它底下的 bash 與兩個 sleep。
- OpenCode：bash 與兩個 sleep。

最後以 `ps` 確認沒有任何 probe 留下的 sleep 或 `codex-linux-sandbox`。

## 步驟 5：Codex exec 唯讀對應（#73 TBD-7）

`internal/backend/codex/verify_live_test.go` 的 `TestVerifyLive_CodexExecReadOnlyProfile`，1 次模型呼叫：

- 先執行 `codex sandbox -P :read-only -C <ws> -- sleep A`，不經模型。
- 再以 adapter 的 Oracle 角色（`exec -s read-only`）讓模型執行 `sleep B`。
- 兩次都在命令執行期間讀取 `codex-linux-sandbox` 的 `--permission-profile`，解析成 JSON 後比較，並確認沒有任何 `"access":"write"`。

```
codex sandbox -P :read-only: {"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"root"}},"access":"read"}]},"network":"restricted"}
exec -s read-only:          {"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"root"}},"access":"read"}]},"network":"restricted"}
--- PASS: TestVerifyLive_CodexExecReadOnlyProfile (6.87s)
```

失效對照：以 shim 只把 exec 參數裡的 `read-only` 換成 `workspace-write`（`:read-only` 不受影響），透過 `ASURA_VERIFY_CODEX_BIN` 執行：

- exec 這邊得到的權限設定多了 workspace、`slash_tmp`、`tmpdir` 的 `write`。
- 測試失敗，兩項檢查都抓到：`passes a different permission profile`、`grants write access`。

結論：在 Codex 0.163.0 上，`exec -s read-only` 與 `codex sandbox -P :read-only` 交給 OS 沙箱的權限設定相同。#73 的 TBD-7 由這個測試補驗；之後驗證新版本時，以同一個測試重跑。

## Gate（2026-10-28，步驟 1～6 文件之後；併入 main 後重跑見下節）

`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...`：exit 0，24 個套件 ok，沒有 FAIL。build 產生的 `asura` 執行檔已刪除。

skip 共 27 個，與 #73 收尾時（[phase5.md](../../issue-73/evidence/phase5.md) Task 5.5）的 26 個相同，只多了新增的 `TestVerifyLive_CodexExecReadOnlyProfile`（未設 `ASURA_VERIFY_LIVE`）。被 skip 的 live 測試不算通過；本 issue 的 live 結果見上方各節，是另外設 `ASURA_VERIFY_LIVE=1` 執行的。

## Gate（2026-10-28，併入 main 之後）

`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...`。build 產生的 `asura` 執行檔每次都已刪除。

- 第一輪（修正前）：兩次都失敗，原因不同。
  - `TestRuntimeSelectionLocalAuto` 版本探測逾時。fork 測試沒節流，同時累積數千個行程（見步驟 2）；單獨重跑通過。
  - `TestCancelProbe` 出現假綠燈（見步驟 4）。
- 第二輪（節流＋背景子行程寫標記，`;` 版）：兩次都失敗在 `TestRuntimeVerifyCommand`，原因是假 codex 用 `;` 切開命令（見步驟 4「新命令重跑」）。這是確定會發生的回歸，不是偶發。
- 最終（`&&` 版）：連續兩次 exit 0，24 個套件 ok，沒有 FAIL；skip 27 個，與上節相同，不算通過。`gofmt -l` 對本分支改過的 Go 檔沒有輸出。
