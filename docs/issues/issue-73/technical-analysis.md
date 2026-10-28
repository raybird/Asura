# Issue 73 技術分析 - Runtime 版本改為「通過驗證即可使用」

## 技術可行性分析

- 判定點集中：所有判定路徑最後都經過各 runtime 的 `Probe`（清冊）或 `ValidateVersion`（adapter Run）。把「版本是否可用」換成「身分是否在內建或本機紀錄中」，只需要改這兩類函式與它們的呼叫端，admission、auto 選擇與 stage guard 都沿用清冊結果，不用各自改寫。
- 身分計算可行：OMP／Antigravity 已經計算 cli.js、Bun 與 node_modules 樹的 SHA-256（`clisandbox/probe.go`、`clisandbox/run.go:147-172`）；OpenCode／Codex 只要多算一次已解析執行檔的 SHA-256。
- probe 已存在：Codex 的唯讀、workspace-write、JSONL、取消 probe，以及 OpenCode 的 live probe，都在測試檔裡。需要改的是把它們從 `_test.go` 搬成產品程式可呼叫的驗證流程，並拿掉寫死的版本條件。
- 未知：這些 probe 在 Codex 0.163.0、OpenCode 1.18.31 上能不能執行、結果是否可信，要實跑才知道（README「風險與首要驗證」）。

## 方案選型與取捨

### 放行規則（2026-10-21 使用者確認方向）

```diff
 runtime 可用的條件
-  版本 == 寫死的值（OpenCode 依路徑是前綴或精確）
+  身分 ∈ 內建已驗證
+  或 身分 ∈ 本機驗證紀錄（由 asura runtime verify 在所有 probe 通過後寫入）
```

| 方案 | 取捨 | 結論 |
|---|---|---|
| 只要有安裝就通過 | 最方便，但新版本若改變唯讀旗標語意，任務會在可寫的狀態下照常成功，沒有錯誤訊號 | 不採用 |
| 已測版本當最低版本（≥） | 不需驗證，但往上的版本沒有證據；Codex 仍在 0.x，跳版可能等同大改版 | 不採用 |
| 通過驗證才可用 | 需要使用者執行一次驗證（需要登入、花少量費用），但保留 fail-closed | 採用 |

### 驗證內容（Q2）

只做不呼叫模型的檢查，只驗得了版本字串與旗標是否存在，證明不了沙箱行為，因此採用會呼叫模型的 live probe：

| Runtime | probe |
|---|---|
| OpenCode、Codex | 唯讀角色寫不進 workspace；workspace-write 只能寫 workspace；輸出可解析；取消會回收行程樹 |
| OMP、Antigravity | 在 Asura 的 bwrap 內可執行且輸出可解析；取消；登入憑證邊界 |

### 紀錄位置與綁定（Q3、Q7）

| 方案 | 取捨 | 結論 |
|---|---|---|
| config.toml 允許清單 | 使用者手動加版本就繞過驗證 | 不採用 |
| 存進 SQLite | 換 `--db` 就要重驗；沒有 config 時散落在各目錄的 `asura.db` | 不採用 |
| `$XDG_DATA_HOME/asura/` 下的獨立紀錄檔，綁版本與 SHA-256 | 同一使用者的同一安裝只需驗證一次；systemd user service 讀得到；換執行檔即失效 | 採用 |

### 其他確認

- 內建版本保留為已驗證（Q4），現有使用者升級 Asura 後不用重驗。
- OpenCode 統一精確比對（Q5），不再保留 `1.18` 前綴。
- 只有 `asura runtime verify` 會執行 probe（Q6）；提供 `--list`、`--remove`（Q9）。

## 架構影響評估

- **清冊契約**：新增 reason `version_unverified`，需加入 `validUnavailableReason` 白名單。`InventorySnapshot.Validate` 要求 policy 版本完全相等，調升 policy 版本會讓升級前封存的排隊任務與 cron 驗證失敗，因此維持 `inventory-v1`，只擴充 reason（2026-10-21 Q13）。
- **封存與 stage guard**：語意不變。stage 執行前重新取清冊，已驗證的身分若被移除，該 stage 依既有規則拒絕且不改選（SCN-009，2026-10-21 Q11）。
- **daemon**：移除 `daemon.go:154-155,231` 寫死的版本，改用與 CLI 相同的判定，消除路徑間的不一致。
- **並行**：daemon 執行中時，使用者可能同時執行 `verify` 或 `--remove`。紀錄檔以「寫入暫存檔再 rename」更新，讀取端每次判定時重新讀檔，不做快取。
- **測試**：live probe 從 `_test.go` 搬到產品程式後，原本的 live 測試改為呼叫同一套 probe，避免測試與產品各維護一份。

## 實作細節與待釐清項目

- 紀錄檔格式（暫定）：JSON，每筆含 `runtime`、`version`、`sha256`（OMP／Antigravity 為 hash 集合）、`verified_at`、`asura_version`；以 `0600` 權限寫入，權限不安全時比照 config 拒絕讀取。
- OpenCode 有唯讀與寫入兩個角色設定；身分以兩者共用的執行檔計算，兩個角色的 probe 都要通過。
- probe 使用 runtime 的預設模型，紀錄不綁模型（2026-10-21 Q12）。
- probe 在 Asura 建立的暫存 workspace 執行，不碰使用者的專案目錄。
- TBD-1～TBD-4 已於 2026-10-21 解決，結論見 README「待確認事項」。
