# 安裝與設定

從安裝到可用的流程：`install.sh` → `asura doctor` → `asura setup` → user service 或 standalone daemon。只支援 Linux amd64／arm64，全程不需要 root。

最後驗證日期：安裝與發布者驗證為 2026-10-15，證據見 [Issue 47](../issues/issue-47/implementation-plan.md)；其餘為 2026-10-09，證據見 [Issue 45](../issues/issue-45/implementation-plan.md)。

## 安裝

```bash
curl -fsSL https://raw.githubusercontent.com/raybird/Asura/main/install.sh | sh
# 指定版本或安裝目錄
curl -fsSL https://raw.githubusercontent.com/raybird/Asura/main/install.sh | sh -s -- --version v1.2.0 --install-dir ~/bin
# 無法驗證發布者時拒絕安裝，而不是只核對 checksum（見「發布者驗證」）
curl -fsSL https://raw.githubusercontent.com/raybird/Asura/main/install.sh | sh -s -- --require-attestation
```

- 未指定 `--version` 時安裝 GitHub `releases/latest` 的正式版（不含 prerelease）；`--version` 可指定較舊的正式版或 prerelease。
- 只支援含版本資訊的 Release（v1.1.0 起）；v1.0.0 請從 [Releases](https://github.com/raybird/Asura/releases/tag/v1.0.0) 手動下載。
- 預設安裝到 `~/.local/bin/asura`；不修改 shell rc，安裝目錄不在 `PATH` 時只提示。
- 需要 `curl` 或 `wget`、`tar`、`sha256sum` 或 `shasum`、`uname`；缺少任一項會在下載與變更檔案前失敗。
- 建議另裝 `gh` 2.68.0 以上（不需登入），installer 才能驗證發布者，見下節。

### 發布者驗證

checksum manifest 與 archive 來自同一個 Release，SHA-256 只能偵測下載損壞與檔案不一致；能改寫 Release 檔案的人可以兩者一起換掉。因此 Release workflow 會為每個 archive 產生 GitHub artifact attestation，並把驗證檔 `asura_<tag>_attestation.sigstore.json` 一併放進 Release。

| 情況 | installer 的行為 |
|---|---|
| 有 `gh` 2.68.0 以上 | 以 `gh attestation verify` 驗證 archive：簽章必須來自本 repo 的 `.github/workflows/release.yml`、來源 ref 為 `refs/tags/<tag>`，且不接受 self-hosted runner。驗證失敗或缺驗證檔就拒絕安裝，不會退回只核對 checksum |
| 沒有 `gh`，或 `gh` 太舊 | 照常安裝，stderr 警告只核對了 SHA-256 checksum 並說明原因 |
| 加 `--require-attestation` | 沒有可用的 `gh` 時，在下載前就失敗 |
| 安裝 v1.1.0 | v1.1.0 是最後一個沒有驗證檔的版本：照常安裝並警告；加 `--require-attestation` 則拒絕。之後的版本（含 prerelease）一律要求驗證 |

限制：

- `gh` 驗證時要連到 Sigstore（`tuf-repo-cdn.sigstore.dev`）取得信任根；連不到視為驗證失敗、拒絕安裝。installer 不提供略過驗證的選項。
- 這個驗證防的是 v1.1.0 之後的 Release 檔案被替換或竄改。v1.1.0 只核對 checksum：能改寫 Release 檔案的人可以同時替換 v1.1.0 的檔案、再把 latest 指回 v1.1.0，預設安裝只會留下一行警告。需要防護時請加 `--require-attestation`。取消 v1.1.0 免驗由 [#58](https://github.com/raybird/Asura/issues/58) 追蹤。
- `install.sh` 本身由 `main` 分支提供，能改寫 repo 的人可以直接修改 installer，不在防護範圍內。

手動驗證已下載的 archive（未登入 `gh` 時必須加 `--bundle`）：

```bash
gh attestation verify asura_<tag>_linux_amd64.tar.gz --bundle asura_<tag>_attestation.sigstore.json \
  --hostname github.com --repo raybird/Asura --signer-workflow raybird/Asura/.github/workflows/release.yml \
  --source-ref refs/tags/<tag> --deny-self-hosted-runners
```

### 替換前的檢查

替換前依序檢查：checksum manifest 只能有一筆對應 asset、SHA-256 相符、archive 只含預期目錄下的一般檔案、需要驗證時發布者 attestation 通過、暫存的新 binary 執行 `version --json` 回報的版本與架構正確。任一步失敗都會保留原本的 binary 並清除暫存檔。`<install-dir>/asura` 若是 symlink 或目錄會直接拒絕，請自行移除或改用 `--install-dir`。重新安裝與升降版都會重新下載驗證；installer 不修改設定或資料，也不重啟執行中的 daemon。

```bash
asura version          # asura v1.2.0 (commit abc1234, linux/amd64)
asura version --json   # {"arch":"amd64","commit":"abc1234","os":"linux","version":"v1.2.0"}
```

自行編譯的版本顯示 `dev`／`unknown`。

## 診斷：asura doctor

```bash
asura doctor
```

唯讀檢查平台、`/proc`、`bwrap`、config、DB 與 socket 目錄、allowed roots、各 runtime（與 daemon 相同的 inventory 判定，並列出實際使用的 executable）、user service，以及 daemon socket 是否接受連線（只做連線，不驗證對方身分或健康狀態）。

| 狀態 | exit | 意義 |
|---|---|---|
| `READY` | 0 | 所有檢查通過 |
| `DEGRADED` | 0 | 必要條件與至少一個 runtime 可用；有 optional runtime 或 user service 等缺項 |
| `NOT_READY` | 1 | 必要條件失敗，或沒有任何可用 runtime |

用法錯誤時 exit 2。目錄可寫性只依權限與檔案系統唯讀狀態評估，不實際寫入。doctor 不開 DB、不寫 config 或 credential、不呼叫模型，也不保證 runtime 已登入或模型請求會成功。所有探測在 10 秒內結束；環境變數與 config 只顯示是否設定及其中的路徑，不顯示其他值，也不轉述 runtime 或 systemctl 的原始輸出。

## 設定：config.toml

位置：`$XDG_CONFIG_HOME/asura/config.toml`（未設定時為 `~/.config/asura/config.toml`）。

```toml
schema_version = 1
allowed_roots = ["/home/me/projects"]           # setup 與 service install 要求；standalone daemon 可省略
db = "/home/me/.local/share/asura/asura.db"     # setup 新建時寫入解析後的絕對路徑
socket = "/run/user/1000/asura/asura.sock"
# 以下皆可省略
workers = 2
queue = 10
runtime_priority = ["codex", "opencode"]
memory_utility_policy = "observe"

[context]
limit = 120000
policy = "observe"
reserve_ratio = 0.2
```

- **沒有 config 檔**：每個指令沿用 v1.0 預設（目前目錄的 `asura.db`、`~/.asura/asura.sock`，status／cancel／audit／runtime inventory 未設 `ASURA_SOCKET` 時直接讀本機 DB）。
- **有 config 檔**：未設定的路徑改用 XDG：DB 為 `$XDG_DATA_HOME/asura/asura.db`（`~/.local/share/asura/asura.db`），socket 為 `$XDG_RUNTIME_DIR/asura/asura.sock`，沒有 runtime dir 時用 `$XDG_STATE_HOME/asura/asura.sock`（`~/.local/state/asura/asura.sock`）。相對路徑的 XDG 變數視為未設定。舊 DB 不會自動搬移。
- **優先序**：每個設定依 CLI flag > 環境變數 > config > 內建預設；只比較該設定實際存在的來源。明確給的 flag 一律勝出，即使是空值或等於預設值。環境變數為空等同未設定。

| 設定 | CLI flag | 環境變數 |
|---|---|---|
| `socket` | `--socket` | `ASURA_SOCKET` |
| `db` | `--db` | — |
| `allowed_roots` | `--allowed-roots` | — |
| `workers`／`queue` | `--workers`／`--queue` | — |
| `runtime_priority` | — | `ASURA_RUNTIME_PRIORITY` |
| `[context]` | — | `ASURA_CONTEXT_LIMIT`／`ASURA_CONTEXT_POLICY`／`ASURA_CONTEXT_RESERVE_RATIO` |
| `memory_utility_policy` | — | `ASURA_MEMORY_UTILITY_POLICY` |

驗證是 fail-closed：TOML 語法錯誤、未知欄位、不支援的 `schema_version`、無效的 enum／路徑／數值、無法讀取，或檔案非本人擁有、檔案或所在目錄可被 group／others 寫入，都會讓需要設定的指令以 exit 1 結束，不退回內建預設，錯誤訊息不回顯設定值。有 config 時，socket 路徑超過 107 bytes 會讓 daemon 拒絕啟動。config 不含 secret 欄位；`--permission` 與 Web 設定（含 `ASURA_WEB_KEY`）不放進 config。config 只提供 operator defaults，不改變 runtime selection 或已封存 Task 的執行契約。

## 初始設定：asura setup

```bash
asura setup                                        # 終端機互動：選 allowed roots 與是否啟用 service
asura setup --allowed-roots /home/me/projects       # 腳本模式
asura setup --allowed-roots /home/me/projects --service
```

- 建立 config（目錄 0700、檔案 0600）與 data 目錄（0700），不初始化 SQLite。新建的 config 會寫入當下依 XDG 解析的絕對 `db` 與 `socket`，讓 shell、service 與 doctor 一定使用同一個 DB 和 socket。
- 既有 config 沒有 `allowed_roots` 時會失敗並提示手動加入，不改寫 config，也不宣稱完成。
- 非互動模式必須提供 `--allowed-roots`，不會等待輸入；每個 root 都必須是既有目錄。
- 只有互動時明確回答同意，或給了 `--service`，才會安裝、啟用並啟動 user service。
- 重跑時保留既有設定、資料與 unit；`--allowed-roots` 與既有 config 不同時直接失敗，請手動修改 config。
- 目前目錄有舊的 `asura.db` 時只提示如何在 config 設定 `db`，不搬移或刪除。
- 不安裝或登入 runtime、不修改 shell rc、不啟動 Web。中途失敗會顯示 `Setup incomplete`，修正後重跑即可完成。
- 結尾執行 doctor。設定建立後 setup 即以 exit 0 結束；doctor 為 NOT_READY 時改顯示 `Setup complete; environment NOT_READY`，請依 doctor 的 `[fail]` 項目補齊（例如安裝並登入 runtime）。

## User service：asura service

```bash
asura service install    # 寫入 user manager 的 unit 目錄（通常是 ~/.config/systemd/user/asura.service）、daemon-reload、enable（不啟動）
asura service start | stop | restart | status
asura service uninstall  # disable --now、移除 unit、daemon-reload
```

- 由 systemd user service 管理 daemon，使用正式 config；`install` 需要先有含 `allowed_roots` 的 config（執行 `asura setup`），避免 service 以 HOME 作為 allowed root。
- unit 放在 systemd user manager 實際搜尋的目錄（依 manager 自己的 `XDG_CONFIG_HOME`，未設定時為 `~/.config`）。CLI 端若使用不同的 `XDG_CONFIG_HOME`，unit 會加上 `Environment=XDG_CONFIG_HOME=...`，讓 service 讀到同一份 config。
- 停止時先等待任務排空，最多 60 秒，逾時由 systemd 回收整個 unit 的進程；中斷的任務在下次啟動時標為 failed，不自動重跑。
- 異常結束 5 秒後重啟，60 秒內最多啟動 3 次；手動 `stop` 不會自動重啟。不設定 linger，unit 不含 secret。
- 只管理帶有 Asura 標記的 unit；同名但非 Asura 建立的 unit，install、start、stop、restart、uninstall 都會拒絕處理（status 只讀取）。
- 沒有 systemd 或 user bus 時，`asura service` 會明確失敗；直接執行 `asura daemon` 即可依 config 運作，doctor 會回報 DEGRADED。

## 移除與資料保留

```bash
asura service uninstall     # 有安裝 service 時
rm ~/.local/bin/asura       # 或 --install-dir 指定的位置
```

不提供資料清除指令。以下資料會保留，是否刪除由你決定：

| 資料 | 位置 |
|---|---|
| config | `~/.config/asura/config.toml`（或 `$XDG_CONFIG_HOME`） |
| Task／Memory DB | config 的 `db`，或 `~/.local/share/asura/asura.db`；無 config 時為當時目錄的 `asura.db` |
| socket | `$XDG_RUNTIME_DIR/asura/` 或 `~/.local/state/asura/`；無 config 時為 `~/.asura/` |
