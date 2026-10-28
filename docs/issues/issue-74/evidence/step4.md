# Issue 74 步驟 2～5 證據（2026-10-28）

## 步驟 2：共用實作回收整棵子孫樹

- 實作：`internal/backend/process_tree.go` 的 `KillProcessTree`。
  - 以 `/proc/<pid>/stat` 找出 group 成員，再沿 PPID 找出所有子孫。
  - 先以 `SIGSTOP` 凍結並重新掃描，直到集合不再變大；之後對整個集合送 `SIGKILL`，最後送 `kill(-pgid)`。
  - `opencode.KillProcessGroup` 改呼叫它，Codex adapter 的行程內取消也改呼叫它。
- `TestKillProcessGroupReapsDetachedDescendants`：步驟 1 的紅燈轉綠（0.10s），無關行程保留。
- `TestKillProcessGroupReapsForkingDescendants`：不斷 fork 另開 session 子行程的 shell，取消後全部回收。
  - 突變（拿掉凍結，單次掃描直接 `SIGKILL`）跑 3 次，2 次失敗（1095、4372 個子行程殘留），1 次通過。
  - 這個測試抓得到「沒有凍結與重新掃描」，但不保證每次都抓到，只能當回歸網，不能當確定性的紅燈。
- 突變（整個函式退回只送 `kill(-pgid)`）時，上面兩個測試都失敗，步驟 3 的測試也失敗。

## 步驟 3：外部取消路徑

`cmd/asura/runtime_selection_test.go` 的 `TestRuntimeSelectionLocalCancelReapsDetachedCommand`，結構同既有的 `TestRuntimeSelectionLocalCancel`：

- 假 codex 在背景留下 `setsid /bin/sleep <marker>` 後持續執行；`asura run` 在 goroutine 裡跑。
- 看到 PGID 與 sleep 之後，執行 `handleCancel`（外部 `asura cancel`）。
- 修正後：通過（0.12s），sleep 被回收，任務狀態為 cancelled。
- 退回只送 `kill(-pgid)`：失敗，`runtime survived cancel`。留下來的 setsid sleep 握著 runtime 的輸出 pipe，`asura run` 無法結束；這也是 #74 的症狀。

## 步驟 4：加強 cancellation probe

- 命令從 `echo started > F; sleep N` 改為 `echo started > F; sleep N & setsid sleep N+2000 & wait`。判定改為兩個 sleep 都消失，而且 group 已不存在。
- 單元紅燈：`TestCancelProbe` 新增 `inGroup` 的假 runtime：命令跑在 runtime 的 group 裡，group 被殺時前景命令一起結束。
  - 舊命令：probe 通過（`err = <nil>, want process tree survived`）。
  - 新命令：probe 失敗。
- 測試工具修正：
  - 假 runtime 的 shell 改為結束時立即回收。否則殭屍行程留在 group 裡，`kill(-pgid, 0)` 會成功，probe 因為錯的理由失敗。
  - 清除殘留的 helper 改成只掃描 `/proc` 一次。

### Live（2026-10-28）

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

## Gate（2026-10-28，步驟 1～6 文件之後）

`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...`：exit 0，24 個套件 ok，沒有 FAIL。build 產生的 `asura` 執行檔已刪除。

skip 共 27 個，與 #73 收尾時（[phase5.md](../../issue-73/evidence/phase5.md) Task 5.5）的 26 個相同，只多了新增的 `TestVerifyLive_CodexExecReadOnlyProfile`（未設 `ASURA_VERIFY_LIVE`）。被 skip 的 live 測試不算通過；本 issue 的 live 結果見上方各節，是另外設 `ASURA_VERIFY_LIVE=1` 執行的。
