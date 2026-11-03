# Issue 75 實作計畫：OpenCode 寫入角色在 bwrap 內執行

## 設計方案

### 寫入角色的啟動

`Adapter.Run` 的角色是寫入角色（`RoleStriker`，且不是隔離的唯讀設定）時，`opencode run ...` 改由 bwrap 啟動，其他角色不變：

```
bwrap --unshare-pid --unshare-ipc --die-with-parent
      --ro-bind / / --dev /dev --proc /proc --tmpfs /tmp
      --tmpfs /run --ro-bind <resolv.conf 所在目錄> <同一路徑>
      --overlay-src <HOME> --tmp-overlay <HOME>
      --bind <OpenCode data> <OpenCode data> --bind <OpenCode state> <OpenCode state>
      --bind <workspace> <workspace> --chdir <workspace>
      -- <opencode> run --format json --dir <workspace> ... 
```

- **寫回主機的路徑**（Q1）：data 與 state 目錄依 OpenCode 的 XDG 規則決定：設了 `XDG_DATA_HOME`／`XDG_STATE_HOME` 就用 `<XDG>/opencode`，否則是 `~/.local/share/opencode`、`~/.local/state/opencode`。目錄不存在時先建立，bind 才有掛載點。workspace 最後才 bind，所以位在 `/tmp` 或 HOME 底下的 workspace（測試的 `t.TempDir()`）仍然寫回主機。
- **HOME 暫時 overlay**（Q5）：HOME 以 `--tmp-overlay` 掛載，沙箱內可寫、結束即丟；data、state、workspace 在 overlay 之後 bind，寫入照樣回到主機。HOME 以外維持唯讀。步驟 1 實測：唯讀 HOME 讓使用者的 MCP server 起不來，overlay 後三個都是 connected。
- **`/run` 遮蔽**（Q6）：`/run` 換成 tmpfs，`/var/run` 是指向它的 symlink，一併被遮蔽。`/etc/resolv.conf` 解析後若位在 `/run` 底下（Ubuntu 是 `/run/systemd/resolve/stub-resolv.conf`），把它所在的目錄唯讀掛回，名稱解析才能用。2026-10-29 實測：docker socket 兩條路徑都被擋，DNS、HTTPS、MCP、models 都正常。
- **環境變數**：照舊用 `buildStandardExecutionEnv` 的結果，交給 bwrap 傳下去，不加 `--clearenv`。bwrap 是 process group 的 leader，`/proc/<pgid>/environ` 仍然帶著 `ASURA_TASK_ID`，所以 `IsTaskProcessGroup` 與 `VerifyTaskProcessGroup` 的身分檢查不受影響。
- **取消**：process group 的 leader 從 opencode 變成 bwrap。`KillProcessGroup`（#74）會沿 PPID 找到 namespace 的 init（內層 bwrap）並 kill 它，核心一併回收 namespace 內的所有行程，包含取消前就已脫離行程樹、被 init 接手的命令（SCN-003）。`--die-with-parent` 讓 Asura 行程結束時，沙箱也跟著結束。
- **namespace 檢查**：啟動前比照 clisandbox，先以同一組 namespace 與 overlay 參數執行 `/usr/bin/true`，失敗就以 `FailureUnavailable` 回傳，不在沙箱外執行（SCN-004）。找不到 bwrap、或核心不支援非特權 overlay 時也一樣。

不重用 `clisandbox.Run`。它會把 HOME 換成 tmpfs、以 memfd 傳登入快照、`--clearenv`、只接受 OMP／Antigravity 的設定形狀，與 Q1「沿用 OpenCode 自己的 data 目錄與登入」不合。只需要的 bwrap 參數在 adapter 內組成，`ProbeSandboxDependency` 則直接重用。

### inventory

`Adapter.Probe` 在寫入角色的 adapter（非 `Isolated`）先呼叫 `clisandbox.ProbeSandboxDependency`。失敗時回 `dependency_not_found`。`RoleRoutingBackend.Probe` 會依序探測兩個 adapter，任一失敗就讓整個 OpenCode 判為不可用（Q3 更正）。daemon、inventory、doctor、本機 run 都經過同一個 Probe，不另外修改。

## 實作步驟

### 步驟 1 — 首要驗證：真實 OpenCode run 在 bwrap 內的可寫路徑
- 產出：手動以設計方案的 bwrap 參數執行 OpenCode 1.18.31 的 `opencode run --format json --auto`，1～2 次模型呼叫，記錄命令與輸出。模型執行的命令同時做三件事：寫 workspace 內、寫 workspace 外的同層目錄、寫 `/tmp`。
- 相依：無
- 驗收編號：SCN-001、SCN-002（前提）
- 完成判準：
  - 任務完成、有文字輸出，session 在沙箱外以 `opencode session list` 看得到。
  - workspace 內的檔案存在；workspace 外的檔案不存在；主機的 `/tmp` 沒有模型寫的檔案。
  - stderr 與 `~/.local/share/opencode/log` 裡，沒有唯讀檔案系統（EROFS）造成的錯誤。
  - 結論為以下其中一個：
    - 不需要其他可寫路徑：照計畫進行。
    - 需要：停下來，向使用者回報需要的路徑，不擅自擴大 Q1 的範圍。
- 驗證：live 契約驗證，記錄於 `evidence/step1.md`
- 狀態：✅ 已完成（2026-10-29）
- 證據：[evidence/step1.md](./evidence/step1.md)。唯讀 HOME 下邊界有效，但 MCP server 起不來；回報後 Q5 改為 HOME 暫時 overlay，overlay 下真實執行通過，寫回主機的只需要 workspace 與 data、state。沙箱內 10 次有 2 次逾時，原因未確認（README TBD-3）

### 步驟 2 — 寫入角色改以 bwrap 啟動
- 產出：依設計方案修改 `Adapter.Run`。以假的 opencode 腳本寫確定性測試，HOME 與 XDG 目錄都指向測試暫存目錄：
  - 寫 workspace 外的同層目錄、HOME 下的檔案、`<config>/opencode`、`<cache>/opencode`、HOME 以外的路徑，結束後主機上都沒有；workspace 內的寫入留下（SCN-001）。
  - 寫 data、state 目錄的檔案留在主機上；寫 `/tmp` 與 HOME 其他部分的檔案不留（SCN-002）。
  - 唯讀角色的啟動方式不變（既有測試）。
- 相依：步驟 1
- 驗收編號：SCN-001、SCN-002
- 完成判準：
  - 新測試在修改前以「寫到 workspace 外」為理由失敗，修改後通過。
  - 既有的 opencode 套件測試全部通過，包含身分檢查與 H-01 測試。
- 驗證：單元紅綠；`go test -race ./internal/backend/opencode/...`
- 狀態：✅ 已完成（2026-11-03）
- 證據：[evidence/steps2-4.md](./evidence/steps2-4.md)「步驟 2」：新測試先紅後綠；三個既有測試的呼叫紀錄改寫到 workspace 內（其中兩個原本會變成假綠燈）；gate 通過

### 步驟 2b — 遮蔽主機 /run 底下的 socket
- 產出：確定性測試。測試建立一個 `/run` 底下的 socket 不切實際，改由假 opencode 在沙箱內回報：`/run/docker.sock`、`/var/run/docker.sock`、`/run/user/<uid>` 是否存在，以及 `/etc/resolv.conf` 是否可讀。
- 相依：步驟 2
- 驗收編號：SCN-007
- 完成判準：
  - 拿掉 `--tmpfs /run` 時，主機有 docker socket 的環境下測試失敗。主機沒有 docker socket 時，紅燈改以 `/run/user/<uid>` 判定。
  - 加回後通過，`/etc/resolv.conf` 仍可讀。
- 驗證：單元紅綠；live 佐證在步驟 5（沙箱內 `_ping` 失敗、模型呼叫正常）
- 狀態：✅ 已完成（2026-11-03）
- 證據：[evidence/steps2-4.md](./evidence/steps2-4.md)「步驟 2b」：拿掉 `--tmpfs /run` 時看得到 docker socket 與 `/run/user/1000`；拿掉 resolv.conf 掛回時讀不到；最終版通過

### 步驟 3 — 取消回收已脫離行程樹的命令
- 產出：確定性測試。假的 opencode 先以 `setsid` 在背景啟動 `sleep`，啟動它的 shell 隨即結束，假 opencode 本身繼續執行。兩條取消路徑都要測：行程內取消，以及經 `KillProcessGroup` 的外部取消。
- 相依：步驟 2
- 驗收編號：SCN-003
- 完成判準：
  - 拿掉步驟 2 的 bwrap 時，測試以「殘留」為理由失敗。
  - 加回後，5 秒內該 sleep 與 process group 都不存在。
- 驗證：單元紅綠（紅燈以暫時退回步驟 2 取得，記錄於證據）
- 狀態：✅ 已完成（2026-11-03）
- 證據：[evidence/steps2-4.md](./evidence/steps2-4.md)「步驟 3」：不經 bwrap 時兩條取消路徑都留下殘留；最終版 5 秒內回收

### 步驟 4 — 缺 bwrap 時 fail-closed
- 產出：
  - `Adapter.Probe` 探測 bwrap。
  - 測試：PATH 找不到 bwrap 時，寫入 adapter 與 `RoleRoutingBackend` 的 Probe 回 `dependency_not_found`；Run 在啟動 opencode 前以 `FailureUnavailable` 失敗。
  - `cmd/asura` 的 inventory 測試確認 OpenCode 判為 unavailable、reason 為 `dependency_not_found`。
- 相依：步驟 2
- 驗收編號：SCN-004
- 完成判準：
  - 新測試先紅後綠。
  - 假 opencode 在測試中確實沒有被執行（以標記檔判定）。
- 驗證：單元＋ `cmd/asura` 測試
- 狀態：✅ 已完成（2026-11-03）
- 證據：[evidence/steps2-4.md](./evidence/steps2-4.md)「步驟 4」：inventory、doctor、auto run、daemon ask 都判 `dependency_not_found`；三個突變各自被抓到

### 步驟 5 — live 驗證、端到端與內建版本
- 產出：
  - `ASURA_VERIFY_LIVE=1` 執行 `TestVerifyLive_OpenCode`（1.18.31），全部 probe 通過。
  - 失效對照：暫時讓寫入角色不經 bwrap，workspace-boundary 失敗；另以 `verification.BwrapControl` 把 probe 根目錄可寫掛進沙箱，寫成可重複執行的 `TestVerifyLive_OpenCodeSandboxControl`。
  - 端到端，做法同 #73、#74 的 `evidence/e2e.md`：驗證前拒絕；verify 後 daemon、inventory、doctor、本機 run 都放行；remove 後回到拒絕。
  - 內建版本 1.18.23：寫入角色實際修改一個 workspace 檔案（SCN-006）。使用 #73 留下的隔離安裝（TBD-2）。
- 相依：步驟 2、2b、3、4
- 驗收編號：SCN-005、SCN-006；SCN-001～003、SCN-007 的 live 佐證
- 完成判準：
  - live 與對照的實際輸出都已記錄。
  - 結束後 `~/.local/share/asura/` 還原到開始前的狀態。
  - 留下的行程已以 ps 確認並清除。
- 驗證：live，記錄於 `evidence/e2e.md`
- 狀態：✅ 已完成（2026-11-03）
- 證據：[evidence/e2e.md](./evidence/e2e.md)：1.18.31 四個 probe 通過；三組失效對照都抓到；端到端四條路徑在驗證前、後、移除後的判定都正確；內建 1.18.23 改寫了 workspace 檔案。真實紀錄目錄已還原為不存在

### 步驟 6 — 文件
- 產出：
  - `docs/guides/runtimes.md`：說明 OpenCode 寫入角色的隔離方式、寫回主機的路徑、HOME 暫時 overlay（寫入不保存）、需要 bwrap；移除「OpenCode 新版本過不了驗證」與「取消前已脫離行程樹」兩條已知限制。
  - `README.md`「安裝」：OpenCode 需要 bwrap。
  - `CHANGELOG.md`。
- 相依：步驟 5
- 驗收編號：常青文件
- 完成判準：文件描述與程式、live 結果一致
- 驗證：人工核對
- 狀態：✅ 已完成（2026-11-03）
- 證據：`docs/guides/runtimes.md` 新增「OpenCode 寫入角色的沙箱」並更新已知限制；`README.md`、`docs/guides/cli-reference.md`、`CHANGELOG.md`。對照 `sandbox.go`、`clisandbox.ProbeSandboxDependency` 與 live 結果人工核對

### 步驟 7 — PR #86 審查修正
- 產出：
  - inventory 要求 bwrap 0.10 以上，預檢失敗帶 bwrap stderr（SCN-004 修訂）。
  - workspace 蓋住 HOME、`/tmp`、`/var/tmp`、`/run` 時拒絕（SCN-008）。
  - 沙箱參數：只掛執行檔與 resolv.conf 檔案、`--new-session`、`--tmpfs /var/tmp`。
  - CI 改裝 bubblewrap 0.11.1；文件與 TBD 依 Q7～Q10 更新。
- 相依：步驟 2～6；規格修訂 `e0ffc67`
- 驗收編號：SCN-004、SCN-008；SCN-001、SCN-002、SCN-007 的回歸
- 完成判準：新測試先紅後綠；沙箱參數變更後 live verify 與失效對照重跑通過；CI 綠燈
- 驗證：單元、直接探測與 live，記錄於 `evidence/review-fixes.md`
- 狀態：✅ 已完成（2026-11-03）
- 證據：[evidence/review-fixes.md](./evidence/review-fixes.md)

## 使用方式對照

使用者操作不變。差異如下：

- 寫入角色寫回主機的只有 workspace，加上 OpenCode 自己的 data／state 目錄。HOME 其他部分在沙箱內可寫，但執行結束就丟棄，例如 Go build cache、npm cache、MCP server 自己的資料；HOME 以外唯讀。
- 主機必須有可建立 user namespace 的 bwrap，與 OMP、Antigravity 相同。
- OpenCode 新版本可以透過 `asura runtime verify opencode` 驗證後使用。

## 測試策略與驗收對應

| 驗收編號 | 層級 | 證據 |
|---|---|---|
| SCN-001 | 單元（假 opencode，真 bwrap）＋ live probe | 步驟 1、2、5 |
| SCN-002 | 單元＋ live 觀察 | 步驟 1、2 |
| SCN-003 | 單元（真 bwrap）＋ live cancellation probe | 步驟 3、5 |
| SCN-007 | 單元（真 bwrap）＋ live 觀察 | 步驟 2b、5 |
| SCN-004 | 單元＋ `cmd/asura` 測試 | 步驟 4 |
| SCN-005 | live 端到端 | 步驟 5 |
| SCN-006 | live（內建版本） | 步驟 5 |

單元測試需要可用的 bwrap，沒有時以 `t.Skip` 跳過。各步驟的證據要列出被 skip 的測試，skip 不算通過（project.md「綠燈不等於有跑到」）。

## 風險與首要驗證

見 [README.md](./README.md)「風險與首要驗證」；首要驗證即步驟 1。

## 檢查清單

- [x] 步驟 1：真實 run 的可寫路徑已確認
- [x] 步驟 2：寫入角色在 bwrap 內，SCN-001、002 先紅後綠
- [x] 步驟 2b：主機 /run 底下的 socket 已遮蔽
- [x] 步驟 3：已脫離行程樹的命令被回收
- [x] 步驟 4：缺 bwrap 時 fail-closed
- [x] 步驟 5：live verify、失效對照、端到端、內建版本
- [x] 步驟 6：文件與 CHANGELOG
- [x] 步驟 7：PR #86 審查修正
- [x] 專案 gate 通過，skip 已列出（[evidence/e2e.md](./evidence/e2e.md)「Gate」）
