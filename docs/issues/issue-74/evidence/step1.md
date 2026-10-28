# Issue 74 步驟 1 證據：重現與 OpenCode 行程樹（2026-10-28）

## 重現測試（紅燈）

`internal/backend/opencode/process_tree_test.go` 的 `TestKillProcessGroupReapsDetachedDescendants`。假 runtime 是一個 `sh -c`，以 `Setpgid` 啟動（同 adapter），在 group 裡留下四種命令：

| 命令 | 模擬的情境 |
|---|---|
| `setsid sleep A` | OpenCode bash 工具另開 session |
| `setsid sh -c 'sleep B & wait'` | 另開 session／process group，底下再有子孫 |
| `sleep C` | 前景子行程（現行實作就回收得到） |
| `setsid bwrap --unshare-pid ... sh -c 'sleep E & wait'` | Codex：bwrap 另開 process group 與 PID namespace，init 脫離 group |

另外有一個以獨立 group 啟動的 `sleep D`，它不是 group 的子孫，必須保留（SCN-003）。bwrap 無法建立 PID namespace 的環境會略過 E，並記 log。

現行 `KillProcessGroup`（只送 `kill(-pgid)`）的結果：

```
--- FAIL: TestKillProcessGroupReapsDetachedDescendants (5.08s)
    process_tree_test.go:104: descendants survived cancellation: map[425561:[3989568] 425562:[3989572] 425565:[3989575]]
```

- A、B、E 留了下來（marker 結尾 1、2、5），C 被回收。
- D 沒有被殺，「unrelated process ... was killed」沒有出現。
- 測試的 cleanup 以 PID 精準清除殘留，之後 `ps` 找不到任何 marker 行程。

## OpenCode 1.18.31 的實際行程樹（live，1 次模型呼叫）

`opencode run --format json --dir <ws> --auto`，參數與 adapter 相同。prompt 使用 `verification.shellPrompt` 的文字，命令為 `echo started > <ws>/started; sleep 7411 & setsid sleep 7412 & wait`。看到 started 標記後，從 `sleep` 往上記錄 `pid ppid pgid sid`：

```
== sleep 7411
4029502 4029499 4029499 4029499 sleep
4029499 4015437 4029499 4029499 bash
4015437 4015434 4015437 4015437 opencode
== sleep 7412
4029503 4029499 4029503 4029503 sleep
4029499 4015437 4029499 4029499 bash
4015437 4015434 4015437 4015437 opencode
```

（`opencode` 以上是本次測試的 shell，與 Asura 無關，已略去。）

- bash 工具另開 session（`sid=4029499`），但取消當下它仍是 opencode 的子行程。README 的前提「取消當下所有命令仍是 group 成員的子孫」在 OpenCode 上成立，照計畫進行。
- 以 Asura 現行方式 `kill -KILL -- -4015437` 之後，`setsid sleep 7412` 變成 `ppid=1` 繼續執行，重現了 #74。bash 與前景的 `sleep 7411` 這次跟著結束了。#73 的紀錄是單一 `sleep` 也留下來；兩次結果不同的原因沒有追查，加強後的 probe 以另開 session 的子行程判定，不依賴這個差異。
- 殘留的行程以 PID 精準清除。暫時 workspace 裡 OpenCode 依使用者的全域設定建立的 `mcp-memory-libsql.db` 已刪除，目錄已移除。

## Codex 0.163.0 的行程樹（建立 issue 時，2026-10-28）

見 [README](../README.md)「當前流程」：命令跑在 bwrap 另開的 process group 與 PID namespace 裡。`kill(-pgid)` 之後，namespace 的 init 被 PID 1 接手；直接 kill 這個 init，namespace 裡的行程（包括另開 session 的）會一併結束。
