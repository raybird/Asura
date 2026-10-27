# Issue 73 Phase 5 證據：PR #77 審查修正（2026-10-27）

來源：PR #77 獨立審查 [留言](https://github.com/raybird/Asura/pull/77#issuecomment-6438498249)（Reviewed HEAD `50f958a`），以及 README 的 Q17～Q19。

## Task 5.1 — OpenCode 跨路徑整合測試（MUST FIX 1，SCN-008）

### 變更

`cmd/asura/runtime_verification_test.go` 的 `TestRuntimeVerificationAcrossPaths` 改成 `codex`、`opencode` 兩個子測試，產品程式不變。

- 每個子測試的 PATH 只放該 runtime 的假執行檔（加上 `/usr/bin:/bin`），所以 auto three-face 的每個 stage 都只能選它。
- 五個階段相同：
  1. 驗證前
  2. 寫入紀錄後
  3. 換掉執行檔（版本字串相同）
  4. 還原執行檔
  5. 移除紀錄
- 每個階段都檢查四條路徑：
  - 本機 inventory 與經 daemon 的 inventory
  - doctor
  - 本機 `run --backend auto --mode three-face`
  - daemon `ask`
- available 的階段另外確認假 runtime 真的被叫去執行模型回合：codex 看到 `exec`，opencode 看到 `run`。
- 假 opencode：`--version` 回 `1.18.31`，其他呼叫輸出一行 OpenCode JSON 的 `text` 事件。

### 紅燈：突變檢查

原本的測試在下列突變下全綠（審查者實測）。改寫後，每個突變單獨套用時測試都失敗，之後已用 `git checkout` 還原：

| 突變 | 失敗的檢查 |
|---|---|
| `internal/daemon/daemon.go` 兩個 OpenCode adapter 拿掉 `VerificationPath` | after verification：daemon inventory、daemon ask，且 ask 沒有執行 opencode |
| `cmd/asura/inventory.go` `localBackend` 的 OpenCode 拿掉 `VerificationPath`（inventory／doctor 共用） | after verification：本機 inventory、doctor |
| `cmd/asura/run.go` auto 路徑的 OpenCode 拿掉 `VerificationPath` | after verification：本機 run，且沒有執行 opencode |

### 綠燈

```
--- PASS: TestRuntimeVerificationAcrossPaths (1.39s)
    --- PASS: TestRuntimeVerificationAcrossPaths/codex (0.68s)
    --- PASS: TestRuntimeVerificationAcrossPaths/opencode (0.71s)
```

同時更正 [task-2.4.md](./task-2.4.md) 與 [e2e.md](./e2e.md) 對 SCN-008 覆蓋範圍的描述。
