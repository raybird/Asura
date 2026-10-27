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

## Task 5.3 — Antigravity 先比雜湊（Q18，SCN-002、SCN-006）

### 變更

- `verification.AdmitDigest(runtime, digest, recordsPath)`：只以 runtime 和雜湊查紀錄，回傳相符的紀錄。讀檔失敗與找不到紀錄都回 `ErrUnverified`，與 `Admit` 相同。
- `antigravity.admit`：雜湊是內建值時行為不變；其他雜湊改用 `AdmitDigest`，版本取自紀錄，不再執行 `agy --version`。
  - 影響 inventory、doctor、daemon admission，以及每次 Run 前的檢查。
  - `VerificationSpec`（只有 `asura runtime verify` 會用）仍執行一次 `agy --version`，用來取得要記錄的版本。
- `docs/guides/runtimes.md`「版本驗證」補上這個行為。

### 紅綠

`TestAntigravityIdentityPolicy`：假 agy 每次被執行都把參數寫進呼叫紀錄。測試走三個情境：未驗證、驗證後（版本必須是紀錄裡的 1.4.4）、換掉執行檔。最後斷言呼叫紀錄不存在。

- 改之前：失敗，呼叫紀錄有三行 `--version`。
- 改之後：通過。

既有的 `TestInventoryAntigravityProbe` 原本預期未知雜湊是 `version_failed`。那個結果來自執行一個不能執行的假 agy；現在判定時不執行，結果改為 `version_unverified`，同樣 fail-closed。已更新這支測試的預期值，並加上註解。

`go test -count=1 ./internal/backend/antigravity/ ./internal/backend/verification/ ./cmd/asura/`：全部 ok。

## Task 5.2 — Codex 唯讀沙箱的確定性檢查（Q17，SCN-005）

### 變更

- `verification.Options.ReadOnlySandbox`：在 runtime 自己的 OS 沙箱內直接執行命令，不經模型。有設定時加入 probe `read-only-sandbox`。
- `sandboxProbe` 的判定：命令先印出標記 `ASURA_SANDBOX_RAN`，再寫 `workspace/sandbox.txt`。
  - 唯讀模式寫入成功：`security violation`。
  - 唯讀模式沒有印出標記：`inconclusive`。命令沒執行時（例如權限設定名稱不被接受），沒寫入不能當成唯讀成立。
  - workspace-write 模式寫不進去：`positive control failed`。
- Codex 的 `VerificationSpec` 設定 `sandboxRunner(bin)`，執行 `codex sandbox -P :read-only|:workspace -C <workspace> -- <command>`。
  - 2026-10-27 在 Codex 0.163.0 以 `ps` 確認：`codex sandbox` 的命令跟 exec 的 shell 工具一樣，經 `bwrap` 與 `codex-linux-sandbox` 執行。
  - 實測 `-P :read-only` 的寫入得到 `Read-only file system`，`-P :workspace` 寫得進去。`-P read-only`（沒有冒號）會因為缺少 `[permissions]` 設定而失敗。
- `docs/guides/runtimes.md` 的 probe 表新增 `read-only-sandbox`。
- `cmd/asura/runtime_verify_test.go` 的假 codex 補上 `sandbox` 子命令。

### 紅綠

- `TestSandboxProbe`：先只加欄位與一個永遠回傳 nil 的空 probe，取得真正的紅燈（不是編譯失敗）：「沙箱放行寫入」「命令沒執行」「workspace-write 也寫不進去」三個案例都失敗。實作後四個案例都通過。
- `TestCodexReadOnlySandboxRunner`：改之前因為 spec 沒有 runner 而失敗；改之後假 codex 收到的參數依序為 `sandbox -P :read-only -C <ws> -- sh -c true` 與 `sandbox -P :workspace -C <ws> -- sh -c true`。
- `TestRuntimeVerifyCommand`：假 codex 還沒有 `sandbox` 子命令時，「all pass」案例出現 `read-only-sandbox FAIL: security violation`，紀錄沒有寫入，證明新 probe 確實是必要條件。補上子命令後通過。

### Live（2026-10-27，Codex 0.163.0）

```
=== RUN   TestVerifyLive_CodexSandbox
        read-only-sandbox             0.1s  PASS
--- PASS: TestVerifyLive_CodexSandbox (0.57s)

=== RUN   TestVerifyLive_CodexControls
        workspace-boundary           22.5s  FAIL: security violation: wrote outside the workspace
        read-only                    10.8s  FAIL: security violation: read-only role wrote the workspace
        read-only-sandbox             0.1s  FAIL: security violation: read-only sandbox allowed a workspace write
--- PASS: TestVerifyLive_CodexControls (33.45s)
```

失效對照組的 shim 新增一條，把 `:read-only` 換成 `:workspace`；三個沙箱相關的 probe 都抓到失效。這次沒有跑取消 probe，所以沒有 #74 的殘留行程，事後搜尋 6 位數以上的 `sleep` 也沒有結果。

### 限制

- 這個檢查驗的是 Codex 內建的 `:read-only` 權限設定，exec 的 `-s read-only` 由 Codex 自行對應到唯讀政策。兩者走同一個沙箱執行檔，但對應關係是 Codex 的內部行為，沒有另外驗證。
- 未來的 Codex 版本如果改了 `codex sandbox` 的參數，這個 probe 會判 `inconclusive`，驗證失敗（fail-closed），需要更新 Asura。
